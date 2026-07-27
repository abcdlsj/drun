package main

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
)

type containerSnapshot struct {
	info  *containerInfo
	image *imageInfo
}

func (a *application) check(ctx context.Context, options checkOptions) error {
	a.ui.header("container update check")

	names, err := a.listRunningContainers(ctx)
	if err != nil {
		return fmt.Errorf("list running containers: %w", err)
	}
	if len(names) == 0 {
		a.ui.success("No running containers found")
		return nil
	}

	var snapshots []containerSnapshot
	var checkErrors []error
	var warnings []string
	currentImages := make(map[string]*imageInfo)
	if err := a.ui.spin(fmt.Sprintf("Inspecting %d running containers", len(names)), func() error {
		for _, name := range names {
			info, inspectErr := a.inspect(ctx, name)
			if inspectErr != nil {
				checkErrors = append(checkErrors, fmt.Errorf("inspect %s: %w", name, inspectErr))
				continue
			}
			if info.HostConfig.AutoRemove {
				warnings = append(warnings, fmt.Sprintf("Skipping %s because --rm containers cannot be rolled back", name))
				continue
			}
			image, ok := currentImages[info.Image]
			if !ok {
				image, inspectErr = a.inspectImage(ctx, info.Image)
				if inspectErr != nil {
					checkErrors = append(checkErrors, fmt.Errorf("inspect current image for %s: %w", name, inspectErr))
					continue
				}
				currentImages[info.Image] = image
			}
			snapshots = append(snapshots, containerSnapshot{info: info, image: image})
		}
		return nil
	}); err != nil {
		return err
	}
	for _, warning := range warnings {
		a.ui.warning(warning)
	}

	latestImages := make(map[string]*imageInfo)
	imageReferences := make(map[string]bool)
	for _, snapshot := range snapshots {
		imageReferences[snapshot.info.Config.Image] = true
	}
	for _, reference := range sortedKeys(imageReferences) {
		var latest *imageInfo
		err := a.ui.spin("Checking "+reference, func() error {
			if _, pullErr := a.docker.run(ctx, "pull", reference); pullErr != nil {
				return pullErr
			}
			var inspectErr error
			latest, inspectErr = a.inspectImage(ctx, reference)
			return inspectErr
		})
		if err != nil {
			checkErrors = append(checkErrors, fmt.Errorf("check image %s: %w", reference, err))
			a.ui.warning(fmt.Sprintf("Could not check %s", reference))
			continue
		}
		latestImages[reference] = latest
	}

	updates := make([]updateCandidate, 0)
	for _, snapshot := range snapshots {
		latest, ok := latestImages[snapshot.info.Config.Image]
		if !ok || sameImageID(snapshot.info.Image, latest.ID) {
			continue
		}
		updates = append(updates, newUpdateCandidate(snapshot.info, snapshot.image, latest))
	}
	sort.Slice(updates, func(i, j int) bool { return updates[i].Name < updates[j].Name })

	if len(updates) == 0 {
		if len(checkErrors) > 0 {
			return errors.Join(checkErrors...)
		}
		a.ui.success(fmt.Sprintf("No updates available across %d running containers", len(names)))
		return nil
	}

	var selected []updateCandidate
	if options.yes {
		a.ui.listUpdates(updates)
		selected = updates
	} else if !a.ui.isInteractive() {
		a.ui.listUpdates(updates)
		a.ui.info("Run drun check --yes to update every listed container")
		return errors.Join(checkErrors...)
	} else {
		selected, err = a.ui.selectUpdates(updates)
		if err != nil {
			return fmt.Errorf("select containers: %w", err)
		}
	}
	if len(selected) == 0 {
		a.ui.warning("No containers selected")
		return errors.Join(checkErrors...)
	}

	updated := 0
	var updateErrors []error
	for index, candidate := range selected {
		a.ui.info(fmt.Sprintf("Updating %s (%d/%d)", candidate.Name, index+1, len(selected)))
		if err := a.updateCheckedContainer(ctx, candidate); err != nil {
			updateErrors = append(updateErrors, fmt.Errorf("update %s: %w", candidate.Name, err))
			a.ui.warning(fmt.Sprintf("%s failed; check the container state", candidate.Name))
			continue
		}
		updated++
		a.ui.success(fmt.Sprintf("%s updated %s -> %s", candidate.Name, candidate.CurrentVersion, candidate.LatestVersion))
	}

	if updated == len(selected) {
		a.ui.success(fmt.Sprintf("Updated all %d selected containers", updated))
	} else {
		a.ui.warning(fmt.Sprintf("Updated %d of %d selected containers", updated, len(selected)))
	}
	return errors.Join(append(checkErrors, updateErrors...)...)
}

func (a *application) listRunningContainers(ctx context.Context) ([]string, error) {
	output, err := a.docker.run(ctx, "ps", "--format", "{{.Names}}")
	if err != nil {
		return nil, err
	}
	names := strings.Fields(string(output))
	sort.Strings(names)
	return names, nil
}

func (a *application) updateCheckedContainer(ctx context.Context, candidate updateCandidate) error {
	info, err := a.inspect(ctx, candidate.Name)
	if err != nil {
		return err
	}
	if !info.State.Running {
		return errors.New("container is no longer running")
	}
	if sameImageID(info.Image, candidate.LatestImageID) {
		a.ui.info(candidate.Name + " was already updated")
		return nil
	}
	if !sameImageID(info.Image, candidate.CurrentImageID) {
		return fmt.Errorf("container image changed during selection (%s -> %s)", shortImageID(candidate.CurrentImageID), shortImageID(info.Image))
	}
	currentImage, err := a.inspectImage(ctx, info.Image)
	if err != nil {
		return err
	}
	compose, isCompose, err := composeServiceFromContainer(info)
	if err != nil {
		return err
	}
	if isCompose {
		return a.replaceCompose(ctx, info, compose.upArgs(), candidate.LatestImageID)
	}
	runArgs := buildRunArgs(info, &currentImage.Config)
	backupName := backupContainerName(candidate.Name, a.now())
	return a.replace(ctx, info, runArgs, backupName, candidate.LatestImageID)
}

func newUpdateCandidate(info *containerInfo, current, latest *imageInfo) updateCandidate {
	currentVersion, latestVersion := displayImageVersions(current, latest)
	return updateCandidate{
		Name:           strings.TrimPrefix(info.Name, "/"),
		Image:          info.Config.Image,
		CurrentImageID: info.Image,
		LatestImageID:  latest.ID,
		CurrentVersion: currentVersion,
		LatestVersion:  latestVersion,
	}
}

func displayImageVersions(current, latest *imageInfo) (string, string) {
	if currentTag, latestTag := preferredImageTag(current), preferredImageTag(latest); currentTag != "" && latestTag != "" && currentTag != latestTag {
		return currentTag, latestTag
	}
	if currentVersion, latestVersion := imageLabel(current, "org.opencontainers.image.version"), imageLabel(latest, "org.opencontainers.image.version"); currentVersion != "" && latestVersion != "" && currentVersion != latestVersion {
		return currentVersion, latestVersion
	}
	if currentRevision, latestRevision := imageLabel(current, "org.opencontainers.image.revision"), imageLabel(latest, "org.opencontainers.image.revision"); currentRevision != "" && latestRevision != "" && currentRevision != latestRevision {
		return shortRevision(currentRevision), shortRevision(latestRevision)
	}
	return shortImageID(current.ID), shortImageID(latest.ID)
}

func displayImageIdentity(image *imageInfo) string {
	if tag := preferredImageTag(image); tag != "" {
		return tag
	}
	if imageVersion := imageLabel(image, "org.opencontainers.image.version"); imageVersion != "" {
		return imageVersion
	}
	if revision := imageLabel(image, "org.opencontainers.image.revision"); revision != "" {
		return shortRevision(revision)
	}
	return shortImageID(image.ID)
}

func preferredImageTag(image *imageInfo) string {
	if image == nil {
		return ""
	}
	tags := append([]string(nil), image.RepoTags...)
	sort.Strings(tags)
	for _, reference := range tags {
		tag := imageTag(reference)
		if tag != "" && tag != "latest" && tag != "<none>" {
			return tag
		}
	}
	return ""
}

func imageTag(reference string) string {
	colon := strings.LastIndex(reference, ":")
	slash := strings.LastIndex(reference, "/")
	if colon <= slash {
		return ""
	}
	return reference[colon+1:]
}

func imageLabel(image *imageInfo, key string) string {
	if image == nil {
		return ""
	}
	value := strings.TrimSpace(image.Config.Labels[key])
	if value == "latest" {
		return ""
	}
	return value
}

func shortRevision(revision string) string {
	if len(revision) > 12 {
		return revision[:12]
	}
	return revision
}

func sameImageID(left, right string) bool {
	return strings.TrimSpace(left) != "" && strings.TrimSpace(left) == strings.TrimSpace(right)
}

func shortImageID(id string) string {
	id = strings.TrimPrefix(strings.TrimSpace(id), "sha256:")
	if len(id) > 12 {
		return id[:12]
	}
	return id
}
