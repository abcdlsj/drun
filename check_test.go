package main

import (
	"context"
	"encoding/json"
	"fmt"
	"reflect"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
)

func TestCheckPullsSharedImageOnceAndUpdatesEveryOutdatedContainer(t *testing.T) {
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

func TestCheckDoesNotUpdateWithoutInteractiveTerminalOrYes(t *testing.T) {
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

func TestDisplayImageVersionsPrefersTagsThenRevisionThenImageID(t *testing.T) {
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

func TestUpdateSelectorDefaultsToAllAndTogglesSelection(t *testing.T) {
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
	calls      [][]string
	containers map[string]*containerInfo
	images     map[string]imageInfo
	latest     imageInfo
	updated    map[string]bool
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
	}
	return nil, nil
}

func countDockerCalls(calls [][]string, command string) int {
	count := 0
	for _, call := range calls {
		if len(call) > 0 && reflect.DeepEqual(call[0], command) {
			count++
		}
	}
	return count
}
