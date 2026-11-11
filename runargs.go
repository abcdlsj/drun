package main

import (
	"bytes"
	"encoding/csv"
	"fmt"
	"reflect"
	"slices"
	"sort"
	"strconv"
	"strings"
	"time"
)

func buildRunArgs(info *containerInfo, imageDefaults *containerConfig) []string {
	name := strings.TrimPrefix(info.Name, "/")
	args := []string{"run", "-d", "--name", name}
	args = appendContainerResources(args, info)
	args = appendContainerConfiguration(args, info, imageDefaults)
	args = appendContainerLimits(args, info)
	args = appendContainerNetworking(args, info, imageDefaults)
	return appendContainerProcess(args, info, imageDefaults)
}

func appendContainerResources(args []string, info *containerInfo) []string {
	if policy := restartPolicyArg(info.HostConfig.RestartPolicy); policy != "" {
		args = append(args, "--restart", policy)
	}
	for _, bind := range info.HostConfig.Binds {
		args = append(args, "--volume", bind)
	}
	for _, mount := range info.HostConfig.Mounts {
		if value := mountArg(mount); value != "" {
			args = append(args, "--mount", value)
		}
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
	if info.Config.Tty {
		args = append(args, "--tty")
	}
	if info.Config.OpenStdin {
		args = append(args, "--interactive")
	}
	if signal := info.Config.StopSignal; signal != "" && (imageDefaults == nil || signal != imageDefaults.StopSignal) {
		args = append(args, "--stop-signal", signal)
	}
	if info.Config.StopTimeout != nil {
		args = append(args, "--stop-timeout", strconv.Itoa(*info.Config.StopTimeout))
	}
	args = appendHealthcheck(args, info.Config.Healthcheck, imageDefaults)
	if logging := info.HostConfig.LogConfig; logging.Type != "" && (logging.Type != "json-file" || len(logging.Config) > 0) {
		args = append(args, "--log-driver", logging.Type)
		for _, key := range sortedKeys(logging.Config) {
			args = append(args, "--log-opt", key+"="+logging.Config[key])
		}
	}
	if runtime := info.HostConfig.Runtime; runtime != "" && runtime != "runc" {
		args = append(args, "--runtime", runtime)
	}
	return args
}

func appendHealthcheck(args []string, health *healthConfig, imageDefaults *containerConfig) []string {
	if health == nil || len(health.Test) == 0 {
		return args
	}
	if imageDefaults != nil && reflect.DeepEqual(health, imageDefaults.Healthcheck) {
		return args
	}
	switch health.Test[0] {
	case "NONE":
		return append(args, "--no-healthcheck")
	case "CMD-SHELL":
		if len(health.Test) > 1 {
			args = append(args, "--health-cmd", health.Test[1])
		}
	case "CMD":
		args = append(args, "--health-cmd", shellCommand(health.Test[1:]))
	}
	if health.Interval > 0 {
		args = append(args, "--health-interval", time.Duration(health.Interval).String())
	}
	if health.Timeout > 0 {
		args = append(args, "--health-timeout", time.Duration(health.Timeout).String())
	}
	if health.StartPeriod > 0 {
		args = append(args, "--health-start-period", time.Duration(health.StartPeriod).String())
	}
	if health.Retries > 0 {
		args = append(args, "--health-retries", strconv.Itoa(health.Retries))
	}
	return args
}

func appendContainerLimits(args []string, info *containerInfo) []string {
	host := info.HostConfig
	if host.Memory > 0 {
		args = append(args, "--memory", strconv.FormatInt(host.Memory, 10))
	}
	if host.MemoryReservation > 0 {
		args = append(args, "--memory-reservation", strconv.FormatInt(host.MemoryReservation, 10))
	}
	if host.MemorySwap != 0 {
		args = append(args, "--memory-swap", strconv.FormatInt(host.MemorySwap, 10))
	}
	if host.NanoCPUs > 0 {
		args = append(args, "--cpus", strconv.FormatFloat(float64(host.NanoCPUs)/1e9, 'f', -1, 64))
	}
	if host.CPUShares > 0 {
		args = append(args, "--cpu-shares", strconv.FormatInt(host.CPUShares, 10))
	}
	if host.CPUPeriod > 0 {
		args = append(args, "--cpu-period", strconv.FormatInt(host.CPUPeriod, 10))
	}
	if host.CPUQuota > 0 {
		args = append(args, "--cpu-quota", strconv.FormatInt(host.CPUQuota, 10))
	}
	if host.CpusetCpus != "" {
		args = append(args, "--cpuset-cpus", host.CpusetCpus)
	}
	if host.CpusetMems != "" {
		args = append(args, "--cpuset-mems", host.CpusetMems)
	}
	if host.PidsLimit != nil && *host.PidsLimit > 0 {
		args = append(args, "--pids-limit", strconv.FormatInt(*host.PidsLimit, 10))
	}
	if host.OomKillDisable != nil && *host.OomKillDisable {
		args = append(args, "--oom-kill-disable")
	}
	if host.OomScoreAdj != 0 {
		args = append(args, "--oom-score-adj", strconv.Itoa(host.OomScoreAdj))
	}
	for _, limit := range host.Ulimits {
		args = append(args, "--ulimit", fmt.Sprintf("%s=%d:%d", limit.Name, limit.Soft, limit.Hard))
	}
	for _, key := range sortedKeys(host.Sysctls) {
		args = append(args, "--sysctl", key+"="+host.Sysctls[key])
	}
	for _, device := range host.Devices {
		value := device.PathOnHost
		if device.PathInContainer != "" {
			value += ":" + device.PathInContainer
		}
		if device.CgroupPermissions != "" && device.CgroupPermissions != "rwm" {
			value += ":" + device.CgroupPermissions
		}
		args = append(args, "--device", value)
	}
	for _, request := range host.DeviceRequests {
		if value := gpusArg(request); value != "" {
			args = append(args, "--gpus", value)
		}
	}
	if mode := host.IpcMode; mode != "" && mode != "private" && mode != "shareable" {
		args = append(args, "--ipc", mode)
	}
	if host.PidMode != "" {
		args = append(args, "--pid", host.PidMode)
	}
	if host.UTSMode != "" {
		args = append(args, "--uts", host.UTSMode)
	}
	if host.CgroupParent != "" {
		args = append(args, "--cgroup-parent", host.CgroupParent)
	}
	return args
}

func gpusArg(request deviceRequest) string {
	isGPU := false
	for _, capabilities := range request.Capabilities {
		if slices.Contains(capabilities, "gpu") {
			isGPU = true
		}
	}
	if !isGPU {
		return ""
	}
	switch {
	case len(request.DeviceIDs) > 0:
		return `"device=` + strings.Join(request.DeviceIDs, ",") + `"`
	case request.Count < 0:
		return "all"
	case request.Count > 0:
		return strconv.Itoa(request.Count)
	}
	return ""
}

func appendContainerNetworking(args []string, info *containerInfo, imageDefaults *containerConfig) []string {
	network := info.HostConfig.NetworkMode
	if network != "" && network != "default" {
		args = append(args, "--network", network)
		if primary := primaryNetwork(network); primary != "" {
			for _, alias := range userNetworkAliases(info, primary) {
				args = append(args, "--network-alias", alias)
			}
			args = appendStaticAddresses(args, info, primary, "--ip", "--ip6")
		}
	}
	if !sharesHostname(network) {
		if hostname := info.Config.Hostname; hostname != "" && hostname != shortID(info.ID) {
			args = append(args, "--hostname", hostname)
		}
		if info.Config.Domainname != "" {
			args = append(args, "--domainname", info.Config.Domainname)
		}
	}
	for _, port := range sortedKeys(info.Config.ExposedPorts) {
		if _, published := info.HostConfig.PortBindings[port]; published {
			continue
		}
		if imageDefaults != nil {
			if _, inherited := imageDefaults.ExposedPorts[port]; inherited {
				continue
			}
		}
		args = append(args, "--expose", port)
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

// sharesHostname reports network modes where Docker rejects --hostname.
func sharesHostname(networkMode string) bool {
	return networkMode == "host" || strings.HasPrefix(networkMode, "container:")
}

func appendStaticAddresses(args []string, info *containerInfo, networkName, ipv4Flag, ipv6Flag string) []string {
	endpoint, ok := info.NetworkSettings.Networks[networkName]
	if !ok || endpoint.IPAMConfig == nil {
		return args
	}
	if address := endpoint.IPAMConfig.IPv4Address; address != "" {
		args = append(args, ipv4Flag, address)
	}
	if address := endpoint.IPAMConfig.IPv6Address; address != "" {
		args = append(args, ipv6Flag, address)
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

// mountArg rebuilds a --mount value. Fields are CSV-encoded, matching how Docker parses them.
func mountArg(mount mountSpec) string {
	if mount.Target == "" {
		return ""
	}
	fields := []string{"type=" + mount.Type}
	if mount.Source != "" {
		fields = append(fields, "source="+mount.Source)
	}
	fields = append(fields, "target="+mount.Target)
	if mount.ReadOnly {
		fields = append(fields, "readonly")
	}
	if mount.BindOptions != nil && mount.BindOptions.Propagation != "" {
		fields = append(fields, "bind-propagation="+mount.BindOptions.Propagation)
	}
	if mount.VolumeOptions != nil && mount.VolumeOptions.NoCopy {
		fields = append(fields, "volume-nocopy")
	}
	if mount.TmpfsOptions != nil {
		if mount.TmpfsOptions.SizeBytes > 0 {
			fields = append(fields, "tmpfs-size="+strconv.FormatInt(mount.TmpfsOptions.SizeBytes, 10))
		}
		if mount.TmpfsOptions.Mode > 0 {
			fields = append(fields, "tmpfs-mode="+strconv.FormatInt(mount.TmpfsOptions.Mode, 8))
		}
	}
	var buffer bytes.Buffer
	writer := csv.NewWriter(&buffer)
	_ = writer.Write(fields)
	writer.Flush()
	return strings.TrimRight(buffer.String(), "\r\n")
}

func appendAnonymousVolumes(args []string, info *containerInfo) []string {
	covered := make(map[string]bool, len(info.HostConfig.Binds)+len(info.HostConfig.Mounts))
	for _, bind := range info.HostConfig.Binds {
		parts := strings.Split(bind, ":")
		if len(parts) >= 2 {
			destinationIndex := len(parts) - 1
			if len(parts) >= 3 && isVolumeMode(parts[len(parts)-1]) {
				destinationIndex--
			}
			covered[parts[destinationIndex]] = true
		}
	}
	for _, mount := range info.HostConfig.Mounts {
		covered[mount.Target] = true
	}
	for _, mount := range info.Mounts {
		if mount.Type != "volume" || mount.Name == "" || covered[mount.Destination] {
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
