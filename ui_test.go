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
