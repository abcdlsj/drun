package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"
)

func TestCheckSharedImage(t *testing.T) {
	docker := newCheckDocker()
	ui := &fakeTerminal{}
	app := newApplication(docker, ui)

	if err := app.check(context.Background(), checkOptions{}); err != nil {
		t.Fatalf("check() error = %v", err)
	}

	if got := countDockerCalls(docker.calls, "pull"); got != 1 {
		t.Fatalf("pull calls = %d, want 1; calls: %#v", got, docker.calls)
	}
	if got := countDockerCalls(docker.calls, "stop"); got != 2 {
		t.Fatalf("stop calls = %d, want 2; calls: %#v", got, docker.calls)
	}
	if !docker.updated["api"] || !docker.updated["web"] || docker.updated["current"] {
		t.Fatalf("updated containers = %#v", docker.updated)
	}
}

func TestCheckNonInteractive(t *testing.T) {
	docker := newCheckDocker()
	ui := &fakeTerminal{nonInteractive: true}
	app := newApplication(docker, ui)

	if err := app.check(context.Background(), checkOptions{}); err != nil {
		t.Fatalf("check() error = %v", err)
	}
	if got := countDockerCalls(docker.calls, "stop"); got != 0 {
		t.Fatalf("non-interactive check stopped containers: %#v", docker.calls)
	}
}

func TestCheckLocalImage(t *testing.T) {
	docker := newCheckDocker()
	for id, image := range docker.images {
		image.RepoDigests = nil
		docker.images[id] = image
	}
	docker.latest.RepoDigests = nil
	app := newApplication(docker, &fakeTerminal{})

	if err := app.check(context.Background(), checkOptions{}); err != nil {
		t.Fatalf("check() error = %v", err)
	}
	if got := countDockerCalls(docker.calls, "pull"); got != 0 {
		t.Fatalf("local images triggered %d pulls", got)
	}
}

func TestCheckYes(t *testing.T) {
	docker := newCheckDocker()
	ui := &fakeTerminal{nonInteractive: true}
	app := newApplication(docker, ui)

	if err := app.check(context.Background(), checkOptions{yes: true}); err != nil {
		t.Fatalf("check() error = %v", err)
	}
	if ui.selectCalls != 0 {
		t.Fatalf("selector opened %d times", ui.selectCalls)
	}
	if got := countDockerCalls(docker.calls, "stop"); got != 2 {
		t.Fatalf("stop calls = %d, want 2; calls: %#v", got, docker.calls)
	}
}

func TestCheckedContainerCurrent(t *testing.T) {
	info := sampleContainerInfo()
	info.Image = "sha256:new"
	docker := &fakeDocker{inspectInfo: info}
	app := newApplication(docker, &fakeTerminal{})

	changed, err := app.updateCheckedContainer(context.Background(), updateCandidate{
		Name: "web", CurrentImageID: "sha256:old", LatestImageID: "sha256:new",
	}, func(_ string, action func() error) error { return action() })
	if err != nil {
		t.Fatalf("updateCheckedContainer() error = %v", err)
	}
	if changed {
		t.Fatal("updateCheckedContainer() changed an already-current container")
	}
	if len(docker.calls) != 1 || docker.calls[0][0] != "inspect" {
		t.Fatalf("already-current container was mutated: %#v", docker.calls)
	}
}

func TestImageCheckConcurrency(t *testing.T) {
	docker := &parallelCheckDocker{
		started: make(chan string, maxConcurrentImageChecks),
		release: make(chan struct{}),
	}
	app := newApplication(docker, &fakeTerminal{})
	references := make([]string, maxConcurrentImageChecks+1)
	for index := range references {
		references[index] = fmt.Sprintf("example/image-%d:latest", index)
	}

	type result struct {
		images int
		errors int
	}
	done := make(chan result, 1)
	go func() {
		var got result
		app.checkLatestImages(context.Background(), references, func(check imageCheckResult) {
			if check.err != nil {
				got.errors++
			} else {
				got.images++
			}
		})
		done <- got
	}()

	var releaseOnce sync.Once
	release := func() { releaseOnce.Do(func() { close(docker.release) }) }
	defer release()
	for range maxConcurrentImageChecks {
		select {
		case <-docker.started:
		case <-time.After(time.Second):
			t.Fatal("image pulls did not start concurrently")
		}
	}
	select {
	case <-docker.started:
		t.Fatalf("more than %d image pulls started concurrently", maxConcurrentImageChecks)
	case <-time.After(20 * time.Millisecond):
	}
	release()

	select {
	case got := <-done:
		if got.images != len(references) || got.errors != 0 {
			t.Fatalf("checkLatestImages() returned %d images and %d errors", got.images, got.errors)
		}
	case <-time.After(time.Second):
		t.Fatal("concurrent image check did not finish")
	}
}

func TestCheckCompose(t *testing.T) {
	docker := newCheckDocker()
	docker.containers["web"].Config.Labels = sampleComposeContainerInfo().Config.Labels
	ui := &fakeTerminal{}
	app := newApplication(docker, ui)

	if err := app.check(context.Background(), checkOptions{}); err != nil {
		t.Fatalf("check() error = %v", err)
	}
	if got := countDockerCalls(docker.calls, "compose"); got != 1 {
		t.Fatalf("compose calls = %d, want 1; calls: %#v", got, docker.calls)
	}
	for _, call := range docker.calls {
		if len(call) > 0 && call[0] == "run" {
			for index, argument := range call {
				if argument == "--name" && index+1 < len(call) && call[index+1] == "web" {
					t.Fatalf("Compose container web used docker run: %#v", docker.calls)
				}
			}
		}
	}
}

func TestImageVersions(t *testing.T) {
	tests := []struct {
		name        string
		current     imageInfo
		latest      imageInfo
		wantCurrent string
		wantLatest  string
	}{
		{
			name:        "tags",
			current:     imageInfo{ID: "sha256:old", RepoTags: []string{"example/app:latest", "example/app:v1.2.0"}},
			latest:      imageInfo{ID: "sha256:new", RepoTags: []string{"example/app:latest", "example/app:v1.3.0"}},
			wantCurrent: "v1.2.0",
			wantLatest:  "v1.3.0",
		},
		{
			name: "oci revision",
			current: imageInfo{ID: "sha256:old", Config: containerConfig{Labels: map[string]string{
				"org.opencontainers.image.version":  "latest",
				"org.opencontainers.image.revision": "1234567890abcdef",
			}}},
			latest: imageInfo{ID: "sha256:new", Config: containerConfig{Labels: map[string]string{
				"org.opencontainers.image.version":  "latest",
				"org.opencontainers.image.revision": "abcdef1234567890",
			}}},
			wantCurrent: "1234567890ab",
			wantLatest:  "abcdef123456",
		},
		{
			name:        "image IDs",
			current:     imageInfo{ID: "sha256:111111111111aaaaaaaa"},
			latest:      imageInfo{ID: "sha256:222222222222bbbbbbbb"},
			wantCurrent: "111111111111",
			wantLatest:  "222222222222",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			current, latest := displayImageVersions(&test.current, &test.latest)
			if current != test.wantCurrent || latest != test.wantLatest {
				t.Fatalf("displayImageVersions() = %q, %q; want %q, %q", current, latest, test.wantCurrent, test.wantLatest)
			}
		})
	}
}

func TestRemoteImage(t *testing.T) {
	tests := []struct {
		name      string
		reference string
		digests   []string
		want      bool
	}{
		{name: "matching digest", reference: "nginx:latest", digests: []string{"nginx@sha256:abc"}, want: true},
		{name: "canonical Docker Hub", reference: "docker.io/library/nginx:latest", digests: []string{"nginx@sha256:abc"}, want: true},
		{name: "local build", reference: "example/local:latest"},
		{name: "retagged image", reference: "example/local:latest", digests: []string{"example/upstream@sha256:abc"}},
		{name: "pinned digest", reference: "nginx@sha256:abc", digests: []string{"nginx@sha256:abc"}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			image := &imageInfo{RepoDigests: test.digests}
			if got := remotelyUpdatableImage(test.reference, image); got != test.want {
				t.Fatalf("remotelyUpdatableImage() = %v, want %v", got, test.want)
			}
		})
	}
}

func TestConciseError(t *testing.T) {
	err := fmt.Errorf("docker pull example/app: exit status 1: Using default tag: latest\nError response from daemon: pull access denied")
	if got, want := conciseError(err), "pull access denied"; got != want {
		t.Fatalf("conciseError() = %q, want %q", got, want)
	}
}

type checkDocker struct {
	mu         sync.Mutex
	calls      [][]string
	containers map[string]*containerInfo
	images     map[string]imageInfo
	latest     imageInfo
	updated    map[string]bool
}

type parallelCheckDocker struct {
	started chan string
	release chan struct{}
}

func (d *parallelCheckDocker) run(ctx context.Context, args ...string) ([]byte, error) {
	if len(args) > 0 && args[0] == "pull" {
		d.started <- args[len(args)-1]
		select {
		case <-d.release:
			return nil, nil
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
	if len(args) > 2 && args[0] == "image" && args[1] == "inspect" {
		return json.Marshal([]imageInfo{{ID: "sha256:" + args[2]}})
	}
	return nil, nil
}

func newCheckDocker() *checkDocker {
	old := imageInfo{ID: "sha256:old", RepoTags: []string{"example/app:v1.0.0"}, RepoDigests: []string{"example/app@sha256:old"}}
	latest := imageInfo{ID: "sha256:new", RepoTags: []string{"example/app:v1.1.0"}, RepoDigests: []string{"example/app@sha256:new"}}
	containers := map[string]*containerInfo{}
	for _, name := range []string{"api", "web"} {
		info := sampleContainerInfo()
		info.Name = "/" + name
		info.Image = old.ID
		info.Config.Image = "example/app:latest"
		containers[name] = info
	}
	current := sampleContainerInfo()
	current.Name = "/current"
	current.Image = latest.ID
	current.Config.Image = "example/app:latest"
	containers["current"] = current
	return &checkDocker{
		containers: containers,
		images:     map[string]imageInfo{old.ID: old, latest.ID: latest},
		latest:     latest,
		updated:    map[string]bool{},
	}
}

func (d *checkDocker) run(_ context.Context, args ...string) ([]byte, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.calls = append(d.calls, append([]string(nil), args...))
	if len(args) == 0 {
		return nil, nil
	}
	switch args[0] {
	case "ps":
		return []byte("api\ncurrent\nweb\n"), nil
	case "inspect":
		var infos []containerInfo
		for _, name := range args[3:] {
			info, ok := d.containers[name]
			if !ok {
				return nil, fmt.Errorf("unknown container %s", name)
			}
			copy := *info
			if d.updated[name] {
				copy.Image = d.latest.ID
			}
			infos = append(infos, copy)
		}
		return json.Marshal(infos)
	case "image":
		var images []imageInfo
		for _, reference := range args[2:] {
			if reference == "example/app:latest" {
				images = append(images, d.latest)
				continue
			}
			image, ok := d.images[reference]
			if !ok {
				return nil, fmt.Errorf("unknown image %s", reference)
			}
			images = append(images, image)
		}
		return json.Marshal(images)
	case "run":
		for index, argument := range args {
			if argument == "--name" && index+1 < len(args) {
				d.updated[args[index+1]] = true
				break
			}
		}
	case "compose":
		for name, info := range d.containers {
			if info.Config.Labels[composeProjectLabel] == "demo" && info.Config.Labels[composeServiceLabel] == "web" {
				d.updated[name] = true
			}
		}
	}
	return nil, nil
}

func countDockerCalls(calls [][]string, command string) int {
	count := 0
	for _, call := range calls {
		if len(call) > 0 && call[0] == command {
			count++
		}
	}
	return count
}

func TestCheckBatchesInspect(t *testing.T) {
	docker := newCheckDocker()
	app := newApplication(docker, &fakeTerminal{nonInteractive: true})

	if err := app.check(context.Background(), checkOptions{}); err != nil {
		t.Fatalf("check() error = %v", err)
	}
	if got := countDockerCalls(docker.calls, "inspect"); got != 1 {
		t.Fatalf("container inspect calls = %d, want 1; calls: %#v", got, docker.calls)
	}
	if got := countDockerCalls(docker.calls, "image"); got != 2 {
		t.Fatalf("image inspect calls = %d, want 1 batch and 1 after pull; calls: %#v", got, docker.calls)
	}
}

func TestCheckComposeReplicas(t *testing.T) {
	docker := newCheckDocker()
	labels := sampleComposeContainerInfo().Config.Labels
	docker.containers["api"].Config.Labels = labels
	docker.containers["web"].Config.Labels = labels
	ui := &rowTerminal{}
	app := newApplication(docker, ui)

	if err := app.check(context.Background(), checkOptions{yes: true}); err != nil {
		t.Fatalf("check() error = %v", err)
	}
	if got := countDockerCalls(docker.calls, "compose"); got != 1 {
		t.Fatalf("compose calls = %d, want 1 for both replicas; calls: %#v", got, docker.calls)
	}
	if got := len(ui.scannedRows); got != 2 {
		t.Fatalf("scan rows = %d, want the service and the current container", got)
	}
}

func TestCheckInterruptBetweenContainers(t *testing.T) {
	docker := newCheckDocker()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	ui := &rowTerminal{onRow: func(row containerRow) {
		if row.state == rowUpdated {
			cancel()
		}
	}}
	app := newApplication(docker, ui)

	err := app.check(ctx, checkOptions{yes: true})
	if !errors.Is(err, errInterrupted) {
		t.Fatalf("check() error = %v, want errInterrupted", err)
	}
	if got := countDockerCalls(docker.calls, "run"); got != 1 {
		t.Fatalf("run calls = %d, want only the first container replaced", got)
	}
	last := ui.updates[len(ui.updates)-1]
	if last.state != rowNotStarted {
		t.Fatalf("last row = %#v, want not started", last)
	}
}

func TestReplaceIgnoresCancellation(t *testing.T) {
	docker := &contextDocker{fakeDocker: fakeDocker{inspectInfo: sampleContainerInfo(), replacementStarted: true}}
	app := newApplication(docker, &fakeTerminal{})
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	step := func(_ string, action func() error) error { return action() }
	if err := app.replace(ctx, sampleContainerInfo(), []string{"run", "--name", "web"}, "web-backup", "sha256:new", step); err != nil {
		t.Fatalf("replace() error = %v", err)
	}
	if got := docker.calls[len(docker.calls)-1]; got[0] != "rm" {
		t.Fatalf("replacement did not finish: %#v", docker.calls)
	}
}

type rowTerminal struct {
	fakeTerminal
	scannedRows []containerRow
	updates     []containerRow
	onRow       func(containerRow)
}

func (ui *rowTerminal) scanned(rows []containerRow) { ui.scannedRows = rows }

func (ui *rowTerminal) row(row containerRow) {
	if row.inUpdatePhase() {
		ui.updates = append(ui.updates, row)
	}
	if ui.onRow != nil {
		ui.onRow(row)
	}
}

// contextDocker fails like the real CLI once its context is cancelled.
type contextDocker struct {
	fakeDocker
}

func (d *contextDocker) run(ctx context.Context, args ...string) ([]byte, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return d.fakeDocker.run(ctx, args...)
}
