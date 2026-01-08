package tui

import (
	"fmt"
	"sort"
	"strings"

	"github.com/charmbracelet/bubbles/progress"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

var (
	progressHeaderStyle = lipgloss.NewStyle().
		Foreground(lipgloss.Color("#7D56F4")).
		Bold(true)

	progressBorderStyle = lipgloss.NewStyle().
		Foreground(lipgloss.Color("240"))

	hostStyle   = lipgloss.NewStyle()
	doneStyle   = lipgloss.NewStyle().Foreground(lipgloss.Color("#04B575"))
	errStyle    = lipgloss.NewStyle().Foreground(lipgloss.Color("#FF5F87"))
	statusStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("#777777"))
)

type ProgressMsg struct {
	Host    string
	Current int64
	Total   int64
	Err     error
}

type DoneMsg struct{}

type hostState struct {
	progress progress.Model
	percent  float64
	current  int64
	total    int64
	err      error
	done     bool
}

type Model struct {
	hosts     []string
	states    map[string]*hostState
	quitting  bool
	width     int
}

func NewModel(hosts []string) Model {
	states := make(map[string]*hostState)
	for _, h := range hosts {
		prog := progress.New(
			progress.WithGradient("#7D56F4", "#04B575"),
			progress.WithoutPercentage(),
		)
		states[h] = &hostState{
			progress: prog,
		}
	}
	sort.Strings(hosts)

	return Model{
		hosts:  hosts,
		states: states,
		width:  100,
	}
}

func (m Model) Init() tea.Cmd {
	return nil
}

func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.KeyMsg:
		switch msg.String() {
		case "ctrl+c", "q":
			m.quitting = true
			return m, tea.Quit
		}
		return m, nil

	case tea.WindowSizeMsg:
		m.width = msg.Width
		return m, nil

	case ProgressMsg:
		state, ok := m.states[msg.Host]
		if !ok {
			return m, nil
		}

		if msg.Err != nil {
			state.err = msg.Err
			state.done = true
		} else {
			state.current = msg.Current
			state.total = msg.Total
			if msg.Total > 0 {
				state.percent = float64(msg.Current) / float64(msg.Total)
			}

			if msg.Current >= msg.Total && msg.Total > 0 {
				state.done = true
			}
		}
		return m, nil

	case DoneMsg:
		m.quitting = true
		return m, tea.Quit

	default:
		return m, nil
	}
}

func (m Model) View() string {
	if m.quitting {
		return m.renderFinal()
	}

	var s strings.Builder

	// Header
	s.WriteString(progressHeaderStyle.Render("File Transfer Progress"))
	s.WriteString("\n\n")

	// Calculate column widths
	hostW := 25
	statusW := 18
	progressW := 35
	sizeW := 25

	m.renderProgressTable(&s, hostW, statusW, progressW, sizeW)

	s.WriteString("\n")
	s.WriteString(statusStyle.Render("Press q to quit"))

	return s.String()
}

func (m *Model) renderProgressTable(b *strings.Builder, hostW, statusW, progressW, sizeW int) {
	hBorder := "─"

	// Top border
	topBorder := "┌" + strings.Repeat(hBorder, hostW) + "┬" +
		strings.Repeat(hBorder, statusW) + "┬" +
		strings.Repeat(hBorder, progressW) + "┬" +
		strings.Repeat(hBorder, sizeW) + "┐"
	b.WriteString(progressBorderStyle.Render(topBorder))
	b.WriteString("\n")

	// Header row
	b.WriteString(progressBorderStyle.Render("│"))
	b.WriteString(fmt.Sprintf(" %-*s ", hostW-2, "Host"))
	b.WriteString(progressBorderStyle.Render("│"))
	b.WriteString(fmt.Sprintf(" %-*s ", statusW-2, "Status"))
	b.WriteString(progressBorderStyle.Render("│"))
	b.WriteString(fmt.Sprintf(" %-*s ", progressW-2, "Progress"))
	b.WriteString(progressBorderStyle.Render("│"))
	b.WriteString(fmt.Sprintf(" %-*s ", sizeW-2, "Size"))
	b.WriteString(progressBorderStyle.Render("│"))
	b.WriteString("\n")

	// Separator
	sepBorder := "├" + strings.Repeat(hBorder, hostW) + "┼" +
		strings.Repeat(hBorder, statusW) + "┼" +
		strings.Repeat(hBorder, progressW) + "┼" +
		strings.Repeat(hBorder, sizeW) + "┤"
	b.WriteString(progressBorderStyle.Render(sepBorder))
	b.WriteString("\n")

	// Data rows
	for _, h := range m.hosts {
		state := m.states[h]

		var status, progressStr, sizeStr string

		if state.err != nil {
			status = errStyle.Render("✗ Error")
			errMsg := state.err.Error()
			if len(errMsg) > progressW-2 {
				errMsg = errMsg[:progressW-5] + "..."
			}
			progressStr = errMsg
			sizeStr = "-"
		} else if state.done {
			status = doneStyle.Render("✓ Done")
			progressStr = "[" + strings.Repeat("=", progressW-10) + "]"
			sizeStr = formatBytes(state.total)
		} else {
			status = statusStyle.Render("⏳ Transferring")
			percent := state.percent
			if percent > 1 {
				percent = 1
			}
			fillWidth := progressW - 10
			progressed := int(percent * float64(fillWidth))
			remaining := fillWidth - progressed
			progressStr = "[" + strings.Repeat("=", progressed) + strings.Repeat("-", remaining) + "]"
			sizeStr = fmt.Sprintf("%s/%s", formatBytes(state.current), formatBytes(state.total))
		}

		hostStr := truncate(h, hostW-2)

		b.WriteString(progressBorderStyle.Render("│"))
		b.WriteString(" " + padRight(hostStr, hostW-2) + " ")
		b.WriteString(progressBorderStyle.Render("│"))
		b.WriteString(" " + padRight(status, statusW-2) + " ")
		b.WriteString(progressBorderStyle.Render("│"))
		b.WriteString(" " + padRight(progressStr, progressW-2) + " ")
		b.WriteString(progressBorderStyle.Render("│"))
		b.WriteString(" " + padRight(sizeStr, sizeW-2) + " ")
		b.WriteString(progressBorderStyle.Render("│"))
		b.WriteString("\n")
	}

	// Bottom border
	bottomBorder := "└" + strings.Repeat(hBorder, hostW) + "┴" +
		strings.Repeat(hBorder, statusW) + "┴" +
		strings.Repeat(hBorder, progressW) + "┴" +
		strings.Repeat(hBorder, sizeW) + "┘"
	b.WriteString(progressBorderStyle.Render(bottomBorder))
}

func (m Model) renderFinal() string {
	var s strings.Builder

	// Header
	s.WriteString(progressHeaderStyle.Render("File Transfer Complete"))
	s.WriteString("\n\n")

	// Calculate column widths
	hostW := 25
	statusW := 18
	resultW := 35
	sizeW := 25

	hBorder := "─"

	// Top border
	topBorder := "┌" + strings.Repeat(hBorder, hostW) + "┬" +
		strings.Repeat(hBorder, statusW) + "┬" +
		strings.Repeat(hBorder, resultW) + "┬" +
		strings.Repeat(hBorder, sizeW) + "┐"
	s.WriteString(progressBorderStyle.Render(topBorder))
	s.WriteString("\n")

	// Header row
	s.WriteString(progressBorderStyle.Render("│"))
	s.WriteString(fmt.Sprintf(" %-*s ", hostW-2, "Host"))
	s.WriteString(progressBorderStyle.Render("│"))
	s.WriteString(fmt.Sprintf(" %-*s ", statusW-2, "Status"))
	s.WriteString(progressBorderStyle.Render("│"))
	s.WriteString(fmt.Sprintf(" %-*s ", resultW-2, "Result"))
	s.WriteString(progressBorderStyle.Render("│"))
	s.WriteString(fmt.Sprintf(" %-*s ", sizeW-2, "Size"))
	s.WriteString(progressBorderStyle.Render("│"))
	s.WriteString("\n")

	// Separator
	sepBorder := "├" + strings.Repeat(hBorder, hostW) + "┼" +
		strings.Repeat(hBorder, statusW) + "┼" +
		strings.Repeat(hBorder, resultW) + "┼" +
		strings.Repeat(hBorder, sizeW) + "┤"
	s.WriteString(progressBorderStyle.Render(sepBorder))
	s.WriteString("\n")

	// Data rows
	for _, h := range m.hosts {
		state := m.states[h]

		var status, result, size string

		if state.err != nil {
			status = errStyle.Render("✗ Failed")
			errMsg := state.err.Error()
			if len(errMsg) > resultW-2 {
				errMsg = errMsg[:resultW-5] + "..."
			}
			result = errMsg
			size = "-"
		} else if state.done {
			status = doneStyle.Render("✓ Success")
			result = "Transfer completed"
			size = formatBytes(state.total)
		} else {
			status = statusStyle.Render("⚠ Interrupted")
			result = "Transfer incomplete"
			size = "-"
		}

		hostStr := truncate(h, hostW-2)

		s.WriteString(progressBorderStyle.Render("│"))
		s.WriteString(" " + padRight(hostStr, hostW-2) + " ")
		s.WriteString(progressBorderStyle.Render("│"))
		s.WriteString(" " + padRight(status, statusW-2) + " ")
		s.WriteString(progressBorderStyle.Render("│"))
		s.WriteString(" " + padRight(result, resultW-2) + " ")
		s.WriteString(progressBorderStyle.Render("│"))
		s.WriteString(" " + padRight(size, sizeW-2) + " ")
		s.WriteString(progressBorderStyle.Render("│"))
		s.WriteString("\n")
	}

	// Bottom border
	bottomBorder := "└" + strings.Repeat(hBorder, hostW) + "┴" +
		strings.Repeat(hBorder, statusW) + "┴" +
		strings.Repeat(hBorder, resultW) + "┴" +
		strings.Repeat(hBorder, sizeW) + "┘"
	s.WriteString(progressBorderStyle.Render(bottomBorder))
	s.WriteString("\n") // Add trailing newline

	return s.String()
}

func formatBytes(b int64) string {
	const unit = 1024
	if b < unit {
		return fmt.Sprintf("%d B", b)
	}
	div, exp := int64(unit), 0
	for n := b / unit; n >= unit; n /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %cB", float64(b)/float64(div), "KMGTPE"[exp])
}
