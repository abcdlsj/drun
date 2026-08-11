package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os/exec"
	"slices"
	"sort"
	"strconv"
	"strings"
	"time"
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
	output, err := cmd.CombinedOutput()
	if err != nil {
		command := shellCommand(append([]string{"docker"}, args...))
		message := strings.TrimSpace(string(output))
		if message == "" {
			return nil, fmt.Errorf("%s: %w", command, err)
		}
		return nil, fmt.Errorf("%s: %w: %s", command, err, message)
	}
	return output, nil
}

type terminal interface {
	header(string)
	group(string)
	endGroup()
	info(string)
	warning(string)
	success(string)
	command([]string)
	summary(*containerInfo)
	updateAvailable(updateCandidate)
	listUpdates([]updateCandidate)
	selectUpdates([]updateCandidate) ([]updateCandidate, error)
	isInteractive() bool
	confirm(string) (bool, error)
	spin(string, func() error) error
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
	a.ui.header("Safe container refresh")

	var info *containerInfo
	var currentImage *imageInfo
	if err := a.ui.spin("Inspecting container configuration", func() error {
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
		a.ui.summary(info)
		a.ui.command(append([]string{"docker"}, replacementArgs...))
		a.ui.info("Dry run complete; the container was not changed")
		return nil
	}

	var latestImage *imageInfo
	if err := a.ui.spin("Checking the latest image", func() error {
		if _, err := a.docker.run(ctx, "pull", info.Config.Image); err != nil {
			return err
		}
		var err error
		latestImage, err = a.inspectImage(ctx, info.Config.Image)
		return err
	}); err != nil {
		return fmt.Errorf("pull image %q: %w", info.Config.Image, err)
	}
	if sameImageID(info.Image, latestImage.ID) {
		a.ui.success(fmt.Sprintf("Container %s is already up to date (%s)", strings.TrimPrefix(info.Name, "/"), displayImageIdentity(currentImage)))
		return nil
	}

	candidate := newUpdateCandidate(info, currentImage, latestImage)
	a.ui.updateAvailable(candidate)
	a.ui.command(append([]string{"docker"}, replacementArgs...))

	if !options.yes {
		confirmed, err := a.ui.confirm("Replace the container with this configuration?")
		if err != nil {
			return fmt.Errorf("read confirmation: %w", err)
		}
		if !confirmed {
			a.ui.warning("Cancelled; the original container is untouched")
			return errCancelled
		}
	}

	if isCompose {
		if err := a.replaceCompose(ctx, info, replacementArgs, latestImage.ID); err != nil {
			return err
		}
	} else {
		backupName := backupContainerName(strings.TrimPrefix(info.Name, "/"), a.now())
		if err := a.replace(ctx, info, replacementArgs, backupName, latestImage.ID); err != nil {
			return err
		}
	}

	a.ui.success(fmt.Sprintf("Container %s updated %s -> %s", strings.TrimPrefix(info.Name, "/"), candidate.CurrentVersion, candidate.LatestVersion))
	return nil
}

func (a *application) replaceCompose(ctx context.Context, info *containerInfo, args []string, expectedImageID string) error {
	name := strings.TrimPrefix(info.Name, "/")
	return a.ui.spin("Recreating the Docker Compose service", func() error {
		if _, err := a.docker.run(ctx, args...); err != nil {
			return err
		}
		replacement, err := a.inspect(ctx, name)
		if err != nil {
			return fmt.Errorf("verify replacement: %w", err)
		}
		if !sameImageID(replacement.Image, expectedImageID) {
			return fmt.Errorf("replacement uses image %s, expected %s", shortImageID(replacement.Image), shortImageID(expectedImageID))
		}
		return nil
	})
}

func (a *application) inspect(ctx context.Context, name string) (*containerInfo, error) {
	output, err := a.docker.run(ctx, "inspect", name)
	if err != nil {
		return nil, err
	}

	var containers []containerInfo
	if err := json.Unmarshal(output, &containers); err != nil {
		return nil, fmt.Errorf("decode docker inspect output: %w", err)
	}
	if len(containers) == 0 {
		return nil, errors.New("container not found")
	}
	return &containers[0], nil
}

func (a *application) inspectImage(ctx context.Context, reference string) (*imageInfo, error) {
	if reference == "" {
		return nil, errors.New("container image ID is empty")
	}
	output, err := a.docker.run(ctx, "image", "inspect", reference)
	if err != nil {
		return nil, err
	}

	var images []imageInfo
	if err := json.Unmarshal(output, &images); err != nil {
		return nil, fmt.Errorf("decode docker image inspect output: %w", err)
	}
	if len(images) == 0 {
		return nil, errors.New("image not found")
	}
	return &images[0], nil
}

func (a *application) replace(ctx context.Context, info *containerInfo, runArgs []string, backupName, expectedImageID string) error {
	name := strings.TrimPrefix(info.Name, "/")
	if info.State.Running {
		if err := a.ui.spin("Stopping the current container", func() error {
			_, err := a.docker.run(ctx, "stop", name)
			return err
		}); err != nil {
			return fmt.Errorf("stop current container: %w", err)
		}
	}

	if err := a.ui.spin("Creating a rollback point", func() error {
		_, err := a.docker.run(ctx, "rename", name, backupName)
		return err
	}); err != nil {
		if info.State.Running {
			_, _ = a.docker.run(ctx, "start", name)
		}
		return fmt.Errorf("rename current container: %w", err)
	}

	replaceErr := a.ui.spin("Starting the replacement container", func() error {
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
		a.ui.warning("Replacement failed; restoring the original container")
		rollbackErr := a.rollback(ctx, name, backupName, info.State.Running)
		if rollbackErr != nil {
			return errors.Join(fmt.Errorf("start replacement: %w", replaceErr), fmt.Errorf("rollback failed: %w", rollbackErr))
		}
		return fmt.Errorf("start replacement (original container restored): %w", replaceErr)
	}

	if err := a.ui.spin("Removing the rollback point", func() error {
		_, err := a.docker.run(ctx, "rm", backupName)
		return err
	}); err != nil {
		a.ui.warning(fmt.Sprintf("Replacement succeeded, but backup %s could not be removed: %v", backupName, err))
	}
	return nil
}

func (a *application) rollback(ctx context.Context, name, backupName string, wasRunning bool) error {
	var rollbackErrors []error
	if _, err := a.docker.run(ctx, "rm", "-f", name); err != nil {
		// docker run can fail before creating the replacement, so this cleanup is best-effort.
		a.ui.info("No partial replacement container needed cleanup")
	}
	if _, err := a.docker.run(ctx, "rename", backupName, name); err != nil {
		rollbackErrors = append(rollbackErrors, err)
		return errors.Join(rollbackErrors...)
	}
	if wasRunning {
		if _, err := a.docker.run(ctx, "start", name); err != nil {
			rollbackErrors = append(rollbackErrors, err)
		}
	}
	return errors.Join(rollbackErrors...)
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

	containerName := strings.TrimPrefix(info.Name, "/")
	for _, networkName := range networkNames {
		args := []string{"network", "connect"}
		for _, alias := range userNetworkAliases(info, networkName) {
			args = append(args, "--alias", alias)
		}
		args = append(args, networkName, containerName)
		if _, err := a.docker.run(ctx, args...); err != nil {
			return fmt.Errorf("connect network %q: %w", networkName, err)
		}
	}
	return nil
}

func backupContainerName(name string, now time.Time) string {
	return fmt.Sprintf("%s-drun-backup-%d", name, now.Unix())
}

func buildRunArgs(info *containerInfo, imageDefaults *containerConfig) []string {
	name := strings.TrimPrefix(info.Name, "/")
	args := []string{"run", "-d", "--name", name}
	args = appendContainerResources(args, info)
	args = appendContainerConfiguration(args, info, imageDefaults)
	args = appendContainerNetworking(args, info)
	return appendContainerProcess(args, info, imageDefaults)
}

func appendContainerResources(args []string, info *containerInfo) []string {
	if policy := restartPolicyArg(info.HostConfig.RestartPolicy); policy != "" {
		args = append(args, "--restart", policy)
	}
	for _, bind := range info.HostConfig.Binds {
		args = append(args, "--volume", bind)
	}
	args = appendAnonymousVolumes(args, info)

	ports := sortedKeys(info.HostConfig.PortBindings)
	for _, port := range ports {
		for _, binding := range info.HostConfig.PortBindings[port] {
			published := binding.HostPort
			if binding.HostIP != "" {
				published = binding.HostIP + ":" + published
			}
			if published != "" {
				args = append(args, "--publish", published+":"+port)
			}
		}
	}
	return args
}

func appendContainerConfiguration(args []string, info *containerInfo, imageDefaults *containerConfig) []string {
	for _, env := range environmentOverrides(info.Config.Env, imageDefaults) {
		args = append(args, "--env", env)
	}
	for _, label := range labelOverrides(info.Config.Labels, imageDefaults) {
		args = append(args, "--label", label+"="+info.Config.Labels[label])
	}
	for _, capability := range info.HostConfig.CapAdd {
		args = append(args, "--cap-add", capability)
	}
	for _, capability := range info.HostConfig.CapDrop {
		args = append(args, "--cap-drop", capability)
	}
	for _, option := range info.HostConfig.SecurityOpt {
		args = append(args, "--security-opt", option)
	}
	for _, host := range info.HostConfig.ExtraHosts {
		args = append(args, "--add-host", host)
	}
	for _, dns := range info.HostConfig.DNS {
		args = append(args, "--dns", dns)
	}
	for _, group := range info.HostConfig.GroupAdd {
		args = append(args, "--group-add", group)
	}

	if info.Config.User != "" && (imageDefaults == nil || info.Config.User != imageDefaults.User) {
		args = append(args, "--user", info.Config.User)
	}
	if info.Config.WorkingDir != "" && (imageDefaults == nil || info.Config.WorkingDir != imageDefaults.WorkingDir) {
		args = append(args, "--workdir", info.Config.WorkingDir)
	}
	if info.HostConfig.Privileged {
		args = append(args, "--privileged")
	}
	if info.HostConfig.PublishAllPorts {
		args = append(args, "--publish-all")
	}
	if info.HostConfig.ReadonlyRootfs {
		args = append(args, "--read-only")
	}
	if info.HostConfig.Init != nil && *info.HostConfig.Init {
		args = append(args, "--init")
	}
	if info.HostConfig.ShmSize > 0 && info.HostConfig.ShmSize != 64*1024*1024 {
		args = append(args, "--shm-size", strconv.FormatInt(info.HostConfig.ShmSize, 10)+"b")
	}
	return args
}

func appendContainerNetworking(args []string, info *containerInfo) []string {
	if network := info.HostConfig.NetworkMode; network != "" && network != "default" {
		args = append(args, "--network", network)
		if primaryNetwork(network) != "" {
			for _, alias := range userNetworkAliases(info, primaryNetwork(network)) {
				args = append(args, "--network-alias", alias)
			}
		}
	}
	for _, destination := range sortedKeys(info.HostConfig.Tmpfs) {
		value := destination
		if options := info.HostConfig.Tmpfs[destination]; options != "" {
			value += ":" + options
		}
		args = append(args, "--tmpfs", value)
	}
	return args
}

func appendContainerProcess(args []string, info *containerInfo, imageDefaults *containerConfig) []string {
	entrypointOverridden := imageDefaults == nil || !slices.Equal(info.Config.Entrypoint, imageDefaults.Entrypoint)
	commandOverridden := imageDefaults == nil || !slices.Equal(info.Config.Cmd, imageDefaults.Cmd)
	if entrypointOverridden {
		entrypoint := ""
		if len(info.Config.Entrypoint) > 0 {
			entrypoint = info.Config.Entrypoint[0]
		}
		args = append(args, "--entrypoint", entrypoint)
	}
	args = append(args, info.Config.Image)
	if entrypointOverridden && len(info.Config.Entrypoint) > 1 {
		args = append(args, info.Config.Entrypoint[1:]...)
	}
	if entrypointOverridden || commandOverridden {
		args = append(args, info.Config.Cmd...)
	}
	return args
}

func environmentOverrides(containerEnv []string, imageDefaults *containerConfig) []string {
	if imageDefaults == nil {
		return containerEnv
	}
	defaults := make(map[string]string, len(imageDefaults.Env))
	for _, value := range imageDefaults.Env {
		defaults[environmentKey(value)] = value
	}
	overrides := make([]string, 0, len(containerEnv))
	for _, value := range containerEnv {
		if defaultValue, ok := defaults[environmentKey(value)]; !ok || defaultValue != value {
			overrides = append(overrides, value)
		}
	}
	return overrides
}

func environmentKey(value string) string {
	key, _, _ := strings.Cut(value, "=")
	return key
}

func labelOverrides(containerLabels map[string]string, imageDefaults *containerConfig) []string {
	labels := sortedKeys(containerLabels)
	if imageDefaults == nil {
		return labels
	}
	overrides := labels[:0]
	for _, label := range labels {
		if value, ok := imageDefaults.Labels[label]; !ok || value != containerLabels[label] {
			overrides = append(overrides, label)
		}
	}
	return overrides
}

func appendAnonymousVolumes(args []string, info *containerInfo) []string {
	boundDestinations := make(map[string]bool, len(info.HostConfig.Binds))
	for _, bind := range info.HostConfig.Binds {
		parts := strings.Split(bind, ":")
		if len(parts) >= 2 {
			destinationIndex := len(parts) - 1
			if len(parts) >= 3 && isVolumeMode(parts[len(parts)-1]) {
				destinationIndex--
			}
			boundDestinations[parts[destinationIndex]] = true
		}
	}
	for _, mount := range info.Mounts {
		if mount.Type != "volume" || mount.Name == "" || boundDestinations[mount.Destination] {
			continue
		}
		volume := mount.Name + ":" + mount.Destination
		if !mount.RW {
			volume += ":ro"
		}
		args = append(args, "--volume", volume)
	}
	return args
}

func isVolumeMode(value string) bool {
	for _, option := range strings.Split(value, ",") {
		switch option {
		case "ro", "rw", "z", "Z", "nocopy", "private", "rprivate", "shared", "rshared", "slave", "rslave", "delegated", "cached", "consistent":
		default:
			return false
		}
	}
	return value != ""
}

func restartPolicyArg(policy restartPolicy) string {
	if policy.Name == "" || policy.Name == "no" {
		return ""
	}
	if policy.Name == "on-failure" && policy.MaximumRetryCount > 0 {
		return fmt.Sprintf("%s:%d", policy.Name, policy.MaximumRetryCount)
	}
	return policy.Name
}

func primaryNetwork(mode string) string {
	switch {
	case mode == "", mode == "default":
		return "bridge"
	case mode == "bridge", mode == "host", mode == "none":
		return mode
	case strings.HasPrefix(mode, "container:"):
		return ""
	default:
		return mode
	}
}

func userNetworkAliases(info *containerInfo, networkName string) []string {
	network, ok := info.NetworkSettings.Networks[networkName]
	if !ok {
		return nil
	}
	containerName := strings.TrimPrefix(info.Name, "/")
	aliases := make([]string, 0, len(network.Aliases))
	for _, alias := range network.Aliases {
		if alias != "" && alias != containerName && alias != info.ID && alias != shortID(info.ID) {
			aliases = append(aliases, alias)
		}
	}
	return aliases
}

func shortID(id string) string {
	if len(id) > 12 {
		return id[:12]
	}
	return id
}

func sortedKeys[V any](values map[string]V) []string {
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}
