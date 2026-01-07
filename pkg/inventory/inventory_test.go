package inventory

import (
	"os"
	"path/filepath"
	"testing"
)

func TestInventoryLoad(t *testing.T) {
	// Create temporary config file
	content := `
[ssh]
user = "default_user"
port = 22
key_path = "~/.ssh/id_rsa"

[exec]
parallel = 5

[hosts.web]
addresses = ["192.168.1.10", "192.168.1.11"]
user = "web_user"

[hosts.db]
addresses = ["192.168.1.20"]

[hosts."192.168.1.10"]
port = 2222
`
	tmpfile, err := os.CreateTemp("", "config.toml")
	if err != nil {
		t.Fatal(err)
	}
	defer os.Remove(tmpfile.Name())

	if _, err := tmpfile.Write([]byte(content)); err != nil {
		t.Fatal(err)
	}
	if err := tmpfile.Close(); err != nil {
		t.Fatal(err)
	}

	// Load inventory
	inv, err := New(tmpfile.Name())
	if err != nil {
		t.Fatalf("Failed to create inventory: %v", err)
	}

	// Test 1: Group inheritance (web group user override)
	hosts, err := inv.GetHosts([]string{"web"})
	if err != nil {
		t.Fatal(err)
	}
	
	found10 := false
	found11 := false

	for _, h := range hosts {
		if h.Address == "192.168.1.10" {
			found10 = true
			// Host override (port=2222) should win over default (22)
			if h.Port != 2222 {
				t.Errorf("Expected host 192.168.1.10 port 2222, got %d", h.Port)
			}
			// Group override (user="web_user") should win over default
			if h.User != "web_user" {
				t.Errorf("Expected host 192.168.1.10 user web_user, got %s", h.User)
			}
		}
		if h.Address == "192.168.1.11" {
			found11 = true
			// No host override, should have default port 22
			if h.Port != 22 {
				t.Errorf("Expected host 192.168.1.11 port 22, got %d", h.Port)
			}
			// Group override
			if h.User != "web_user" {
				t.Errorf("Expected host 192.168.1.11 user web_user, got %s", h.User)
			}
		}
	}

	if !found10 || !found11 {
		t.Error("Failed to find all hosts in web group")
	}

	// Test 2: Default values (db group)
	dbHosts, err := inv.GetHosts([]string{"db"})
	if err != nil {
		t.Fatal(err)
	}
	if len(dbHosts) != 1 {
		t.Fatal("Expected 1 db host")
	}
	dbHost := dbHosts[0]
	if dbHost.User != "default_user" {
		t.Errorf("Expected db host user default_user, got %s", dbHost.User)
	}

	// Test 3: Global settings
	cfg := inv.GetConfig()
	if cfg.Exec.Parallel != 5 {
		t.Errorf("Expected parallel 5, got %d", cfg.Exec.Parallel)
	}
}

func TestExpandPath(t *testing.T) {
	home, _ := os.UserHomeDir()
	
	tests := []struct {
		input    string
		expected string
	}{
		{"/tmp/file", "/tmp/file"},
		{"~/.ssh/id_rsa", filepath.Join(home, ".ssh", "id_rsa")},
		{"", ""},
	}

	for _, tt := range tests {
		result := ExpandPath(tt.input)
		if result != tt.expected {
			t.Errorf("ExpandPath(%s): expected %s, got %s", tt.input, tt.expected, result)
		}
	}
}
