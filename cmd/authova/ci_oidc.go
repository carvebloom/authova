package main

// This subprocess consumer is limited to a new CI fixture. It neither loads Core
// configuration nor opens a database, creates a user, or starts a service.
import (
	"bytes"
	"context"
	"crypto/subtle"
	"crypto/tls"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"errors"
	"io"
	"math"
	"mime"
	"net"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/coreos/go-oidc/v3/oidc"
	"golang.org/x/oauth2"
)

type ciOIDCRequest struct {
	Issuer          string  `json:"issuer"`
	ClientID        string  `json:"client_id"`
	ClientSecret    string  `json:"client_secret"`
	RedirectURI     string  `json:"redirect_uri"`
	Code            string  `json:"code"`
	ExpectedState   string  `json:"expected_state"`
	CallbackState   string  `json:"callback_state"`
	Nonce           string  `json:"nonce"`
	Verifier        string  `json:"code_verifier"`
	ExpectedSubject string  `json:"expected_subject"`
	CA              string  `json:"ca_pem"`
	FlowOrigin      float64 `json:"flow_origin_utc"`
	StateDeadline   float64 `json:"state_deadline_utc"`
	CodeObserved    float64 `json:"code_observed_utc"`
	CodeDeadline    float64 `json:"code_deadline_utc"`
}

func ciOIDCProof(input io.Reader, output io.Writer) error {
	refused := errors.New("CI_OIDC_PROOF_REFUSED")
	origin := time.Now()
	ctx, cancel := context.WithTimeout(context.Background(), 40*time.Second)
	defer cancel()
	type readResult struct {
		body []byte
		err  error
	}
	read := make(chan readResult, 1)
	go func() { body, err := io.ReadAll(io.LimitReader(input, 32769)); read <- readResult{body, err} }()
	var body []byte
	var e error
	select {
	case result := <-read:
		body, e = result.body, result.err
	case <-ctx.Done():
		return refused
	}
	if e != nil || len(body) > 32768 || !utf8.Valid(body) {
		return refused
	}
	// Decode a closed, nonduplicate string/number object, UTF-8 only.
	var fields map[string]json.RawMessage
	d := json.NewDecoder(strings.NewReader(string(body)))
	start, e := d.Token()
	if e != nil || start != json.Delim('{') {
		return refused
	}
	fields = make(map[string]json.RawMessage)
	for d.More() {
		key, err := d.Token()
		name, ok := key.(string)
		if err != nil || !ok || fields[name] != nil {
			return refused
		}
		var value json.RawMessage
		if d.Decode(&value) != nil {
			return refused
		}
		fields[name] = value
	}
	if _, e = d.Token(); e != nil || d.Decode(new(any)) != io.EOF || len(fields) != 15 {
		return refused
	}
	d = json.NewDecoder(strings.NewReader(string(body)))
	d.DisallowUnknownFields()
	var r ciOIDCRequest
	if d.Decode(&r) != nil {
		return refused
	}
	u, e := ciOIDCParameters(r, float64(origin.UnixNano())/1e9)
	if e != nil {
		return refused
	}
	check := func() error {
		now := float64(time.Now().UnixNano()) / 1e9
		if now < r.CodeObserved || now >= r.CodeDeadline || now >= r.StateDeadline || time.Since(origin) >= 40*time.Second {
			return refused
		}
		return nil
	}
	block, rest := pem.Decode([]byte(r.CA))
	if block == nil || block.Type != "CERTIFICATE" || len(strings.TrimSpace(string(rest))) != 0 {
		return refused
	}
	cert, e := x509.ParseCertificate(block.Bytes)
	if e != nil || !cert.IsCA {
		return refused
	}
	roots := x509.NewCertPool()
	roots.AddCert(cert)
	transport := &http.Transport{TLSClientConfig: &tls.Config{MinVersion: tls.VersionTLS12, RootCAs: roots}, Proxy: nil,
		DialContext: func(ctx context.Context, network, address string) (net.Conn, error) {
			if address != u.Host {
				return nil, refused
			}
			return (&net.Dialer{Timeout: 10 * time.Second}).DialContext(ctx, network, "127.0.0.1:8443")
		}}
	defer transport.CloseIdleConnections()
	calls := make(map[string]int)
	guarded := ciRoundTrip(func(request *http.Request) (*http.Response, error) {
		if check() != nil || request.URL.Scheme != "https" || request.URL.Host != u.Host ||
			request.URL.RawQuery != "" || request.URL.User != nil || request.URL.Fragment != "" || request.URL.RawPath != "" || request.URL.ForceQuery {
			return nil, refused
		}
		method := "GET"
		switch request.URL.Path {
		case u.Path + "/.well-known/openid-configuration", u.Path + "/keys":
		case u.Path + "/oauth/token":
			method = "POST"
		default:
			return nil, refused
		}
		if request.Method != method || calls[request.URL.Path] != 0 {
			return nil, refused
		}
		calls[request.URL.Path]++
		response, err := transport.RoundTrip(request)
		if err != nil {
			return nil, err
		}
		kind, _, mimeErr := mime.ParseMediaType(response.Header.Get("Content-Type"))
		if len(response.Header.Values("Content-Type")) != 1 || mimeErr != nil || (kind != "application/json" && kind != "application/jwk-set+json") {
			response.Body.Close()
			return nil, refused
		}
		data, first := io.ReadAll(io.LimitReader(response.Body, 1048577))
		closed := response.Body.Close()
		if first != nil || closed != nil {
			return nil, errors.Join(first, closed)
		}
		if len(data) > 1048576 || check() != nil || (response.ContentLength >= 0 && response.ContentLength != int64(len(data))) {
			return nil, refused
		}
		response.Body = io.NopCloser(bytes.NewReader(data))
		return response, nil
	})
	client := &http.Client{Transport: guarded, Timeout: 10 * time.Second,
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	ctx = oidc.ClientContext(ctx, client)
	provider, e := oidc.NewProvider(ctx, r.Issuer)
	if e != nil || check() != nil {
		return refused
	}
	endpoint := provider.Endpoint()
	if endpoint.AuthURL != r.Issuer+"/authorize" || endpoint.TokenURL != r.Issuer+"/oauth/token" {
		return refused
	}
	endpoint.AuthStyle = oauth2.AuthStyleInHeader
	config := oauth2.Config{ClientID: r.ClientID, ClientSecret: r.ClientSecret, Endpoint: endpoint, RedirectURL: r.RedirectURI}
	token, e := config.Exchange(ctx, r.Code, oauth2.VerifierOption(r.Verifier))
	if e != nil || check() != nil {
		return refused
	}
	raw, ok := token.Extra("id_token").(string)
	if !ok {
		return refused
	}
	parts := strings.Split(raw, ".")
	if len(parts) != 3 {
		return refused
	}
	header, err := base64.RawURLEncoding.DecodeString(parts[0])
	var key struct {
		Alg string `json:"alg"`
		Kid string `json:"kid"`
	}
	if err != nil || len(header) > 4096 || json.Unmarshal(header, &key) != nil || key.Alg != "RS256" || key.Kid == "" {
		return refused
	}
	identity, e := provider.Verifier(&oidc.Config{ClientID: r.ClientID, SupportedSigningAlgs: []string{"RS256"}}).Verify(ctx, raw)
	if e != nil || check() != nil || len(identity.Audience) != 1 || identity.Audience[0] != r.ClientID ||
		identity.Issuer != r.Issuer || subtle.ConstantTimeCompare([]byte(identity.Subject), []byte(r.ExpectedSubject)) != 1 ||
		subtle.ConstantTimeCompare([]byte(identity.Nonce), []byte(r.Nonce)) != 1 || identity.VerifyAccessToken(token.AccessToken) != nil {
		return refused
	}
	if check() != nil {
		return refused
	}
	_, e = io.WriteString(output, "{\"schema\":\"authova-ci-oidc-proof/v1\",\"result\":\"PASS\"}\n")
	if e != nil || check() != nil {
		return refused
	}
	return nil
}

// The only caller constructs the fixed host/path guarded transport above.
type ciRoundTrip func(*http.Request) (*http.Response, error)

func (f ciRoundTrip) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func ciOIDCParameters(r ciOIDCRequest, now float64) (*url.URL, error) {
	refused := errors.New("CI_OIDC_PROOF_REFUSED")
	u, e := url.Parse(r.Issuer)
	if e != nil || u.Scheme != "https" || u.Hostname() != "host.docker.internal" || u.Port() == "" ||
		u.User != nil || u.RawQuery != "" || u.Fragment != "" || u.RawPath != "" || u.ForceQuery ||
		!regexp.MustCompile(`^/a/[0-9a-f]{32}$`).MatchString(u.Path) {
		return nil, refused
	}
	port, e := strconv.Atoi(u.Port())
	if e != nil || port < 1 || port > 65535 {
		return nil, refused
	}
	redirect, e := url.Parse(r.RedirectURI)
	if e != nil || redirect.Scheme != "https" || redirect.Host != u.Host || redirect.Path != "/ci/callback" || redirect.RawQuery != "" || redirect.Fragment != "" || redirect.User != nil || redirect.RawPath != "" || redirect.ForceQuery {
		return nil, refused
	}
	if r.Code == "" || r.ClientID == "" || r.ClientSecret == "" || r.Nonce == "" || r.ExpectedSubject == "" ||
		len(r.ExpectedState) < 32 || subtle.ConstantTimeCompare([]byte(r.ExpectedState), []byte(r.CallbackState)) != 1 ||
		!regexp.MustCompile(`^[A-Za-z0-9._~-]{43,128}$`).MatchString(r.Verifier) {
		return nil, refused
	}
	for _, v := range []float64{r.FlowOrigin, r.StateDeadline, r.CodeObserved, r.CodeDeadline} {
		if math.IsNaN(v) || math.IsInf(v, 0) || v <= 0 {
			return nil, refused
		}
	}
	if r.StateDeadline > r.FlowOrigin+300 || r.CodeObserved < r.FlowOrigin || r.CodeDeadline > r.CodeObserved+60 {
		return nil, refused
	}
	nowLimit := now + 40
	if now < r.CodeObserved || nowLimit >= r.CodeDeadline || nowLimit >= r.StateDeadline {
		return nil, refused
	}
	return u, nil
}
