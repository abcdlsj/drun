package main

import (
	"bytes"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
)

func TestShellCommandQuotesDisplayWithoutChangingArguments(t *testing.T) {
	args := []string{"docker", "run", "--env", "GREETING=hello world", "--env", "TOKEN=$(touch /tmp/nope)", "it's-safe"}
	want := `docker run --env 'GREETING=hello world' --env 'TOKEN=$(touch /tmp/nope)' 'it'"'"'s-safe'`
	if got := shellCommand(args); got != want {
		t.Fatalf("shellCommand() = %q, want %q", got, want)
	}
}

func TestParseFlags(t *testing.T) {
	options, err := parseFlags([]string{"--dry-run", "--yes", "web"})
	if err != nil {
		t.Fatalf("parseFlags() error = %v", err)
	}
	if options.containerName != "web" || !options.dryRun || !options.yes {
		t.Fatalf("parseFlags() = %#v", options)
	}
}

func TestParseCheckFlags(t *testing.T) {
	options, err := parseFlags([]string{"check", "--yes"})
	if err != nil {
		t.Fatalf("parseFlags() error = %v", err)
	}
	if options.command != "check" || !options.yes || options.containerName != "" {
		t.Fatalf("parseFlags() = %#v", options)
	}
}

func TestParseFlagsWithoutContainerStartsUpdateCheck(t *testing.T) {
	options, err := parseFlags(nil)
	if err != nil {
		t.Fatalf("parseFlags(nil) error = %v", err)
	}
	if options.command != "check" || options.containerName != "" {
		t.Fatalf("parseFlags(nil) = %#v", options)
	}
}

func TestParseFlagsYesWithoutContainerUpdatesAll(t *testing.T) {
	options, err := parseFlags([]string{"--yes"})
	if err != nil {
		t.Fatalf("parseFlags(--yes) error = %v", err)
	}
	if options.command != "check" || !options.yes {
		t.Fatalf("parseFlags(--yes) = %#v", options)
	}
}

func TestParseFlagsDryRunRequiresContainer(t *testing.T) {
	if _, err := parseFlags([]string{"--dry-run"}); err == nil {
		t.Fatal("parseFlags(--dry-run) error = nil, want an error")
	}
}

func TestParseFlagsHelpIsNotAnError(t *testing.T) {
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

func TestUpdateAvailableUsesCompactTwoLineLayout(t *testing.T) {
	var output bytes.Buffer
	ui := &terminalUI{out: &output, interactive: true, width: 96}
	ui.updateAvailable(updateCandidate{
		Name:           "web",
		Image:          "ghcr.io/acme/web:main",
		CurrentVersion: "v1.4.2",
		LatestVersion:  "v1.5.0",
	})

	view := output.String()
	for _, text := range []string{"update", "web", "v1.4.2 → v1.5.0", "ghcr.io/acme/web:main"} {
		if !strings.Contains(view, text) {
			t.Fatalf("updateAvailable() output does not contain %q: %q", text, view)
		}
	}
	if lines := strings.Count(strings.TrimSpace(view), "\n") + 1; lines != 2 {
		t.Fatalf("updateAvailable() rendered %d lines, want 2: %q", lines, view)
	}
}

func TestStatusMarkersRemainASCIIInPlainOutput(t *testing.T) {
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

func TestUpdateSelectorDoesNotSeparateEveryItemWithBlankLines(t *testing.T) {
	model := newUpdateSelectorModel([]updateCandidate{
		{Name: "api", Image: "ghcr.io/acme/api:main", CurrentVersion: "v1", LatestVersion: "v2"},
		{Name: "web", Image: "ghcr.io/acme/web:main", CurrentVersion: "v3", LatestVersion: "v4"},
	})

	view := model.View()
	if blankLines := strings.Count(view, "\n\n"); blankLines != 2 {
		t.Fatalf("selector rendered %d blank separators, want only header and footer separation: %q", blankLines, view)
	}
	if !strings.Contains(view, "2 of 2 selected") {
		t.Fatalf("selector does not show a concise selection count: %q", view)
	}
}

func TestUpdateSelectorFitsMoreRowsAfterCompaction(t *testing.T) {
	model := newUpdateSelectorModel(make([]updateCandidate, 10))
	updated, _ := model.Update(tea.WindowSizeMsg{Height: 12})
	got := updated.(updateSelectorModel).maxRows
	if got != 3 {
		t.Fatalf("maxRows = %d, want 3", got)
	}
}

func TestConfirmationPlacesSafeDefaultBeforeDestructiveAction(t *testing.T) {
	view := newConfirmModel("Replace?").View()
	keep := strings.Index(view, "Keep current")
	replace := strings.LastIndex(view, "Replace")
	if keep == -1 || replace == -1 || keep >= replace {
		t.Fatalf("confirmation choices are not ordered safe-first: %q", view)
	}
}
