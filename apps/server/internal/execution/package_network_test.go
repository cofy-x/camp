package execution

import (
	"bufio"
	"bytes"
	"context"
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"net/url"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestPackageNetworkRejectsPrivateReservedAndTranslatedAddresses(t *testing.T) {
	for _, name := range []string{"0.0.0.1", "10.0.0.1", "100.64.0.1", "127.0.0.1", "169.254.169.254", "172.16.1.1", "192.168.1.1", "192.0.0.1", "192.0.2.1", "198.18.1.1", "198.51.100.1", "203.0.113.1", "240.0.0.1", "::", "::1", "::ffff:127.0.0.1", "fc00::1", "fe80::1", "ff02::1", "64:ff9b::a00:1", "2001:db8::1", "2002:a00:1::1"} {
		if publicPackageAddress(netip.MustParseAddr(name)) {
			t.Fatal("private/reserved package destination accepted", name)
		}
	}
	for _, name := range []string{"1.1.1.1", "8.8.8.8", "2606:4700:4700::1111"} {
		if !publicPackageAddress(netip.MustParseAddr(name)) {
			t.Fatal("public package destination refused", name)
		}
	}
}

func TestPackageNetworkPinsDNSAddressAndPreservesVerifiedTLS(t *testing.T) {
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Proxy-Authorization") != "" {
			t.Error("proxy capability reached package server")
		}
		_, _ = io.WriteString(w, "verified package bytes")
	}))
	defer server.Close()
	var lookups, dials atomic.Int32
	lookup := func(_ context.Context, network, host string) ([]netip.Addr, error) {
		lookups.Add(1)
		if network != "ip" || host != "registry.example" {
			t.Error("unexpected DNS destination", network, host)
		}
		return []netip.Addr{netip.MustParseAddr("1.1.1.1")}, nil
	}
	dial := func(ctx context.Context, network, address string) (net.Conn, error) {
		dials.Add(1)
		if address != "1.1.1.1:443" {
			t.Error("checked numeric address was not used", address)
		}
		return (&net.Dialer{}).DialContext(ctx, network, server.Listener.Addr().String())
	}
	proxy, err := openPackageNetwork(context.Background(), lookup, dial)
	if err != nil {
		t.Fatal(err)
	}
	defer proxy.Close()
	proxyURL, _ := url.Parse(proxy.URL())
	roots := x509.NewCertPool()
	roots.AddCert(server.Certificate())
	serverName := server.Certificate().IPAddresses[0].String()
	transport := &http.Transport{Proxy: http.ProxyURL(proxyURL), TLSClientConfig: &tls.Config{RootCAs: roots, ServerName: serverName, MinVersion: tls.VersionTLS12}}
	defer transport.CloseIdleConnections()
	client := &http.Client{Transport: transport, Timeout: 5 * time.Second}
	response, err := client.Get("https://registry.example/package.tgz")
	if err != nil {
		t.Fatal(err)
	}
	data, err := io.ReadAll(response.Body)
	_ = response.Body.Close()
	if err != nil || string(data) != "verified package bytes" || lookups.Load() != 1 || dials.Load() != 1 {
		t.Fatal("package tunnel lost TLS bytes or repeated DNS", string(data), err, lookups.Load(), dials.Load())
	}
	if strings.Contains(proxy.Redact(proxy.URL()+" "+proxy.authorization), proxy.token) {
		t.Fatal("package capability was not redacted")
	}
}

func TestPackageNetworkDeniesUnauthenticatedAndReboundRequests(t *testing.T) {
	var dials atomic.Int32
	lookup := func(context.Context, string, string) ([]netip.Addr, error) {
		return []netip.Addr{netip.MustParseAddr("1.1.1.1"), netip.MustParseAddr("10.0.0.1")}, nil
	}
	dial := func(context.Context, string, string) (net.Conn, error) {
		dials.Add(1)
		return nil, fmt.Errorf("must not dial")
	}
	proxy, err := openPackageNetwork(context.Background(), lookup, dial)
	if err != nil {
		t.Fatal(err)
	}
	defer proxy.Close()
	for _, authority := range []string{"registry.example:443", "127.0.0.1:443", "localhost:443", "registry.example:80", "user@registry.example:443", "[fe80::1%en0]:443"} {
		if connection, err := proxy.connect(context.Background(), authority); err == nil {
			_ = connection.Close()
			t.Fatal("unsafe package destination admitted", authority)
		}
	}
	proxyURL, _ := url.Parse(proxy.Origin())
	transport := &http.Transport{Proxy: http.ProxyURL(proxyURL)}
	defer transport.CloseIdleConnections()
	client := &http.Client{Transport: transport, Timeout: 2 * time.Second}
	if response, err := client.Get("https://registry.example/package"); err == nil {
		_ = response.Body.Close()
		t.Fatal("unauthenticated package tunnel admitted")
	}
	if dials.Load() != 0 {
		t.Fatal("unsafe/unauthenticated request reached the network", dials.Load())
	}
}

func TestPackageNetworkCloseStopsOwnedTunnelAndTransferIsBounded(t *testing.T) {
	listener, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	upstreamDone := make(chan struct{})
	go func() {
		defer close(upstreamDone)
		c, err := listener.Accept()
		if err == nil {
			defer c.Close()
			_, _ = io.Copy(io.Discard, c)
		}
	}()
	proxy, err := openPackageNetwork(context.Background(), func(context.Context, string, string) ([]netip.Addr, error) {
		return []netip.Addr{netip.MustParseAddr("1.1.1.1")}, nil
	}, func(ctx context.Context, network, _ string) (net.Conn, error) {
		return (&net.Dialer{}).DialContext(ctx, network, listener.Addr().String())
	})
	if err != nil {
		t.Fatal(err)
	}
	defer proxy.Close()
	client, err := net.Dial("tcp", proxy.listener.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	_, err = fmt.Fprintf(client, "CONNECT registry.example:443 HTTP/1.1\r\nHost: registry.example:443\r\nProxy-Authorization: %s\r\n\r\n", proxy.authorization)
	if err != nil {
		t.Fatal(err)
	}
	response, err := http.ReadResponse(bufio.NewReader(client), &http.Request{Method: http.MethodConnect})
	if err != nil || response.StatusCode != 200 {
		t.Fatal("owned tunnel failed", err, response)
	}
	closed := make(chan struct{})
	go func() { _ = proxy.Close(); close(closed) }()
	select {
	case <-closed:
	case <-time.After(2 * time.Second):
		t.Fatal("package tunnel outlived owner close")
	}
	select {
	case <-upstreamDone:
	case <-time.After(2 * time.Second):
		t.Fatal("upstream tunnel survived close")
	}
	var destination bytes.Buffer
	proxy.remaining.Store(3)
	n, err := (packageTransfer{proxy, &destination}).Write([]byte("abcdef"))
	if n != 3 || err == nil || destination.String() != "abc" {
		t.Fatal("package transfer exceeded its budget", n, err, destination.String())
	}
}
