package execution

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"io"
	"log"
	"net"
	"net/http"
	"net/netip"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

// PackageNetwork is a per-install CONNECT capability for the trusted package
// manager. It neither intercepts TLS nor exposes a general host network grant.
type PackageNetwork struct {
	ctx                  context.Context
	cancel               context.CancelFunc
	server               *http.Server
	listener             net.Listener
	lookup               func(context.Context, string, string) ([]netip.Addr, error)
	dial                 func(context.Context, string, string) (net.Conn, error)
	token, authorization string
	remaining            atomic.Int64
	slots                chan struct{}
	mu                   sync.Mutex
	closed               bool
	connections          map[net.Conn]bool
	workers              sync.WaitGroup
	closeOnce            sync.Once
}

func OpenPackageNetwork(ctx context.Context) (*PackageNetwork, error) {
	return openPackageNetwork(ctx, net.DefaultResolver.LookupNetIP, (&net.Dialer{Timeout: 10 * time.Second}).DialContext)
}

func openPackageNetwork(ctx context.Context, lookup func(context.Context, string, string) ([]netip.Addr, error), dial func(context.Context, string, string) (net.Conn, error)) (*PackageNetwork, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	var secret [32]byte
	if _, err := rand.Read(secret[:]); err != nil {
		return nil, err
	}
	listener, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		return nil, err
	}
	ctx, cancel := context.WithCancel(ctx)
	p := &PackageNetwork{ctx: ctx, cancel: cancel, listener: listener, token: hex.EncodeToString(secret[:]), slots: make(chan struct{}, 8), connections: map[net.Conn]bool{}, lookup: lookup, dial: dial}
	p.authorization = "Basic " + base64.StdEncoding.EncodeToString([]byte("camp:"+p.token))
	p.remaining.Store(2 << 30)
	p.server = &http.Server{Handler: http.HandlerFunc(p.serve), ReadHeaderTimeout: 5 * time.Second, IdleTimeout: 10 * time.Second, MaxHeaderBytes: 8 << 10, ErrorLog: log.New(io.Discard, "", 0)}
	go func() { _ = p.server.Serve(listener) }()
	go func() { <-ctx.Done(); _ = p.Close() }()
	return p, nil
}

func (p *PackageNetwork) Origin() string { return "http://" + p.listener.Addr().String() }
func (p *PackageNetwork) URL() string {
	return "http://camp:" + p.token + "@" + p.listener.Addr().String()
}
func (p *PackageNetwork) Redact(text string) string {
	text = strings.ReplaceAll(text, p.authorization, "[redacted]")
	text = strings.ReplaceAll(text, strings.TrimPrefix(p.authorization, "Basic "), "[redacted]")
	return strings.ReplaceAll(text, p.token, "[redacted]")
}

var nonPublicPackageNetworks = []netip.Prefix{
	netip.MustParsePrefix("0.0.0.0/8"), netip.MustParsePrefix("100.64.0.0/10"), netip.MustParsePrefix("192.0.0.0/24"), netip.MustParsePrefix("192.0.2.0/24"), netip.MustParsePrefix("192.88.99.0/24"), netip.MustParsePrefix("198.18.0.0/15"), netip.MustParsePrefix("198.51.100.0/24"), netip.MustParsePrefix("203.0.113.0/24"), netip.MustParsePrefix("240.0.0.0/4"),
	netip.MustParsePrefix("2001::/23"), netip.MustParsePrefix("2001:db8::/32"), netip.MustParsePrefix("2002::/16"),
}

func publicPackageAddress(address netip.Addr) bool {
	address = address.Unmap()
	if !address.IsValid() || address.Zone() != "" || !address.IsGlobalUnicast() || address.IsPrivate() {
		return false
	}
	// Restrict IPv6 to globally allocated unicast space; translation/tunneling
	// prefixes must not turn an apparently public address into private IPv4.
	if address.Is6() && !netip.MustParsePrefix("2000::/3").Contains(address) {
		return false
	}
	for _, prefix := range nonPublicPackageNetworks {
		if prefix.Contains(address) {
			return false
		}
	}
	return true
}

func (p *PackageNetwork) connect(ctx context.Context, authority string) (net.Conn, error) {
	host, port, err := net.SplitHostPort(authority)
	if err != nil || port != "443" || len(host) == 0 || len(host) > 253 || strings.ContainsAny(host, "/%\\@?#\x00\r\n") {
		return nil, errors.New("Package network requires a public HTTPS destination")
	}
	host = strings.ToLower(strings.TrimSuffix(host, "."))
	if host == "localhost" || strings.HasSuffix(host, ".localhost") || strings.HasSuffix(host, ".local") || strings.HasSuffix(host, ".internal") {
		return nil, errors.New("Private package destination is denied")
	}
	resolve, stop := context.WithTimeout(ctx, 5*time.Second)
	addresses, err := p.lookup(resolve, "ip", host)
	stop()
	if err != nil || len(addresses) == 0 || len(addresses) > 64 {
		return nil, errors.New("Package destination could not be resolved")
	}
	for _, address := range addresses {
		if !publicPackageAddress(address) {
			return nil, errors.New("Private package destination is denied")
		}
	}
	// Dial the checked numeric address. A second DNS resolution would re-open
	// the private-network boundary to rebinding between validation and connect.
	for _, address := range addresses {
		connection, err := p.dial(ctx, "tcp", net.JoinHostPort(address.Unmap().String(), port))
		if err == nil {
			return connection, nil
		}
	}
	return nil, errors.New("Package destination could not be connected")
}

func (p *PackageNetwork) serve(w http.ResponseWriter, r *http.Request) {
	p.mu.Lock()
	if p.closed {
		p.mu.Unlock()
		http.Error(w, "Package network is closed", http.StatusServiceUnavailable)
		return
	}
	p.workers.Add(1)
	p.mu.Unlock()
	defer p.workers.Done()
	if r.Method != http.MethodConnect || r.RequestURI != r.Host || r.Header.Get("Origin") != "" || r.ContentLength > 0 || len(r.TransferEncoding) != 0 || subtle.ConstantTimeCompare([]byte(r.Header.Get("Proxy-Authorization")), []byte(p.authorization)) != 1 {
		http.Error(w, "Package network capability is unavailable", http.StatusForbidden)
		return
	}
	if p.remaining.Load() <= 0 {
		http.Error(w, "Package transfer exceeds its limit", http.StatusRequestEntityTooLarge)
		return
	}
	ctx, cancel := context.WithCancel(p.ctx)
	stop := context.AfterFunc(r.Context(), cancel)
	defer func() { stop(); cancel() }()
	select {
	case p.slots <- struct{}{}:
		defer func() { <-p.slots }()
	case <-ctx.Done():
		return
	}
	upstream, err := p.connect(ctx, r.Host)
	if err != nil {
		http.Error(w, err.Error(), http.StatusForbidden)
		return
	}
	defer upstream.Close()
	client, buffer, err := http.NewResponseController(w).Hijack()
	if err != nil {
		return
	}
	defer client.Close()
	p.mu.Lock()
	if p.closed {
		p.mu.Unlock()
		return
	}
	p.connections[client], p.connections[upstream] = true, true
	p.mu.Unlock()
	defer func() { p.mu.Lock(); delete(p.connections, client); delete(p.connections, upstream); p.mu.Unlock() }()
	deadline := time.Now().Add(180 * time.Second)
	_ = client.SetDeadline(deadline)
	_ = upstream.SetDeadline(deadline)
	if _, err := buffer.WriteString("HTTP/1.1 200 Connection Established\r\n\r\n"); err != nil {
		return
	}
	if err := buffer.Flush(); err != nil {
		return
	}
	done := make(chan struct{}, 2)
	go func() { _, _ = io.Copy(packageTransfer{p, upstream}, buffer.Reader); done <- struct{}{} }()
	go func() { _, _ = io.Copy(packageTransfer{p, client}, upstream); done <- struct{}{} }()
	completed := 0
	select {
	case <-done:
		completed = 1
	case <-ctx.Done():
	}
	_ = client.Close()
	_ = upstream.Close()
	for ; completed < 2; completed++ {
		<-done
	}
}

type packageTransfer struct {
	network     *PackageNetwork
	destination io.Writer
}

func (w packageTransfer) Write(data []byte) (int, error) {
	before := w.network.remaining.Add(-int64(len(data))) + int64(len(data))
	if before <= 0 {
		return 0, errors.New("Package transfer exceeds its limit")
	}
	if before < int64(len(data)) {
		n, err := w.destination.Write(data[:int(before)])
		return n, errors.Join(err, errors.New("Package transfer exceeds its limit"))
	}
	return w.destination.Write(data)
}

func (p *PackageNetwork) Close() error {
	p.closeOnce.Do(func() {
		p.mu.Lock()
		p.closed = true
		p.cancel()
		connections := make([]net.Conn, 0, len(p.connections))
		for connection := range p.connections {
			connections = append(connections, connection)
		}
		p.mu.Unlock()
		_ = p.server.Close()
		for _, connection := range connections {
			_ = connection.Close()
		}
		p.workers.Wait()
	})
	return nil
}
