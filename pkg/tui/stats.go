package tui

import (
	"fmt"
	"sort"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

var (
	statsHeaderStyle = lipgloss.NewStyle().
		Foreground(lipgloss.Color("#7D56F4")).
		Bold(true)

	statsBorderStyle = lipgloss.NewStyle().
		Foreground(lipgloss.Color("240"))

	existsStyle    = lipgloss.NewStyle().Foreground(lipgloss.Color("#04B575"))
	notFoundStyle  = lipgloss.NewStyle().Foreground(lipgloss.Color("#FF5F87"))
	detailStyle    = lipgloss.NewStyle().Foreground(lipgloss.Color("#888888"))
	dirStyle       = lipgloss.NewStyle().Foreground(lipgloss.Color("#FAFAFA"))
)

// StatsMsg updates stats status
type StatsMsg struct {
	Host      string
	Exists    bool
	IsDir     bool
	Size      int64
	Mode      int64
	ModTime   int64
	Owner     string
	Group     string
	Err       error
	EndTime   time.Time
}

// StatsModel represents the file stats TUI model
type StatsModel struct {
	hosts      []string
	path       string
	states     map[string]*statsState
	quitting   bool
	width      int
	tickCount  int
	startTime  time.Time
}

type statsState struct {
	exists    bool
	isDir     bool
	size      int64
	mode      int64
	modTime   int64
	owner     string
	group     string
	err       error
	done      bool
	endTime   time.Time
}

// NewStatsModel creates a new stats model
func NewStatsModel(hosts []string, path string) StatsModel {
	sort.Strings(hosts)

	return StatsModel{
		hosts:     hosts,
		path:      path,
		states:    make(map[string]*statsState),
		startTime: time.Now(),
	}
}

func (m StatsModel) Init() tea.Cmd {
	return nil
}

func (m StatsModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
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

	case StatsMsg:
		state, ok := m.states[msg.Host]
		if !ok {
			state = &statsState{}
			m.states[msg.Host] = state
		}

		state.exists = msg.Exists
		state.isDir = msg.IsDir
		state.size = msg.Size
		state.mode = msg.Mode
		state.modTime = msg.ModTime
		state.owner = msg.Owner
		state.group = msg.Group
		state.err = msg.Err
		state.done = true
		state.endTime = msg.EndTime

		m.tickCount++
		return m, nil

	case DoneMsg:
		m.quitting = true
		return m, tea.Quit

	default:
		return m, nil
	}
}

func (m StatsModel) View() string {
	if m.quitting {
		return m.renderFinal()
	}

	var s strings.Builder

	// Header
	s.WriteString(statsHeaderStyle.Render("File Stats"))
	s.WriteString("\n\n")

	// Calculate column widths
	pathW := 30
	statusW := 22
	sizeW := 25
	permsW := 10
	ownerW := 18

	m.renderStatsTable(&s, pathW, statusW, sizeW, permsW, ownerW)

	s.WriteString("\n")
	s.WriteString(detailStyle.Render("Press q to quit"))

	return s.String()
}

func (m *StatsModel) renderStatsTable(b *strings.Builder, pathW, statusW, sizeW, permsW, ownerW int) {
	hBorder := "─"

	// Top border
	topBorder := "┌" + strings.Repeat(hBorder, pathW) + "┬" +
		strings.Repeat(hBorder, statusW) + "┬" +
		strings.Repeat(hBorder, sizeW) + "┬" +
		strings.Repeat(hBorder, permsW) + "┬" +
		strings.Repeat(hBorder, ownerW) + "┐"
	b.WriteString(statsBorderStyle.Render(topBorder))
	b.WriteString("\n")

	// Header row
	b.WriteString(statsBorderStyle.Render("│"))
	b.WriteString(fmt.Sprintf(" %-*s ", pathW-2, "Host"))
	b.WriteString(statsBorderStyle.Render("│"))
	b.WriteString(fmt.Sprintf(" %-*s ", statusW-2, "Status"))
	b.WriteString(statsBorderStyle.Render("│"))
	b.WriteString(fmt.Sprintf(" %-*s ", sizeW-2, "Size"))
	b.WriteString(statsBorderStyle.Render("│"))
	b.WriteString(fmt.Sprintf(" %-*s ", permsW-2, "Mode"))
	b.WriteString(statsBorderStyle.Render("│"))
	b.WriteString(fmt.Sprintf(" %-*s ", ownerW-2, "Owner:Group"))
	b.WriteString(statsBorderStyle.Render("│"))
	b.WriteString("\n")

	// Separator
	sepBorder := "├" + strings.Repeat(hBorder, pathW) + "┼" +
		strings.Repeat(hBorder, statusW) + "┼" +
		strings.Repeat(hBorder, sizeW) + "┼" +
		strings.Repeat(hBorder, permsW) + "┼" +
		strings.Repeat(hBorder, ownerW) + "┤"
	b.WriteString(statsBorderStyle.Render(sepBorder))
	b.WriteString("\n")

	// Data rows
	for _, h := range m.hosts {
		state := m.states[h]
		var status, size, perms, owner string

		if state == nil {
			status = detailStyle.Render("Checking...")
			size = "-"
			perms = "-"
			owner = "-"
		} else if state.err != nil {
			status = notFoundStyle.Render("Error")
			size = "-"
			perms = "-"
			owner = "-"
		} else if !state.exists {
			status = notFoundStyle.Render("Not found")
			size = "-"
			perms = "-"
			owner = "-"
		} else {
			if state.isDir {
				status = existsStyle.Render("DIR")
			} else {
				status = existsStyle.Render("FILE")
			}
			size = formatBytes(state.size)
			perms = fmt.Sprintf("%04o", state.mode)
			owner = fmt.Sprintf("%s:%s", state.owner, state.group)
		}

		hostStr := truncate(h, pathW-2)

		b.WriteString(statsBorderStyle.Render("│"))
		b.WriteString(" " + padRight(hostStr, pathW-2) + " ")
		b.WriteString(statsBorderStyle.Render("│"))
		b.WriteString(" " + padRight(status, statusW-2) + " ")
		b.WriteString(statsBorderStyle.Render("│"))
		b.WriteString(" " + padRight(size, sizeW-2) + " ")
		b.WriteString(statsBorderStyle.Render("│"))
		b.WriteString(" " + padRight(perms, permsW-2) + " ")
		b.WriteString(statsBorderStyle.Render("│"))
		b.WriteString(" " + padRight(owner, ownerW-2) + " ")
		b.WriteString(statsBorderStyle.Render("│"))
		b.WriteString("\n")
	}

	// Bottom border
	bottomBorder := "└" + strings.Repeat(hBorder, pathW) + "┴" +
		strings.Repeat(hBorder, statusW) + "┴" +
		strings.Repeat(hBorder, sizeW) + "┴" +
		strings.Repeat(hBorder, permsW) + "┴" +
		strings.Repeat(hBorder, ownerW) + "┘"
	b.WriteString(statsBorderStyle.Render(bottomBorder))
}

func (m StatsModel) renderFinal() string {
	var s strings.Builder

	// Header
	s.WriteString(statsHeaderStyle.Render("File Stats Complete"))
	s.WriteString("\n\n")
	s.WriteString(fmt.Sprintf("Path: %s\n\n", m.path))

	// Calculate column widths
	pathW := 30
	statusW := 22
	sizeW := 25
	permsW := 10
	ownerW := 18
	timeW := 12

	hBorder := "─"

	// Top border
	topBorder := "┌" + strings.Repeat(hBorder, pathW) + "┬" +
		strings.Repeat(hBorder, statusW) + "┬" +
		strings.Repeat(hBorder, sizeW) + "┬" +
		strings.Repeat(hBorder, permsW) + "┬" +
		strings.Repeat(hBorder, ownerW) + "┬" +
		strings.Repeat(hBorder, timeW) + "┐"
	s.WriteString(statsBorderStyle.Render(topBorder))
	s.WriteString("\n")

	// Header row
	s.WriteString(statsBorderStyle.Render("│"))
	s.WriteString(fmt.Sprintf(" %-*s ", pathW-2, "Host"))
	s.WriteString(statsBorderStyle.Render("│"))
	s.WriteString(fmt.Sprintf(" %-*s ", statusW-2, "Status"))
	s.WriteString(statsBorderStyle.Render("│"))
	s.WriteString(fmt.Sprintf(" %-*s ", sizeW-2, "Size"))
	s.WriteString(statsBorderStyle.Render("│"))
	s.WriteString(fmt.Sprintf(" %-*s ", permsW-2, "Mode"))
	s.WriteString(statsBorderStyle.Render("│"))
	s.WriteString(fmt.Sprintf(" %-*s ", ownerW-2, "Owner:Group"))
	s.WriteString(statsBorderStyle.Render("│"))
	s.WriteString(fmt.Sprintf(" %-*s ", timeW-2, "Time"))
	s.WriteString(statsBorderStyle.Render("│"))
	s.WriteString("\n")

	// Separator
	sepBorder := "├" + strings.Repeat(hBorder, pathW) + "┼" +
		strings.Repeat(hBorder, statusW) + "┼" +
		strings.Repeat(hBorder, sizeW) + "┼" +
		strings.Repeat(hBorder, permsW) + "┼" +
		strings.Repeat(hBorder, ownerW) + "┼" +
		strings.Repeat(hBorder, timeW) + "┤"
	s.WriteString(statsBorderStyle.Render(sepBorder))
	s.WriteString("\n")

	// Data rows
	for _, h := range m.hosts {
		state := m.states[h]
		var status, size, perms, owner, timeStr string

		if state.err != nil {
			status = notFoundStyle.Render("Error")
			size = "-"
			perms = "-"
			owner = "-"
			timeStr = "-"
		} else if !state.exists {
			status = notFoundStyle.Render("Not found")
			size = "-"
			perms = "-"
			owner = "-"
			timeStr = "-"
		} else {
			if state.isDir {
				status = existsStyle.Render("DIR")
			} else {
				status = existsStyle.Render("FILE")
			}
			size = formatBytes(state.size)
			perms = fmt.Sprintf("%04o", state.mode)
			owner = fmt.Sprintf("%s:%s", state.owner, state.group)
			timeStr = fmt.Sprintf("%.0fms", float64(state.endTime.Sub(m.startTime).Milliseconds()))
		}

		hostStr := truncate(h, pathW-2)

		s.WriteString(statsBorderStyle.Render("│"))
		s.WriteString(" " + padRight(hostStr, pathW-2) + " ")
		s.WriteString(statsBorderStyle.Render("│"))
		s.WriteString(" " + padRight(status, statusW-2) + " ")
		s.WriteString(statsBorderStyle.Render("│"))
		s.WriteString(" " + padRight(size, sizeW-2) + " ")
		s.WriteString(statsBorderStyle.Render("│"))
		s.WriteString(" " + padRight(perms, permsW-2) + " ")
		s.WriteString(statsBorderStyle.Render("│"))
		s.WriteString(" " + padRight(owner, ownerW-2) + " ")
		s.WriteString(statsBorderStyle.Render("│"))
		s.WriteString(" " + padRight(timeStr, timeW-2) + " ")
		s.WriteString(statsBorderStyle.Render("│"))
		s.WriteString("\n")
	}

	// Bottom border
	bottomBorder := "└" + strings.Repeat(hBorder, pathW) + "┴" +
		strings.Repeat(hBorder, statusW) + "┴" +
		strings.Repeat(hBorder, sizeW) + "┴" +
		strings.Repeat(hBorder, permsW) + "┴" +
		strings.Repeat(hBorder, ownerW) + "┴" +
		strings.Repeat(hBorder, timeW) + "┘"
	s.WriteString(statsBorderStyle.Render(bottomBorder))
	s.WriteString("\n")

	return s.String()
}
