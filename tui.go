package main

import (
	"errors"
	"fmt"
	"io"
	"sort"
	"strings"

	"github.com/charmbracelet/bubbles/spinner"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
)

var (
	colorAccent = lipgloss.AdaptiveColor{Light: "#0A7EA4", Dark: "#39D0D8"}
	colorGreen  = lipgloss.AdaptiveColor{Light: "#1A7F37", Dark: "#5EE38E"}
	colorYellow = lipgloss.AdaptiveColor{Light: "#9A6700", Dark: "#FFD866"}
	colorRed    = lipgloss.AdaptiveColor{Light: "#CF222E", Dark: "#FF6B6B"}
	colorMuted  = lipgloss.AdaptiveColor{Light: "#6E7781", Dark: "#7F8A9A"}
	colorText   = lipgloss.AdaptiveColor{Light: "#1F2328", Dark: "#E7EDF5"}

	styleBrand  = lipgloss.NewStyle().Bold(true).Foreground(colorAccent)
	styleAccent = lipgloss.NewStyle().Foreground(colorAccent)
	styleGreen  = lipgloss.NewStyle().Foreground(colorGreen)
	styleYellow = lipgloss.NewStyle().Foreground(colorYellow)
	styleRed    = lipgloss.NewStyle().Foreground(colorRed)
	styleMuted  = lipgloss.NewStyle().Foreground(colorMuted)
	styleText   = lipgloss.NewStyle().Foreground(colorText)
	styleStrong = lipgloss.NewStyle().Bold(true).Foreground(colorText)
)

const indent = "  "

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

type itemKind int

const (
	itemStep itemKind = iota
	itemNotice
	itemPlan
	itemTable
)

type stepState int

const (
	stepRunning stepState = iota
	stepDone
	stepFailed
)

type tuiItem struct {
	kind    itemKind
	text    string
	state   stepState
	notice  noticeKind
	update  *updateCandidate
	command []string
}

type tuiModel struct {
	title     string
	info      *containerInfo
	items     []tuiItem
	rows      []containerRow
	scanStats string

	phase         tuiPhase
	cursor        int
	selected      map[string]bool
	selectReply   chan []string
	question      string
	replaceChoice bool
	confirmReply  chan bool

	spinner     spinner.Model
	width       int
	height      int
	interrupt   func()
	interrupted bool
	finished    bool
	err         error
}

func newTUIModel(title string, interrupt func()) tuiModel {
	component := spinner.New()
	component.Spinner = spinner.MiniDot
	component.Style = styleAccent
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
		return m.handleKey(message)
	case stepStartMsg:
		m.items = append(m.items, tuiItem{kind: itemStep, text: message.title})
	case stepEndMsg:
		for index := len(m.items) - 1; index >= 0; index-- {
			if m.items[index].kind == itemStep && m.items[index].state == stepRunning {
				m.items[index].state = stepDone
				if message.err != nil {
					m.items[index].state = stepFailed
				}
				break
			}
		}
	case noticeMsg:
		m.items = append(m.items, tuiItem{kind: itemNotice, notice: message.kind, text: message.text})
	case targetMsg:
		m.info = message.info
	case planMsg:
		m.items = append(m.items, tuiItem{kind: itemPlan, update: message.update, command: message.command})
	case confirmRequestMsg:
		m.phase, m.question, m.replaceChoice, m.confirmReply = phaseConfirming, message.question, false, message.reply
	case rowsMsg:
		m.rows = append([]containerRow(nil), message.rows...)
		m.items = append(m.items, tuiItem{kind: itemTable})
		m.scanStats = scanStats(m.rows)
	case rowMsg:
		for index := range m.rows {
			if m.rows[index].key == message.row.key {
				m.rows[index] = message.row
			}
		}
		if !message.row.inUpdatePhase() {
			m.scanStats = scanStats(m.rows)
		}
	case selectRequestMsg:
		sort.SliceStable(m.rows, func(i, j int) bool { return rowRank(m.rows[i].state) < rowRank(m.rows[j].state) })
		m.phase, m.cursor, m.selectReply = phaseSelecting, 0, message.reply
	case finishMsg:
		m.finished, m.err = true, message.err
		return m, tea.Quit
	}
	return m, nil
}

func (m tuiModel) handleKey(key tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch m.phase {
	case phaseSelecting:
		return m.handleSelectKey(key), nil
	case phaseConfirming:
		return m.handleConfirmKey(key), nil
	}
	if key.String() == "ctrl+c" && !m.interrupted {
		m.interrupted = true
		m.interrupt()
	}
	return m, nil
}

func (m tuiModel) handleSelectKey(key tea.KeyMsg) tuiModel {
	updatable := m.updatableCount()
	switch key.String() {
	case "up", "k":
		if m.cursor > 0 {
			m.cursor--
		}
	case "down", "j":
		if m.cursor < updatable-1 {
			m.cursor++
		}
	case " ", "x":
		if m.cursor < updatable {
			rowKey := m.rows[m.cursor].key
			m.selected[rowKey] = !m.selected[rowKey]
		}
	case "a":
		selectAll := m.selectedCount() < updatable
		for index := range updatable {
			m.selected[m.rows[index].key] = selectAll
		}
	case "enter":
		if m.selectedCount() == 0 {
			break
		}
		var keys []string
		for index := range updatable {
			if m.selected[m.rows[index].key] {
				keys = append(keys, m.rows[index].key)
			}
		}
		m.selectReply <- keys
		m.phase = phaseRunning
	case "q", "esc", "ctrl+c":
		m.selectReply <- nil
		m.phase = phaseRunning
	}
	return m
}

func (m tuiModel) handleConfirmKey(key tea.KeyMsg) tuiModel {
	reply := func(confirmed bool) tuiModel {
		m.confirmReply <- confirmed
		m.phase = phaseRunning
		return m
	}
	switch key.String() {
	case "left", "right", "h", "l", "tab", "shift+tab":
		m.replaceChoice = !m.replaceChoice
	case "y":
		return reply(true)
	case "n", "q", "esc", "ctrl+c":
		return reply(false)
	case "enter":
		return reply(m.replaceChoice)
	}
	return m
}

func (m tuiModel) updatableCount() int {
	count := 0
	for _, row := range m.rows {
		if row.state == rowUpdatable {
			count++
		}
	}
	return count
}

func (m tuiModel) selectedCount() int {
	count := 0
	for _, row := range m.rows {
		if row.state == rowUpdatable && m.selected[row.key] {
			count++
		}
	}
	return count
}

// View draws the live frame. The finished frame is printed by tui.close instead,
// because the inline renderer drops lines that do not fit on screen.
func (m tuiModel) View() string {
	if m.finished {
		return ""
	}
	return m.render()
}

func (m tuiModel) render() string {
	header := m.renderHeader()
	footer := m.renderFooter()
	var before, after []string
	tableAt := -1
	for index, item := range m.items {
		if item.kind == itemTable {
			tableAt = index
			continue
		}
		rendered := m.renderItem(item)
		if item.kind == itemStep && index > 0 && m.items[index-1].kind != itemStep {
			rendered = "\n" + rendered
		}
		if tableAt < 0 {
			before = append(before, rendered)
		} else {
			after = append(after, rendered)
		}
	}

	var sections []string
	sections = append(sections, header)
	sections = append(sections, before...)
	if tableAt >= 0 {
		budget := 0
		if m.height > 0 && !m.finished {
			used := lipgloss.Height(strings.Join(append(append(append([]string{header}, before...), after...), footer), "\n"))
			budget = max(3, m.height-used-2)
		}
		sections = append(sections, m.renderTable(budget))
	}
	sections = append(sections, after...)
	if footer != "" {
		sections = append(sections, footer)
	}
	return strings.Join(sections, "\n") + "\n"
}

func (m tuiModel) contentWidth() int {
	if m.width <= 0 {
		return 96
	}
	return max(40, m.width-len(indent))
}

func (m tuiModel) renderHeader() string {
	line := indent + styleBrand.Render("drun")
	if m.title == "" {
		stats := m.scanStats
		if stats == "" {
			stats = "checking for updates"
		}
		return "\n" + line + "  " + styleMuted.Render(stats) + "\n"
	}
	line += "  " + styleStrong.Render(m.title)
	if m.info == nil {
		return "\n" + line + "\n"
	}
	status := styleGreen.Render("● " + m.info.State.Status)
	if !m.info.State.Running {
		status = styleYellow.Render("● " + m.info.State.Status)
	}
	kind := "standalone"
	if isComposeContainer(m.info) {
		kind = "compose"
	}
	details := styleMuted.Render(ansi.Truncate(m.info.Config.Image+" · "+kind, m.contentWidth()-6, "…"))
	return "\n" + line + "  " + status + "\n" + indent + "      " + details + "\n"
}

func (m tuiModel) renderItem(item tuiItem) string {
	switch item.kind {
	case itemStep:
		switch item.state {
		case stepRunning:
			return indent + m.spinner.View() + " " + styleText.Render(item.text)
		case stepFailed:
			return indent + styleRed.Render("×") + " " + styleText.Render(item.text)
		default:
			return indent + styleGreen.Render("✓") + " " + styleMuted.Render(item.text)
		}
	case itemNotice:
		mark, style := "·", styleAccent
		switch item.notice {
		case noticeWarning:
			mark, style = "!", styleYellow
		case noticeSuccess:
			mark, style = "✓", styleGreen
		}
		text := lipgloss.NewStyle().Width(m.contentWidth() - 2).Render(item.text)
		return "\n" + prefixLines(text, indent+style.Bold(true).Render(mark)+" ", indent+"  ")
	case itemPlan:
		return m.renderPlan(item)
	}
	return ""
}

func (m tuiModel) renderPlan(item tuiItem) string {
	var lines []string
	if item.update != nil {
		change := styleStrong.Render(item.update.CurrentVersion) + styleMuted.Render(" → ") + styleStrong.Render(item.update.LatestVersion)
		lines = append(lines, indent+styleAccent.Bold(true).Render("update")+"  "+change)
	} else {
		lines = append(lines, indent+styleAccent.Bold(true).Render("replacement"))
	}
	body := lipgloss.NewStyle().
		Foreground(colorText).
		BorderStyle(lipgloss.NormalBorder()).
		BorderLeft(true).
		BorderForeground(colorMuted).
		PaddingLeft(1).
		Width(m.contentWidth() - 2).
		Render(shellCommand(item.command))
	lines = append(lines, prefixLines(body, indent, indent))
	return "\n" + strings.Join(lines, "\n")
}

func (m tuiModel) renderFooter() string {
	if m.finished {
		return m.renderOutcome()
	}
	switch m.phase {
	case phaseSelecting:
		count := m.selectedCount()
		if count == 0 {
			return "\n" + indent + styleMuted.Render("↑↓ move · space select · a all · q quit")
		}
		action := styleAccent.Bold(true).Render(fmt.Sprintf("enter update %d", count))
		return "\n" + indent + action + styleMuted.Render(" · space toggle · a all · q quit")
	case phaseConfirming:
		keep := renderChoice("Keep current", !m.replaceChoice)
		replace := renderChoice("Replace", m.replaceChoice)
		return "\n" + indent + styleText.Render(m.question) + "\n" + indent + keep + "   " + replace +
			"\n" + indent + styleMuted.Render("←→ choose · enter confirm · y replace · n keep")
	}
	if m.interrupted {
		return "\n" + indent + styleYellow.Render("Stopping; a replacement in progress finishes first…")
	}
	return "\n" + indent + styleMuted.Render("ctrl+c stop")
}

func (m tuiModel) renderOutcome() string {
	var summary checkSummaryError
	switch {
	// These outcomes are already explained by a notice or by the rows above.
	case m.err == nil, errors.Is(m.err, errCancelled), errors.Is(m.err, errInterrupted), errors.As(m.err, &summary):
		return ""
	}
	text := lipgloss.NewStyle().Width(m.contentWidth() - 2).Render(m.err.Error())
	return "\n" + prefixLines(styleRed.Render(text), indent+styleRed.Bold(true).Render("×")+" ", indent+"  ")
}

func renderChoice(label string, selected bool) string {
	if selected {
		return styleAccent.Bold(true).Render("● " + label)
	}
	return styleMuted.Render("○ " + label)
}

// visibleRows hides the scan results once updates start, so the table only follows the work in progress.
func (m tuiModel) visibleRows() []containerRow {
	for _, row := range m.rows {
		if row.inUpdatePhase() {
			var rows []containerRow
			for _, candidate := range m.rows {
				if candidate.inUpdatePhase() {
					rows = append(rows, candidate)
				}
			}
			return rows
		}
	}
	return m.rows
}

func (m tuiModel) renderTable(budget int) string {
	rows := m.visibleRows()
	if len(rows) == 0 {
		return ""
	}
	nameWidth, imageWidth := 0, 0
	for _, row := range rows {
		nameWidth = max(nameWidth, ansi.StringWidth(row.name))
		imageWidth = max(imageWidth, ansi.StringWidth(row.image))
	}
	available := m.contentWidth() - 4
	nameWidth = min(nameWidth, 24)
	imageWidth = min(imageWidth, 36)
	if detailWidth := available - nameWidth - imageWidth - 4; detailWidth < 18 {
		imageWidth = max(0, available-nameWidth-4-18)
		if imageWidth < 10 {
			imageWidth = 0
		}
	}
	detailWidth := available - nameWidth - 2
	if imageWidth > 0 {
		detailWidth -= imageWidth + 2
	}

	// Full error lines are a luxury; drop them when the table must scroll.
	wrapErrors := true
	if budget > 0 {
		lines := 0
		for index, row := range rows {
			lines += len(m.renderRow(row, index, nameWidth, imageWidth, detailWidth, true))
		}
		wrapErrors = lines <= budget
	}
	start, end := 0, len(rows)
	if budget > 0 && len(rows) > budget {
		focus := 0
		if m.phase == phaseSelecting {
			focus = m.cursor
		} else {
			for index, row := range rows {
				if row.state == rowUpdating {
					focus = index
				}
			}
		}
		visible := max(1, budget-2)
		start = min(max(0, focus-visible/2), len(rows)-visible)
		end = start + visible
	}

	var lines []string
	if start > 0 {
		lines = append(lines, indent+styleMuted.Render(fmt.Sprintf("  ↑ %d more", start)))
	}
	for index := start; index < end; index++ {
		lines = append(lines, m.renderRow(rows[index], index, nameWidth, imageWidth, detailWidth, wrapErrors)...)
	}
	if end < len(rows) {
		lines = append(lines, indent+styleMuted.Render(fmt.Sprintf("  ↓ %d more", len(rows)-end)))
	}
	return "\n" + strings.Join(lines, "\n")
}

func (m tuiModel) renderRow(row containerRow, index, nameWidth, imageWidth, detailWidth int, wrapErrors bool) []string {
	cursor := " "
	if m.phase == phaseSelecting && index == m.cursor && row.state == rowUpdatable {
		cursor = styleAccent.Bold(true).Render("›")
	}
	mark, nameStyle, detailStyle := m.rowStyle(row)

	badge := ""
	if row.update != nil && row.update.Compose && (row.state == rowUpdatable || row.state == rowQueued || row.state == rowUpdating) {
		badge = "compose" + replicaSuffix(row.update)
	}
	detailRoom := detailWidth
	if badge != "" {
		detailRoom -= ansi.StringWidth(badge) + 2
	}
	detail := ansi.Truncate(row.detail, max(1, detailRoom), "…")
	failed := row.state == rowFailed || row.state == rowUpdateFailed
	wrapError := wrapErrors && failed && detail != row.detail
	if wrapError {
		detail = "failed"
	}
	cells := indent + cursor + " " + mark + " " + nameStyle.Render(pad(ansi.Truncate(row.name, nameWidth, "…"), nameWidth))
	if imageWidth > 0 {
		cells += "  " + styleMuted.Render(pad(ansi.Truncate(row.image, imageWidth, "…"), imageWidth))
	}
	cells += "  " + detailStyle.Render(detail)
	if badge != "" {
		cells += "  " + styleAccent.Render(badge)
	}
	lines := []string{cells}

	if wrapError {
		text := lipgloss.NewStyle().Width(m.contentWidth() - 6).Render(row.detail)
		lines = append(lines, prefixLines(styleRed.Render(text), indent+"      ", indent+"      "))
	}
	return lines
}

func (m tuiModel) rowStyle(row containerRow) (string, lipgloss.Style, lipgloss.Style) {
	switch row.state {
	case rowChecking, rowUpdating:
		return m.spinner.View(), styleText, styleAccent
	case rowUpdatable:
		mark := styleAccent.Render("↑")
		if m.phase == phaseSelecting {
			mark = styleMuted.Render("○")
			if m.selected[row.key] {
				mark = styleAccent.Bold(true).Render("●")
			}
		}
		return mark, styleStrong, styleText
	case rowCurrent:
		return styleGreen.Render("✓"), styleMuted, styleMuted
	case rowFailed:
		return styleRed.Render("×"), styleText, styleRed
	case rowQueued:
		return styleMuted.Render("·"), styleText, styleMuted
	case rowUpdated:
		return styleGreen.Render("✓"), styleStrong, styleGreen
	case rowUnchanged:
		return styleMuted.Render("✓"), styleText, styleMuted
	case rowUpdateFailed:
		return styleRed.Render("×"), styleStrong, styleRed
	default: // skipped, not started
		return styleMuted.Render("–"), styleMuted, styleMuted
	}
}

// rowRank orders the selection list: actionable rows first, then problems, then the rest.
func rowRank(state rowState) int {
	switch state {
	case rowUpdatable:
		return 0
	case rowFailed:
		return 1
	case rowCurrent:
		return 2
	default:
		return 3
	}
}

func scanStats(rows []containerRow) string {
	counts := make(map[rowState]int)
	for _, row := range rows {
		counts[row.state]++
	}
	var parts []string
	add := func(state rowState, singular, pluralForm string) {
		switch count := counts[state]; {
		case count == 1:
			parts = append(parts, "1 "+singular)
		case count > 1:
			parts = append(parts, fmt.Sprintf("%d %s", count, pluralForm))
		}
	}
	if counts[rowChecking] > 0 {
		parts = append(parts, fmt.Sprintf("checking %d", counts[rowChecking]))
	}
	add(rowUpdatable, "update", "updates")
	add(rowCurrent, "up to date", "up to date")
	add(rowSkipped, "skipped", "skipped")
	add(rowFailed, "failed", "failed")
	return strings.Join(parts, " · ")
}

func pad(text string, width int) string {
	return text + strings.Repeat(" ", max(0, width-ansi.StringWidth(text)))
}

func prefixLines(text, first, rest string) string {
	lines := strings.Split(text, "\n")
	for index := range lines {
		if index == 0 {
			lines[index] = first + lines[index]
		} else {
			lines[index] = rest + lines[index]
		}
	}
	return strings.Join(lines, "\n")
}
