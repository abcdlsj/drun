package main

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"
)

func TestBuildRunArgsPreservesConfiguration(t *testing.T) {
	init := true
	info := sampleContainerInfo()
	info.Config.Cmd = []string{"serve", "--message", "hello world; $HOME"}
	info.Config.Entrypoint = []string{"/usr/bin/tini", "--", "/app/server"}
	info.Config.Env = []string{"GREETING=hello world", "TOKEN=$(do-not-run)"}
	info.Config.Labels = map[string]string{"com.example.owner": "platform team"}
	info.Config.User = "1000:1000"
	info.Config.WorkingDir = "/srv/app data"
	info.HostConfig.Binds = []string{"data:/data:ro"}
	info.HostConfig.CapAdd = []string{"NET_ADMIN"}
	info.HostConfig.CapDrop = []string{"MKNOD"}
	info.HostConfig.DNS = []string{"1.1.1.1"}
	info.HostConfig.ExtraHosts = []string{"api.local:10.0.0.8"}
	info.HostConfig.GroupAdd = []string{"video"}
	info.HostConfig.Init = &init
	info.HostConfig.NetworkMode = "frontend"
	info.HostConfig.PortBindings = map[string][]portBinding{
		"443/tcp": {{HostIP: "127.0.0.1", HostPort: "8443"}},
		"80/tcp":  {{HostPort: "8080"}},
	}
	info.HostConfig.Privileged = true
	info.HostConfig.PublishAllPorts = true
	info.HostConfig.ReadonlyRootfs = true
	info.HostConfig.RestartPolicy = restartPolicy{Name: "on-failure", MaximumRetryCount: 5}
	info.HostConfig.SecurityOpt = []string{"no-new-privileges"}
	info.HostConfig.ShmSize = 67108864
	info.HostConfig.Tmpfs = map[string]string{"/run": "rw,noexec"}
	info.Mounts = []mountInfo{
		{Type: "volume", Name: "data", Destination: "/data", RW: false},
		{Type: "volume", Name: "anonymous-id", Destination: "/cache", RW: true},
	}
	info.NetworkSettings.Networks = map[string]networkInfo{
		"frontend": {Aliases: []string{"web", info.ID, "api"}},
	}

	want := []string{
		"run", "-d", "--name", "web",
		"--restart", "on-failure:5",
		"--volume", "data:/data:ro",
		"--volume", "anonymous-id:/cache",
		"--publish", "127.0.0.1:8443:443/tcp",
		"--publish", "8080:80/tcp",
		"--env", "GREETING=hello world",
		"--env", "TOKEN=$(do-not-run)",
		"--label", "com.example.owner=platform team",
		"--cap-add", "NET_ADMIN",
		"--cap-drop", "MKNOD",
		"--security-opt", "no-new-privileges",
		"--add-host", "api.local:10.0.0.8",
		"--dns", "1.1.1.1",
		"--group-add", "video",
		"--user", "1000:1000",
		"--workdir", "/srv/app data",
		"--privileged",
		"--publish-all",
		"--read-only",
		"--init",
		"--network", "frontend",
		"--network-alias", "api",
		"--tmpfs", "/run:rw,noexec",
		"--entrypoint", "/usr/bin/tini",
		"nginx:latest", "--", "/app/server", "serve", "--message", "hello world; $HOME",
	}

	got := buildRunArgs(info, nil)
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("buildRunArgs() mismatch\n got: %#v\nwant: %#v", got, want)
	}
}

func TestBuildRunArgsDoesNotDuplicateVolumeWithoutMode(t *testing.T) {
	info := sampleContainerInfo()
	info.HostConfig.Binds = []string{"web-data:/srv/data"}
	info.Mounts = []mountInfo{{Type: "volume", Name: "web-data", Destination: "/srv/data", RW: true}}

	args := buildRunArgs(info, nil)
	volumeCount := 0
	for _, argument := range args {
		if argument == "web-data:/srv/data" {
			volumeCount++
		}
	}
	if volumeCount != 1 {
		t.Fatalf("volume appears %d times in %#v", volumeCount, args)
	}
}

func TestRefreshDryRunDoesNotMutateDocker(t *testing.T) {
	docker := &fakeDocker{inspectInfo: sampleContainerInfo()}
	ui := &fakeTerminal{confirmed: true}
	app := newApplication(docker, ui)

	if err := app.refresh(context.Background(), refreshOptions{containerName: "web", dryRun: true}); err != nil {
		t.Fatalf("refresh() error = %v", err)
	}
	want := [][]string{{"inspect", "web"}, {"image", "inspect", "sha256:old"}}
	if !reflect.DeepEqual(docker.calls, want) {
		t.Fatalf("docker calls = %#v, want %#v", docker.calls, want)
	}
}

func TestRefreshComposeDryRunPrintsComposeCommandWithoutMutation(t *testing.T) {
	info := sampleComposeContainerInfo()
	docker := &fakeDocker{inspectInfo: info}
	ui := &recordingTerminal{fakeTerminal: fakeTerminal{confirmed: true}}
	app := newApplication(docker, ui)

	if err := app.refresh(context.Background(), refreshOptions{containerName: "demo-web-1", dryRun: true}); err != nil {
		t.Fatalf("refresh() error = %v", err)
	}
	wantCalls := [][]string{{"inspect", "demo-web-1"}, {"image", "inspect", "sha256:old"}}
	if !reflect.DeepEqual(docker.calls, wantCalls) {
		t.Fatalf("docker calls = %#v, want %#v", docker.calls, wantCalls)
	}
	wantCommand := []string{
		"docker", "compose", "--project-name", "demo",
		"--project-directory", "/srv/demo",
		"--file", "/srv/demo/compose.yml",
		"--file", "/srv/demo/compose.prod.yml",
		"--env-file", "/srv/demo/.env.prod",
		"up", "--detach", "--no-deps", "--pull", "never", "--force-recreate", "web",
	}
	if len(ui.commands) != 1 || !reflect.DeepEqual(ui.commands[0], wantCommand) {
		t.Fatalf("commands = %#v, want %#v", ui.commands, wantCommand)
	}
}

func TestRefreshComposeContainerUsesComposeInsteadOfDockerRun(t *testing.T) {
	info := sampleComposeContainerInfo()
	docker := &fakeDocker{inspectInfo: info}
	ui := &fakeTerminal{confirmed: true}
	app := newApplication(docker, ui)

	if err := app.refresh(context.Background(), refreshOptions{containerName: "demo-web-1", yes: true}); err != nil {
		t.Fatalf("refresh() error = %v", err)
	}
	if got := countDockerCalls(docker.calls, "compose"); got != 1 {
		t.Fatalf("compose calls = %d, want 1; calls: %#v", got, docker.calls)
	}
	for _, call := range docker.calls {
		if len(call) > 0 && (call[0] == "run" || call[0] == "stop" || call[0] == "rename") {
			t.Fatalf("Compose container used standalone replacement: %#v", docker.calls)
		}
	}
}

func TestRefreshRejectsIncompleteComposeMetadata(t *testing.T) {
	info := sampleContainerInfo()
	info.Config.Labels = map[string]string{composeProjectLabel: "demo"}
	docker := &fakeDocker{inspectInfo: info}
	app := newApplication(docker, &fakeTerminal{})

	err := app.refresh(context.Background(), refreshOptions{containerName: "web", yes: true})
	if err == nil || !strings.Contains(err.Error(), "incomplete Docker Compose metadata") {
		t.Fatalf("refresh() error = %v, want incomplete metadata error", err)
	}
}

func TestRefreshCancellationLeavesContainerUntouched(t *testing.T) {
	docker := &fakeDocker{inspectInfo: sampleContainerInfo()}
	ui := &fakeTerminal{confirmed: false}
	app := newApplication(docker, ui)

	err := app.refresh(context.Background(), refreshOptions{containerName: "web"})
	if !errors.Is(err, errCancelled) {
		t.Fatalf("refresh() error = %v, want errCancelled", err)
	}
	want := [][]string{
		{"inspect", "web"},
		{"image", "inspect", "sha256:old"},
		{"pull", "nginx:latest"},
		{"image", "inspect", "nginx:latest"},
	}
	if !reflect.DeepEqual(docker.calls, want) {
		t.Fatalf("docker calls = %#v, want %#v", docker.calls, want)
	}
}

func TestRefreshReplacesContainerAndRemovesBackup(t *testing.T) {
	info := sampleContainerInfo()
	info.NetworkSettings.Networks["metrics"] = networkInfo{Aliases: []string{"collector"}}
	docker := &fakeDocker{inspectInfo: info}
	ui := &fakeTerminal{confirmed: true}
	app := newApplication(docker, ui)
	app.now = func() time.Time { return time.Unix(1234, 0) }

	if err := app.refresh(context.Background(), refreshOptions{containerName: "web"}); err != nil {
		t.Fatalf("refresh() error = %v", err)
	}

	wantPrefix := [][]string{
		{"inspect", "web"},
		{"image", "inspect", "sha256:old"},
		{"pull", "nginx:latest"},
		{"image", "inspect", "nginx:latest"},
		{"stop", "web"},
		{"rename", "web", "web-drun-backup-1234"},
	}
	if !reflect.DeepEqual(docker.calls[:6], wantPrefix) {
		t.Fatalf("docker call prefix = %#v, want %#v", docker.calls[:6], wantPrefix)
	}
	if got := docker.calls[len(docker.calls)-2]; !reflect.DeepEqual(got, []string{"network", "connect", "--alias", "collector", "metrics", "web"}) {
		t.Fatalf("network connect call = %#v", got)
	}
	if got := docker.calls[len(docker.calls)-1]; !reflect.DeepEqual(got, []string{"rm", "web-drun-backup-1234"}) {
		t.Fatalf("cleanup call = %#v", got)
	}
}

func TestRefreshRollsBackWhenReplacementFails(t *testing.T) {
	docker := &fakeDocker{
		inspectInfo: sampleContainerInfo(),
		failWhen: func(args []string) error {
			if len(args) > 0 && args[0] == "run" {
				return errors.New("port is already allocated")
			}
			return nil
		},
	}
	ui := &fakeTerminal{confirmed: true}
	app := newApplication(docker, ui)
	app.now = func() time.Time { return time.Unix(1234, 0) }

	err := app.refresh(context.Background(), refreshOptions{containerName: "web"})
	if err == nil || !strings.Contains(err.Error(), "original container restored") {
		t.Fatalf("refresh() error = %v, want successful rollback message", err)
	}

	wantTail := [][]string{
		{"rm", "-f", "web"},
		{"rename", "web-drun-backup-1234", "web"},
		{"start", "web"},
	}
	gotTail := docker.calls[len(docker.calls)-3:]
	if !reflect.DeepEqual(gotTail, wantTail) {
		t.Fatalf("rollback calls = %#v, want %#v", gotTail, wantTail)
	}
}

func TestRefreshRollsBackWhenReplacementUsesUnexpectedImage(t *testing.T) {
	docker := &fakeDocker{inspectInfo: sampleContainerInfo(), replacementImageID: "sha256:unexpected"}
	ui := &fakeTerminal{confirmed: true}
	app := newApplication(docker, ui)
	app.now = func() time.Time { return time.Unix(1234, 0) }

	err := app.refresh(context.Background(), refreshOptions{containerName: "web", yes: true})
	if err == nil || !strings.Contains(err.Error(), "original container restored") {
		t.Fatalf("refresh() error = %v, want image verification rollback", err)
	}
	wantTail := [][]string{
		{"rm", "-f", "web"},
		{"rename", "web-drun-backup-1234", "web"},
		{"start", "web"},
	}
	if got := docker.calls[len(docker.calls)-3:]; !reflect.DeepEqual(got, wantTail) {
		t.Fatalf("rollback calls = %#v, want %#v", got, wantTail)
	}
}

func TestRefreshRejectsAutoRemoveContainer(t *testing.T) {
	info := sampleContainerInfo()
	info.HostConfig.AutoRemove = true
	docker := &fakeDocker{inspectInfo: info}
	app := newApplication(docker, &fakeTerminal{})

	err := app.refresh(context.Background(), refreshOptions{containerName: "web", yes: true})
	if err == nil || !strings.Contains(err.Error(), "--rm") {
		t.Fatalf("refresh() error = %v, want --rm safety error", err)
	}
	if len(docker.calls) != 2 {
		t.Fatalf("docker calls = %#v, want container and image inspect only", docker.calls)
	}
}

func TestBuildRunArgsOmitsImageDefaults(t *testing.T) {
	info := sampleContainerInfo()
	info.Config.Env = []string{"PATH=/usr/bin", "APP_MODE=production"}
	info.Config.Labels = map[string]string{
		"org.opencontainers.image.version": "v1.0.0",
		"managed-by":                       "compose",
	}
	info.Config.WorkingDir = "/app"
	info.Config.Entrypoint = []string{"/app/server"}
	info.Config.Cmd = []string{"serve"}
	info.HostConfig.ShmSize = 64 * 1024 * 1024
	defaults := containerConfig{
		Env:        []string{"PATH=/usr/bin"},
		Labels:     map[string]string{"org.opencontainers.image.version": "v1.0.0"},
		WorkingDir: "/app",
		Entrypoint: []string{"/app/server"},
		Cmd:        []string{"serve"},
	}

	args := buildRunArgs(info, &defaults)
	command := strings.Join(args, " ")
	for _, unwanted := range []string{"PATH=/usr/bin", "org.opencontainers.image.version", "--workdir", "--entrypoint", "--shm-size", " serve"} {
		if strings.Contains(command, unwanted) {
			t.Fatalf("command contains image default %q: %s", unwanted, command)
		}
	}
	if !strings.Contains(command, "--env APP_MODE=production") || !strings.Contains(command, "--label managed-by=compose") {
		t.Fatalf("command dropped runtime overrides: %s", command)
	}
}

func TestRefreshSkipsReplacementWhenImageIsCurrent(t *testing.T) {
	docker := &fakeDocker{inspectInfo: sampleContainerInfo(), latestImageID: "sha256:old"}
	ui := &fakeTerminal{confirmed: true}
	app := newApplication(docker, ui)

	if err := app.refresh(context.Background(), refreshOptions{containerName: "web"}); err != nil {
		t.Fatalf("refresh() error = %v", err)
	}
	for _, call := range docker.calls {
		if len(call) > 0 && (call[0] == "stop" || call[0] == "rename" || call[0] == "run") {
			t.Fatalf("up-to-date container was mutated: %#v", docker.calls)
		}
	}
}

func sampleContainerInfo() *containerInfo {
	info := &containerInfo{ID: "1234567890abcdef", Image: "sha256:old", Name: "/web"}
	info.Config.Image = "nginx:latest"
	info.State.Running = true
	info.State.Status = "running"
	info.HostConfig.NetworkMode = "bridge"
	info.HostConfig.PortBindings = map[string][]portBinding{}
	info.HostConfig.Tmpfs = map[string]string{}
	info.NetworkSettings.Networks = map[string]networkInfo{"bridge": {}}
	return info
}

func sampleComposeContainerInfo() *containerInfo {
	info := sampleContainerInfo()
	info.Name = "/demo-web-1"
	info.Config.Labels = map[string]string{
		composeProjectLabel:         "demo",
		composeServiceLabel:         "web",
		composeWorkingDirLabel:      "/srv/demo",
		composeConfigFilesLabel:     "/srv/demo/compose.yml,/srv/demo/compose.prod.yml",
		composeEnvironmentFileLabel: "/srv/demo/.env.prod",
		composeOneoffLabel:          "False",
	}
	return info
}

type fakeDocker struct {
	inspectInfo        *containerInfo
	latestImageID      string
	replacementImageID string
	calls              [][]string
	failWhen           func([]string) error
	replacementStarted bool
}

func (d *fakeDocker) run(_ context.Context, args ...string) ([]byte, error) {
	d.calls = append(d.calls, append([]string(nil), args...))
	if d.failWhen != nil {
		if err := d.failWhen(args); err != nil {
			return nil, err
		}
	}
	if len(args) > 0 && (args[0] == "run" || args[0] == "compose") {
		d.replacementStarted = true
	}
	if len(args) > 0 && args[0] == "inspect" {
		info := *d.inspectInfo
		if d.replacementStarted {
			info.Image = d.replacementID()
		}
		return json.Marshal([]containerInfo{info})
	}
	if len(args) > 2 && args[0] == "image" && args[1] == "inspect" {
		id := d.inspectInfo.Image
		if args[2] == d.inspectInfo.Config.Image {
			id = d.latestID()
		}
		image := imageInfo{ID: id, Config: d.inspectInfo.Config}
		return json.Marshal([]imageInfo{image})
	}
	return nil, nil
}

func (d *fakeDocker) latestID() string {
	if d.latestImageID != "" {
		return d.latestImageID
	}
	return "sha256:new"
}

func (d *fakeDocker) replacementID() string {
	if d.replacementImageID != "" {
		return d.replacementImageID
	}
	return d.latestID()
}

type fakeTerminal struct {
	confirmed      bool
	nonInteractive bool
	selectCalls    int
}

func (*fakeTerminal) header(string)                   {}
func (*fakeTerminal) info(string)                     {}
func (*fakeTerminal) warning(string)                  {}
func (*fakeTerminal) success(string)                  {}
func (*fakeTerminal) command([]string)                {}
func (*fakeTerminal) summary(*containerInfo)          {}
func (*fakeTerminal) updateAvailable(updateCandidate) {}
func (*fakeTerminal) listUpdates([]updateCandidate)   {}
func (ui *fakeTerminal) selectUpdates(updates []updateCandidate) ([]updateCandidate, error) {
	ui.selectCalls++
	return updates, nil
}
func (ui *fakeTerminal) isInteractive() bool                   { return !ui.nonInteractive }
func (ui *fakeTerminal) confirm(string) (bool, error)          { return ui.confirmed, nil }
func (*fakeTerminal) spin(_ string, action func() error) error { return action() }

type recordingTerminal struct {
	fakeTerminal
	commands [][]string
}

func (ui *recordingTerminal) command(args []string) {
	ui.commands = append(ui.commands, append([]string(nil), args...))
}
