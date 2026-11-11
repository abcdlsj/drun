package main

import (
	"github.com/charmbracelet/bubbles/spinner"
	tea "github.com/charmbracelet/bubbletea"
)

type (
	stepStartMsg struct{ title string }
	stepEndMsg   struct{ err error }
	noticeMsg    struct {
		kind noticeKind
		text string
	}
	targetMsg struct{ info *containerInfo }
	planMsg   struct {
		update  *updateCandidate
		command []string
	}
	confirmRequestMsg struct {
		question string
		reply    chan bool
	}
	rowsMsg          struct{ rows []containerRow }
	rowMsg           struct{ row containerRow }
	selectRequestMsg struct{ reply chan []string }
	finishMsg        struct{ err error }
)

type tuiPhase int

const (
	phaseRunning tuiPhase = iota
	phaseSelecting
	phaseConfirming
)

type stepState int

const (
	stepRunning stepState = iota
	stepDone
	stepFailed
)

type step struct {
	title string
	state stepState
}

type notice struct {
	kind noticeKind
	text string
}

type plan struct {
	update  *updateCandidate
	command []string
}

// tuiModel holds everything the view shows. A single-container refresh fills
// steps, plan, and confirmation; a scan fills rows and selection.
type tuiModel struct {
	title   string
	info    *containerInfo
	steps   []step
	plan    *plan
	planAt  int // steps before planAt ran before the plan was shown
	notices []notice
	rows    []containerRow
	scanned bool

	phase        tuiPhase
	cursor       int
	selected     map[string]bool
	selectReply  chan []string
	question     string
	replace      bool
	confirmReply chan bool

	spinner     spinner.Model
	width       int
	height      int
	interrupt   func()
	interrupted bool
	finished    bool
	err         error
}

func newTUIModel(title string, interrupt func()) tuiModel {
	component := spinner.New(spinner.WithSpinner(spinner.MiniDot), spinner.WithStyle(fg(colorAccent)))
	if interrupt == nil {
		interrupt = func() {}
	}
	return tuiModel{title: title, spinner: component, interrupt: interrupt, selected: map[string]bool{}}
}

func (m tuiModel) Init() tea.Cmd {
	return m.spinner.Tick
}

func (m tuiModel) Update(message tea.Msg) (tea.Model, tea.Cmd) {
	switch message := message.(type) {
	case tea.WindowSizeMsg:
		m.width, m.height = message.Width, message.Height
	case spinner.TickMsg:
		if m.finished {
			return m, nil
		}
		var command tea.Cmd
		m.spinner, command = m.spinner.Update(message)
		return m, command
	case tea.KeyMsg:
		return m.handleKey(message), nil
	case stepStartMsg:
		m.steps = append(m.steps, step{title: message.title})
	case stepEndMsg:
		for index := len(m.steps) - 1; index >= 0; index-- {
			if m.steps[index].state == stepRunning {
				m.steps[index].state = stepDone
				if message.err != nil {
					m.steps[index].state = stepFailed
				}
				break
			}
		}
	case noticeMsg:
		m.notices = append(m.notices, notice{kind: message.kind, text: message.text})
	case targetMsg:
		m.info = message.info
	case planMsg:
		m.plan, m.planAt = &plan{update: message.update, command: message.command}, len(m.steps)
	case confirmRequestMsg:
		m.phase, m.question, m.replace, m.confirmReply = phaseConfirming, message.question, false, message.reply
	case rowsMsg:
		m.rows, m.scanned = append([]containerRow(nil), message.rows...), true
	case rowMsg:
		for index := range m.rows {
			if m.rows[index].key == message.row.key {
				m.rows[index] = message.row
			}
		}
	case selectRequestMsg:
		m.phase, m.cursor, m.selectReply = phaseSelecting, 0, message.reply
	case finishMsg:
		m.finished, m.err = true, message.err
		return m, tea.Quit
	}
	return m, nil
}

func (m tuiModel) handleKey(key tea.KeyMsg) tuiModel {
	switch m.phase {
	case phaseSelecting:
		return m.handleSelectKey(key)
	case phaseConfirming:
		return m.handleConfirmKey(key)
	}
	if key.String() == "ctrl+c" && !m.interrupted {
		m.interrupted = true
		m.interrupt()
	}
	return m
}

func (m tuiModel) handleSelectKey(key tea.KeyMsg) tuiModel {
	updates := m.updates()
	switch key.String() {
	case "up", "k":
		m.cursor = max(0, m.cursor-1)
	case "down", "j":
		m.cursor = min(len(updates)-1, m.cursor+1)
	case " ", "x":
		if m.cursor < len(updates) {
			rowKey := updates[m.cursor].key
			m.selected[rowKey] = !m.selected[rowKey]
		}
	case "a":
		selectAll := m.selectedCount() < len(updates)
		for _, row := range updates {
			m.selected[row.key] = selectAll
		}
	case "enter":
		var keys []string
		for _, row := range updates {
			if m.selected[row.key] {
				keys = append(keys, row.key)
			}
		}
		if len(keys) > 0 {
			m.selectReply <- keys
			m.phase = phaseRunning
		}
	case "q", "esc", "ctrl+c":
		m.selectReply <- nil
		m.phase = phaseRunning
	}
	return m
}

func (m tuiModel) handleConfirmKey(key tea.KeyMsg) tuiModel {
	answer := func(confirmed bool) tuiModel {
		m.confirmReply <- confirmed
		m.phase = phaseRunning
		return m
	}
	switch key.String() {
	case "left", "right", "h", "l", "tab", "shift+tab":
		m.replace = !m.replace
	case "y":
		return answer(true)
	case "n", "q", "esc", "ctrl+c":
		return answer(false)
	case "enter":
		return answer(m.replace)
	}
	return m
}

// updates lists the rows that can be selected, in display order.
func (m tuiModel) updates() []containerRow {
	return m.rowsIn(rowUpdatable)
}

func (m tuiModel) rowsIn(states ...rowState) []containerRow {
	var rows []containerRow
	for _, row := range m.rows {
		for _, state := range states {
			if row.state == state {
				rows = append(rows, row)
				break
			}
		}
	}
	return rows
}

func (m tuiModel) selectedCount() int {
	count := 0
	for _, row := range m.updates() {
		if m.selected[row.key] {
			count++
		}
	}
	return count
}

func (m tuiModel) updating() bool {
	for _, row := range m.rows {
		if row.inUpdatePhase() {
			return true
		}
	}
	return false
}
