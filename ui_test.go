package main

import "testing"

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
