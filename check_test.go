package main

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
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
	})
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
	references := []string{"example/a:latest", "example/b:latest", "example/c:latest", "example/d:latest", "example/e:latest"}

	type result struct {
		images map[string]*imageInfo
		errors map[string]error
	}
	done := make(chan result, 1)
	go func() {
		images, imageErrors := app.checkLatestImages(context.Background(), references)
		done <- result{images: images, errors: imageErrors}
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
		if len(got.images) != len(references) || len(got.errors) != 0 {
			t.Fatalf("checkLatestImages() returned %d images and %d errors", len(got.images), len(got.errors))
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

func TestUpdateSelection(t *testing.T) {
	updates := []updateCandidate{{Name: "api"}, {Name: "web"}}
	model := newUpdateSelectorModel(updates)
	if !model.selected[0] || !model.selected[1] {
		t.Fatalf("default selection = %#v, want every update selected", model.selected)
	}

	updated, _ := model.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{' '}})
	model = updated.(updateSelectorModel)
	if model.selected[0] || !model.selected[1] {
		t.Fatalf("selection after space = %#v", model.selected)
	}

	updated, _ = model.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'a'}})
	model = updated.(updateSelectorModel)
	if !model.selected[0] || !model.selected[1] {
		t.Fatalf("selection after a = %#v, want all selected", model.selected)
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
		d.started <- args[1]
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
	old := imageInfo{ID: "sha256:old", RepoTags: []string{"example/app:v1.0.0"}}
	latest := imageInfo{ID: "sha256:new", RepoTags: []string{"example/app:v1.1.0"}}
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
		info, ok := d.containers[args[1]]
		if !ok {
			return nil, fmt.Errorf("unknown container %s", args[1])
		}
		copy := *info
		if d.updated[args[1]] {
			copy.Image = d.latest.ID
		}
		return json.Marshal([]containerInfo{copy})
	case "image":
		reference := args[2]
		if reference == "example/app:latest" {
			return json.Marshal([]imageInfo{d.latest})
		}
		image, ok := d.images[reference]
		if !ok {
			return nil, fmt.Errorf("unknown image %s", reference)
		}
		return json.Marshal([]imageInfo{image})
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
