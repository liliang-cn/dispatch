// Package logger provides a simple structured logger for dispatch
package logger

import (
	"fmt"
	"io"
	"os"
	"strings"
	"sync"
	"time"
)

// LogLevel represents the log level
type LogLevel int

const (
	// DEBUG level for detailed debugging information
	DEBUG LogLevel = iota
	// INFO level for general informational messages
	INFO
	// WARN level for warning messages
	WARN
	// ERROR level for error messages
	ERROR
)

// String returns the string representation of the log level
func (l LogLevel) String() string {
	switch l {
	case DEBUG:
		return "DEBUG"
	case INFO:
		return "INFO"
	case WARN:
		return "WARN"
	case ERROR:
		return "ERROR"
	default:
		return "UNKNOWN"
	}
}

// ParseLogLevel parses a string to LogLevel
func ParseLogLevel(s string) LogLevel {
	switch strings.ToUpper(s) {
	case "DEBUG":
		return DEBUG
	case "INFO":
		return INFO
	case "WARN", "WARNING":
		return WARN
	case "ERROR":
		return ERROR
	default:
		return INFO
	}
}

// Colors for terminal output
const (
	colorReset  = "\033[0m"
	colorRed    = "\033[31m"
	colorYellow = "\033[33m"
	colorGreen  = "\033[32m"
	colorBlue   = "\033[34m"
	colorGray   = "\033[90m"
)

// Logger is a simple structured logger
type Logger struct {
	mu       sync.Mutex
	level    LogLevel
	output   io.Writer
	noColor  bool
	showTime bool
}

// Config holds logger configuration
type Config struct {
	Level    string
	Output   string // "stdout", "stderr", or file path
	NoColor  bool
	ShowTime bool
}

// New creates a new logger with the given configuration
func New(cfg *Config) *Logger {
	level := INFO
	if cfg != nil && cfg.Level != "" {
		level = ParseLogLevel(cfg.Level)
	}

	output := io.Writer(os.Stdout)
	noColor := false
	showTime := false

	if cfg != nil {
		showTime = cfg.ShowTime
		noColor = cfg.NoColor

		if cfg.Output == "stderr" {
			output = os.Stderr
		} else if cfg.Output != "" && cfg.Output != "stdout" {
			// File output
			f, err := os.OpenFile(cfg.Output, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0644)
			if err == nil {
				output = f
				noColor = true // Disable colors for file output
			}
		}
	}

	// Detect if output is a terminal
	if !noColor {
		if f, ok := output.(*os.File); ok {
			noColor = !isTerminal(f.Fd())
		}
	}

	return &Logger{
		level:    level,
		output:   output,
		noColor:  noColor,
		showTime: showTime,
	}
}

// NewWithLevel creates a new logger with the specified log level
func NewWithLevel(level string) *Logger {
	return New(&Config{Level: level})
}

// SetLevel sets the log level
func (l *Logger) SetLevel(level LogLevel) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.level = level
}

// SetOutput sets the output writer
func (l *Logger) SetOutput(w io.Writer) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.output = w
}

// log is the internal logging method
func (l *Logger) log(level LogLevel, format string, args ...interface{}) {
	if level < l.level {
		return
	}

	l.mu.Lock()
	defer l.mu.Unlock()

	msg := fmt.Sprintf(format, args...)

	var levelStr, color string
	switch level {
	case DEBUG:
		levelStr = "DEBUG"
		color = colorGray
	case INFO:
		levelStr = "INFO "
		color = colorGreen
	case WARN:
		levelStr = "WARN "
		color = colorYellow
	case ERROR:
		levelStr = "ERROR"
		color = colorRed
	}

	if l.noColor {
		if l.showTime {
			fmt.Fprintf(l.output, "%s [%s] %s\n", time.Now().Format("2006-01-02 15:04:05"), levelStr, msg)
		} else {
			fmt.Fprintf(l.output, "[%s] %s\n", levelStr, msg)
		}
	} else {
		if l.showTime {
			fmt.Fprintf(l.output, "%s [%s%s%s] %s\n",
				time.Now().Format("2006-01-02 15:04:05"),
				color, levelStr, colorReset, msg)
		} else {
			fmt.Fprintf(l.output, "[%s%s%s] %s\n", color, levelStr, colorReset, msg)
		}
	}
}

// Debug logs a debug message
func (l *Logger) Debug(format string, args ...interface{}) {
	l.log(DEBUG, format, args...)
}

// Info logs an info message
func (l *Logger) Info(format string, args ...interface{}) {
	l.log(INFO, format, args...)
}

// Warn logs a warning message
func (l *Logger) Warn(format string, args ...interface{}) {
	l.log(WARN, format, args...)
}

// Error logs an error message
func (l *Logger) Error(format string, args ...interface{}) {
	l.log(ERROR, format, args...)
}

// WithField returns a log entry with fields
func (l *Logger) WithField(key string, value interface{}) *Entry {
	return &Entry{
		logger: l,
		fields: map[string]interface{}{key: value},
	}
}

// WithFields returns a log entry with multiple fields
func (l *Logger) WithFields(fields map[string]interface{}) *Entry {
	return &Entry{
		logger: l,
		fields: fields,
	}
}

// Entry represents a log entry with fields
type Entry struct {
	logger *Logger
	fields map[string]interface{}
}

// Debug logs a debug message with fields
func (e *Entry) Debug(format string, args ...interface{}) {
	e.log(DEBUG, format, args...)
}

// Info logs an info message with fields
func (e *Entry) Info(format string, args ...interface{}) {
	e.log(INFO, format, args...)
}

// Warn logs a warning message with fields
func (e *Entry) Warn(format string, args ...interface{}) {
	e.log(WARN, format, args...)
}

// Error logs an error message with fields
func (e *Entry) Error(format string, args ...interface{}) {
	e.log(ERROR, format, args...)
}

func (e *Entry) log(level LogLevel, format string, args ...interface{}) {
	if len(e.fields) == 0 {
		e.logger.log(level, format, args...)
		return
	}

	// Build prefix with fields
	parts := make([]string, 0, len(e.fields))
	for k, v := range e.fields {
		parts = append(parts, fmt.Sprintf("%s=%v", k, v))
	}
	prefix := strings.Join(parts, " ")

	msg := fmt.Sprintf(format, args...)
	e.logger.log(level, "%s %s", prefix, msg)
}

// Default logger instance
var std = New(&Config{Level: "INFO"})

// SetDefault sets the default logger
func SetDefault(l *Logger) {
	std = l
}

// Debug logs a debug message using the default logger
func Debug(format string, args ...interface{}) {
	std.Debug(format, args...)
}

// Info logs an info message using the default logger
func Info(format string, args ...interface{}) {
	std.Info(format, args...)
}

// Warn logs a warning message using the default logger
func Warn(format string, args ...interface{}) {
	std.Warn(format, args...)
}

// Error logs an error message using the default logger
func Error(format string, args ...interface{}) {
	std.Error(format, args...)
}
