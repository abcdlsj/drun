package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"runtime/debug"
	"strings"
	"syscall"

	"github.com/charmbracelet/lipgloss"
	"golang.org/x/term"
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
		fmt.Printf("drun %s\n", currentVersion())
		return
	}
	if options.showHelp {
		return
	}

	// Interrupts cancel pending work; replacements already underway finish or roll back first.
	signalCtx, stopSignals := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stopSignals()
	ctx, cancel := context.WithCancel(signalCtx)
	defer cancel()

	run := func(app *application) error {
		if options.command == "check" {
			return app.check(ctx, checkOptions{yes: options.yes})
		}
		return app.refresh(ctx, refreshOptions{
			containerName: options.containerName,
			yes:           options.yes,
			dryRun:        options.dryRun,
		})
	}

	var runErr error
	if interactiveTerminal(os.Stdin, os.Stdout, options.noColor) {
		// Resolve adaptive colors before Bubble Tea owns the terminal input.
		lipgloss.HasDarkBackground()
		ui := startTUI(os.Stdin, os.Stdout, options.containerName, cancel)
		runErr = run(newApplication(newDockerCLI(), ui))
		ui.close(os.Stdout, runErr)
	} else {
		ui := newPlainUI(os.Stdin, os.Stdout, os.Stderr)
		runErr = run(newApplication(newDockerCLI(), ui))
		if runErr != nil && !errors.Is(runErr, errCancelled) {
			ui.error(runErr.Error())
		}
	}
	if runErr != nil {
		os.Exit(1)
	}
}

func interactiveTerminal(in, out *os.File, noColor bool) bool {
	return term.IsTerminal(int(in.Fd())) && term.IsTerminal(int(out.Fd())) && !noColor && os.Getenv("NO_COLOR") == ""
}

func parseFlags(args []string) (cliOptions, error) {
	options := cliOptions{command: "refresh"}
	if len(args) > 0 && args[0] == "version" {
		if len(args) != 1 {
			return options, fmt.Errorf("drun version does not accept arguments")
		}
		options.showVersion = true
		return options, nil
	}
	if len(args) > 0 && args[0] == "check" {
		options.command = "check"
		flags := flag.NewFlagSet("drun check", flag.ContinueOnError)
		flags.SetOutput(os.Stderr)
		addSharedFlags(flags, &options, "update every container with an available image")
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
	addSharedFlags(flags, &options, "skip confirmation or update every available container")
	flags.BoolVar(&options.dryRun, "dry-run", false, "inspect and print the replacement command only")
	flags.BoolVar(&options.showVersion, "version", false, "print version information")
	flags.BoolVar(&options.showVersion, "v", false, "print version information")
	flags.Usage = func() {
		fmt.Fprintln(flags.Output(), "Usage: drun [options] [container_name]")
		fmt.Fprintln(flags.Output(), "       drun check [options]")
		fmt.Fprintln(flags.Output(), "       drun version")
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
	if flags.NArg() == 0 {
		if options.dryRun {
			flags.Usage()
			return options, fmt.Errorf("--dry-run requires a container name")
		}
		options.command = "check"
		return options, nil
	}
	if flags.NArg() > 1 {
		flags.Usage()
		return options, fmt.Errorf("at most one container name is accepted")
	}

	options.containerName = flags.Arg(0)
	return options, nil
}

func addSharedFlags(flags *flag.FlagSet, options *cliOptions, yesUsage string) {
	flags.BoolVar(&options.yes, "yes", false, yesUsage)
	flags.BoolVar(&options.yes, "y", false, yesUsage)
	flags.BoolVar(&options.noColor, "no-color", false, "disable colors and interactive rendering")
}

func currentVersion() string {
	moduleVersion := ""
	if buildInfo, ok := debug.ReadBuildInfo(); ok {
		moduleVersion = buildInfo.Main.Version
	}
	return selectVersion(version, moduleVersion)
}

func selectVersion(injected, module string) string {
	for _, candidate := range []string{injected, module} {
		candidate = strings.TrimSpace(candidate)
		if candidate != "" && candidate != "dev" && candidate != "(devel)" {
			return candidate
		}
	}
	return "dev"
}
