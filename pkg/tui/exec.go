package tui

import (
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/mattn/go-runewidth"
)

const maxOutputLen = 50

var (
	execHeaderStyle  = lipgloss.NewStyle().Foreground(lipgloss.Color("#7D56F4")).Bold(true)
	execBorderStyle  = lipgloss.NewStyle().Foreground(lipgloss.Color("62"))
	execRunningStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("#FAFAFA"))
	execDoneStyle    = lipgloss.NewStyle().Foreground(lipgloss.Color("#04B575"))
	execErrorStyle   = lipgloss.NewStyle().Foreground(lipgloss.Color("#FF5F87"))
	execDetailStyle  = lipgloss.NewStyle().Foreground(lipgloss.Color("#888888"))
)

var spinnerFrames = []string{"⠋", "⠙", "⠹", "⠸", "⠼", "⠴", "⠦", "⠧", "⠇", "⠏"}

// ExecStatusMsg updates execution status
type ExecStatusMsg struct {
	Host     string
	Status   string
	Output   string
	ExitCode int
	Err      error
}

// ExecDoneMsg signals all executions complete
type ExecDoneMsg struct{}

type execHostState struct {
	status   string
	output   string
	exitCode int
	err      error
	started  time.Time
}

// ExecModel is the TUI model for exec command
type ExecModel struct {
	hosts    []string
	states   map[string]*execHostState
	mu       sync.RWMutex
	quitting bool
	spinner  int
	command  string
}

type tickMsg time.Time

// NewExecModel creates a new exec TUI model
func NewExecModel(hosts []string) ExecModel {
	states := make(map[string]*execHostState)
	for _, h := range hosts {
		states[h] = &execHostState{
			status:  "running",
			started: time.Now(),
		}
	}
	sort.Strings(hosts)

	return ExecModel{
		hosts:   hosts,
		states:  states,
		spinner: 0,
		command: "",
	}
}

func (m *ExecModel) SetCommand(cmd string) {
	m.command = cmd
}

func (m *ExecModel) Init() tea.Cmd {
	return tea.Tick(80*time.Millisecond, func(t time.Time) tea.Msg {
		return tickMsg(t)
	})
}

func (m *ExecModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.KeyMsg:
		switch msg.String() {
		case "ctrl+c", "q":
			m.quitting = true
			return m, tea.Quit
		}

	case tickMsg:
		m.spinner = (m.spinner + 1) % len(spinnerFrames)
		return m, tea.Tick(80*time.Millisecond, func(t time.Time) tea.Msg {
			return tickMsg(t)
		})

	case ExecStatusMsg:
		m.mu.Lock()
		if state, ok := m.states[msg.Host]; ok {
			if msg.Status != "" {
				state.status = msg.Status
			}
			state.output = msg.Output
			if msg.ExitCode != 0 {
				state.exitCode = msg.ExitCode
			}
			if msg.Err != nil {
				state.err = msg.Err
			}
		}
		m.mu.Unlock()
		return m, nil

	case ExecDoneMsg:
		m.quitting = true
		return m, tea.Quit
	}

	return m, nil
}

func (m *ExecModel) View() string {
	m.mu.RLock()
	defer m.mu.RUnlock()

	var b strings.Builder

	// Header
	b.WriteString(execHeaderStyle.Render("Command: " + m.command))
	b.WriteString("\n\n")

	// Fixed column widths (content width, not including borders)
	const (
		colHost   = 18
		colStatus = 12
		colTime   = 8
		colOutput = 40
	)

	// Border parts
	hLine := strings.Repeat("─", colHost+2) + "┬" + strings.Repeat("─", colStatus+2) + "┬" + strings.Repeat("─", colTime+2) + "┬" + strings.Repeat("─", colOutput+2)
	sepLine := strings.Repeat("─", colHost+2) + "┼" + strings.Repeat("─", colStatus+2) + "┼" + strings.Repeat("─", colTime+2) + "┼" + strings.Repeat("─", colOutput+2)
	bLine := strings.Repeat("─", colHost+2) + "┴" + strings.Repeat("─", colStatus+2) + "┴" + strings.Repeat("─", colTime+2) + "┴" + strings.Repeat("─", colOutput+2)

	// Top border
	b.WriteString(execBorderStyle.Render("┌" + hLine + "┐"))
	b.WriteString("\n")

	// Header row
	b.WriteString(execBorderStyle.Render("│"))
	b.WriteString(fmt.Sprintf(" %-*s ", colHost, "Host"))
	b.WriteString(execBorderStyle.Render("│"))
	b.WriteString(fmt.Sprintf(" %-*s ", colStatus, "Status"))
	b.WriteString(execBorderStyle.Render("│"))
	b.WriteString(fmt.Sprintf(" %-*s ", colTime, "Time"))
	b.WriteString(execBorderStyle.Render("│"))
	b.WriteString(fmt.Sprintf(" %-*s ", colOutput, "Output"))
	b.WriteString(execBorderStyle.Render("│"))
	b.WriteString("\n")

	// Separator
	b.WriteString(execBorderStyle.Render("├" + sepLine + "┤"))
	b.WriteString("\n")

	// Data rows
	for _, h := range m.hosts {
		state := m.states[h]
		elapsed := time.Since(state.started).Milliseconds()

		// Status - plain text, no colors
		var status string
		switch state.status {
		case "running":
			status = spinnerFrames[m.spinner] + " Running"
		case "done":
			status = "* Done"
		case "error":
			status = "x Error"
		default:
			status = spinnerFrames[m.spinner] + " Running"
		}

		// Duration
		var duration string
		if elapsed > 1000 {
			duration = fmt.Sprintf("%.1fs", float64(elapsed)/1000)
		} else {
			duration = fmt.Sprintf("%dms", elapsed)
		}

		// Output
		output := ""
		if state.err != nil {
			output = truncate(state.err.Error(), colOutput)
		} else if state.output != "" {
			lines := strings.Split(strings.TrimSpace(state.output), "\n")
			for j := len(lines) - 1; j >= 0; j-- {
				line := strings.TrimSpace(lines[j])
				if line != "" {
					output = truncate(line, colOutput)
					break
				}
			}
		}

		// Truncate host if needed
		hostStr := h
		if len(hostStr) > colHost {
			hostStr = hostStr[:colHost-3] + "..."
		}

		b.WriteString(execBorderStyle.Render("│"))
		b.WriteString(fmt.Sprintf(" %-*s ", colHost, hostStr))
		b.WriteString(execBorderStyle.Render("│"))
		b.WriteString(fmt.Sprintf(" %-*s ", colStatus, status))
		b.WriteString(execBorderStyle.Render("│"))
		b.WriteString(fmt.Sprintf(" %-*s ", colTime, duration))
		b.WriteString(execBorderStyle.Render("│"))
		b.WriteString(fmt.Sprintf(" %-*s ", colOutput, output))
		b.WriteString(execBorderStyle.Render("│"))
		b.WriteString("\n")
	}

	// Bottom border
	b.WriteString(execBorderStyle.Render("└" + bLine + "┘"))
	b.WriteString("\n")

	if !m.quitting {
		b.WriteString("\n")
		b.WriteString(execDetailStyle.Render("Press q to quit"))
	}

	return b.String()
}

func truncate(s string, max int) string {
	if len(s) <= max {
		return s
	}
	if max <= 3 {
		return s[:max]
	}
	return s[:max-3] + "..."
}

func padRight(s string, width int) string {
	visibleWidth := runewidth.StringWidth(stripAnsi(s))
	if visibleWidth >= width {
		return s
	}
	return s + strings.Repeat(" ", width-visibleWidth)
}

func runeWidth(s string) int {
	// Simple width calculation - count visible characters
	return len([]rune(stripAnsi(s)))
}

// stripAnsi removes ANSI escape codes from a string
func stripAnsi(s string) string {
	var result strings.Builder
	inEscape := false
	for _, r := range s {
		if r == '\033' {
			inEscape = true
			continue
		}
		if inEscape {
			if r == 'm' {
				inEscape = false
			}
			continue
		}
		result.WriteRune(r)
	}
	return result.String()
}

// RenderFinalResult is no longer needed but kept for compatibility
func (m *ExecModel) RenderFinalResult() string {
	return ""
}
