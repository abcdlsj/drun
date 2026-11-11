package main

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"strings"
	"text/tabwriter"
)

// plainUI writes line-oriented output for pipes, logs, and --no-color.
type plainUI struct {
	in  *bufio.Reader
	out io.Writer
	err io.Writer
}

func newPlainUI(in io.Reader, out, errOut io.Writer) *plainUI {
	return &plainUI{in: bufio.NewReader(in), out: out, err: errOut}
}

func (ui *plainUI) interactive() bool { return false }

func (ui *plainUI) notice(kind noticeKind, message string) {
	fmt.Fprintf(ui.out, "[%s] %s\n", plainMark(kind), message)
}

func (ui *plainUI) error(message string) {
	fmt.Fprintf(ui.err, "[x] %s\n", message)
}

func (ui *plainUI) step(title string, action func() error) error {
	err := action()
	if err != nil {
		fmt.Fprintf(ui.out, "[x]  %s\n", title)
		return err
	}
	fmt.Fprintf(ui.out, "[ok] %s\n", title)
	return nil
}

func (ui *plainUI) target(info *containerInfo) {
	kind := "standalone"
	if isComposeContainer(info) {
		kind = "compose"
	}
	fmt.Fprintf(ui.out, "container: %s (%s, %s)\nimage: %s\n", containerName(info), info.State.Status, kind, info.Config.Image)
}

func (ui *plainUI) plan(update *updateCandidate, command []string) {
	if update != nil {
		fmt.Fprintf(ui.out, "update: %s -> %s\n", update.CurrentVersion, update.LatestVersion)
	}
	fmt.Fprintf(ui.out, "$ %s\n", shellCommand(command))
}

func (ui *plainUI) confirm(question string) (bool, error) {
	fmt.Fprintf(ui.out, "%s [y/N]: ", question)
	response, err := ui.in.ReadString('\n')
	if err != nil && len(response) == 0 {
		return false, err
	}
	response = strings.ToLower(strings.TrimSpace(response))
	return response == "y" || response == "yes", nil
}

func (ui *plainUI) rows([]containerRow) {}

func (ui *plainUI) row(row containerRow) {
	switch row.state {
	case rowUpdated:
		fmt.Fprintf(ui.out, "[+] %s updated: %s\n", row.name, row.detail)
	case rowUnchanged:
		fmt.Fprintf(ui.out, "[i] %s: %s\n", row.name, row.detail)
	case rowUpdateFailed:
		fmt.Fprintf(ui.out, "[x] %s update failed: %s\n", row.name, row.detail)
	case rowNotStarted:
		fmt.Fprintf(ui.out, "[!] %s: %s\n", row.name, row.detail)
	}
}

func (ui *plainUI) scanned(rows []containerRow) {
	writer := tabwriter.NewWriter(ui.out, 0, 0, 2, ' ', 0)
	fmt.Fprintln(writer, "NAME\tSTATE\tIMAGE\tDETAIL")
	for _, row := range rows {
		detail := strings.ReplaceAll(row.detail, "→", "->")
		if row.update != nil && row.update.Compose {
			detail += " [compose" + replicaSuffix(row.update) + "]"
		}
		fmt.Fprintf(writer, "%s\t%s\t%s\t%s\n", row.name, plainState(row.state), row.image, detail)
	}
	_ = writer.Flush()
}

func (ui *plainUI) selectUpdates([]containerRow) ([]string, error) {
	return nil, errors.New("selecting updates requires an interactive terminal")
}

func plainMark(kind noticeKind) string {
	switch kind {
	case noticeWarning:
		return "!"
	case noticeSuccess:
		return "+"
	default:
		return "i"
	}
}

func plainState(state rowState) string {
	switch state {
	case rowUpdatable:
		return "update"
	case rowCurrent:
		return "current"
	case rowSkipped:
		return "skipped"
	case rowFailed:
		return "failed"
	default:
		return "checking"
	}
}

func replicaSuffix(update *updateCandidate) string {
	if len(update.Replicas) > 1 {
		return fmt.Sprintf(" ×%d", len(update.Replicas))
	}
	return ""
}
