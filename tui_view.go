package main

import (
	"errors"
	"fmt"
	"strings"

	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
)

var (
	colorAccent   = lipgloss.AdaptiveColor{Light: "#2E8B94", Dark: "#8AD7DD"}
	colorStrong   = lipgloss.AdaptiveColor{Light: "#11151C", Dark: "#F1F4F7"}
	colorText     = lipgloss.AdaptiveColor{Light: "#2B3038", Dark: "#CBD3DC"}
	colorDim      = lipgloss.AdaptiveColor{Light: "#6B7380", Dark: "#7B8695"}
	colorFaint    = lipgloss.AdaptiveColor{Light: "#B8BEC6", Dark: "#3A434F"}
	colorGreen    = lipgloss.AdaptiveColor{Light: "#3D8B5A", Dark: "#9BD6AF"}
	colorAmber    = lipgloss.AdaptiveColor{Light: "#A16B16", Dark: "#E6C68A"}
	colorRed      = lipgloss.AdaptiveColor{Light: "#B5474B", Dark: "#EE9A9D"}
	colorCursorBg = lipgloss.AdaptiveColor{Light: "#E3F2F3", Dark: "#1A2A2F"}
	colorPanelBg  = lipgloss.AdaptiveColor{Light: "#F1F3F5", Dark: "#19202A"}
	colorOnAccent = lipgloss.AdaptiveColor{Light: "#FFFFFF", Dark: "#0E1A1C"}
)

func fg(color lipgloss.TerminalColor) lipgloss.Style {
	return lipgloss.NewStyle().Foreground(color)
}

// Every line starts with a two-column gutter; the selection bar sits in its first column.
const gutter = "  "

// View draws the live frame. The finished frame is printed by tui.close instead,
// because the inline renderer drops lines that do not fit on screen.
func (m tuiModel) View() string {
	if m.finished {
		return ""
	}
	return m.render()
}

func (m tuiModel) render() string {
	header := m.viewHeader()
	var tail []string
	if notices := m.viewNotices(); notices != "" {
		tail = append(tail, notices)
	}
	if footer := m.viewFooter(); footer != "" {
		tail = append(tail, footer)
	}

	var body string
	if m.scanned {
		budget := 0
		if m.height > 0 && !m.finished {
			used := lipgloss.Height(header) + 3
			for _, block := range tail {
				used += lipgloss.Height(block) + 1
			}
			budget = max(4, m.height-used)
		}
		body = m.viewScan(budget)
	} else {
		body = m.viewRefresh()
	}

	blocks := []string{header}
	if body != "" {
		blocks = append(blocks, body)
	}
	blocks = append(blocks, tail...)
	return "\n" + strings.Join(blocks, "\n\n") + "\n"
}

func (m tuiModel) lineWidth() int {
	if m.width <= 0 {
		return 100
	}
	return max(48, m.width-1)
}

// innerWidth is the space to the right of the gutter.
func (m tuiModel) innerWidth() int {
	return m.lineWidth() - len(gutter)
}

func (m tuiModel) viewHeader() string {
	width := m.innerWidth()
	brand := fg(colorAccent).Bold(true).Render("drun")
	lines := make([]string, 0, 3)
	if m.title != "" {
		left := brand + "  " + fg(colorStrong).Bold(true).Render(m.title)
		right := ""
		if m.info != nil {
			right = m.viewContainerState()
		}
		lines = append(lines, gutter+spread(left, right, width))
		if m.info != nil {
			kind := "standalone"
			if isComposeContainer(m.info) {
				kind = "compose"
			}
			detail := fg(colorDim).Render(m.info.Config.Image) + fg(colorFaint).Render("  ·  ") + fg(colorDim).Render(kind)
			lines = append(lines, gutter+"      "+truncate(detail, width-6))
		}
	} else {
		left := brand + "  " + fg(colorDim).Render(m.scanSubtitle())
		lines = append(lines, gutter+spread(left, m.scanProgress(), width))
	}
	lines = append(lines, gutter+fg(colorFaint).Render(strings.Repeat("─", width)))
	return strings.Join(lines, "\n")
}

func (m tuiModel) viewContainerState() string {
	if m.info.State.Running {
		return fg(colorGreen).Render("● " + m.info.State.Status)
	}
	return fg(colorAmber).Render("● " + m.info.State.Status)
}

func (m tuiModel) scanSubtitle() string {
	if !m.scanned {
		return "checking for updates"
	}
	total := 0
	for _, row := range m.rows {
		total += row.containerCount()
	}
	return plural(total, "container")
}

// scanProgress is the right side of the scan header: progress while work runs, totals otherwise.
func (m tuiModel) scanProgress() string {
	if !m.scanned {
		return m.spinner.View()
	}
	if m.updating() {
		active := m.rowsIn(rowQueued, rowUpdating, rowUpdated, rowUnchanged, rowUpdateFailed, rowNotStarted)
		done := len(active) - len(m.rowsIn(rowQueued, rowUpdating))
		return progressBar(done, len(active)) + fg(colorDim).Render(fmt.Sprintf("  %d/%d", done, len(active)))
	}
	if checking := len(m.rowsIn(rowChecking)); checking > 0 {
		done := len(m.rows) - checking
		return progressBar(done, len(m.rows)) + fg(colorDim).Render(fmt.Sprintf("  %d/%d", done, len(m.rows)))
	}
	var parts []string
	for _, total := range []struct {
		state rowState
		mark  string
		color lipgloss.TerminalColor
	}{
		{rowUpdatable, "↑", colorAccent},
		{rowCurrent, "✓", colorGreen},
		{rowSkipped, "–", colorDim},
		{rowFailed, "×", colorRed},
	} {
		if count := len(m.rowsIn(total.state)); count > 0 {
			parts = append(parts, fg(total.color).Render(total.mark)+" "+fg(colorText).Render(fmt.Sprint(count)))
		}
	}
	return strings.Join(parts, "   ")
}

func progressBar(done, total int) string {
	const width = 14
	filled := 0
	if total > 0 {
		filled = done * width / total
	}
	return fg(colorAccent).Render(strings.Repeat("━", filled)) + fg(colorFaint).Render(strings.Repeat("━", width-filled))
}

// scanSection is one titled group of rows. Sections with a higher fold
// value fold into one summary line first when the terminal is short.
type scanSection struct {
	label string
	style lipgloss.Style
	rows  []containerRow
	fold  int
}

func (m tuiModel) scanSections() []scanSection {
	var sections []scanSection
	if m.updating() {
		label := "RESULTS"
		if len(m.rowsIn(rowQueued, rowUpdating)) > 0 {
			label = "UPDATING"
		}
		sections = []scanSection{{label: label, style: fg(colorAccent).Bold(true),
			rows: m.rowsIn(rowQueued, rowUpdating, rowUpdated, rowUnchanged, rowUpdateFailed, rowNotStarted)}}
	} else {
		sections = []scanSection{
			{label: "UPDATES", style: fg(colorAccent).Bold(true), rows: m.updates()},
			{label: "CHECKING", style: fg(colorDim).Bold(true), rows: m.rowsIn(rowChecking), fold: 1},
			{label: "FAILED", style: fg(colorRed).Bold(true), rows: m.rowsIn(rowFailed), fold: 2},
			{label: "UP TO DATE", style: fg(colorDim).Bold(true), rows: m.rowsIn(rowCurrent), fold: 3},
			{label: "SKIPPED", style: fg(colorDim).Bold(true), rows: m.rowsIn(rowSkipped), fold: 4},
		}
	}
	nonEmpty := sections[:0]
	for _, section := range sections {
		if len(section.rows) > 0 {
			nonEmpty = append(nonEmpty, section)
		}
	}
	return nonEmpty
}

type scanLayout struct {
	columns   columns
	folded    map[int]bool
	window    int // rows shown from the first section; 0 shows all
	fullError bool
}

// viewScan draws the grouped rows, giving up detail until they fit in budget lines.
func (m tuiModel) viewScan(budget int) string {
	sections := m.scanSections()
	if len(sections) == 0 {
		return ""
	}
	layout := scanLayout{columns: m.columns(sections), folded: map[int]bool{}, fullError: true}
	view := m.drawSections(sections, layout)
	if budget <= 0 || lipgloss.Height(view) <= budget {
		return view
	}
	layout.fullError = false
	view = m.drawSections(sections, layout)
	for fold := 4; fold >= 1 && lipgloss.Height(view) > budget; fold-- {
		for index, section := range sections {
			if section.fold == fold {
				layout.folded[index] = true
			}
		}
		view = m.drawSections(sections, layout)
	}
	if overflow := lipgloss.Height(view) - budget; overflow > 0 {
		layout.window = max(1, len(sections[0].rows)-overflow-2)
		view = m.drawSections(sections, layout)
	}
	return view
}

func (m tuiModel) drawSections(sections []scanSection, layout scanLayout) string {
	var lines []string
	for index, section := range sections {
		if index > 0 && !(layout.folded[index] && layout.folded[index-1]) {
			lines = append(lines, "")
		}
		title := gutter + section.style.Render(section.label) + "  " + fg(colorDim).Render(fmt.Sprint(len(section.rows)))
		if layout.folded[index] {
			names := make([]string, len(section.rows))
			for rowIndex, row := range section.rows {
				names[rowIndex] = row.name
			}
			summary := fg(colorDim).Render(strings.Join(names, " · "))
			lines = append(lines, truncate(title+"   "+summary, m.lineWidth()))
			continue
		}
		lines = append(lines, title)

		start, end := 0, len(section.rows)
		if index == 0 && layout.window > 0 && layout.window < len(section.rows) {
			start = min(max(0, m.focus(section.rows)-layout.window/2), len(section.rows)-layout.window)
			end = start + layout.window
		}
		if start > 0 {
			lines = append(lines, gutter+"   "+fg(colorDim).Render(fmt.Sprintf("↑ %d more", start)))
		}
		for rowIndex := start; rowIndex < end; rowIndex++ {
			cursor := m.phase == phaseSelecting && section.label == "UPDATES" && rowIndex == m.cursor
			lines = append(lines, m.viewRow(section.rows[rowIndex], cursor, layout)...)
		}
		if end < len(section.rows) {
			lines = append(lines, gutter+"   "+fg(colorDim).Render(fmt.Sprintf("↓ %d more", len(section.rows)-end)))
		}
	}
	return strings.Join(lines, "\n")
}

// focus is the row a scrolled section keeps in view.
func (m tuiModel) focus(rows []containerRow) int {
	if m.phase == phaseSelecting {
		return m.cursor
	}
	for index, row := range rows {
		if row.state == rowUpdating {
			return index
		}
	}
	return 0
}

// columns holds shared column widths so every section lines up.
type columns struct {
	name, image int
}

const markWidth = 3 // mark plus two spaces

func (m tuiModel) columns(sections []scanSection) columns {
	var cols columns
	for _, section := range sections {
		for _, row := range section.rows {
			cols.name = max(cols.name, ansi.StringWidth(row.name))
			cols.image = max(cols.image, ansi.StringWidth(row.image))
		}
	}
	cols.name = min(cols.name, 24)
	cols.image = min(cols.image, 34)
	if room := m.innerWidth() - markWidth - cols.name - 2 - 26; cols.image > room {
		cols.image = room
		if cols.image < 12 {
			cols.image = 0
		}
	}
	return cols
}

func (m tuiModel) viewRow(row containerRow, cursor bool, layout scanLayout) []string {
	cols := layout.columns
	paint := func(style lipgloss.Style, text string) string {
		if cursor {
			style = style.Background(colorCursorBg)
		}
		return style.Render(text)
	}
	plain := lipgloss.NewStyle()

	line := gutter
	if cursor {
		line = fg(colorAccent).Render("▌") + paint(plain, " ")
	}
	mark, nameStyle := m.rowMark(row)
	if row.state == rowChecking || row.state == rowUpdating {
		line += m.spinner.View()
	} else {
		line += paint(mark.style, mark.text)
	}
	line += paint(plain, "  ") + paint(nameStyle, padRight(truncate(row.name, cols.name), cols.name))
	if cols.image > 0 {
		line += paint(plain, "  ") + paint(fg(colorDim), padRight(truncate(row.image, cols.image), cols.image))
	}
	line += paint(plain, "  ")

	detailWidth := m.innerWidth() - markWidth - cols.name - 2
	if cols.image > 0 {
		detailWidth -= cols.image + 2
	}
	detail, truncated := m.rowDetail(row, cursor, detailWidth, paint)
	line += detail
	if cursor {
		line += paint(plain, strings.Repeat(" ", max(0, m.lineWidth()-ansi.StringWidth(line))))
	}
	lines := []string{line}

	if truncated && layout.fullError {
		text := lipgloss.NewStyle().Width(m.innerWidth() - markWidth).Render(row.detail)
		for _, wrapped := range strings.Split(text, "\n") {
			lines = append(lines, gutter+"   "+fg(colorRed).Faint(true).Render(wrapped))
		}
	}
	return lines
}

type mark struct {
	text  string
	style lipgloss.Style
}

func (m tuiModel) rowMark(row containerRow) (mark, lipgloss.Style) {
	strong := fg(colorStrong).Bold(true)
	switch row.state {
	case rowUpdatable:
		if m.phase != phaseSelecting {
			return mark{"↑", fg(colorAccent)}, strong
		}
		if m.selected[row.key] {
			return mark{"●", fg(colorAccent)}, strong
		}
		return mark{"○", fg(colorDim)}, fg(colorText)
	case rowCurrent:
		return mark{"✓", fg(colorGreen)}, fg(colorText)
	case rowFailed:
		return mark{"×", fg(colorRed)}, fg(colorText)
	case rowQueued:
		return mark{"·", fg(colorDim)}, fg(colorText)
	case rowUpdated:
		return mark{"✓", fg(colorGreen)}, strong
	case rowUnchanged:
		return mark{"✓", fg(colorDim)}, fg(colorText)
	case rowUpdateFailed:
		return mark{"×", fg(colorRed)}, strong
	case rowChecking, rowUpdating:
		return mark{}, strong
	default: // skipped, not started
		return mark{"–", fg(colorFaint)}, fg(colorDim)
	}
}

// rowDetail renders the last column and reports whether an error message was cut short.
func (m tuiModel) rowDetail(row containerRow, cursor bool, width int, paint func(lipgloss.Style, string) string) (string, bool) {
	switch row.state {
	case rowUpdatable, rowQueued, rowUpdated:
		if row.update == nil {
			break
		}
		from := padRight(row.update.CurrentVersion, m.versionWidth())
		to := row.update.LatestVersion
		toStyle := fg(colorText)
		switch {
		case row.state == rowUpdated:
			toStyle = fg(colorGreen).Bold(true)
		case row.state == rowQueued:
			toStyle = fg(colorDim)
		case cursor || m.selected[row.key] || m.phase != phaseSelecting:
			toStyle = fg(colorAccent).Bold(true)
		}
		versions := paint(fg(colorDim), from) + paint(fg(colorFaint), " → ") + paint(toStyle, to)
		used := ansi.StringWidth(from) + 3 + ansi.StringWidth(to)
		if tag := composeTag(row); tag != "" && used+2+ansi.StringWidth(tag) <= width {
			versions += paint(lipgloss.NewStyle(), "  ") + paint(fg(colorDim).Italic(true), tag)
		}
		if row.state == rowQueued {
			versions += paint(fg(colorDim), "  waiting")
		}
		return truncate(versions, width), false
	case rowFailed, rowUpdateFailed:
		text := truncate(row.detail, width)
		return paint(fg(colorRed), text), text != row.detail
	case rowUpdating:
		return fg(colorAccent).Render(truncate(row.detail, width)), false
	case rowChecking:
		return fg(colorDim).Render("checking…"), false
	}
	return paint(fg(colorDim), truncate(row.detail, width)), false
}

func (m tuiModel) versionWidth() int {
	width := 0
	for _, row := range m.rows {
		if row.update != nil {
			width = max(width, ansi.StringWidth(row.update.CurrentVersion))
		}
	}
	return min(width, 16)
}

func composeTag(row containerRow) string {
	if !row.compose {
		return ""
	}
	if row.replicas > 1 {
		return fmt.Sprintf("compose ×%d", row.replicas)
	}
	return "compose"
}

// viewRefresh draws the single-container flow: steps, then the plan, then the steps that follow it.
func (m tuiModel) viewRefresh() string {
	var blocks []string
	before, after := m.steps, []step(nil)
	if m.plan != nil {
		before, after = m.steps[:m.planAt], m.steps[m.planAt:]
	}
	if len(before) > 0 {
		blocks = append(blocks, m.viewSteps(before))
	}
	if m.plan != nil {
		blocks = append(blocks, m.viewPlan())
	}
	if len(after) > 0 {
		blocks = append(blocks, m.viewSteps(after))
	}
	return strings.Join(blocks, "\n\n")
}

func (m tuiModel) viewSteps(steps []step) string {
	lines := make([]string, len(steps))
	for index, step := range steps {
		switch step.state {
		case stepRunning:
			lines[index] = gutter + m.spinner.View() + "  " + fg(colorStrong).Render(step.title)
		case stepFailed:
			lines[index] = gutter + fg(colorRed).Render("×") + "  " + fg(colorText).Render(step.title)
		default:
			lines[index] = gutter + fg(colorGreen).Render("✓") + "  " + fg(colorDim).Render(step.title)
		}
	}
	return strings.Join(lines, "\n")
}

func (m tuiModel) viewPlan() string {
	label := fg(colorDim).Bold(true).Render("COMMAND")
	if update := m.plan.update; update != nil {
		label = fg(colorAccent).Bold(true).Render("UPDATE") + "   " +
			fg(colorDim).Render(update.CurrentVersion) + fg(colorFaint).Render(" → ") + fg(colorAccent).Bold(true).Render(update.LatestVersion)
	}
	panel := lipgloss.NewStyle().
		Foreground(colorText).
		Background(colorPanelBg).
		Padding(1, 2).
		Width(m.innerWidth()).
		Render(wrapCommand(m.plan.command, m.innerWidth()-4))
	return gutter + label + "\n\n" + prefixLines(panel, gutter)
}

func (m tuiModel) viewNotices() string {
	var lines []string
	for _, notice := range m.notices {
		color, symbol := colorAccent, "·"
		switch notice.kind {
		case noticeWarning:
			color, symbol = colorAmber, "!"
		case noticeSuccess:
			color, symbol = colorGreen, "✓"
		}
		lines = append(lines, m.viewMessage(fg(color).Bold(true).Render(symbol), fg(colorStrong).Render, notice.text))
	}
	if m.finished && showsError(m.err) {
		lines = append(lines, m.viewMessage(fg(colorRed).Bold(true).Render("×"), fg(colorRed).Render, m.err.Error()))
	}
	return strings.Join(lines, "\n")
}

func (m tuiModel) viewMessage(symbol string, render func(...string) string, text string) string {
	wrapped := lipgloss.NewStyle().Width(m.innerWidth() - markWidth).Render(text)
	lines := strings.Split(wrapped, "\n")
	for index, line := range lines {
		lead := "   "
		if index == 0 {
			lead = symbol + "  "
		}
		lines[index] = gutter + lead + render(line)
	}
	return strings.Join(lines, "\n")
}

// showsError reports whether the final frame must print err; the others are already explained on screen.
func showsError(err error) bool {
	var summary checkSummaryError
	return err != nil && !errors.Is(err, errCancelled) && !errors.Is(err, errInterrupted) && !errors.As(err, &summary)
}

func (m tuiModel) viewFooter() string {
	if m.finished {
		return ""
	}
	switch m.phase {
	case phaseSelecting:
		update := fg(colorFaint).Render("⏎ update")
		if count := m.selectedCount(); count > 0 {
			update = fg(colorAccent).Bold(true).Render(fmt.Sprintf("⏎ update %d", count))
		}
		return gutter + strings.Join([]string{key("↑↓", "move"), key("␣", "select"), key("a", "all"), update, key("q", "quit")}, "    ")
	case phaseConfirming:
		buttons := button("Keep current", !m.replace) + "  " + button("Replace", m.replace)
		hints := strings.Join([]string{key("←→", "choose"), key("⏎", "confirm"), key("y/n", "answer")}, "    ")
		return gutter + fg(colorStrong).Bold(true).Render(m.question) + "\n\n" + gutter + buttons + "\n\n" + gutter + hints
	}
	if m.interrupted {
		return gutter + fg(colorAmber).Render("Stopping after the current step…")
	}
	return gutter + key("ctrl+c", "stop")
}

func key(keys, label string) string {
	return fg(colorText).Bold(true).Render(keys) + " " + fg(colorDim).Render(label)
}

func button(label string, active bool) string {
	style := lipgloss.NewStyle().Padding(0, 2)
	if active {
		return style.Background(colorAccent).Foreground(colorOnAccent).Bold(true).Render(label)
	}
	return style.Background(colorPanelBg).Foreground(colorDim).Render(label)
}

// wrapCommand breaks a shell command between arguments, keeping each flag
// next to its value and indenting continuation lines.
func wrapCommand(args []string, width int) string {
	var words []string
	for index := 0; index < len(args); index++ {
		word := shellQuote(args[index])
		if strings.HasPrefix(args[index], "--") && !strings.Contains(args[index], "=") &&
			index+1 < len(args) && !strings.HasPrefix(args[index+1], "-") && index+2 < len(args) {
			index++
			word += " " + shellQuote(args[index])
		}
		words = append(words, word)
	}
	var lines []string
	line := ""
	for _, word := range words {
		switch {
		case line == "":
			line = word
		case ansi.StringWidth(line)+1+ansi.StringWidth(word) <= width:
			line += " " + word
		default:
			lines = append(lines, line)
			line = "  " + word
		}
	}
	lines = append(lines, line)
	return ansi.Hardwrap(strings.Join(lines, "\n"), width, true)
}

// spread places right at the far edge of width, or drops it when the line is too narrow.
func spread(left, right string, width int) string {
	gap := width - ansi.StringWidth(left) - ansi.StringWidth(right)
	if right == "" || gap < 3 {
		return left
	}
	return left + strings.Repeat(" ", gap) + right
}

func truncate(text string, width int) string {
	return ansi.Truncate(text, max(1, width), "…")
}

func padRight(text string, width int) string {
	return text + strings.Repeat(" ", max(0, width-ansi.StringWidth(text)))
}

func prefixLines(text, prefix string) string {
	lines := strings.Split(text, "\n")
	for index := range lines {
		lines[index] = prefix + lines[index]
	}
	return strings.Join(lines, "\n")
}
