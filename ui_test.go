package main

import (
	"bytes"
	"errors"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"
)

func TestShellQuote(t *testing.T) {
	args := []string{"docker", "run", "--env", "GREETING=hello world", "--env", "TOKEN=$(touch /tmp/nope)", "it's-safe"}
	want := `docker run --env 'GREETING=hello world' --env 'TOKEN=$(touch /tmp/nope)' 'it'"'"'s-safe'`
	if got := shellCommand(args); got != want {
		t.Fatalf("shellCommand() = %q, want %q", got, want)
	}
}

func TestFlagRouting(t *testing.T) {
	tests := []struct {
		name    string
		args    []string
		want    cliOptions
		wantErr bool
	}{
		{name: "container refresh", args: []string{"--dry-run", "--yes", "web"}, want: cliOptions{command: "refresh", containerName: "web", dryRun: true, yes: true}},
		{name: "explicit check", args: []string{"check", "--yes"}, want: cliOptions{command: "check", yes: true}},
		{name: "default check", want: cliOptions{command: "check"}},
		{name: "update all", args: []string{"--yes"}, want: cliOptions{command: "check", yes: true}},
		{name: "dry run requires container", args: []string{"--dry-run"}, wantErr: true},
		{name: "version command", args: []string{"version"}, want: cliOptions{command: "refresh", showVersion: true}},
		{name: "version flag", args: []string{"--version"}, want: cliOptions{command: "refresh", showVersion: true}},
		{name: "short version flag", args: []string{"-v"}, want: cliOptions{command: "refresh", showVersion: true}},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got, err := parseFlags(test.args)
			if test.wantErr {
				if err == nil {
					t.Fatalf("parseFlags(%q) error = nil", test.args)
				}
				return
			}
			if err != nil {
				t.Fatalf("parseFlags(%q) error = %v", test.args, err)
			}
			if got != test.want {
				t.Fatalf("parseFlags(%q) = %#v, want %#v", test.args, got, test.want)
			}
		})
	}
}

func TestHelpFlag(t *testing.T) {
	for _, args := range [][]string{{"--help"}, {"check", "--help"}} {
		options, err := parseFlags(args)
		if err != nil {
			t.Fatalf("parseFlags(%q) error = %v", args, err)
		}
		if !options.showHelp {
			t.Fatalf("parseFlags(%q) showHelp = false", args)
		}
	}
}

func TestVersionResolution(t *testing.T) {
	tests := []struct {
		injected string
		module   string
		want     string
	}{
		{injected: "v1.2.3", module: "v1.0.0", want: "v1.2.3"},
		{injected: "dev", module: "v1.2.3", want: "v1.2.3"},
		{injected: "dev", module: "(devel)", want: "dev"},
	}
	for _, test := range tests {
		if got := selectVersion(test.injected, test.module); got != test.want {
			t.Fatalf("selectVersion(%q, %q) = %q, want %q", test.injected, test.module, got, test.want)
		}
	}
}

func TestPlainStatus(t *testing.T) {
	var output, errorOutput bytes.Buffer
	ui := newPlainUI(strings.NewReader(""), &output, &errorOutput)

	ui.notice(noticeInfo, "details")
	ui.notice(noticeWarning, "careful")
	ui.notice(noticeSuccess, "done")
	ui.error("failed")

	if got, want := output.String(), "[i] details\n[!] careful\n[+] done\n"; got != want {
		t.Fatalf("plain status output = %q, want %q", got, want)
	}
	if got, want := errorOutput.String(), "[x] failed\n"; got != want {
		t.Fatalf("plain error output = %q, want %q", got, want)
	}
}

func TestPlainScanTable(t *testing.T) {
	var output bytes.Buffer
	ui := newPlainUI(strings.NewReader(""), &output, &output)
	ui.scanned(sampleRows())

	view := output.String()
	for _, text := range []string{"NAME", "web", "update", "v1.4.2 -> v1.5.0 [compose ×2]", "redis", "current", "local image"} {
		if !strings.Contains(view, text) {
			t.Fatalf("scan table does not contain %q:\n%s", text, view)
		}
	}
}

func TestConfirmDefault(t *testing.T) {
	reply := make(chan bool, 1)
	model := send(newTUIModel("web", nil), confirmRequestMsg{question: "Replace web?", reply: reply})
	view := model.View()
	keep, replace := strings.Index(view, "Keep current"), strings.LastIndex(view, "Replace")
	if keep == -1 || replace == -1 || keep >= replace {
		t.Fatalf("confirmation choices are not ordered safe-first: %q", view)
	}

	send(model, tea.KeyMsg{Type: tea.KeyEnter})
	if <-reply {
		t.Fatal("Enter confirmed replacement without selecting Replace")
	}

	model = send(newTUIModel("web", nil), confirmRequestMsg{question: "Replace web?", reply: reply})
	model = send(model, tea.KeyMsg{Type: tea.KeyRight})
	send(model, tea.KeyMsg{Type: tea.KeyEnter})
	if !<-reply {
		t.Fatal("Enter did not confirm after selecting Replace")
	}
}

func TestUpdateSelection(t *testing.T) {
	reply := make(chan []string, 1)
	model := send(newTUIModel("", nil), rowsMsg{rows: sampleRows()})
	model = send(model, selectRequestMsg{reply: reply})
	if model.rows[0].state != rowUpdatable || model.rows[1].state != rowUpdatable {
		t.Fatalf("updates are not listed first: %#v", model.rows)
	}

	model = send(model, tea.KeyMsg{Type: tea.KeyEnter})
	if len(reply) != 0 {
		t.Fatal("Enter submitted an empty selection")
	}
	model = send(model, tea.KeyMsg{Type: tea.KeySpace, Runes: []rune{' '}})
	if !model.selected["compose:demo/web"] || model.selected["container:api"] {
		t.Fatalf("selection after space = %#v", model.selected)
	}
	model = send(model, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'a'}})
	model = send(model, tea.KeyMsg{Type: tea.KeyDown})
	model = send(model, tea.KeyMsg{Type: tea.KeyDown})
	if model.cursor != 1 {
		t.Fatalf("cursor = %d, want it to stay on the last update", model.cursor)
	}
	send(model, tea.KeyMsg{Type: tea.KeyEnter})
	if got := <-reply; len(got) != 2 {
		t.Fatalf("selected keys = %#v, want both updates", got)
	}
}

func TestTableRendering(t *testing.T) {
	model := send(newTUIModel("", nil), tea.WindowSizeMsg{Width: 100, Height: 40})
	model = send(model, rowsMsg{rows: sampleRows()})
	view := model.View()
	for _, text := range []string{"drun", "2 updates · 1 up to date · 1 skipped", "web", "v1.4.2 → v1.5.0", "compose ×2", "ghcr.io/acme/web:main", "local image"} {
		if !strings.Contains(view, text) {
			t.Fatalf("view does not contain %q:\n%s", text, view)
		}
	}
	for _, line := range strings.Split(view, "\n") {
		if width := ansi.StringWidth(line); width > 100 {
			t.Fatalf("line is %d columns wide, wider than the terminal: %q", width, line)
		}
	}
}

func TestUpdatePhaseRows(t *testing.T) {
	model := send(newTUIModel("", nil), rowsMsg{rows: sampleRows()})
	row := sampleRows()[0]
	row.state, row.detail = rowUpdating, "Starting the replacement"
	model = send(model, rowMsg{row: row})

	view := model.View()
	if !strings.Contains(view, "Starting the replacement") || strings.Contains(view, "redis") {
		t.Fatalf("update view should only show containers being updated:\n%s", view)
	}
	if !strings.Contains(view, "2 updates") {
		t.Fatalf("header lost the scan summary during updates:\n%s", view)
	}
}

func TestInterrupt(t *testing.T) {
	calls := 0
	model := newTUIModel("", func() { calls++ })
	model = send(model, tea.KeyMsg{Type: tea.KeyCtrlC})
	model = send(model, tea.KeyMsg{Type: tea.KeyCtrlC})
	if calls != 1 {
		t.Fatalf("interrupt called %d times, want 1", calls)
	}
	if !strings.Contains(model.View(), "Stopping") {
		t.Fatalf("interrupted view does not explain the stop:\n%s", model.View())
	}
}

func TestFinishedView(t *testing.T) {
	model := send(newTUIModel("web", nil), stepStartMsg{title: "Inspecting web"})
	model = send(model, stepEndMsg{})
	model = send(model, finishMsg{err: errors.New("pull access denied")})
	if model.View() != "" {
		t.Fatal("finished program should leave the final frame to tui.close")
	}
	final := model.render()
	if !strings.Contains(final, "Inspecting web") || !strings.Contains(final, "pull access denied") || strings.Contains(final, "ctrl+c") {
		t.Fatalf("final frame = %q", final)
	}
}

func send(model tuiModel, message tea.Msg) tuiModel {
	updated, _ := model.Update(message)
	return updated.(tuiModel)
}

func sampleRows() []containerRow {
	web := &updateCandidate{Name: "demo-web-1", Compose: true, Replicas: []string{"demo-web-1", "demo-web-2"}, CurrentVersion: "v1.4.2", LatestVersion: "v1.5.0"}
	api := &updateCandidate{Name: "api", CurrentVersion: "a1b2c3", LatestVersion: "d4e5f6"}
	return []containerRow{
		{key: "compose:demo/web", name: "web", image: "ghcr.io/acme/web:main", state: rowUpdatable, detail: "v1.4.2 → v1.5.0", update: web},
		{key: "container:redis", name: "redis", image: "redis:7", state: rowCurrent, detail: "up to date"},
		{key: "container:api", name: "api", image: "acme/api:latest", state: rowUpdatable, detail: "a1b2c3 → d4e5f6", update: api},
		{key: "container:builder", name: "builder", image: "local/build:dev", state: rowSkipped, detail: "local image"},
	}
}
