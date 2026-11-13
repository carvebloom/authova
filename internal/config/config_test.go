package config

import (
	"encoding/base64"
	"testing"
)

func TestSMSConfiguration(t *testing.T) {
	for _, tc := range []struct {
		name, url, token, mode string
		valid                  bool
	}{{"https", "https://sms.example.test/send", "adapter-token", "production", true}, {"missing-token", "https://sms.example.test/send", "", "production", false}, {"orphan-token", "", "token", "production", false}, {"http-production", "http://sms.example.test/send", "token", "production", false}, {"credentials-in-url", "https://user:pass@sms.example.test/send", "token", "production", false}, {"query-secret", "https://sms.example.test/send?key=secret", "token", "production", false}, {"loopback-demo", "http://127.0.0.1:18999/send", "token", "development", true}} {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("AUTHOVA_MODE", tc.mode)
			t.Setenv("AUTHOVA_BASE_URL", "https://authova.example.test")
			t.Setenv("AUTHOVA_CONSOLE_URL", "https://console.example.test")
			t.Setenv("AUTHOVA_DATABASE_URL", "test-only")
			t.Setenv("AUTHOVA_MASTER_KEY_FILE", "")
			t.Setenv("AUTHOVA_MASTER_KEY", base64.StdEncoding.EncodeToString(make([]byte, 32)))
			t.Setenv("AUTHOVA_SMS_URL", tc.url)
			t.Setenv("AUTHOVA_SMS_TOKEN", tc.token)
			_, e := Load()
			if (e == nil) != tc.valid {
				t.Fatal("SMS validation", e)
			}
		})
	}
}

func TestDatabaseDefaultDoesNotFallback(t *testing.T) {
	for _, tc := range []struct {
		driver, dsn, want string
		valid             bool
	}{
		{"", "authova_runtime:fixture@tcp(127.0.0.1:3306)/fixture", "mysql", true},
		{"postgres", "postgres://fixture@localhost/fixture", "postgres", true},
		{"", "postgres://fixture@localhost/fixture", "mysql", false},
		{"mysql", "postgres://fixture@localhost/fixture", "mysql", false},
		{"unknown", "fixture", "unknown", false},
	} {
		t.Run(tc.driver+tc.want, func(t *testing.T) {
			t.Setenv("AUTHOVA_MODE", "development")
			t.Setenv("AUTHOVA_BASE_URL", "http://127.0.0.1:18080")
			t.Setenv("AUTHOVA_CONSOLE_URL", "http://127.0.0.1:18090")
			t.Setenv("AUTHOVA_MASTER_KEY_FILE", "")
			t.Setenv("AUTHOVA_MASTER_KEY", base64.StdEncoding.EncodeToString(make([]byte, 32)))
			t.Setenv("AUTHOVA_DATABASE_DRIVER", tc.driver)
			t.Setenv("AUTHOVA_DATABASE_URL", tc.dsn)
			c, e := Load()
			if c.Dialect != tc.want || (e == nil) != tc.valid {
				t.Fatal("engine selection or fail-closed behavior changed")
			}
		})
	}
}
