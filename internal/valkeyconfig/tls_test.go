package valkeyconfig

import (
	"context"
	"encoding/json"
	"net"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/valkey-io/valkey-go"
)

func TestValkeyTLSACL(t *testing.T) {
	file := os.Getenv("AUTHOVA_TEST_VALKEY_TLS_CONFIG")
	if file == "" {
		t.Skip("isolated TLS fixture required; run scripts/valkey-tls-gates.py")
	}
	data, err := os.ReadFile(file)
	if err != nil {
		t.Fatal("cannot read TLS fixture")
	}
	var configs map[string]Config
	if json.Unmarshal(data, &configs) != nil {
		t.Fatal("invalid TLS fixture")
	}
	role := os.Getenv("AUTHOVA_TEST_VALKEY_ROLE")
	if role != "core" && role != "console" {
		t.Fatal("explicit TLS ACL role required")
	}
	c := configs[role]
	client := func(c Config) (valkey.Client, error) {
		options, e := c.Options()
		if e != nil {
			return nil, e
		}
		options.Dialer = net.Dialer{Timeout: time.Second}
		options.ConnWriteTimeout = time.Second
		return valkey.NewClient(options)
	}
	good, err := client(c)
	if err != nil {
		t.Fatal("valid TLS ACL connection failed")
	}
	defer good.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	own, other := "authova:limit:acl-test", "authova:console:acl-test"
	if role == "console" {
		own, other = other, own
	}
	forbiddenCommand := "GET"
	command := "INCR"
	if role == "console" {
		command = "GET"
		forbiddenCommand = "INCR"
	}
	if err = good.Do(ctx, good.B().Arbitrary("PING").Build()).Error(); err != nil {
		t.Fatal("allowed TLS command failed")
	}
	for name, args := range map[string][]string{
		"cross-prefix": {command, other}, "flush": {"FLUSHDB"}, "config": {"CONFIG", "GET", "port"}, "acl": {"ACL", "WHOAMI"},
		"lua-key-escape":     {"EVAL", "return redis.call('" + command + "',ARGV[1])", "1", own, other},
		"lua-command-escape": {"EVAL", "return redis.call('" + forbiddenCommand + "',KEYS[1])", "1", own},
	} {
		t.Run(name, func(t *testing.T) {
			err := good.Do(ctx, good.B().Arbitrary(args[0]).Args(args[1:]...).Build()).Error()
			if err == nil || !(strings.Contains(err.Error(), "NOPERM") || strings.Contains(err.Error(), "permission")) {
				t.Fatal("ACL boundary did not reject command")
			}
		})
	}
	for name, edit := range map[string]func(*Config){
		"wrong-ca":            func(c *Config) { c.CAFile = configs["untrusted"].CAFile },
		"system-roots":        func(c *Config) { c.CAFile = "" },
		"wrong-san":           func(c *Config) { c.ServerName = "wrong.example.invalid" },
		"wrong-password":      func(c *Config) { c.PasswordFile = ""; c.Password = "incorrect" },
		"missing-client-cert": func(c *Config) { c.CertFile = ""; c.KeyFile = "" },
		"untrusted-client":    func(c *Config) { c.CertFile = configs["untrusted"].CertFile; c.KeyFile = configs["untrusted"].KeyFile },
		"default-user-off":    func(c *Config) { c.Username = ""; c.Password = ""; c.PasswordFile = "" },
		"plaintext":           func(c *Config) { c.TLS = false; c.CAFile = ""; c.ServerName = ""; c.CertFile = ""; c.KeyFile = "" },
	} {
		t.Run(name, func(t *testing.T) {
			bad := c
			edit(&bad)
			connection, e := client(bad)
			if connection != nil {
				defer connection.Close()
			}
			if e == nil {
				probe, done := context.WithTimeout(context.Background(), time.Second)
				defer done()
				e = connection.Do(probe, connection.B().Ping().Build()).Error()
			}
			if e == nil {
				t.Fatal("invalid transport or authentication unexpectedly succeeded")
			}
		})
	}
}
