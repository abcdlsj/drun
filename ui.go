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
}

func newTerminalUI(in io.Reader, out, errOut io.Writer, noColor bool) *terminalUI {
	inputTTY := false
	if file, ok := in.(*os.File); ok {
		inputTTY = term.IsTerminal(int(file.Fd()))
	}
	outputTTY := false
	if file, ok := out.(*os.File); ok {
		outputTTY = term.IsTerminal(int(file.Fd()))
	}
	interactive := inputTTY && outputTTY && !noColor && os.Getenv("NO_COLOR") == ""
	return &terminalUI{in: in, out: out, err: errOut, interactive: interactive}
}

func (ui *terminalUI) header() {
	if !ui.interactive {
		fmt.Fprintln(ui.out, "drun - safe container refresh")
		return
	}
	brand := lipgloss.NewStyle().Bold(true).Foreground(colorCyan).Render("DRUN")
	subtitle := lipgloss.NewStyle().Foreground(colorMuted).Render("safe container refresh")
	fmt.Fprintf(ui.out, "\n  %s  %s\n\n", brand, subtitle)
}

func (ui *terminalUI) info(message string) {
	ui.printStatus("i", colorCyan, message, ui.out)
}

func (ui *terminalUI) warning(message string) {
	ui.printStatus("!", colorYellow, message, ui.out)
}

func (ui *terminalUI) success(message string) {
	ui.printStatus("+", colorGreen, message, ui.out)
}

func (ui *terminalUI) error(message string) {
	ui.printStatus("x", colorRed, message, ui.err)
}

func (ui *terminalUI) printStatus(mark string, color lipgloss.Color, message string, writer io.Writer) {
	if !ui.interactive {
		fmt.Fprintf(writer, "[%s] %s\n", mark, message)
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

func (ui *terminalUI) command(args []string) {
	command := shellCommand(args)
	if !ui.interactive {
		fmt.Fprintf(ui.out, "$ %s\n", command)
		return
	}

	label := lipgloss.NewStyle().Bold(true).Foreground(colorCyan).Render("replacement command")
	body := lipgloss.NewStyle().
		Foreground(colorText).
		BorderStyle(lipgloss.NormalBorder()).
		BorderLeft(true).
		BorderForeground(colorCyan).
		PaddingLeft(2).
		Width(92).
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
		mark := lipgloss.NewStyle().Bold(true).Foreground(colorRed).Render("x")
		return fmt.Sprintf("  %s  %s\n", mark, text)
	}
	mark := lipgloss.NewStyle().Bold(true).Foreground(colorGreen).Render("+")
	return fmt.Sprintf("  %s  %s\n", mark, text)
}

type confirmModel struct {
	message   string
	selection int
	confirmed bool
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
	replace := renderChoice("Replace", m.selection == 0, colorGreen)
	keep := renderChoice("Keep current", m.selection == 1, colorYellow)
	hint := lipgloss.NewStyle().Foreground(colorMuted).Render("←/→ select · enter confirm")
	return fmt.Sprintf("  %s\n\n  %s   %s\n  %s\n", prompt, replace, keep, hint)
}

func renderChoice(label string, selected bool, color lipgloss.Color) string {
	marker := "○"
	style := lipgloss.NewStyle().Foreground(colorMuted)
	if selected {
		marker = "●"
		style = lipgloss.NewStyle().Bold(true).Foreground(color)
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
