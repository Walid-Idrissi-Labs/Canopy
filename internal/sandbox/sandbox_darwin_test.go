//go:build darwin

package sandbox

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"math/big"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

// A writable directory that only shares a name's beginning with a denied one is not inside it, so
// it is not opened again as if it were.
func TestASiblingThatSharesAPrefixIsNotInsideTheDeniedDirectory(t *testing.T) {
	profile := Policy{Writable: []string{"/x/canopy-other"}, DenyRead: []string{"/x/canopy"},
		Reopen: []string{"/x/canopy"}}.profile()
	if strings.Contains(profile, "(allow file-read*\n  (subpath \"/x/canopy-other\")") {
		t.Fatalf("a sibling was treated as inside the denied directory:\n%s", profile)
	}
}

// A credential location inside the workspace stays unwritable: canopy run in the home directory
// makes the home directory the workspace.
func TestACredentialInsideTheWorkspaceStaysUnwritable(t *testing.T) {
	requireSandbox(t)
	home := t.TempDir()
	if resolved, err := filepath.EvalSymlinks(home); err == nil {
		home = resolved
	}
	t.Setenv("HOME", home)
	ssh := filepath.Join(home, ".ssh")
	if err := os.MkdirAll(ssh, 0o700); err != nil {
		t.Fatal(err)
	}
	p := ForWorkspace(home)
	_, _ = run(t, p, "echo planted > "+filepath.Join(ssh, "authorized_keys"))
	if _, err := os.Stat(filepath.Join(ssh, "authorized_keys")); err == nil {
		t.Fatal("a command in the home directory wrote ~/.ssh/authorized_keys")
	}
	if out, err := run(t, p, "echo ok > "+filepath.Join(home, "notes.txt")); err != nil {
		t.Fatalf("the rest of the workspace is not writable: %v %s", err, out)
	}
}

// Only Canopy's own data directories are opened again for a workspace inside them. A workspace
// inside ~/.ssh stays as denied as the rest of ~/.ssh.
func TestOnlyCanopysDataDirectoryIsOpenedAgain(t *testing.T) {
	requireSandbox(t)
	home := t.TempDir()
	if resolved, err := filepath.EvalSymlinks(home); err == nil {
		home = resolved
	}
	t.Setenv("HOME", home)
	inSSH := filepath.Join(home, ".ssh", "project")
	if err := os.MkdirAll(inSSH, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(home, ".ssh", "id_ed25519"), []byte("PRIVATE"), 0o600); err != nil {
		t.Fatal(err)
	}
	p := ForWorkspace(inSSH)
	if out, _ := run(t, p, "cat "+filepath.Join(home, ".ssh", "id_ed25519")+"; echo x > "+filepath.Join(inSSH, "f")); strings.Contains(out, "PRIVATE") {
		t.Fatal("a workspace inside ~/.ssh opened ~/.ssh")
	}
	if _, err := os.Stat(filepath.Join(inSSH, "f")); err == nil {
		t.Fatal("a workspace inside ~/.ssh was written")
	}
	worktree := filepath.Join(home, "Library", "Application Support", "canopy", "worktrees", "repo")
	if err := os.MkdirAll(worktree, 0o755); err != nil {
		t.Fatal(err)
	}
	if out, err := run(t, ForWorkspace(worktree), "cd '"+worktree+"' && echo ok > made && cat made"); err != nil || !strings.Contains(out, "ok") {
		t.Fatalf("a worktree in Canopy's data directory is not usable: %v %s", err, out)
	}
}

// With the network off, a name does not resolve either: a lookup goes through mDNSResponder, which
// would otherwise ask the network for the command, and a name can carry data out.
func TestNoNameResolvesWithTheNetworkOff(t *testing.T) {
	requireSandbox(t)
	lookup := "dscacheutil -q host -a name example.com"
	if out, err := exec.Command("/bin/sh", "-c", lookup).Output(); err != nil || !strings.Contains(string(out), "address") {
		t.Skip("example.com does not resolve here even outside the sandbox")
	}
	p := Policy{Writable: []string{t.TempDir()}, Network: NetworkNone}
	if out, _ := run(t, p, lookup); strings.Contains(out, "address") {
		t.Fatalf("a name resolved with the network off:\n%s", out)
	}
}

// With the network off, a URL cannot be handed to another application, which would fetch it
// outside the sandbox: LaunchServices does not answer. The scheme belongs to no application, so
// nothing is opened either way; what matters is whether LaunchServices was reached.
func TestNoURLIsHandedOnWithTheNetworkOff(t *testing.T) {
	requireSandbox(t)
	probe := "open -g 'x-canopy-probe-nonexistent://probe' 2>&1"
	if out, _ := exec.Command("/bin/sh", "-c", probe).CombinedOutput(); !strings.Contains(string(out), "-10814") {
		t.Skip("LaunchServices does not answer as expected here even outside the sandbox")
	}
	p := Policy{Writable: []string{t.TempDir()}, Network: NetworkNone}
	if out, _ := run(t, p, probe); strings.Contains(out, "-10814") {
		t.Fatalf("LaunchServices answered a command with the network off:\n%s", out)
	}
}

// With the network off, trustd, which runs outside the sandbox, does not fetch the URLs a
// certificate names for a command: a certificate's issuer address could carry anything to anyone.
func TestNoCertificateURLIsFetchedWithTheNetworkOff(t *testing.T) {
	requireSandbox(t)
	if _, err := exec.LookPath("security"); err != nil {
		t.Skip(err)
	}
	var mu sync.Mutex
	fetched := map[string]bool{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		fetched[r.URL.Path] = true
		mu.Unlock()
		http.NotFound(w, r)
	}))
	defer server.Close()
	was := func(path string) bool { mu.Lock(); defer mu.Unlock(); return fetched[path] }
	// A URL of its own for each run: trustd remembers one it has tried.
	verify := func(path string) string {
		leaf := filepath.Join(t.TempDir(), "leaf.pem")
		writeLeafNaming(t, leaf, server.URL+path)
		return "security verify-cert -c " + leaf + " -p ssl -s probe.invalid >/dev/null 2>&1; true"
	}
	if out, err := exec.Command("/bin/sh", "-c", verify("/outside")).CombinedOutput(); err != nil || !was("/outside") {
		t.Skipf("trustd does not fetch the issuer here even outside the sandbox: %v %s", err, out)
	}
	p := ForWorkspace(t.TempDir())
	p.Network = NetworkNone
	_, _ = run(t, p, verify("/inside"))
	if was("/inside") {
		t.Fatal("trustd fetched a certificate's URL for a command with the network off")
	}
}

// The rules no test above can exercise without a system prompt or a real fetch are at least in
// the network-off profile: AppleEvents, which can drive any application, and nsurlsessiond.
func TestTheNetworkOffProfileDeniesOtherProgramsReachingOut(t *testing.T) {
	profile := Policy{Network: NetworkNone}.profile()
	for _, rule := range []string{"(deny appleevent-send)", `(global-name "com.apple.nsurlsessiond")`,
		`(global-name "com.apple.coreservices.launchservicesd")`} {
		if !strings.Contains(profile, rule) {
			t.Errorf("the network-off profile lacks %s", rule)
		}
	}
}

// writeLeafNaming writes a certificate whose issuer is not at hand, naming where to fetch it.
func writeLeafNaming(t *testing.T, path, issuerURL string) {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	issuer := &x509.Certificate{SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "canopy probe CA"},
		NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(time.Hour), IsCA: true,
		BasicConstraintsValid: true, KeyUsage: x509.KeyUsageCertSign, SubjectKeyId: []byte{1, 2, 3, 4}}
	template := &x509.Certificate{SerialNumber: big.NewInt(2), Subject: pkix.Name{CommonName: "canopy probe"},
		NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(time.Hour),
		IssuingCertificateURL: []string{issuerURL}, KeyUsage: x509.KeyUsageDigitalSignature,
		ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}, DNSNames: []string{"probe.invalid"}}
	der, err := x509.CreateCertificate(rand.Reader, template, issuer, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}), 0o600); err != nil {
		t.Fatal(err)
	}
}
