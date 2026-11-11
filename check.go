package main

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
)

const maxConcurrentImageChecks = 8

var errInterrupted = errors.New("interrupted; no further changes were made")

type rowState int

const (
	rowChecking rowState = iota
	rowUpdatable
	rowCurrent
	rowSkipped
	rowFailed
	rowQueued
	rowUpdating
	rowUpdated
	rowUnchanged
	rowUpdateFailed
	rowNotStarted
)

// containerRow is one line of the scan: a standalone container or a Compose service.
type containerRow struct {
	key      string
	name     string
	image    string
	compose  bool
	replicas int
	state    rowState
	detail   string
	update   *updateCandidate
}

// containerCount is the number of running containers the row stands for.
func (row containerRow) containerCount() int {
	return max(1, row.replicas)
}

func (row containerRow) inUpdatePhase() bool {
	return row.state >= rowQueued
}

type updateCandidate struct {
	Name           string
	Image          string
	Compose        bool
	Replicas       []string
	CurrentImageID string
	LatestImageID  string
	CurrentVersion string
	LatestVersion  string
}

// scanUnit groups the containers that are updated together: one standalone
// container, or every replica of a Compose service.
type scanUnit struct {
	row        *containerRow
	containers []*containerInfo
	images     []*imageInfo
	reference  string
}

func (a *application) check(ctx context.Context, options checkOptions) error {
	names, err := a.listRunningContainers(ctx)
	if err != nil {
		return fmt.Errorf("list running containers: %w", err)
	}
	if len(names) == 0 {
		a.ui.notice(noticeSuccess, "No running containers found")
		return nil
	}

	var rows []*containerRow
	var units []*scanUnit
	_ = a.ui.step(fmt.Sprintf("Inspecting %d running containers", len(names)), func() error {
		rows, units = a.inspectForScan(ctx, names)
		return nil
	})
	a.ui.rows(rowValues(rows))

	byReference := make(map[string][]*scanUnit)
	for _, unit := range units {
		byReference[unit.reference] = append(byReference[unit.reference], unit)
	}
	a.checkLatestImages(ctx, sortedKeys(byReference), func(result imageCheckResult) {
		for _, unit := range byReference[result.reference] {
			unit.resolve(result)
			a.ui.row(*unit.row)
		}
	})
	if ctx.Err() != nil {
		a.ui.notice(noticeWarning, "Interrupted while checking; nothing was changed")
		return errInterrupted
	}
	a.ui.scanned(rowValues(rows))

	checkFailures := 0
	var updates []*containerRow
	for _, row := range rows {
		switch row.state {
		case rowFailed:
			checkFailures++
		case rowUpdatable:
			updates = append(updates, row)
		}
	}
	if len(updates) == 0 {
		if checkFailures > 0 {
			return newCheckSummaryError(checkFailures, 0)
		}
		a.ui.notice(noticeSuccess, fmt.Sprintf("All %d running containers are up to date", len(names)))
		return nil
	}

	var selectedKeys []string
	switch {
	case options.yes:
		for _, row := range updates {
			selectedKeys = append(selectedKeys, row.key)
		}
	case !a.ui.interactive():
		a.ui.notice(noticeInfo, "Run drun --yes to update every listed container")
		return newCheckSummaryError(checkFailures, 0)
	default:
		selectedKeys, err = a.ui.selectUpdates(rowValues(rows))
		if err != nil {
			return fmt.Errorf("select containers: %w", err)
		}
	}
	selected := selectRows(updates, selectedKeys)
	if len(selected) == 0 {
		a.ui.notice(noticeWarning, "No containers selected")
		return newCheckSummaryError(checkFailures, 0)
	}

	for _, row := range selected {
		row.state = rowQueued
		row.detail = "queued"
		a.ui.row(*row)
	}
	updated, unchanged, updateFailures, notStarted := 0, 0, 0, 0
	for _, row := range selected {
		if ctx.Err() != nil {
			notStarted++
			row.state, row.detail = rowNotStarted, "not started"
			a.ui.row(*row)
			continue
		}
		row.state, row.detail = rowUpdating, "Checking current state"
		a.ui.row(*row)
		step := func(title string, action func() error) error {
			row.detail = title
			a.ui.row(*row)
			return action()
		}
		changed, err := a.updateCheckedContainer(ctx, *row.update, step)
		switch {
		case err != nil:
			updateFailures++
			row.state, row.detail = rowUpdateFailed, updateFailureDetail(err)
		case !changed:
			unchanged++
			row.state, row.detail = rowUnchanged, "already up to date"
		default:
			updated++
			row.state, row.detail = rowUpdated, row.update.CurrentVersion+" → "+row.update.LatestVersion
		}
		a.ui.row(*row)
	}

	total := len(selected)
	switch {
	case notStarted > 0:
		a.ui.notice(noticeWarning, fmt.Sprintf("Stopped after %d of %d; %d not started", total-notStarted, total, notStarted))
		return errInterrupted
	case updated == total:
		a.ui.notice(noticeSuccess, fmt.Sprintf("Updated %s", plural(updated, "container")))
	case unchanged == total:
		a.ui.notice(noticeSuccess, fmt.Sprintf("All %s were already up to date", plural(unchanged, "selected container")))
	case updateFailures == 0:
		a.ui.notice(noticeSuccess, fmt.Sprintf("Updated %s; %d already up to date", plural(updated, "container"), unchanged))
	default:
		a.ui.notice(noticeWarning, fmt.Sprintf("Updated %d of %d selected", updated, total))
	}
	return newCheckSummaryError(checkFailures, updateFailures)
}

// inspectForScan inspects containers and their images in batches, classifying
// every container as failed, skipped, or still to be checked against its registry.
func (a *application) inspectForScan(ctx context.Context, names []string) ([]*containerRow, []*scanUnit) {
	var rows []*containerRow
	fail := func(name, image string, err error) {
		rows = append(rows, &containerRow{key: "container:" + name, name: name, image: image, state: rowFailed, detail: conciseError(err)})
	}
	skip := func(name, image, reason string) {
		rows = append(rows, &containerRow{key: "container:" + name, name: name, image: image, state: rowSkipped, detail: reason})
	}

	infos, err := a.inspectMany(ctx, names)
	if err != nil {
		// A container may have exited since docker ps; fall back to one call per container.
		infos = infos[:0]
		for _, name := range names {
			info, inspectErr := a.inspect(ctx, name)
			if inspectErr != nil {
				fail(name, "", inspectErr)
				continue
			}
			infos = append(infos, info)
		}
	}

	images, imageErrors := a.inspectImagesOf(ctx, infos)
	unitsByKey := make(map[string]*scanUnit)
	var units []*scanUnit
	for _, info := range infos {
		name, reference := containerName(info), info.Config.Image
		if info.HostConfig.AutoRemove {
			skip(name, reference, "created with --rm")
			continue
		}
		if err := imageErrors[info.Image]; err != nil {
			fail(name, reference, err)
			continue
		}
		image := images[info.Image]
		compose, isCompose, err := composeServiceFromContainer(info)
		if err != nil {
			skip(name, reference, err.Error())
			continue
		}
		if reason := localImageReason(reference, image); reason != "" {
			skip(name, reference, reason)
			continue
		}

		key := "container:" + name
		if isCompose {
			key = "compose:" + compose.project + "/" + compose.service + "/" + reference
		}
		if unit, ok := unitsByKey[key]; ok {
			unit.containers = append(unit.containers, info)
			unit.images = append(unit.images, image)
			unit.row.replicas++
			continue
		}
		row := &containerRow{key: key, name: name, image: reference, compose: isCompose, replicas: 1, state: rowChecking, detail: "checking"}
		unit := &scanUnit{row: row, containers: []*containerInfo{info}, images: []*imageInfo{image}, reference: reference}
		unitsByKey[key] = unit
		units = append(units, unit)
		rows = append(rows, row)
	}
	sort.SliceStable(rows, func(i, j int) bool { return rows[i].name < rows[j].name })
	return rows, units
}

func (a *application) inspectImagesOf(ctx context.Context, infos []*containerInfo) (map[string]*imageInfo, map[string]error) {
	ids := make(map[string]bool)
	for _, info := range infos {
		if !info.HostConfig.AutoRemove {
			ids[info.Image] = true
		}
	}
	references := sortedKeys(ids)
	images := make(map[string]*imageInfo, len(references))
	imageErrors := make(map[string]error)
	if len(references) == 0 {
		return images, imageErrors
	}
	if batch, err := a.inspectImages(ctx, references); err == nil {
		for index, reference := range references {
			images[reference] = batch[index]
		}
		return images, imageErrors
	}
	for _, reference := range references {
		image, err := a.inspectImage(ctx, reference)
		if err != nil {
			imageErrors[reference] = err
			continue
		}
		images[reference] = image
	}
	return images, imageErrors
}

func (unit *scanUnit) resolve(result imageCheckResult) {
	row := unit.row
	if result.err != nil {
		row.state, row.detail = rowFailed, conciseError(result.err)
		return
	}
	var current *imageInfo
	for index, info := range unit.containers {
		if !sameImageID(info.Image, result.image.ID) {
			current = unit.images[index]
			break
		}
	}
	if current == nil {
		row.state, row.detail = rowCurrent, displayImageIdentity(result.image)
		return
	}
	candidate := newUpdateCandidate(unit.containers, current, result.image)
	row.state, row.detail, row.update = rowUpdatable, candidate.CurrentVersion+" → "+candidate.LatestVersion, &candidate
}

// updateFailureDetail keeps the rollback outcome visible next to the cause.
func updateFailureDetail(err error) string {
	message := err.Error()
	switch {
	case strings.Contains(message, "rollback failed"):
		return "rollback failed: " + conciseError(err)
	case strings.Contains(message, "original container restored"):
		return "restored original: " + conciseError(err)
	}
	return conciseError(err)
}

func rowValues(rows []*containerRow) []containerRow {
	values := make([]containerRow, len(rows))
	for index, row := range rows {
		values[index] = *row
	}
	return values
}

func selectRows(rows []*containerRow, keys []string) []*containerRow {
	wanted := make(map[string]bool, len(keys))
	for _, key := range keys {
		wanted[key] = true
	}
	var selected []*containerRow
	for _, row := range rows {
		if wanted[row.key] {
			selected = append(selected, row)
		}
	}
	return selected
}

func plural(count int, noun string) string {
	if count == 1 {
		return "1 " + noun
	}
	return fmt.Sprintf("%d %ss", count, noun)
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

// localImageReason explains why an image cannot be refreshed from a registry, or returns "".
func localImageReason(reference string, image *imageInfo) string {
	reference = strings.TrimSpace(reference)
	if strings.HasPrefix(reference, "sha256:") || strings.Contains(reference, "@") {
		return "pinned by digest"
	}
	if !remotelyUpdatableImage(reference, image) {
		return "local image"
	}
	return ""
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

// checkLatestImages pulls each reference with bounded concurrency and reports
// every result on the calling goroutine as soon as it arrives.
func (a *application) checkLatestImages(ctx context.Context, references []string, onResult func(imageCheckResult)) {
	if len(references) == 0 {
		return
	}
	jobs := make(chan string, len(references))
	results := make(chan imageCheckResult, len(references))
	for _, reference := range references {
		jobs <- reference
	}
	close(jobs)

	for range min(maxConcurrentImageChecks, len(references)) {
		go func() {
			for reference := range jobs {
				image, err := a.pullImage(ctx, reference)
				results <- imageCheckResult{reference: reference, image: image, err: err}
			}
		}()
	}
	for range references {
		onResult(<-results)
	}
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

// updateCheckedContainer re-validates a candidate picked during the scan and
// replaces it. It reports false when every replica already runs the latest image.
func (a *application) updateCheckedContainer(ctx context.Context, candidate updateCandidate, step stepFunc) (bool, error) {
	replicas := candidate.Replicas
	if len(replicas) == 0 {
		replicas = []string{candidate.Name}
	}
	infos, err := a.inspectMany(ctx, replicas)
	if err != nil {
		return false, err
	}
	outdated := false
	for _, info := range infos {
		if !info.State.Running {
			return false, fmt.Errorf("%s is no longer running", containerName(info))
		}
		if sameImageID(info.Image, candidate.LatestImageID) {
			continue
		}
		if !sameImageID(info.Image, candidate.CurrentImageID) {
			return false, fmt.Errorf("%s image changed during selection (%s -> %s)", containerName(info), shortImageID(candidate.CurrentImageID), shortImageID(info.Image))
		}
		outdated = true
	}
	if !outdated {
		return false, nil
	}

	info := infos[0]
	compose, isCompose, err := composeServiceFromContainer(info)
	if err != nil {
		return false, err
	}
	if isCompose {
		return true, a.replaceCompose(ctx, compose.upArgs(), replicas, candidate.LatestImageID, step)
	}
	currentImage, err := a.inspectImage(ctx, info.Image)
	if err != nil {
		return false, err
	}
	runArgs := buildRunArgs(info, &currentImage.Config)
	return true, a.replace(ctx, info, runArgs, backupContainerName(candidate.Name, a.now()), candidate.LatestImageID, step)
}

func newUpdateCandidate(infos []*containerInfo, current, latest *imageInfo) updateCandidate {
	currentVersion, latestVersion := displayImageVersions(current, latest)
	replicas := make([]string, len(infos))
	for index, info := range infos {
		replicas[index] = containerName(info)
	}
	return updateCandidate{
		Name:           replicas[0],
		Image:          infos[0].Config.Image,
		Compose:        isComposeContainer(infos[0]),
		Replicas:       replicas,
		CurrentImageID: current.ID,
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
