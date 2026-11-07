// Command agent-client obtains a scoped autonomous Agent token and exercises
// either the REST resource or an actual MCP client. Credentials stay in a 0600
// file and memory; access tokens are never printed.
package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

type configuration struct {
	Issuer         string `json:"issuer"`
	ClientID       string `json:"client_id"`
	Secret         string `json:"client_secret"`
	InstallationID string `json:"installation_id"`
	Resource       string `json:"resource"`
	ResourceURL    string `json:"resource_url"`
}
type credentialTransport struct {
	token  string
	origin *url.URL
}

func (t credentialTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	if r.URL.Host != t.origin.Host || r.URL.Scheme != t.origin.Scheme {
		return nil, errors.New("refusing to send credential to another origin")
	}
	clone := r.Clone(r.Context())
	clone.Header.Set("Authorization", "Bearer "+t.token)
	return http.DefaultTransport.RoundTrip(clone)
}
func localHTTP(raw string) bool {
	u, e := url.Parse(raw)
	return e == nil && u.Scheme == "http" && (u.Hostname() == "localhost" || u.Hostname() == "127.0.0.1") && u.User == nil && u.RawQuery == "" && u.Fragment == ""
}
func run(ctx context.Context, c configuration, mode string) error {
	if !localHTTP(c.Issuer) || !localHTTP(c.ResourceURL) || c.ClientID == "" || c.Secret == "" || c.InstallationID == "" || c.Resource == "" {
		return errors.New("example requires explicit local configuration")
	}
	form := url.Values{"grant_type": {"client_credentials"}, "scope": {"tickets:read"}, "installation_id": {c.InstallationID}, "resource": {c.Resource}}
	request, _ := http.NewRequestWithContext(ctx, "POST", c.Issuer+"/oauth/token", strings.NewReader(form.Encode()))
	request.SetBasicAuth(url.QueryEscape(c.ClientID), url.QueryEscape(c.Secret))
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	client := &http.Client{Timeout: 5 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	response, e := client.Do(request)
	if e != nil {
		return errors.New("token endpoint unavailable")
	}
	defer response.Body.Close()
	if response.StatusCode != 200 {
		return fmt.Errorf("token issuance rejected (HTTP %d)", response.StatusCode)
	}
	var token struct {
		Access string `json:"access_token"`
	}
	if json.NewDecoder(io.LimitReader(response.Body, 32768)).Decode(&token) != nil || token.Access == "" {
		return errors.New("invalid token response")
	}
	origin, _ := url.Parse(c.ResourceURL)
	httpClient := &http.Client{Timeout: 10 * time.Second, Transport: credentialTransport{token.Access, origin}, CheckRedirect: client.CheckRedirect}
	if mode == "rest" {
		r, _ := http.NewRequestWithContext(ctx, "GET", c.ResourceURL+"/api/tickets", nil)
		resp, e := httpClient.Do(r)
		if e != nil {
			return errors.New("resource unavailable")
		}
		defer resp.Body.Close()
		if resp.StatusCode != 200 {
			return fmt.Errorf("resource denied (HTTP %d)", resp.StatusCode)
		}
		raw, e := io.ReadAll(io.LimitReader(resp.Body, 32769))
		if e != nil || len(raw) > 32768 || !json.Valid(raw) {
			return errors.New("invalid resource response")
		}
		fmt.Println(string(raw))
		return nil
	}
	if mode != "mcp" {
		return errors.New("mode must be rest or mcp")
	}
	mcpClient := mcp.NewClient(&mcp.Implementation{Name: "authova-agent-example", Version: "0.1.0"}, nil)
	session, e := mcpClient.Connect(ctx, &mcp.StreamableClientTransport{Endpoint: c.ResourceURL + "/mcp", HTTPClient: httpClient}, nil)
	if e != nil {
		return errors.New("MCP connection rejected")
	}
	defer session.Close()
	result, e := session.CallTool(ctx, &mcp.CallToolParams{Name: "list_tickets", Arguments: map[string]any{}})
	if e != nil || result.IsError {
		return errors.New("MCP tool call rejected")
	}
	raw, e := json.Marshal(result)
	if e != nil {
		return e
	}
	fmt.Println(string(raw))
	return nil
}
func main() {
	path := flag.String("config", "", "private 0600 configuration JSON")
	mode := flag.String("mode", "mcp", "mcp or rest")
	flag.Parse()
	info, e := os.Stat(*path)
	if e != nil || info.Mode().Perm()&0077 != 0 {
		fmt.Fprintln(os.Stderr, "private configuration is required")
		os.Exit(1)
	}
	raw, e := os.ReadFile(*path)
	var c configuration
	if e != nil || json.Unmarshal(raw, &c) != nil {
		fmt.Fprintln(os.Stderr, "configuration rejected")
		os.Exit(1)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	if e = run(ctx, c, *mode); e != nil {
		fmt.Fprintln(os.Stderr, e)
		os.Exit(1)
	}
}
