package main

import (
	"fmt"
	"io"

	tea "github.com/charmbracelet/bubbletea"
)

// tui drives one Bubble Tea program for the whole run. The application calls
// its methods from its own goroutine; each call becomes a message to the model.
type tui struct {
	program *tea.Program
	exited  chan struct{}
	final   tuiModel
}

func startTUI(in io.Reader, out io.Writer, title string, interrupt func()) *tui {
	t := &tui{exited: make(chan struct{})}
	t.program = tea.NewProgram(
		newTUIModel(title, interrupt),
		tea.WithInput(in),
		tea.WithOutput(out),
		tea.WithoutSignalHandler(),
	)
	go func() {
		defer close(t.exited)
		finalModel, err := t.program.Run()
		if model, ok := finalModel.(tuiModel); ok && err == nil {
			t.final = model
		}
	}()
	return t
}

// close renders the outcome, stops the program, and prints the final frame so it
// stays in the scrollback in full, even when it is taller than the terminal.
func (t *tui) close(out io.Writer, err error) {
	t.program.Send(finishMsg{err: err})
	<-t.exited
	if t.final.finished {
		fmt.Fprint(out, t.final.render())
	}
}

func (t *tui) interactive() bool { return true }

func (t *tui) notice(kind noticeKind, message string) {
	t.program.Send(noticeMsg{kind: kind, text: message})
}

func (t *tui) step(title string, action func() error) error {
	t.program.Send(stepStartMsg{title: title})
	err := action()
	t.program.Send(stepEndMsg{err: err})
	return err
}

func (t *tui) target(info *containerInfo) {
	t.program.Send(targetMsg{info: info})
}

func (t *tui) plan(update *updateCandidate, command []string) {
	t.program.Send(planMsg{update: update, command: command})
}

func (t *tui) confirm(question string) (bool, error) {
	reply := make(chan bool, 1)
	t.program.Send(confirmRequestMsg{question: question, reply: reply})
	select {
	case confirmed := <-reply:
		return confirmed, nil
	case <-t.exited:
		return false, errCancelled
	}
}

func (t *tui) rows(rows []containerRow) {
	t.program.Send(rowsMsg{rows: rows})
}

func (t *tui) row(row containerRow) {
	t.program.Send(rowMsg{row: row})
}

func (t *tui) scanned([]containerRow) {}

func (t *tui) selectUpdates([]containerRow) ([]string, error) {
	reply := make(chan []string, 1)
	t.program.Send(selectRequestMsg{reply: reply})
	select {
	case keys := <-reply:
		return keys, nil
	case <-t.exited:
		return nil, nil
	}
}
