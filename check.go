package main

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
)

const maxConcurrentImageChecks = 8

type containerSnapshot struct {
	info  *containerInfo
	image *imageInfo
}

func (a *application) check(ctx context.Context, options checkOptions) error {
	a.ui.header("Container updates")

	names, err := a.listRunningContainers(ctx)
	if err != nil {
		return fmt.Errorf("list running containers: %w", err)
	}
	if len(names) == 0 {
		a.ui.success("No running containers found")
		return nil
	}

	var snapshots []containerSnapshot
	checkFailures := 0
	var warnings []string
	currentImages := make(map[string]*imageInfo)
	if err := a.ui.spin(fmt.Sprintf("Inspecting %d running containers", len(names)), func() error {
		for _, name := range names {
			info, inspectErr := a.inspect(ctx, name)
			if inspectErr != nil {
				checkFailures++
				warnings = append(warnings, fmt.Sprintf("Could not inspect %s: %s", name, conciseError(inspectErr)))
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
					checkFailures++
					warnings = append(warnings, fmt.Sprintf("Could not inspect the image for %s: %s", name, conciseError(inspectErr)))
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

	imageReferences := make(map[string]bool)
	skippedReferences := make(map[string]bool)
	for _, snapshot := range snapshots {
		reference := snapshot.info.Config.Image
		if remotelyUpdatableImage(reference, snapshot.image) {
			imageReferences[reference] = true
		} else {
			skippedReferences[reference] = true
		}
	}
	if len(skippedReferences) > 0 {
		label := "images"
		if len(skippedReferences) == 1 {
			label = "image"
		}
		a.ui.info(fmt.Sprintf("Skipped %d local or pinned %s", len(skippedReferences), label))
	}
	references := sortedKeys(imageReferences)
	latestImages := make(map[string]*imageInfo, len(references))
	imageErrors := make(map[string]error)
	if len(references) > 0 {
		checkMessage := fmt.Sprintf("Checking %d images", len(references))
		if len(references) == 1 {
			checkMessage = "Checking " + references[0]
		}
		if err := a.ui.spin(checkMessage, func() error {
			latestImages, imageErrors = a.checkLatestImages(ctx, references)
			return nil
		}); err != nil {
			return err
		}
	}
	for _, reference := range references {
		if err := imageErrors[reference]; err != nil {
			checkFailures++
			a.ui.warning(fmt.Sprintf("Could not check %s: %s", reference, conciseError(err)))
		}
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
		if checkFailures > 0 {
			return newCheckSummaryError(checkFailures, 0)
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
		return newCheckSummaryError(checkFailures, 0)
	} else {
		selected, err = a.ui.selectUpdates(updates)
		if err != nil {
			return fmt.Errorf("select containers: %w", err)
		}
	}
	if len(selected) == 0 {
		a.ui.warning("No containers selected")
		return newCheckSummaryError(checkFailures, 0)
	}

	updated := 0
	alreadyCurrent := 0
	updateFailures := 0
	for index, candidate := range selected {
		a.ui.group(fmt.Sprintf("%s  %d/%d", candidate.Name, index+1, len(selected)))
		changed, err := a.updateCheckedContainer(ctx, candidate)
		if err != nil {
			updateFailures++
			a.ui.warning("Update failed: " + conciseError(err))
			a.ui.endGroup()
			continue
		}
		if !changed {
			alreadyCurrent++
			a.ui.info("Already updated")
			a.ui.endGroup()
			continue
		}
		updated++
		a.ui.success(fmt.Sprintf("Updated %s → %s", candidate.CurrentVersion, candidate.LatestVersion))
		a.ui.endGroup()
	}

	switch {
	case updated == len(selected):
		a.ui.success(fmt.Sprintf("Updated all %d selected containers", updated))
	case alreadyCurrent == len(selected):
		a.ui.success(fmt.Sprintf("All %d selected containers were already current", alreadyCurrent))
	case updated+alreadyCurrent == len(selected):
		a.ui.success(fmt.Sprintf("Updated %d containers; %d were already current", updated, alreadyCurrent))
	default:
		a.ui.warning(fmt.Sprintf("Updated %d of %d selected containers", updated, len(selected)))
	}
	return newCheckSummaryError(checkFailures, updateFailures)
}

type checkSummaryError struct {
	checkFailures  int
	updateFailures int
}

func newCheckSummaryError(checkFailures, updateFailures int) error {
	if checkFailures == 0 && updateFailures == 0 {
		return nil
	}
	return checkSummaryError{checkFailures: checkFailures, updateFailures: updateFailures}
}

func (err checkSummaryError) Error() string {
	parts := make([]string, 0, 2)
	if err.checkFailures > 0 {
		if err.checkFailures == 1 {
			parts = append(parts, "1 update check failed")
		} else {
			parts = append(parts, fmt.Sprintf("%d update checks failed", err.checkFailures))
		}
	}
	if err.updateFailures > 0 {
		if err.updateFailures == 1 {
			parts = append(parts, "1 container update failed")
		} else {
			parts = append(parts, fmt.Sprintf("%d container updates failed", err.updateFailures))
		}
	}
	return strings.Join(parts, "; ")
}

func conciseError(err error) string {
	message := strings.TrimSpace(err.Error())
	if lines := strings.Split(message, "\n"); len(lines) > 0 {
		message = strings.TrimSpace(lines[len(lines)-1])
	}
	message = strings.TrimPrefix(message, "Error response from daemon: ")
	if strings.Contains(message, "exit status") {
		if separator := strings.LastIndex(message, ": "); separator >= 0 {
			message = message[separator+2:]
		}
	}
	runes := []rune(message)
	if len(runes) > 180 {
		message = string(runes[:179]) + "…"
	}
	return message
}

func remotelyUpdatableImage(reference string, image *imageInfo) bool {
	reference = strings.TrimSpace(reference)
	if reference == "" || strings.HasPrefix(reference, "sha256:") || strings.Contains(reference, "@") || image == nil {
		return false
	}
	repository := normalizedRepository(reference)
	for _, digest := range image.RepoDigests {
		digestRepository, _, found := strings.Cut(digest, "@")
		if found && normalizedRepository(digestRepository) == repository {
			return true
		}
	}
	return false
}

func normalizedRepository(reference string) string {
	reference, _, _ = strings.Cut(reference, "@")
	lastSlash := strings.LastIndex(reference, "/")
	if colon := strings.LastIndex(reference, ":"); colon > lastSlash {
		reference = reference[:colon]
	}
	reference = strings.TrimPrefix(reference, "index.docker.io/")
	reference = strings.TrimPrefix(reference, "docker.io/")
	return strings.TrimPrefix(reference, "library/")
}

type imageCheckResult struct {
	reference string
	image     *imageInfo
	err       error
}

func (a *application) checkLatestImages(ctx context.Context, references []string) (map[string]*imageInfo, map[string]error) {
	latestImages := make(map[string]*imageInfo, len(references))
	imageErrors := make(map[string]error)
	if len(references) == 0 {
		return latestImages, imageErrors
	}

	jobs := make(chan string, len(references))
	results := make(chan imageCheckResult, len(references))
	for _, reference := range references {
		jobs <- reference
	}
	close(jobs)

	workerCount := min(maxConcurrentImageChecks, len(references))
	for range workerCount {
		go func() {
			for reference := range jobs {
				if _, err := a.docker.run(ctx, "pull", reference); err != nil {
					results <- imageCheckResult{reference: reference, err: err}
					continue
				}
				image, err := a.inspectImage(ctx, reference)
				results <- imageCheckResult{reference: reference, image: image, err: err}
			}
		}()
	}

	for range references {
		result := <-results
		if result.err != nil {
			imageErrors[result.reference] = result.err
			continue
		}
		latestImages[result.reference] = result.image
	}
	return latestImages, imageErrors
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

func (a *application) updateCheckedContainer(ctx context.Context, candidate updateCandidate) (bool, error) {
	info, err := a.inspect(ctx, candidate.Name)
	if err != nil {
		return false, err
	}
	if !info.State.Running {
		return false, errors.New("container is no longer running")
	}
	if sameImageID(info.Image, candidate.LatestImageID) {
		return false, nil
	}
	if !sameImageID(info.Image, candidate.CurrentImageID) {
		return false, fmt.Errorf("container image changed during selection (%s -> %s)", shortImageID(candidate.CurrentImageID), shortImageID(info.Image))
	}
	currentImage, err := a.inspectImage(ctx, info.Image)
	if err != nil {
		return false, err
	}
	compose, isCompose, err := composeServiceFromContainer(info)
	if err != nil {
		return false, err
	}
	if isCompose {
		args := compose.upArgs()
		return true, a.replaceCompose(ctx, info, args, candidate.LatestImageID)
	}
	runArgs := buildRunArgs(info, &currentImage.Config)
	backupName := backupContainerName(candidate.Name, a.now())
	return true, a.replace(ctx, info, runArgs, backupName, candidate.LatestImageID)
}

func newUpdateCandidate(info *containerInfo, current, latest *imageInfo) updateCandidate {
	currentVersion, latestVersion := displayImageVersions(current, latest)
	return updateCandidate{
		Name:           strings.TrimPrefix(info.Name, "/"),
		Image:          info.Config.Image,
		Compose:        isComposeContainer(info),
		CurrentImageID: info.Image,
		LatestImageID:  latest.ID,
		CurrentVersion: currentVersion,
		LatestVersion:  latestVersion,
	}
}

func isComposeContainer(info *containerInfo) bool {
	labels := info.Config.Labels
	return strings.TrimSpace(labels[composeProjectLabel]) != "" && strings.TrimSpace(labels[composeServiceLabel]) != ""
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
