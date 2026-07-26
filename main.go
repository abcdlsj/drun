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
	command       string
	containerName string
	yes           bool
	dryRun        bool
	noColor       bool
	showVersion   bool
	showHelp      bool
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
	if options.showHelp {
		return
	}

	ui := newTerminalUI(os.Stdin, os.Stdout, os.Stderr, options.noColor)
	app := newApplication(newDockerCLI(), ui)
	var runErr error
	if options.command == "check" {
		runErr = app.check(context.Background(), checkOptions{yes: options.yes})
	} else {
		runErr = app.refresh(context.Background(), refreshOptions{
			containerName: options.containerName,
			yes:           options.yes,
			dryRun:        options.dryRun,
		})
	}
	if runErr != nil {
		if !errors.Is(runErr, errCancelled) {
			ui.error(runErr.Error())
		}
		os.Exit(1)
	}
}

func parseFlags(args []string) (cliOptions, error) {
	options := cliOptions{command: "refresh"}
	if len(args) > 0 && args[0] == "check" {
		options.command = "check"
		flags := flag.NewFlagSet("drun check", flag.ContinueOnError)
		flags.SetOutput(os.Stderr)
		flags.BoolVar(&options.yes, "yes", false, "update every container with an available image")
		flags.BoolVar(&options.yes, "y", false, "update every container with an available image")
		flags.BoolVar(&options.noColor, "no-color", false, "disable colors and interactive rendering")
		flags.Usage = func() {
			fmt.Fprintln(flags.Output(), "Usage: drun check [options]")
			flags.PrintDefaults()
		}
		if err := flags.Parse(args[1:]); err != nil {
			if errors.Is(err, flag.ErrHelp) {
				options.showHelp = true
				return options, nil
			}
			return options, err
		}
		if flags.NArg() != 0 {
			flags.Usage()
			return options, fmt.Errorf("drun check does not accept container names")
		}
		return options, nil
	}

	flags := flag.NewFlagSet("drun", flag.ContinueOnError)
	flags.SetOutput(os.Stderr)
	flags.BoolVar(&options.yes, "yes", false, "skip the confirmation prompt")
	flags.BoolVar(&options.yes, "y", false, "skip the confirmation prompt")
	flags.BoolVar(&options.dryRun, "dry-run", false, "inspect and print the replacement command only")
	flags.BoolVar(&options.noColor, "no-color", false, "disable colors and interactive rendering")
	flags.BoolVar(&options.showVersion, "version", false, "print version information")
	flags.Usage = func() {
		fmt.Fprintln(flags.Output(), "Usage: drun [options] <container_name>")
		fmt.Fprintln(flags.Output(), "       drun check [options]")
		flags.PrintDefaults()
	}

	if err := flags.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			options.showHelp = true
			return options, nil
		}
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
