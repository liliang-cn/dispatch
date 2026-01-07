package ssh

import (
	"crypto/ed25519"
	"crypto/rand"
	"net"
	"os"
	"testing"

	"golang.org/x/crypto/ssh"
)

func TestKnownHostsVerifier(t *testing.T) {
	// Create temp known_hosts file
	tmpFile, err := os.CreateTemp("", "known_hosts")
	if err != nil {
		t.Fatal(err)
	}
	defer os.Remove(tmpFile.Name())
	tmpFile.Close()

	// Generate a key
	pub, _, _ := ed25519.GenerateKey(rand.Reader)
	sshPub, _ := ssh.NewPublicKey(pub)

	// 1. Test Auto-Add
	v, err := NewKnownHostsVerifier(tmpFile.Name(), true)
	if err != nil {
		t.Fatal(err)
	}

	addr := &net.TCPAddr{IP: net.ParseIP("127.0.0.1"), Port: 22}
	
	// Should succeed and add key
	err = v.Verify("127.0.0.1", addr, sshPub)
	if err != nil {
		t.Errorf("Auto-add failed: %v", err)
	}

	// 2. Test Success (already exists)
	err = v.Verify("127.0.0.1", addr, sshPub)
	if err != nil {
		t.Errorf("Verification failed for existing key: %v", err)
	}

	// 3. Test Changed Key
	pub2, _, _ := ed25519.GenerateKey(rand.Reader)
	sshPub2, _ := ssh.NewPublicKey(pub2)
	err = v.Verify("127.0.0.1", addr, sshPub2)
	if err == nil {
		t.Error("Expected error for changed host key, got nil")
	}

	// 4. Test Reject Unknown (autoAdd = false)
	v2, _ := NewKnownHostsVerifier(tmpFile.Name(), false)
	err = v2.Verify("192.168.1.100", &net.TCPAddr{IP: net.ParseIP("192.168.1.100")}, sshPub)
	if err == nil {
		t.Error("Expected error for unknown host key when auto-add is disabled, got nil")
	}
}
