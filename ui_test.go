package main

import (
	"bytes"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
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

func TestUpdateRendering(t *testing.T) {
	var output bytes.Buffer
	ui := &terminalUI{out: &output, interactive: true, width: 96}
	ui.updateAvailable(updateCandidate{
		Name:           "web",
		Image:          "ghcr.io/acme/web:main",
		Compose:        true,
		CurrentVersion: "v1.4.2",
		LatestVersion:  "v1.5.0",
	})

	view := output.String()
	for _, text := range []string{"update", "web", "compose", "v1.4.2 → v1.5.0", "ghcr.io/acme/web:main"} {
		if !strings.Contains(view, text) {
			t.Fatalf("updateAvailable() output does not contain %q: %q", text, view)
		}
	}
}

func TestPlainStatus(t *testing.T) {
	var output bytes.Buffer
	var errorOutput bytes.Buffer
	ui := &terminalUI{out: &output, err: &errorOutput}

	ui.info("details")
	ui.warning("careful")
	ui.success("done")
	ui.error("failed")

	if got, want := output.String(), "[i] details\n[!] careful\n[+] done\n"; got != want {
		t.Fatalf("plain status output = %q, want %q", got, want)
	}
	if got, want := errorOutput.String(), "[x] failed\n"; got != want {
		t.Fatalf("plain error output = %q, want %q", got, want)
	}
}

func TestGroupedStatus(t *testing.T) {
	var output bytes.Buffer
	ui := &terminalUI{out: &output, interactive: true}

	ui.group("web  1/2")
	ui.success("Updated")
	ui.endGroup()

	view := output.String()
	if !strings.Contains(view, "web  1/2") || !strings.Contains(view, "\n    ") {
		t.Fatalf("grouped status has no visible hierarchy: %q", view)
	}
}

func TestConfirmDefault(t *testing.T) {
	model := newConfirmModel("Replace?")
	if model.selection != 1 {
		t.Fatalf("default selection = %d, want Keep current", model.selection)
	}
	view := model.View()
	keep := strings.Index(view, "Keep current")
	replace := strings.LastIndex(view, "Replace")
	if keep == -1 || replace == -1 || keep >= replace {
		t.Fatalf("confirmation choices are not ordered safe-first: %q", view)
	}

	updated, _ := model.Update(tea.KeyMsg{Type: tea.KeyEnter})
	if updated.(confirmModel).confirmed {
		t.Fatal("Enter confirmed replacement without selecting Replace")
	}

	model = newConfirmModel("Replace?")
	updated, _ = model.Update(tea.KeyMsg{Type: tea.KeyRight})
	model = updated.(confirmModel)
	updated, _ = model.Update(tea.KeyMsg{Type: tea.KeyEnter})
	if !updated.(confirmModel).confirmed {
		t.Fatal("Enter did not confirm after selecting Replace")
	}
}
