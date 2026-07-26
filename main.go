package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
)

var version = "dev"

type cliOptions struct {
	containerName string
	yes           bool
	dryRun        bool
	noColor       bool
	showVersion   bool
}

func main() {
	options, err := parseFlags(os.Args[1:])
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(2)
	}

	if options.showVersion {
		fmt.Printf("drun %s\n", version)
		return
	}

	ui := newTerminalUI(os.Stdin, os.Stdout, os.Stderr, options.noColor)
	app := newApplication(newDockerCLI(), ui)
	if err := app.refresh(context.Background(), refreshOptions{
		containerName: options.containerName,
		yes:           options.yes,
		dryRun:        options.dryRun,
	}); err != nil {
		if !errors.Is(err, errCancelled) {
			ui.error(err.Error())
		}
		os.Exit(1)
	}
}

func parseFlags(args []string) (cliOptions, error) {
	var options cliOptions
	flags := flag.NewFlagSet("drun", flag.ContinueOnError)
	flags.SetOutput(os.Stderr)
	flags.BoolVar(&options.yes, "yes", false, "skip the confirmation prompt")
	flags.BoolVar(&options.yes, "y", false, "skip the confirmation prompt")
	flags.BoolVar(&options.dryRun, "dry-run", false, "inspect and print the replacement command only")
	flags.BoolVar(&options.noColor, "no-color", false, "disable colors and interactive rendering")
	flags.BoolVar(&options.showVersion, "version", false, "print version information")
	flags.Usage = func() {
		fmt.Fprintln(flags.Output(), "Usage: drun [options] <container_name>")
		flags.PrintDefaults()
	}

	if err := flags.Parse(args); err != nil {
		return options, err
	}
	if options.showVersion {
		return options, nil
	}
	if flags.NArg() != 1 {
		flags.Usage()
		return options, fmt.Errorf("exactly one container name is required")
	}

	options.containerName = flags.Arg(0)
	return options, nil
}
