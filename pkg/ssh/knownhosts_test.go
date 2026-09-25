package ssh

import (
	"crypto/ed25519"
	"crypto/rand"
	"crypto/rsa"
	"net"
	"os"
	"testing"
	"time"

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

// A host whose key changed is fixed by editing known_hosts. The verifier read
// the file once, so the fix did nothing until the process restarted.
func TestKnownHostsVerifierPicksUpAFixedFile(t *testing.T) {
	path := t.TempDir() + "/known_hosts"
	oldPub, _, _ := ed25519.GenerateKey(rand.Reader)
	oldKey, _ := ssh.NewPublicKey(oldPub)
	newPub, _, _ := ed25519.GenerateKey(rand.Reader)
	newKey, _ := ssh.NewPublicKey(newPub)
	line := func(k ssh.PublicKey) string { return "10.0.0.3 " + string(ssh.MarshalAuthorizedKey(k)) }

	if err := os.WriteFile(path, []byte(line(oldKey)), 0o600); err != nil {
		t.Fatal(err)
	}
	v, err := NewKnownHostsVerifier(path, false)
	if err != nil {
		t.Fatal(err)
	}
	addr := &net.TCPAddr{IP: net.ParseIP("10.0.0.3"), Port: 22}
	if err := v.Verify("10.0.0.3", addr, newKey); err == nil {
		t.Fatal("a changed key was accepted")
	}

	// The operator checks the new key and replaces the entry.
	later := time.Now().Add(2 * time.Second)
	if err := os.WriteFile(path, []byte(line(newKey)), 0o600); err != nil {
		t.Fatal(err)
	}
	_ = os.Chtimes(path, later, later)
	if err := v.Verify("10.0.0.3", addr, newKey); err != nil {
		t.Fatalf("the corrected known_hosts was not picked up: %v", err)
	}
	if err := v.Verify("10.0.0.3", addr, oldKey); err == nil {
		t.Fatal("the replaced key is still accepted")
	}
}

// A host known by its ed25519 key is asked for that key only. Left to the
// defaults, a host with several host keys could present its ECDSA key, which
// then did not match the recorded ed25519 one and read as "host key changed".
func TestHostKeyAlgorithmsFollowTheRecordedKeyTypes(t *testing.T) {
	path := t.TempDir() + "/known_hosts"
	edPub, _, _ := ed25519.GenerateKey(rand.Reader)
	edKey, _ := ssh.NewPublicKey(edPub)
	rsaKey := mustRSAKey(t)
	content := "10.0.0.3 " + string(ssh.MarshalAuthorizedKey(edKey)) +
		"sdt4 " + string(ssh.MarshalAuthorizedKey(rsaKey))
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	v, err := NewKnownHostsVerifier(path, true)
	if err != nil {
		t.Fatal(err)
	}
	if got := v.HostKeyAlgorithms("10.0.0.3", "sdt3"); len(got) != 1 || got[0] != ssh.KeyAlgoED25519 {
		t.Errorf("10.0.0.3 = %v, want only %s", got, ssh.KeyAlgoED25519)
	}
	got := v.HostKeyAlgorithms("10.0.0.4", "sdt4")
	if len(got) == 0 || got[0] != ssh.KeyAlgoRSASHA512 {
		t.Errorf("an RSA host key must be negotiated with SHA-2 first: %v", got)
	}
	if got := v.HostKeyAlgorithms("10.0.0.9"); got != nil {
		t.Errorf("an unknown host must keep the defaults, got %v", got)
	}
}

func mustRSAKey(t *testing.T) ssh.PublicKey {
	t.Helper()
	k, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	pub, err := ssh.NewPublicKey(&k.PublicKey)
	if err != nil {
		t.Fatal(err)
	}
	return pub
}
