// Package valkeyconfig validates transport and ACL credentials before connecting.
// Connections never downgrade from TLS or disable certificate verification.
package valkeyconfig

import (
	"crypto/tls"
	"crypto/x509"
	"errors"
	"io"
	"net"
	"os"
	"strconv"
	"strings"
	"unicode"

	"github.com/valkey-io/valkey-go"
)

type Config struct {
	Address, Username, Password, PasswordFile string
	TLS                                       bool
	CAFile, ServerName, CertFile, KeyFile     string
}

func FromEnv(development bool) (Config, error) {
	get := func(key string) string { return os.Getenv("AUTHOVA_VALKEY_" + key) }
	c := Config{Address: get("ADDRESS"), Username: get("USERNAME"), Password: get("PASSWORD"), PasswordFile: get("PASSWORD_FILE"), TLS: !development, CAFile: get("TLS_CA_FILE"), ServerName: get("TLS_SERVER_NAME"), CertFile: get("TLS_CERT_FILE"), KeyFile: get("TLS_KEY_FILE")}
	if raw := get("TLS"); raw != "" {
		if raw != "true" && raw != "false" {
			return c, errors.New("AUTHOVA_VALKEY_TLS must be true or false")
		}
		c.TLS = raw == "true"
	}
	if !development && !c.TLS {
		return c, errors.New("production Valkey requires TLS")
	}
	if c.Address == "" {
		if c.Username != "" || c.Password != "" || c.PasswordFile != "" || c.CAFile != "" || c.ServerName != "" || c.CertFile != "" || c.KeyFile != "" {
			return c, errors.New("valkey settings require AUTHOVA_VALKEY_ADDRESS")
		}
		return c, nil // Non-server maintenance commands do not require a Valkey connection.
	}
	if !development && c.Username == "" {
		return c, errors.New("production Valkey requires a named ACL user")
	}
	_, err := c.Options()
	return c, err
}

func (c Config) Options() (valkey.ClientOption, error) {
	bad := func(message string) (valkey.ClientOption, error) { return valkey.ClientOption{}, errors.New(message) }
	host, port, err := net.SplitHostPort(c.Address)
	n, portErr := strconv.Atoi(port)
	if err != nil || portErr != nil || n < 1 || n > 65535 || host == "" || strings.ContainsAny(host, "/@?#\\") || strings.IndexFunc(c.Address, unicode.IsSpace) >= 0 || strings.IndexFunc(c.Address, unicode.IsControl) >= 0 {
		return bad("Valkey address must be host:port without credentials or URL components")
	}
	if strings.IndexFunc(c.Username, unicode.IsControl) >= 0 || strings.IndexFunc(c.Username, unicode.IsSpace) >= 0 {
		return bad("invalid Valkey ACL username")
	}
	password := c.Password
	if c.PasswordFile != "" {
		if password != "" {
			return bad("Valkey password and password file are mutually exclusive")
		}
		// Bound reads even for accidentally mounted device/large files.
		f, e := os.Open(c.PasswordFile)
		if e != nil {
			return bad("cannot read Valkey password file")
		}
		data, e := io.ReadAll(io.LimitReader(f, 4097))
		f.Close()
		read := len(data)
		if e != nil || read == 0 || read > 4096 {
			return bad("invalid Valkey password file")
		}
		password = strings.TrimSuffix(strings.TrimSuffix(string(data[:read]), "\n"), "\r")
		if password == "" || strings.ContainsAny(password, "\r\n") {
			return bad("Valkey password file must contain one nonempty value")
		}
	}
	if c.Username != "" && password == "" {
		return bad("named Valkey ACL user requires a password or password file")
	}
	options := valkey.ClientOption{InitAddress: []string{c.Address}, Username: c.Username, Password: password, DisableCache: true}
	if !c.TLS {
		if c.CAFile != "" || c.ServerName != "" || c.CertFile != "" || c.KeyFile != "" {
			return bad("Valkey TLS material requires TLS")
		}
		return options, nil
	}
	config := &tls.Config{MinVersion: tls.VersionTLS12, ServerName: c.ServerName}
	if c.CAFile != "" {
		data, e := os.ReadFile(c.CAFile)
		if e != nil {
			return bad("cannot read Valkey CA file")
		}
		pool := x509.NewCertPool()
		if !pool.AppendCertsFromPEM(data) {
			return bad("invalid Valkey CA file")
		}
		config.RootCAs = pool
	}
	if (c.CertFile == "") != (c.KeyFile == "") {
		return bad("Valkey TLS client certificate and key must be configured together")
	}
	if c.CertFile != "" {
		pair, e := tls.LoadX509KeyPair(c.CertFile, c.KeyFile)
		if e != nil {
			return bad("invalid Valkey TLS client certificate or key")
		}
		config.Certificates = []tls.Certificate{pair}
	}
	options.TLSConfig = config
	return options, nil
}
