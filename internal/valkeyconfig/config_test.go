package valkeyconfig

import (
	"crypto/tls"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestConfigurationBoundaries(t *testing.T) {
	base := Config{Address: "localhost:6379"}
	for name, edit := range map[string]func(*Config){
		"url":              func(c *Config) { c.Address = "rediss://user:secret@localhost:6379" },
		"query":            func(c *Config) { c.Address = "localhost:6379?secret" },
		"empty-host":       func(c *Config) { c.Address = ":6379" },
		"bad-port":         func(c *Config) { c.Address = "localhost:70000" },
		"whitespace":       func(c *Config) { c.Address = "local host:6379" },
		"orphan-ca":        func(c *Config) { c.CAFile = "secret" },
		"missing-password": func(c *Config) { c.Username = "core" },
		"both-passwords":   func(c *Config) { c.Password = "secret"; c.PasswordFile = "secret" },
		"missing-file":     func(c *Config) { c.PasswordFile = "secret" },
		"invalid-ca":       func(c *Config) { c.TLS = true; c.CAFile = "secret" },
		"missing-key":      func(c *Config) { c.TLS = true; c.CertFile = "secret" },
		"missing-cert":     func(c *Config) { c.TLS = true; c.KeyFile = "secret" },
	} {
		t.Run(name, func(t *testing.T) {
			c := base
			edit(&c)
			_, err := c.Options()
			if err == nil || strings.Contains(err.Error(), "secret") {
				t.Fatal("unsafe validation or error disclosure")
			}
		})
	}
	file := filepath.Join(t.TempDir(), "password")
	for _, value := range []string{"value\n", "value\r\n", "value"} {
		if err := os.WriteFile(file, []byte(value), 0600); err != nil {
			t.Fatal(err)
		}
		c := base
		c.Username = "core"
		c.PasswordFile = file
		c.TLS = true
		options, err := c.Options()
		if err != nil || options.Password != "value" || options.TLSConfig.MinVersion != tls.VersionTLS12 || options.TLSConfig.InsecureSkipVerify || !options.DisableCache {
			t.Fatal("incorrect secure options", err)
		}
	}
	for _, value := range []string{"", "\n", "first\nsecond", strings.Repeat("x", 4097)} {
		if err := os.WriteFile(file, []byte(value), 0600); err != nil {
			t.Fatal(err)
		}
		c := base
		c.PasswordFile = file
		if _, err := c.Options(); err == nil {
			t.Fatal("invalid password file accepted")
		}
	}
}

func TestProductionTransportPolicy(t *testing.T) {
	for _, key := range []string{"ADDRESS", "USERNAME", "PASSWORD", "PASSWORD_FILE", "TLS", "TLS_CA_FILE", "TLS_SERVER_NAME", "TLS_CERT_FILE", "TLS_KEY_FILE"} {
		t.Setenv("AUTHOVA_VALKEY_"+key, "")
	}
	t.Setenv("AUTHOVA_VALKEY_ADDRESS", "localhost:6379")
	t.Setenv("AUTHOVA_VALKEY_USERNAME", "core")
	t.Setenv("AUTHOVA_VALKEY_PASSWORD", "private")
	c, err := FromEnv(false)
	if err != nil || !c.TLS {
		t.Fatal("TLS must default on in production", err)
	}
	t.Setenv("AUTHOVA_VALKEY_TLS", "false")
	if _, err = FromEnv(false); err == nil {
		t.Fatal("production plaintext accepted")
	}
	t.Setenv("AUTHOVA_VALKEY_TLS", "invalid")
	if _, err = FromEnv(true); err == nil {
		t.Fatal("invalid TLS flag accepted")
	}
	t.Setenv("AUTHOVA_VALKEY_TLS", "")
	t.Setenv("AUTHOVA_VALKEY_USERNAME", "")
	if _, err = FromEnv(false); err == nil {
		t.Fatal("unnamed production ACL accepted")
	}
	t.Setenv("AUTHOVA_VALKEY_ADDRESS", "")
	t.Setenv("AUTHOVA_VALKEY_PASSWORD", "")
	if _, err = FromEnv(false); err != nil {
		t.Fatal("maintenance commands require no Valkey", err)
	}
}
