package integration

import (
	"context"
	"encoding/json"
	"io"
	"net"
	"net/url"
	"os"
	"strings"
	"sync"
	"testing"

	mysql "github.com/go-sql-driver/mysql"
)

// This affects only test-owned connections, never stops shared database services.
type faultProxy struct {
	listener    net.Listener
	target      string
	mu          sync.Mutex
	failed      bool
	connections []net.Conn
}

func newFaultProxy(t *testing.T, target string) *faultProxy {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	proxy := &faultProxy{listener: listener, target: target}
	t.Cleanup(func() { listener.Close(); proxy.cut(true) })
	go func() {
		for {
			incoming, err := listener.Accept()
			if err != nil {
				return
			}
			go proxy.forward(incoming)
		}
	}()
	return proxy
}
func (p *faultProxy) forward(incoming net.Conn) {
	defer incoming.Close()
	p.mu.Lock()
	failed := p.failed
	p.mu.Unlock()
	if failed {
		return
	}
	outgoing, err := net.Dial("tcp", p.target)
	if err != nil {
		return
	}
	defer outgoing.Close()
	p.mu.Lock()
	if p.failed {
		p.mu.Unlock()
		return
	}
	p.connections = append(p.connections, incoming, outgoing)
	p.mu.Unlock()
	done := make(chan struct{})
	go func() { io.Copy(outgoing, incoming); outgoing.Close(); close(done) }()
	io.Copy(incoming, outgoing)
	incoming.Close()
	<-done
}
func (p *faultProxy) cut(failed bool) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.failed = failed
	if failed {
		for _, connection := range p.connections {
			connection.Close()
		}
		p.connections = nil
	}
}

func TestRealDatabaseNetworkFailure(t *testing.T) {
	configured := false
	for _, driver := range []string{"postgres", "mysql"} {
		dsn := os.Getenv("AUTHOVA_TEST_" + strings.ToUpper(driver) + "_DSN")
		if dsn == "" {
			continue
		}
		configured = true
		t.Run(driver, func(t *testing.T) {
			target := ""
			var rewrite func(string) string
			if driver == "postgres" {
				parsed, err := url.Parse(dsn)
				if err != nil {
					t.Fatal(err)
				}
				target = parsed.Host
				rewrite = func(address string) string { copy := *parsed; copy.Host = address; return copy.String() }
			} else {
				config, err := mysql.ParseDSN(dsn)
				if err != nil {
					t.Fatal(err)
				}
				target = config.Addr
				rewrite = func(address string) string { copy := *config; copy.Addr = address; return copy.FormatDSN() }
			}
			proxy := newFaultProxy(t, target)
			f := setup(t, driver, rewrite(proxy.listener.Addr().String()))
			status, body := f.post(f.app1.Issuer+"/oauth/token", mapForm("grant_type", "client_credentials", "scope", "api.read"), true)
			if status != 200 {
				t.Fatal("token fixture failed")
			}
			var token tokens
			if err := json.Unmarshal(body, &token); err != nil {
				t.Fatal(err)
			}
			if !f.active(token.Access) {
				t.Fatal("prewarmed token inactive")
			}
			proxy.cut(true)
			status, body = f.post(f.app1.Issuer+"/oauth/introspect", mapForm("token", token.Access), true)
			if status != 503 || strings.Contains(string(body), `"active":true`) {
				t.Fatal("network outage produced a reliable authorization decision", status)
			}
			if _, err := f.service.Operator(context.Background(), f.operatorToken, false); err == nil {
				t.Fatal("management authorized during a network outage")
			}
			proxy.cut(false)
			if !f.active(token.Access) {
				t.Fatal("authorization did not recover after network restoration")
			}
		})
	}
	if !configured {
		t.Skip("real database DSNs required")
	}
}
