// Package egress is the one way out of the sandbox when the network is limited: an HTTP proxy on
// the loopback address that forwards to the hosts on an allow list and refuses everything else.
// The sandbox lets a command reach only this proxy's port, so a command that ignores the proxy
// variables reaches nothing at all.
package egress

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"
)

// Registries are the hosts package managers and module proxies fetch from, the allow list for
// `registries` mode: enough to build and test, not enough to post a file anywhere a person could
// read it.
var Registries = []string{
	"proxy.golang.org", "sum.golang.org", "storage.googleapis.com", "golang.org", "go.dev",
	"registry.npmjs.org", "registry.yarnpkg.com", "repo.yarnpkg.com",
	"pypi.org", "files.pythonhosted.org",
	"crates.io", "static.crates.io", "index.crates.io",
	"rubygems.org", "index.rubygems.org",
	"repo.maven.apache.org", "repo1.maven.org", "plugins.gradle.org", "services.gradle.org",
	"api.nuget.org", "pub.dev", "hex.pm", "repo.packagist.org",
	"github.com", "codeload.github.com", "objects.githubusercontent.com", "raw.githubusercontent.com",
}

// Proxy is a running proxy.
type Proxy struct {
	listener net.Listener
	allow    []string
	// Refused is told of every host turned away, so it can be audited and shown.
	refused func(host string)
	dial    func(ctx context.Context, network, address string) (net.Conn, error)
	wg      sync.WaitGroup

	mu      sync.Mutex
	refusal []refusal
	// conns are the connections open now, closed with the proxy so no tunnel outlives it.
	conns  map[net.Conn]struct{}
	closed bool
}

// idleTimeout ends a tunnel nobody has sent anything through for this long.
const idleTimeout = 5 * time.Minute

type refusal struct {
	host string
	at   time.Time
}

// Start listens on a loopback port and forwards to the hosts allowed, a host matching a name
// exactly or, for an entry "*.example.com", any subdomain of it.
func Start(allow []string, refused func(host string)) (*Proxy, error) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return nil, err
	}
	dialer := &net.Dialer{Timeout: 30 * time.Second}
	p := &Proxy{listener: listener, allow: allow, refused: refused, dial: dialer.DialContext,
		conns: map[net.Conn]struct{}{}}
	p.wg.Add(1)
	go p.serve()
	return p, nil
}

// Port is where the proxy listens on 127.0.0.1.
func (p *Proxy) Port() int { return p.listener.Addr().(*net.TCPAddr).Port }

// Env is what a command needs to use the proxy.
func (p *Proxy) Env() []string {
	url := fmt.Sprintf("http://127.0.0.1:%d", p.Port())
	return []string{"HTTP_PROXY=" + url, "HTTPS_PROXY=" + url, "ALL_PROXY=" + url,
		"http_proxy=" + url, "https_proxy=" + url, "all_proxy=" + url, "NO_PROXY=", "no_proxy="}
}

// RefusedSince lists the hosts turned away since a moment, each once, so a command's result can say
// what it was not allowed to reach.
func (p *Proxy) RefusedSince(since time.Time) []string {
	p.mu.Lock()
	defer p.mu.Unlock()
	seen := map[string]bool{}
	var hosts []string
	for _, r := range p.refusal {
		if !r.at.Before(since) && !seen[r.host] {
			seen[r.host] = true
			hosts = append(hosts, r.host)
		}
	}
	return hosts
}

// Close stops the proxy.
func (p *Proxy) Close() {
	_ = p.listener.Close()
	p.mu.Lock()
	p.closed = true
	for c := range p.conns {
		_ = c.Close()
	}
	p.mu.Unlock()
	p.wg.Wait()
}

// track registers a connection to be closed with the proxy, or reports that it is already closed.
func (p *Proxy) track(c net.Conn) bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.closed {
		return false
	}
	p.conns[c] = struct{}{}
	return true
}

func (p *Proxy) untrack(c net.Conn) {
	p.mu.Lock()
	delete(p.conns, c)
	p.mu.Unlock()
}

// Allowed reports whether host may be reached.
func (p *Proxy) Allowed(host string) bool {
	host = strings.ToLower(strings.TrimSuffix(host, "."))
	for _, entry := range p.allow {
		entry = strings.ToLower(entry)
		if suffix, ok := strings.CutPrefix(entry, "*."); ok {
			if strings.HasSuffix(host, "."+suffix) {
				return true
			}
			continue
		}
		if host == entry {
			return true
		}
	}
	return false
}

func (p *Proxy) serve() {
	defer p.wg.Done()
	for {
		conn, err := p.listener.Accept()
		if err != nil {
			return
		}
		p.wg.Add(1)
		go func() {
			defer p.wg.Done()
			p.handle(conn)
		}()
	}
}

func (p *Proxy) handle(client net.Conn) {
	defer func() { _ = client.Close() }()
	if !p.track(client) {
		return
	}
	defer p.untrack(client)
	_ = client.SetReadDeadline(time.Now().Add(30 * time.Second))
	reader := bufio.NewReader(client)
	req, err := http.ReadRequest(reader)
	if err != nil {
		return
	}
	_ = client.SetReadDeadline(time.Time{})

	target := req.Host
	if req.Method != http.MethodConnect && req.URL.Host != "" {
		target = req.URL.Host
	}
	host, port, err := net.SplitHostPort(target)
	if err != nil {
		host, port = target, "80"
		if req.Method == http.MethodConnect {
			port = "443"
		}
	}
	// Web ports only: a registry is reached on 443, or 80, and an allowed host's other ports are
	// services nobody asked to expose to a build.
	if port != "443" && port != "80" {
		host = net.JoinHostPort(host, port)
	}
	if port != "443" && port != "80" || !p.Allowed(host) {
		p.mu.Lock()
		p.refusal = append(p.refusal, refusal{host: host, at: time.Now()})
		if len(p.refusal) > 256 {
			p.refusal = p.refusal[len(p.refusal)-256:]
		}
		p.mu.Unlock()
		if p.refused != nil {
			p.refused(host)
		}
		body := fmt.Sprintf("Canopy's sandbox does not allow %s: only the hosts on its network allow list "+
			"can be reached (CANOPY_SANDBOX_NETWORK).\n", host)
		_, _ = fmt.Fprintf(client, "HTTP/1.1 403 Forbidden\r\nContent-Type: text/plain\r\nContent-Length: %d\r\nConnection: close\r\n\r\n%s",
			len(body), body)
		return
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	upstream, err := p.dial(ctx, "tcp", net.JoinHostPort(host, port))
	cancel()
	if err != nil {
		_, _ = fmt.Fprintf(client, "HTTP/1.1 502 Bad Gateway\r\nConnection: close\r\nContent-Length: 0\r\n\r\n")
		return
	}
	defer func() { _ = upstream.Close() }()
	if !p.track(upstream) {
		return
	}
	defer p.untrack(upstream)

	if req.Method == http.MethodConnect {
		if _, err := io.WriteString(client, "HTTP/1.1 200 Connection Established\r\n\r\n"); err != nil {
			return
		}
		// Anything the client sent after its CONNECT line is already buffered.
		if n := reader.Buffered(); n > 0 {
			buffered, _ := reader.Peek(n)
			if _, err := upstream.Write(buffered); err != nil {
				return
			}
		}
		splice(client, upstream)
		return
	}

	// A plain request in absolute form: sent on as an origin-form request over the new connection.
	req.RequestURI = ""
	req.Header.Del("Proxy-Connection")
	req.Header.Del("Proxy-Authorization")
	req.Header.Set("Connection", "close")
	if err := req.Write(upstream); err != nil {
		return
	}
	_, _ = io.Copy(client, upstream)
}

// splice copies both ways until either side closes, or nothing has moved for idleTimeout.
func splice(a, b net.Conn) {
	done := make(chan struct{}, 2)
	copyTo := func(dst, src net.Conn) {
		buf := make([]byte, 32*1024)
		for {
			_ = src.SetReadDeadline(time.Now().Add(idleTimeout))
			n, err := src.Read(buf)
			if n > 0 {
				if _, werr := dst.Write(buf[:n]); werr != nil {
					break
				}
			}
			if err != nil {
				break
			}
		}
		if tcp, ok := dst.(interface{ CloseWrite() error }); ok {
			_ = tcp.CloseWrite()
		}
		done <- struct{}{}
	}
	go copyTo(a, b)
	go copyTo(b, a)
	<-done
	// One side has finished; the other gets as long as it takes to finish too, up to the timeout.
	<-done
}

// ErrUnknownMode is a CANOPY_SANDBOX_NETWORK value that is not one of the three.
var ErrUnknownMode = errors.New("CANOPY_SANDBOX_NETWORK is open, registries or off")

// Mode is how far a sandboxed command may reach the network.
type Mode string

const (
	// ModeOpen leaves the network alone, the default.
	ModeOpen Mode = "open"
	// ModeRegistries allows package registries and the hosts added, through the proxy.
	ModeRegistries Mode = "registries"
	// ModeOff allows no connections at all.
	ModeOff Mode = "off"
)

// ModeEnvVar chooses the mode; AllowEnvVar adds hosts, comma separated, to the registries list.
const (
	ModeEnvVar  = "CANOPY_SANDBOX_NETWORK"
	AllowEnvVar = "CANOPY_SANDBOX_ALLOW"
)

// ParseMode reads a mode, the empty string meaning open.
func ParseMode(value string) (Mode, error) {
	switch Mode(strings.ToLower(strings.TrimSpace(value))) {
	case "", ModeOpen:
		return ModeOpen, nil
	case ModeRegistries:
		return ModeRegistries, nil
	case ModeOff, "none":
		return ModeOff, nil
	}
	return "", ErrUnknownMode
}

var (
	sharedOnce  sync.Once
	sharedProxy *Proxy
	sharedErr   error
)

// Shared is the one proxy for this process, started the first time it is needed, allowing the
// registries and the hosts in extra.
func Shared(extra []string) (*Proxy, error) {
	sharedOnce.Do(func() {
		allow := append(append([]string(nil), Registries...), extra...)
		sharedProxy, sharedErr = Start(allow, nil)
	})
	return sharedProxy, sharedErr
}

// ExtraHosts reads the hosts added with CANOPY_SANDBOX_ALLOW.
func ExtraHosts(value string) []string {
	var hosts []string
	for _, h := range strings.Split(value, ",") {
		if h = strings.TrimSpace(h); h != "" {
			hosts = append(hosts, h)
		}
	}
	return hosts
}
