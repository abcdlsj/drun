package main

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"strings"
	"unicode"

	"github.com/charmbracelet/bubbles/spinner"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"golang.org/x/term"
)

const (
	colorCyan   = lipgloss.Color("#39D0D8")
	colorGreen  = lipgloss.Color("#5EE38E")
	colorYellow = lipgloss.Color("#FFD866")
	colorRed    = lipgloss.Color("#FF6B6B")
	colorMuted  = lipgloss.Color("#7F8A9A")
	colorText   = lipgloss.Color("#E7EDF5")
)

type terminalUI struct {
	in          io.Reader
	out         io.Writer
	err         io.Writer
	interactive bool
	width       int
}

func newTerminalUI(in io.Reader, out, errOut io.Writer, noColor bool) *terminalUI {
	inputTTY := false
	if file, ok := in.(*os.File); ok {
		inputTTY = term.IsTerminal(int(file.Fd()))
	}
	outputTTY := false
	width := 96
	if file, ok := out.(*os.File); ok {
		outputTTY = term.IsTerminal(int(file.Fd()))
		if terminalWidth, _, err := term.GetSize(int(file.Fd())); err == nil {
			width = terminalWidth
		}
	}
	interactive := inputTTY && outputTTY && !noColor && os.Getenv("NO_COLOR") == ""
	return &terminalUI{in: in, out: out, err: errOut, interactive: interactive, width: width}
}

func (ui *terminalUI) header(subtitleText string) {
	if !ui.interactive {
		fmt.Fprintf(ui.out, "drun - %s\n", subtitleText)
		return
	}
	brand := lipgloss.NewStyle().Bold(true).Foreground(colorCyan).Render("drun")
	subtitle := lipgloss.NewStyle().Foreground(colorMuted).Render(subtitleText)
	fmt.Fprintf(ui.out, "\n  %s  %s\n\n", brand, subtitle)
}

func (ui *terminalUI) isInteractive() bool {
	return ui.interactive
}

func (ui *terminalUI) info(message string) {
	ui.printStatus("·", "i", colorCyan, message, ui.out)
}

func (ui *terminalUI) warning(message string) {
	ui.printStatus("!", "!", colorYellow, message, ui.out)
}

func (ui *terminalUI) success(message string) {
	ui.printStatus("✓", "+", colorGreen, message, ui.out)
}

func (ui *terminalUI) error(message string) {
	ui.printStatus("×", "x", colorRed, message, ui.err)
}

func (ui *terminalUI) printStatus(mark, plainMark string, color lipgloss.Color, message string, writer io.Writer) {
	if !ui.interactive {
		fmt.Fprintf(writer, "[%s] %s\n", plainMark, message)
		return
	}
	marker := lipgloss.NewStyle().Bold(true).Foreground(color).Render(mark)
	fmt.Fprintf(writer, "  %s  %s\n", marker, message)
}

func (ui *terminalUI) summary(info *containerInfo) {
	name := strings.TrimPrefix(info.Name, "/")
	if !ui.interactive {
		fmt.Fprintf(ui.out, "container: %s\nimage: %s\nstate: %s\n", name, info.Config.Image, info.State.Status)
		return
	}

	title := lipgloss.NewStyle().Bold(true).Foreground(colorText).Render(name)
	statusColor := colorGreen
	if !info.State.Running {
		statusColor = colorYellow
	}
	status := lipgloss.NewStyle().Bold(true).Foreground(statusColor).Render("● " + info.State.Status)
	image := lipgloss.NewStyle().Foreground(colorMuted).Render(info.Config.Image)
	fmt.Fprintf(ui.out, "  %s  %s\n  %s\n\n", title, status, image)
}

func (ui *terminalUI) updateAvailable(update updateCandidate) {
	if !ui.interactive {
		fmt.Fprintf(ui.out, "update available: %s %s -> %s\n", update.Name, update.CurrentVersion, update.LatestVersion)
		return
	}
	label := lipgloss.NewStyle().Bold(true).Foreground(colorCyan).Render("update")
	name := lipgloss.NewStyle().Bold(true).Foreground(colorText).Render(update.Name)
	change := lipgloss.NewStyle().Foreground(colorText).Render(update.CurrentVersion + " → " + update.LatestVersion)
	image := lipgloss.NewStyle().Foreground(colorMuted).Render(update.Image)
	fmt.Fprintf(ui.out, "  %s  %s  %s\n          %s\n\n", label, name, change, image)
}

func (ui *terminalUI) listUpdates(updates []updateCandidate) {
	if ui.interactive {
		title := lipgloss.NewStyle().Bold(true).Foreground(colorCyan).Render(fmt.Sprintf("%d updates", len(updates)))
		fmt.Fprintf(ui.out, "  %s\n", title)
		for _, update := range updates {
			name := lipgloss.NewStyle().Bold(true).Foreground(colorText).Render(update.Name)
			change := lipgloss.NewStyle().Foreground(colorText).Render(update.CurrentVersion + " → " + update.LatestVersion)
			image := lipgloss.NewStyle().Foreground(colorMuted).Render(update.Image)
			marker := lipgloss.NewStyle().Foreground(colorCyan).Render("•")
			fmt.Fprintf(ui.out, "  %s %s  %s\n    %s\n", marker, name, change, image)
		}
		fmt.Fprintln(ui.out)
		return
	}
	fmt.Fprintf(ui.out, "%d updates available\n", len(updates))
	for _, update := range updates {
		fmt.Fprintf(ui.out, "- %s: %s -> %s (%s)\n", update.Name, update.CurrentVersion, update.LatestVersion, update.Image)
	}
}

func (ui *terminalUI) selectUpdates(updates []updateCandidate) ([]updateCandidate, error) {
	if !ui.interactive {
		return nil, errorsUnexpectedModel("update selector requires an interactive terminal")
	}
	program := tea.NewProgram(
		newUpdateSelectorModel(updates),
		tea.WithInput(ui.in),
		tea.WithOutput(ui.out),
	)
	finalModel, err := program.Run()
	if err != nil {
		return nil, err
	}
	model, ok := finalModel.(updateSelectorModel)
	if !ok {
		return nil, errorsUnexpectedModel("update selector")
	}
	if model.cancelled {
		return nil, nil
	}
	selected := make([]updateCandidate, 0, len(updates))
	for index, update := range model.updates {
		if model.selected[index] {
			selected = append(selected, update)
		}
	}
	return selected, nil
}

func (ui *terminalUI) command(args []string) {
	command := shellCommand(args)
	if !ui.interactive {
		fmt.Fprintf(ui.out, "$ %s\n", command)
		return
	}

	label := lipgloss.NewStyle().Bold(true).Foreground(colorCyan).Render("command")
	commandWidth := min(88, max(32, ui.width-8))
	body := lipgloss.NewStyle().
		Foreground(colorText).
		BorderStyle(lipgloss.NormalBorder()).
		BorderLeft(true).
		BorderForeground(colorCyan).
		PaddingLeft(2).
		Width(commandWidth).
		Render(command)
	fmt.Fprintf(ui.out, "  %s\n%s\n\n", label, body)
}

func (ui *terminalUI) confirm(message string) (bool, error) {
	if !ui.interactive {
		fmt.Fprintf(ui.out, "%s [y/N]: ", message)
		response, err := bufio.NewReader(ui.in).ReadString('\n')
		if err != nil && len(response) == 0 {
			return false, err
		}
		response = strings.ToLower(strings.TrimSpace(response))
		return response == "y" || response == "yes", nil
	}

	program := tea.NewProgram(
		newConfirmModel(message),
		tea.WithInput(ui.in),
		tea.WithOutput(ui.out),
	)
	finalModel, err := program.Run()
	if err != nil {
		return false, err
	}
	model, ok := finalModel.(confirmModel)
	if !ok {
		return false, errorsUnexpectedModel("confirmation")
	}
	return model.confirmed, nil
}

func (ui *terminalUI) spin(message string, action func() error) error {
	if !ui.interactive {
		fmt.Fprintf(ui.out, "[..] %s\n", message)
		err := action()
		if err != nil {
			fmt.Fprintf(ui.out, "[x]  %s\n", message)
			return err
		}
		fmt.Fprintf(ui.out, "[ok] %s\n", message)
		return nil
	}

	program := tea.NewProgram(
		newTaskModel(message, action),
		tea.WithInput(nil),
		tea.WithOutput(ui.out),
	)
	finalModel, err := program.Run()
	if err != nil {
		return err
	}
	model, ok := finalModel.(taskModel)
	if !ok {
		return errorsUnexpectedModel("spinner")
	}
	return model.err
}

type taskFinishedMsg struct {
	err error
}

type taskModel struct {
	spinner spinner.Model
	message string
	action  func() error
	done    bool
	err     error
}

func newTaskModel(message string, action func() error) taskModel {
	component := spinner.New()
	component.Spinner = spinner.Dot
	component.Style = lipgloss.NewStyle().Foreground(colorCyan)
	return taskModel{spinner: component, message: message, action: action}
}

func (m taskModel) Init() tea.Cmd {
	return tea.Batch(m.spinner.Tick, func() tea.Msg {
		return taskFinishedMsg{err: m.action()}
	})
}

func (m taskModel) Update(message tea.Msg) (tea.Model, tea.Cmd) {
	switch message := message.(type) {
	case taskFinishedMsg:
		m.done = true
		m.err = message.err
		return m, tea.Quit
	default:
		var command tea.Cmd
		m.spinner, command = m.spinner.Update(message)
		return m, command
	}
}

func (m taskModel) View() string {
	text := lipgloss.NewStyle().Foreground(colorText).Render(m.message)
	if !m.done {
		return fmt.Sprintf("  %s %s", m.spinner.View(), text)
	}
	if m.err != nil {
		mark := lipgloss.NewStyle().Bold(true).Foreground(colorRed).Render("×")
		return fmt.Sprintf("  %s  %s\n", mark, text)
	}
	mark := lipgloss.NewStyle().Bold(true).Foreground(colorGreen).Render("✓")
	return fmt.Sprintf("  %s  %s\n", mark, text)
}

type confirmModel struct {
	message   string
	selection int
	confirmed bool
}

type updateSelectorModel struct {
	updates   []updateCandidate
	selected  []bool
	cursor    int
	maxRows   int
	cancelled bool
}

func newUpdateSelectorModel(updates []updateCandidate) updateSelectorModel {
	selected := make([]bool, len(updates))
	for index := range updates {
		selected[index] = true
	}
	return updateSelectorModel{updates: updates, selected: selected, maxRows: 7}
}

func (m updateSelectorModel) Init() tea.Cmd {
	return nil
}

func (m updateSelectorModel) Update(message tea.Msg) (tea.Model, tea.Cmd) {
	switch message := message.(type) {
	case tea.WindowSizeMsg:
		rows := (message.Height - 6) / 2
		if rows < 1 {
			rows = 1
		}
		m.maxRows = rows
	case tea.KeyMsg:
		switch message.String() {
		case "up", "k":
			if m.cursor > 0 {
				m.cursor--
			}
		case "down", "j":
			if m.cursor < len(m.updates)-1 {
				m.cursor++
			}
		case " ":
			if len(m.selected) > 0 {
				m.selected[m.cursor] = !m.selected[m.cursor]
			}
		case "a":
			selectAll := false
			for index := range m.updates {
				if !m.selected[index] {
					selectAll = true
					break
				}
			}
			for index := range m.updates {
				m.selected[index] = selectAll
			}
		case "enter":
			return m, tea.Quit
		case "q", "esc", "ctrl+c":
			m.cancelled = true
			return m, tea.Quit
		}
	}
	return m, nil
}

func (m updateSelectorModel) View() string {
	selectedCount := 0
	for index := range m.updates {
		if m.selected[index] {
			selectedCount++
		}
	}
	title := lipgloss.NewStyle().Bold(true).Foreground(colorCyan).Render("Select updates")
	count := lipgloss.NewStyle().Foreground(colorMuted).
		Render(fmt.Sprintf("%d of %d selected", selectedCount, len(m.updates)))

	start := 0
	if m.cursor >= m.maxRows {
		start = m.cursor - m.maxRows + 1
	}
	end := min(len(m.updates), start+m.maxRows)
	var body strings.Builder
	body.WriteString("  " + title + "  " + count + "\n\n")
	for index := start; index < end; index++ {
		update := m.updates[index]
		cursor := " "
		if index == m.cursor {
			cursor = lipgloss.NewStyle().Bold(true).Foreground(colorCyan).Render("›")
		}
		marker := lipgloss.NewStyle().Foreground(colorMuted).Render("○")
		nameStyle := lipgloss.NewStyle().Foreground(colorText)
		if m.selected[index] {
			marker = lipgloss.NewStyle().Foreground(colorCyan).Render("●")
			nameStyle = lipgloss.NewStyle().Bold(true).Foreground(colorText)
		}
		change := lipgloss.NewStyle().Foreground(colorText).Render(update.CurrentVersion + " → " + update.LatestVersion)
		image := lipgloss.NewStyle().Foreground(colorMuted).Render(update.Image)
		fmt.Fprintf(&body, "  %s %s %s  %s\n      %s\n", cursor, marker, nameStyle.Render(update.Name), change, image)
	}
	if end < len(m.updates) {
		fmt.Fprintf(&body, "  %s\n", lipgloss.NewStyle().Foreground(colorMuted).Render(fmt.Sprintf("… %d more", len(m.updates)-end)))
	}
	hint := lipgloss.NewStyle().Foreground(colorMuted).Render("↑↓ move · space toggle · a all · enter update · q cancel")
	body.WriteString("\n  " + hint + "\n")
	return body.String()
}

func newConfirmModel(message string) confirmModel {
	return confirmModel{message: message, selection: 1}
}

func (m confirmModel) Init() tea.Cmd {
	return nil
}

func (m confirmModel) Update(message tea.Msg) (tea.Model, tea.Cmd) {
	key, ok := message.(tea.KeyMsg)
	if !ok {
		return m, nil
	}
	switch key.String() {
	case "left", "right", "h", "l", "tab", "shift+tab":
		m.selection = 1 - m.selection
	case "y", "r":
		m.selection = 0
		m.confirmed = true
		return m, tea.Quit
	case "n", "k", "esc", "q", "ctrl+c":
		m.selection = 1
		m.confirmed = false
		return m, tea.Quit
	case "enter":
		m.confirmed = m.selection == 0
		return m, tea.Quit
	}
	return m, nil
}

func (m confirmModel) View() string {
	prompt := lipgloss.NewStyle().Foreground(colorText).Render(m.message)
	keep := renderChoice("Keep current", m.selection == 1)
	replace := renderChoice("Replace", m.selection == 0)
	hint := lipgloss.NewStyle().Foreground(colorMuted).Render("←→ select · enter confirm")
	return fmt.Sprintf("  %s\n\n  %s   %s\n  %s\n", prompt, keep, replace, hint)
}

func renderChoice(label string, selected bool) string {
	marker := "○"
	style := lipgloss.NewStyle().Foreground(colorMuted)
	if selected {
		marker = "●"
		style = lipgloss.NewStyle().Bold(true).Foreground(colorCyan)
	}
	return style.Render(marker + " " + label)
}

func errorsUnexpectedModel(component string) error {
	return fmt.Errorf("%s returned an unexpected model", component)
}

func shellCommand(args []string) string {
	quoted := make([]string, len(args))
	for i, arg := range args {
		quoted[i] = shellQuote(arg)
	}
	return strings.Join(quoted, " ")
}

func shellQuote(value string) string {
	if value != "" && strings.IndexFunc(value, func(r rune) bool {
		return !(unicode.IsLetter(r) || unicode.IsDigit(r) || strings.ContainsRune("_@%+=:,./-", r))
	}) == -1 {
		return value
	}
	return "'" + strings.ReplaceAll(value, "'", "'\"'\"'") + "'"
}
