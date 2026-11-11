package main

import (
	"strings"
	"testing"
)

func TestRunArgsRuntimeSettings(t *testing.T) {
	info := sampleContainerInfo()
	pids, timeout, disableOOM := int64(200), 30, true
	info.Config.Hostname = "web-host"
	info.Config.Tty = true
	info.Config.OpenStdin = true
	info.Config.StopSignal = "SIGQUIT"
	info.Config.StopTimeout = &timeout
	info.Config.Healthcheck = &healthConfig{Test: []string{"CMD-SHELL", "curl -f localhost"}, Interval: 30e9, Retries: 3}
	info.Config.ExposedPorts = map[string]struct{}{"80/tcp": {}, "9000/tcp": {}}
	info.HostConfig.NetworkMode = "frontend"
	info.NetworkSettings.Networks = map[string]networkInfo{"frontend": {}}
	frontend := info.NetworkSettings.Networks["frontend"]
	frontend.IPAMConfig = &struct {
		IPv4Address string `json:"IPv4Address"`
		IPv6Address string `json:"IPv6Address"`
	}{IPv4Address: "172.20.0.5"}
	info.NetworkSettings.Networks["frontend"] = frontend
	info.HostConfig.Memory = 512 * 1024 * 1024
	info.HostConfig.NanoCPUs = 1_500_000_000
	info.HostConfig.PidsLimit = &pids
	info.HostConfig.OomKillDisable = &disableOOM
	info.HostConfig.Ulimits = []ulimit{{Name: "nofile", Soft: 1024, Hard: 2048}}
	info.HostConfig.Sysctls = map[string]string{"net.core.somaxconn": "1024"}
	info.HostConfig.Devices = []deviceMapping{{PathOnHost: "/dev/dri", PathInContainer: "/dev/dri", CgroupPermissions: "rwm"}}
	info.HostConfig.DeviceRequests = []deviceRequest{{Count: -1, Capabilities: [][]string{{"gpu"}}}}
	info.HostConfig.LogConfig = logConfig{Type: "json-file", Config: map[string]string{"max-size": "10m"}}
	info.HostConfig.IpcMode = "private"
	info.HostConfig.Mounts = []mountSpec{{Type: "bind", Source: "/srv/a,b", Target: "/data", ReadOnly: true}}
	info.Mounts = []mountInfo{{Type: "bind", Source: "/srv/a,b", Destination: "/data"}}
	defaults := containerConfig{ExposedPorts: map[string]struct{}{"80/tcp": {}}}

	command := shellCommand(buildRunArgs(info, &defaults))
	for _, want := range []string{
		`--mount 'type=bind,"source=/srv/a,b",target=/data,readonly'`,
		"--tty --interactive",
		"--stop-signal SIGQUIT --stop-timeout 30",
		"--health-cmd 'curl -f localhost' --health-interval 30s --health-retries 3",
		"--log-driver json-file --log-opt max-size=10m",
		"--memory 536870912",
		"--cpus 1.5",
		"--pids-limit 200 --oom-kill-disable",
		"--ulimit nofile=1024:2048",
		"--sysctl net.core.somaxconn=1024",
		"--device /dev/dri:/dev/dri",
		"--gpus all",
		"--network frontend --ip 172.20.0.5",
		"--hostname web-host",
		"--expose 9000/tcp",
	} {
		if !strings.Contains(command, want) {
			t.Fatalf("command does not contain %q:\n%s", want, command)
		}
	}
	for _, unwanted := range []string{"--ipc", "--expose 80/tcp", "--volume"} {
		if strings.Contains(command, unwanted) {
			t.Fatalf("command contains %q:\n%s", unwanted, command)
		}
	}
}

func TestRunArgsDefaultHostname(t *testing.T) {
	info := sampleContainerInfo()
	info.Config.Hostname = shortID(info.ID)
	if command := shellCommand(buildRunArgs(info, nil)); strings.Contains(command, "--hostname") {
		t.Fatalf("generated hostname was pinned: %s", command)
	}
	info.Config.Hostname = "custom"
	info.HostConfig.NetworkMode = "host"
	if command := shellCommand(buildRunArgs(info, nil)); strings.Contains(command, "--hostname") {
		t.Fatalf("hostname set with host networking: %s", command)
	}
}

func TestHealthcheckDefault(t *testing.T) {
	info := sampleContainerInfo()
	info.Config.Healthcheck = &healthConfig{Test: []string{"CMD", "/healthz"}, Interval: 5e9}
	defaults := containerConfig{Healthcheck: &healthConfig{Test: []string{"CMD", "/healthz"}, Interval: 5e9}}
	if command := shellCommand(buildRunArgs(info, &defaults)); strings.Contains(command, "--health") {
		t.Fatalf("image healthcheck was pinned: %s", command)
	}
}
