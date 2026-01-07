package dispatch

import (
	"testing"
	"time"
)

func TestNew_Config(t *testing.T) {
	cfg := &Config{
		SSH: &SSHConfig{
			User: "test_user",
			Port: 2222,
		},
		Exec: &ExecConfig{
			Parallel: 50,
			Timeout:  60,
		},
	}

	client, err := New(cfg)
	if err != nil {
		t.Fatalf("Failed to create client: %v", err)
	}

	// Verify config was applied to inventory
	invCfg := client.inv.GetConfig()
	
	if invCfg.SSH.User != "test_user" {
		t.Errorf("Expected SSH user test_user, got %s", invCfg.SSH.User)
	}
	if invCfg.SSH.Port != 2222 {
		t.Errorf("Expected SSH port 2222, got %d", invCfg.SSH.Port)
	}
	if invCfg.Exec.Parallel != 50 {
		t.Errorf("Expected parallel 50, got %d", invCfg.Exec.Parallel)
	}
}

func TestWithOptions(t *testing.T) {
	// compile check for option functions
	_ = WithParallel(10)
	_ = WithTimeout(10 * time.Second)
	_ = WithEnv(map[string]string{"A": "B"})
	_ = WithDir("/tmp")
	_ = WithInput("input")
	_ = WithStreamCallback(func(host, typ string, data []byte) {})
	_ = WithCopyMode(0755)
	_ = WithBackup(true)
	_ = WithUpdateBackup(true)
}
