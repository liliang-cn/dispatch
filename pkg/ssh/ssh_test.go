package ssh

import (
	"testing"
)

func TestMatchHostPattern(t *testing.T) {
	tests := []struct {
		host    string
		pattern string
		match   bool
	}{
		{"example.com", "example.com", true},
		{"example.com", "*.com", true},
		{"192.168.1.10", "192.168.1.*", true},
		{"example.com", "other.com", false},
		{"example.com", "!other.com", true},
		{"example.com", "!example.com", false},
	}

	for _, tt := range tests {
		result := matchHostPattern(tt.host, tt.pattern)
		if result != tt.match {
			t.Errorf("matchHostPattern(%s, %s): expected %v, got %v", tt.host, tt.pattern, tt.match, result)
		}
	}
}

func TestParsePort(t *testing.T) {
	tests := []struct {
		input    string
		expected int
		err      bool
	}{
		{"22", 22, false},
		{"8080", 8080, false},
		{"abc", 0, true},
	}

	for _, tt := range tests {
		port, err := parsePort(tt.input)
		if (err != nil) != tt.err {
			t.Errorf("parsePort(%s) error state: %v", tt.input, err)
		}
		if port != tt.expected {
			t.Errorf("parsePort(%s): expected %d, got %d", tt.input, tt.expected, port)
		}
	}
}
