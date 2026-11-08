// Package config validates deployment settings before opening runtime listeners.
package config

import (
	"encoding/base64"
	"errors"
	"fmt"
	"github.com/carvebloom/authova/internal/valkeyconfig"
	"math"
	"net/url"
	"os"
	"strconv"
	"strings"
)

type Config struct {
	TraceEndpoint         string
	TraceSampleRatio      float64
	WorkerMetricsListen   string
	Mode                  string
	Listen                string
	BaseURL               string
	ConsoleOrigin         string
	Dialect               string
	DSN                   string
	MigrationDSN          string
	Valkey                valkeyconfig.Config
	SMTPAddress           string
	SMTPUsername          string
	SMTPPassword          string
	SMTPFrom              string
	SMSURL                string
	SMSToken              string
	EncryptionKeyringFile string
	MasterKey             []byte
	Connections           int
	KDFConcurrency        int
	HTTPConcurrency       int
	Development           bool
}

func env(key, fallback string) string {
	if v := os.Getenv("AUTHOVA_" + key); v != "" {
		return v
	}
	return fallback
}
func Load() (Config, error) {
	c := Config{
		TraceEndpoint: env("TRACE_OTLP_ENDPOINT", ""), TraceSampleRatio: .01, WorkerMetricsListen: env("WORKER_METRICS_LISTEN", ""),
		EncryptionKeyringFile: env("ENCRYPTION_KEYRING_FILE", ""),
		Mode:                  env("MODE", "production"),
		Listen:                env("LISTEN", ":8080"),
		BaseURL:               strings.TrimRight(env("BASE_URL", ""), "/"),
		ConsoleOrigin:         env("CONSOLE_URL", "http://127.0.0.1:18090"),
		Dialect:               env("DATABASE_DRIVER", "mysql"),
		DSN:                   env("DATABASE_URL", ""),
		MigrationDSN:          env("MIGRATION_DATABASE_URL", ""),
		SMTPAddress:           env("SMTP_ADDRESS", ""),
		SMTPUsername:          env("SMTP_USERNAME", ""),
		SMTPPassword:          env("SMTP_PASSWORD", ""),
		SMTPFrom:              env("SMTP_FROM", "authova@example.invalid"),
		SMSURL:                env("SMS_URL", ""),
		SMSToken:              env("SMS_TOKEN", ""),
		Connections:           64,
		KDFConcurrency:        4,
		HTTPConcurrency:       1024,
	}
	if raw := env("TRACE_SAMPLE_RATIO", ""); raw != "" {
		v, err := strconv.ParseFloat(raw, 64)
		if err != nil || math.IsNaN(v) || math.IsInf(v, 0) || v < 0 || v > 1 {
			return c, errors.New("TRACE_SAMPLE_RATIO must be between 0 and 1")
		}
		c.TraceSampleRatio = v
	}
	c.Development = c.Mode == "development"
	if c.Mode != "development" && c.Mode != "production" {
		return c, errors.New("AUTHOVA_MODE must be production or development")
	}
	var e error
	c.Valkey, e = valkeyconfig.FromEnv(c.Development)
	if e != nil {
		return c, e
	}
	u, e := url.Parse(c.BaseURL)
	if e != nil || u.Host == "" || u.Path != "" || u.RawQuery != "" || u.Fragment != "" || u.User != nil || (!c.Development && u.Scheme != "https") || (c.Development && u.Scheme != "https" && u.Scheme != "http") {
		return c, errors.New("AUTHOVA_BASE_URL must be a canonical HTTPS origin (HTTP allowed in development)")
	}
	console, e := url.Parse(c.ConsoleOrigin)
	if e != nil || console.Host == "" || console.Path != "" || console.User != nil || console.RawQuery != "" || console.Fragment != "" || (!c.Development && console.Scheme != "https") || (c.Development && console.Scheme != "https" && console.Scheme != "http") {
		return c, errors.New("AUTHOVA_CONSOLE_URL must be a canonical HTTPS origin")
	}

	if c.SMSURL != "" {
		sms, err := url.Parse(c.SMSURL)
		if err != nil || sms.Host == "" || sms.User != nil || sms.Fragment != "" || sms.RawQuery != "" || (sms.Scheme != "https" && !(c.Development && sms.Scheme == "http" && (sms.Hostname() == "127.0.0.1" || sms.Hostname() == "localhost"))) || c.SMSToken == "" {
			return c, errors.New("AUTHOVA_SMS_URL requires HTTPS (loopback HTTP in development) and AUTHOVA_SMS_TOKEN")
		}
	}
	if c.SMSURL == "" && c.SMSToken != "" {
		return c, errors.New("AUTHOVA_SMS_TOKEN requires AUTHOVA_SMS_URL")
	}

	if c.Dialect != "mysql" && c.Dialect != "postgres" {
		return c, errors.New("AUTHOVA_DATABASE_DRIVER must be mysql or postgres")
	}
	if c.Dialect == "mysql" && (strings.HasPrefix(c.DSN, "postgres://") || strings.HasPrefix(c.DSN, "postgresql://")) {
		return c, errors.New("PostgreSQL DSN requires explicit AUTHOVA_DATABASE_DRIVER=postgres; no engine fallback")
	}
	if c.DSN == "" {
		return c, errors.New("AUTHOVA_DATABASE_URL required")
	}
	if file := env("MASTER_KEY_FILE", ""); file != "" {
		c.MasterKey, e = os.ReadFile(file)
	} else {
		c.MasterKey, e = base64.StdEncoding.DecodeString(env("MASTER_KEY", ""))
	}
	if e != nil || len(c.MasterKey) != 32 {
		return c, errors.New("32-byte master key required; use AUTHOVA_MASTER_KEY_FILE")
	}
	for key, target := range map[string]*int{"DB_CONNECTIONS": &c.Connections, "KDF_CONCURRENCY": &c.KDFConcurrency} {
		if raw := env(key, ""); raw != "" {
			v, e := strconv.Atoi(raw)
			if e != nil || v < 1 || v > 512 {
				return c, fmt.Errorf("invalid AUTHOVA_%s", key)
			}
			*target = v
		}
	}
	if raw := env("HTTP_CONCURRENCY", ""); raw != "" {
		v, err := strconv.Atoi(raw)
		if err != nil || v < 1 || v > 4096 {
			return c, errors.New("invalid AUTHOVA_HTTP_CONCURRENCY")
		}
		c.HTTPConcurrency = v
	}
	return c, nil
}
