package secure

import (
	"context"
	"crypto/tls"
	"errors"
	"io"
	"net"
	"net/http"
	"strings"
	"time"
)

// PublicHTTPClient protects administrator-configured upstream identity URLs from SSRF.
// DNS is resolved and checked at each dial; proxy environment variables are ignored.
func PublicHTTPClient(insecureLoopback bool) *http.Client {
	dialer := &net.Dialer{Timeout: 5 * time.Second, KeepAlive: 30 * time.Second}
	transport := &http.Transport{TLSClientConfig: &tls.Config{MinVersion: tls.VersionTLS12}, MaxIdleConns: 100, IdleConnTimeout: 90 * time.Second, TLSHandshakeTimeout: 5 * time.Second, ResponseHeaderTimeout: 8 * time.Second}
	transport.DialContext = func(ctx context.Context, network, address string) (net.Conn, error) {
		host, port, e := net.SplitHostPort(address)
		if e != nil {
			return nil, e
		}
		ips, e := net.DefaultResolver.LookupIPAddr(ctx, host)
		if e != nil {
			return nil, e
		}
		for _, ip := range ips {
			if !PublicIP(ip.IP) && !(insecureLoopback && ip.IP.IsLoopback()) {
				return nil, errors.New("upstream address is not public")
			}
		}
		if len(ips) == 0 {
			return nil, errors.New("upstream has no address")
		}
		return dialer.DialContext(ctx, network, net.JoinHostPort(ips[0].IP.String(), port))
	}
	return &http.Client{Transport: schemeGuard{transport, insecureLoopback}, Timeout: 10 * time.Second,
		CheckRedirect: func(r *http.Request, via []*http.Request) error { return http.ErrUseLastResponse }}
}

type schemeGuard struct {
	next     http.RoundTripper
	insecure bool
}

func (g schemeGuard) RoundTrip(r *http.Request) (*http.Response, error) {
	if r.URL.Scheme != "https" && !(g.insecure && r.URL.Scheme == "http" && (r.URL.Hostname() == "localhost" || r.URL.Hostname() == "127.0.0.1")) {
		return nil, errors.New("upstream requires HTTPS")
	}
	if r.URL.User != nil {
		return nil, errors.New("upstream userinfo forbidden")
	}
	response, err := g.next.RoundTrip(r)
	if err != nil {
		return nil, err
	}
	if response.ContentLength > upstreamBodyLimit {
		response.Body.Close()
		return nil, errors.New("upstream response exceeds limit")
	}
	response.Body = &boundedBody{ReadCloser: response.Body}
	return response, nil
}
func PublicIP(ip net.IP) bool {
	if !ip.IsGlobalUnicast() || ip.IsPrivate() || ip.IsLoopback() || ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() || ip.IsMulticast() || ip.IsUnspecified() {
		return false
	}
	for _, raw := range []string{"0.0.0.0/8", "100.64.0.0/10", "192.0.0.0/24", "192.0.2.0/24", "198.18.0.0/15", "198.51.100.0/24", "203.0.113.0/24", "240.0.0.0/4", "2001:db8::/32", "2001::/23", "2002::/16", "64:ff9b::/96", "64:ff9b:1::/48"} {
		_, n, _ := net.ParseCIDR(raw)
		if n.Contains(ip) {
			return false
		}
	}
	return !strings.Contains(ip.String(), "%")
}

const upstreamBodyLimit int64 = 4 * 1024 * 1024

type boundedBody struct {
	io.ReadCloser
	consumed int64
}

func (b *boundedBody) Read(p []byte) (int, error) {
	if b.consumed > upstreamBodyLimit {
		return 0, errors.New("upstream response exceeds limit")
	}
	remaining := upstreamBodyLimit - b.consumed + 1
	if int64(len(p)) > remaining {
		p = p[:remaining]
	}
	n, err := b.ReadCloser.Read(p)
	b.consumed += int64(n)
	if b.consumed > upstreamBodyLimit {
		return n, errors.New("upstream response exceeds limit")
	}
	return n, err
}
func (g schemeGuard) CloseIdleConnections() {
	if closer, ok := g.next.(interface{ CloseIdleConnections() }); ok {
		closer.CloseIdleConnections()
	}
}
