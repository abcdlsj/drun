package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os/exec"
	"sort"
	"strings"
	"time"
	"unicode"
)

var errCancelled = errors.New("operation cancelled")

type commandRunner interface {
	run(context.Context, ...string) ([]byte, error)
}

type dockerCLI struct{}

func newDockerCLI() dockerCLI {
	return dockerCLI{}
}

func (dockerCLI) run(ctx context.Context, args ...string) ([]byte, error) {
	cmd := exec.CommandContext(ctx, "docker", args...)
	// Keep terminal interrupts away from docker so drun decides when a replacement may stop.
	detachProcessGroup(cmd)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		command := shellCommand(append([]string{"docker"}, args...))
		if ctx.Err() != nil {
			return nil, fmt.Errorf("%s: %w", command, ctx.Err())
		}
		message := strings.TrimSpace(stderr.String())
		if message == "" {
			message = strings.TrimSpace(stdout.String())
		}
		if message == "" {
			return nil, fmt.Errorf("%s: %w", command, err)
		}
		return nil, fmt.Errorf("%s: %w: %s", command, err, message)
	}
	return stdout.Bytes(), nil
}

type noticeKind int

const (
	noticeInfo noticeKind = iota
	noticeWarning
	noticeSuccess
)

// stepFunc runs one named stage of work and reports its progress.
type stepFunc func(title string, action func() error) error

type terminal interface {
	interactive() bool
	notice(noticeKind, string)
	step(title string, action func() error) error

	// Single-container refresh.
	target(*containerInfo)
	plan(update *updateCandidate, command []string)
	confirm(question string) (bool, error)

	// Scanning every running container.
	rows([]containerRow)
	row(containerRow)
	scanned([]containerRow)
	selectUpdates([]containerRow) ([]string, error)
}

type application struct {
	docker commandRunner
	ui     terminal
	now    func() time.Time
}

type refreshOptions struct {
	containerName string
	yes           bool
	dryRun        bool
}

type checkOptions struct {
	yes bool
}

func newApplication(docker commandRunner, ui terminal) *application {
	return &application{docker: docker, ui: ui, now: time.Now}
}

func (a *application) refresh(ctx context.Context, options refreshOptions) error {
	var info *containerInfo
	var currentImage *imageInfo
	if err := a.ui.step("Inspecting "+options.containerName, func() error {
		var err error
		info, err = a.inspect(ctx, options.containerName)
		if err != nil {
			return err
		}
		currentImage, err = a.inspectImage(ctx, info.Image)
		return err
	}); err != nil {
		return fmt.Errorf("inspect container: %w", err)
	}
	a.ui.target(info)
	if info.HostConfig.AutoRemove {
		return errors.New("containers created with --rm cannot be replaced safely")
	}

	compose, isCompose, err := composeServiceFromContainer(info)
	if err != nil {
		return err
	}
	var replacementArgs []string
	if isCompose {
		replacementArgs = compose.upArgs()
	} else {
		replacementArgs = buildRunArgs(info, &currentImage.Config)
	}

	if options.dryRun {
		a.ui.plan(nil, append([]string{"docker"}, replacementArgs...))
		a.ui.notice(noticeInfo, "Dry run: the container was not changed")
		return nil
	}

	var latestImage *imageInfo
	if err := a.ui.step("Pulling "+info.Config.Image, func() error {
		var err error
		latestImage, err = a.pullImage(ctx, info.Config.Image)
		return err
	}); err != nil {
		if ctx.Err() != nil {
			a.ui.notice(noticeWarning, "Interrupted; the container was not changed")
			return errInterrupted
		}
		return fmt.Errorf("pull image %q: %w", info.Config.Image, err)
	}
	name := containerName(info)
	if sameImageID(info.Image, latestImage.ID) {
		a.ui.notice(noticeSuccess, fmt.Sprintf("%s is up to date (%s)", name, displayImageIdentity(currentImage)))
		return nil
	}

	candidate := newUpdateCandidate([]*containerInfo{info}, currentImage, latestImage)
	a.ui.plan(&candidate, append([]string{"docker"}, replacementArgs...))

	if !options.yes {
		confirmed, err := a.ui.confirm(fmt.Sprintf("Replace %s with the new image?", name))
		if err != nil {
			return fmt.Errorf("read confirmation: %w", err)
		}
		if !confirmed {
			a.ui.notice(noticeWarning, "Cancelled; the original container is untouched")
			return errCancelled
		}
	}

	if isCompose {
		err = a.replaceCompose(ctx, replacementArgs, []string{name}, latestImage.ID, a.ui.step)
	} else {
		err = a.replace(ctx, info, replacementArgs, backupContainerName(name, a.now()), latestImage.ID, a.ui.step)
	}
	if err != nil {
		return err
	}
	a.ui.notice(noticeSuccess, fmt.Sprintf("Updated %s %s → %s", name, candidate.CurrentVersion, candidate.LatestVersion))
	return nil
}

func (a *application) pullImage(ctx context.Context, reference string) (*imageInfo, error) {
	if _, err := a.docker.run(ctx, "pull", "--quiet", reference); err != nil {
		return nil, err
	}
	return a.inspectImage(ctx, reference)
}

// replaceCompose recreates a Compose service and verifies every known replica.
// It runs to completion even after an interrupt, because Compose cannot be stopped halfway safely.
func (a *application) replaceCompose(ctx context.Context, args, replicas []string, expectedImageID string, step stepFunc) error {
	ctx = context.WithoutCancel(ctx)
	return step("Recreating the Compose service", func() error {
		if _, err := a.docker.run(ctx, args...); err != nil {
			return err
		}
		for _, name := range replicas {
			replacement, err := a.inspect(ctx, name)
			if err != nil {
				return fmt.Errorf("verify %s: %w", name, err)
			}
			if !sameImageID(replacement.Image, expectedImageID) {
				return fmt.Errorf("%s uses image %s, expected %s", name, shortImageID(replacement.Image), shortImageID(expectedImageID))
			}
		}
		return nil
	})
}

func (a *application) inspect(ctx context.Context, name string) (*containerInfo, error) {
	containers, err := a.inspectMany(ctx, []string{name})
	if err != nil {
		return nil, err
	}
	return containers[0], nil
}

func (a *application) inspectMany(ctx context.Context, names []string) ([]*containerInfo, error) {
	output, err := a.docker.run(ctx, append([]string{"inspect", "--type", "container"}, names...)...)
	if err != nil {
		return nil, err
	}
	var containers []*containerInfo
	if err := json.Unmarshal(output, &containers); err != nil {
		return nil, fmt.Errorf("decode docker inspect output: %w", err)
	}
	if len(containers) != len(names) {
		return nil, errors.New("container not found")
	}
	return containers, nil
}

func (a *application) inspectImage(ctx context.Context, reference string) (*imageInfo, error) {
	if reference == "" {
		return nil, errors.New("container image ID is empty")
	}
	images, err := a.inspectImages(ctx, []string{reference})
	if err != nil {
		return nil, err
	}
	return images[0], nil
}

func (a *application) inspectImages(ctx context.Context, references []string) ([]*imageInfo, error) {
	output, err := a.docker.run(ctx, append([]string{"image", "inspect"}, references...)...)
	if err != nil {
		return nil, err
	}
	var images []*imageInfo
	if err := json.Unmarshal(output, &images); err != nil {
		return nil, fmt.Errorf("decode docker image inspect output: %w", err)
	}
	if len(images) != len(references) {
		return nil, errors.New("image not found")
	}
	return images, nil
}

// replace swaps a standalone container for one built from runArgs, keeping the
// original as a rollback point. Once started it ignores cancellation so an
// interrupt can never leave the container stopped or renamed.
func (a *application) replace(ctx context.Context, info *containerInfo, runArgs []string, backupName, expectedImageID string, step stepFunc) error {
	ctx = context.WithoutCancel(ctx)
	name := containerName(info)
	if info.State.Running {
		if err := step("Stopping the current container", func() error {
			_, err := a.docker.run(ctx, "stop", name)
			return err
		}); err != nil {
			return fmt.Errorf("stop current container: %w", err)
		}
	}

	if err := step("Creating a rollback point", func() error {
		_, err := a.docker.run(ctx, "rename", name, backupName)
		return err
	}); err != nil {
		if info.State.Running {
			_, _ = a.docker.run(ctx, "start", name)
		}
		return fmt.Errorf("rename current container: %w", err)
	}

	replaceErr := step("Starting the replacement", func() error {
		if _, err := a.docker.run(ctx, runArgs...); err != nil {
			return err
		}
		replacement, err := a.inspect(ctx, name)
		if err != nil {
			return fmt.Errorf("verify replacement: %w", err)
		}
		if !sameImageID(replacement.Image, expectedImageID) {
			return fmt.Errorf("replacement uses image %s, expected %s", shortImageID(replacement.Image), shortImageID(expectedImageID))
		}
		return a.connectAdditionalNetworks(ctx, info)
	})
	if replaceErr != nil {
		rollbackErr := step("Restoring the original container", func() error {
			return a.rollback(ctx, name, backupName, info.State.Running)
		})
		if rollbackErr != nil {
			return errors.Join(fmt.Errorf("start replacement: %w", replaceErr), fmt.Errorf("rollback failed: %w", rollbackErr))
		}
		return fmt.Errorf("start replacement (original container restored): %w", replaceErr)
	}

	if err := step("Removing the rollback point", func() error {
		_, err := a.docker.run(ctx, "rm", backupName)
		return err
	}); err != nil {
		a.ui.notice(noticeWarning, fmt.Sprintf("%s was updated, but backup %s could not be removed: %s", name, backupName, conciseError(err)))
	}
	return nil
}

func (a *application) rollback(ctx context.Context, name, backupName string, wasRunning bool) error {
	// docker run can fail before creating the replacement, so this removal may find nothing.
	_, removeErr := a.docker.run(ctx, "rm", "-f", name)
	if _, err := a.docker.run(ctx, "rename", backupName, name); err != nil {
		if removeErr != nil {
			return errors.Join(err, removeErr)
		}
		return err
	}
	if wasRunning {
		if _, err := a.docker.run(ctx, "start", name); err != nil {
			return err
		}
	}
	return nil
}

func (a *application) connectAdditionalNetworks(ctx context.Context, info *containerInfo) error {
	primary := primaryNetwork(info.HostConfig.NetworkMode)
	networkNames := make([]string, 0, len(info.NetworkSettings.Networks))
	for name := range info.NetworkSettings.Networks {
		if name != primary && !(primary == "" && name == "bridge") {
			networkNames = append(networkNames, name)
		}
	}
	sort.Strings(networkNames)

	name := containerName(info)
	for _, networkName := range networkNames {
		args := []string{"network", "connect"}
		for _, alias := range userNetworkAliases(info, networkName) {
			args = append(args, "--alias", alias)
		}
		args = appendStaticAddresses(args, info, networkName, "--ip", "--ip6")
		args = append(args, networkName, name)
		if _, err := a.docker.run(ctx, args...); err != nil {
			return fmt.Errorf("connect network %q: %w", networkName, err)
		}
	}
	return nil
}

func backupContainerName(name string, now time.Time) string {
	return fmt.Sprintf("%s-drun-backup-%d", name, now.Unix())
}

func containerName(info *containerInfo) string {
	return strings.TrimPrefix(info.Name, "/")
}
func shellCommand(args []string) string {
	quoted := make([]string, len(args))
	for i, arg := range args {
		quoted[i] = shellQuote(arg)
	}
	return strings.Join(quoted, " ")
}

func shellQuote(value string) string {
	if value != "" && strings.IndexFunc(value, func(r rune) bool {
		return !(unicode.IsLetter(r) || unicode.IsDigit(r) || strings.ContainsRune("_@%+=:,./-", r))
	}) == -1 {
		return value
	}
	return "'" + strings.ReplaceAll(value, "'", "'\"'\"'") + "'"
}
