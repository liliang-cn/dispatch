package tui

import (
	"fmt"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/bubbles/spinner"
)

func TestNewExecModel(t *testing.T) {
	hosts := []string{"host1.example.com", "host2.example.com", "host3.example.com"}
	model := NewExecModel(hosts)

	if len(model.hosts) != 3 {
		t.Errorf("expected 3 hosts, got %d", len(model.hosts))
	}

	if len(model.states) != 3 {
		t.Errorf("expected 3 states, got %d", len(model.states))
	}

	model.SetCommand("ls -la")
	if model.command != "ls -la" {
		t.Errorf("expected command 'ls -la', got '%s'", model.command)
	}
}

func TestExecModel_Init(t *testing.T) {
	model := NewExecModel([]string{"host1"})
	cmd := model.Init()

	if cmd == nil {
		t.Error("Init should return a cmd")
	}
}

func TestExecModel_Update_KeyMsg(t *testing.T) {
	model := NewExecModel([]string{"host1", "host2"})

	// Test quit
	tests := []struct {
		name     string
		key      tea.KeyMsg
		quitting bool
	}{
		{"ctrl+c", tea.KeyMsg{Type: tea.KeyCtrlC}, true},
		{"q", tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'q'}}, true},
		{"up", tea.KeyMsg{Type: tea.KeyUp}, false},
		{"down", tea.KeyMsg{Type: tea.KeyDown}, false},
		{"enter", tea.KeyMsg{Type: tea.KeyEnter}, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m := model
			newModel, _ := m.Update(tt.key)
			newExecModel := newModel.(*ExecModel)
			if newExecModel.quitting != tt.quitting {
				t.Errorf("after %s, quitting=%v, want %v", tt.name, newExecModel.quitting, tt.quitting)
			}
		})
	}
}

func TestExecModel_Update_ExecStatusMsg(t *testing.T) {
	model := NewExecModel([]string{"host1"})

	msg := ExecStatusMsg{
		Host:     "host1",
		Status:   "done",
		Output:   "test output",
		ExitCode: 0,
	}

	newModel, _ := model.Update(msg)
	newExecModel := newModel.(*ExecModel)

	state := newExecModel.states["host1"]
	if state.status != "done" {
		t.Errorf("expected status 'done', got '%s'", state.status)
	}
	if state.output != "test output" {
		t.Errorf("expected output 'test output', got '%s'", state.output)
	}
}

func TestExecModel_Update_ExecDoneMsg(t *testing.T) {
	model := NewExecModel([]string{"host1"})

	newModel, _ := model.Update(ExecDoneMsg{})
	newExecModel := newModel.(*ExecModel)

	if !newExecModel.quitting {
		t.Error("expected quitting=true after ExecDoneMsg")
	}
}

func TestExecModel_Update_SpinnerTick(t *testing.T) {
	model := NewExecModel([]string{"host1"})

	// Send a spinner tick message
	newModel, _ := model.Update(spinner.TickMsg{})
	newExecModel := newModel.(*ExecModel)

	// Check that view contains something
	view := newExecModel.View()
	if view == "" {
		t.Error("expected non-empty view")
	}
}

func TestExecModel_getSummary(t *testing.T) {
	model := NewExecModel([]string{"host1", "host2", "host3"})

	// Set different states
	model.states["host1"].status = "running"
	model.states["host2"].status = "done"
	model.states["host3"].status = "error"

	running, done, failed := model.getSummary()

	if running != 1 {
		t.Errorf("expected 1 running, got %d", running)
	}
	if done != 1 {
		t.Errorf("expected 1 done, got %d", done)
	}
	if failed != 1 {
		t.Errorf("expected 1 failed, got %d", failed)
	}
}

func TestExecModel_View(t *testing.T) {
	model := NewExecModel([]string{"host1"})
	model.SetCommand("test command")
	model.states["host1"].status = "done"
	model.states["host1"].fullOutput = []string{"line 1", "line 2", "line 3"}

	view := model.View()

	if view == "" {
		t.Error("expected non-empty view")
	}
	// Check that view contains header
	if !strings.Contains(view, "Command Execution") {
		t.Error("expected view to contain 'Command Execution'")
	}
	// Check that view has table borders
	if !strings.Contains(view, "┌") || !strings.Contains(view, "┐") {
		t.Error("expected view to contain table borders")
	}
}

func TestExecModel_Update_WindowSize(t *testing.T) {
	model := NewExecModel([]string{"host1", "host2"})

	msg := tea.WindowSizeMsg{
		Width:  100,
		Height: 40,
	}

	newModel, _ := model.Update(msg)
	newExecModel := newModel.(*ExecModel)

	if newExecModel.width != 100 {
		t.Errorf("expected width 100, got %d", newExecModel.width)
	}
}

func TestStripAnsi(t *testing.T) {
	tests := []struct {
		input    string
		expected string
	}{
		{"\033[31mError\033[0m", "Error"},
		{"normal text", "normal text"},
		{"\033[1;32mSuccess\033[0m message", "Success message"},
	}

	for _, tt := range tests {
		result := stripAnsi(tt.input)
		if result != tt.expected {
			t.Errorf("stripAnsi(%q) = %q, want %q", tt.input, result, tt.expected)
		}
	}
}

func TestTruncate(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		maxLen   int
		contains string
	}{
		{"short string", "hello", 10, "hello"},
		{"truncate", "hello world", 8, "..."},
		{"exact fit", "hello", 5, "hello"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := truncate(tt.input, tt.maxLen)
			if !strings.Contains(result, tt.contains) {
				t.Errorf("truncate(%q, %d) = %q, should contain %q", tt.input, tt.maxLen, result, tt.contains)
			}
		})
	}
}

func TestGetFinalSummary(t *testing.T) {
	model := NewExecModel([]string{"host1", "host2", "host3"})
	model.states["host1"].status = "done"
	model.states["host2"].status = "done"
	model.states["host3"].status = "error"
	model.states["host3"].exitCode = 1

	summary := model.GetFinalSummary()

	// Check that summary contains key information
	if len(summary) == 0 {
		t.Error("expected non-empty summary")
	}
}

// Benchmark
func BenchmarkExecModel_Update(b *testing.B) {
	model := NewExecModel([]string{"host1", "host2", "host3", "host4", "host5"})
	msg := ExecStatusMsg{
		Host:   "host1",
		Status: "running",
		Output: "test output line",
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		model.Update(msg)
	}
}

func BenchmarkExecModel_View(b *testing.B) {
	hosts := make([]string, 50)
	for i := range hosts {
		hosts[i] = fmt.Sprintf("host%d.example.com", i)
	}
	model := NewExecModel(hosts)
	model.SetCommand("ls -la")

	// Set some states
	for _, h := range hosts {
		model.states[h].status = "running"
		model.states[h].output = "processing..."
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = model.View()
	}
}
