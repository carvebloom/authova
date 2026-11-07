// demo-rp is a local-only sample relying party. Identity remains in the real Authova core.
package main

import (
	"context"
	"crypto/rand"

	"encoding/base64"
	"encoding/json"
	"errors"
	"html/template"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/coreos/go-oidc/v3/oidc"
	"golang.org/x/oauth2"
)

type app struct {
	AgentInstallationID string   `json:"agent_installation_id,omitempty"`
	AgentResourceURL    string   `json:"agent_resource_url,omitempty"`
	Scopes              []string `json:"scopes,omitempty"`
	Name                string   `json:"name"`
	Key                 string   `json:"key"`
	Issuer              string   `json:"issuer"`
	ClientID            string   `json:"client_id"`
	ClientSecret        string   `json:"client_secret"`
	Audience            string   `json:"audience"`
}
type configuration struct {
	ConsoleURL string `json:"console_url"`
	Origin     string
	Apps       []app
}
type pending struct {
	App, State, Verifier, Nonce string
	Expires                     time.Time
}
type session struct {
	App, Subject, Name, Email, Access, Refresh, CSRF, Issuer string
	Expires                                                  time.Time
}
type relyingParty struct {
	config   configuration
	mu       sync.Mutex
	flows    map[string]pending
	sessions map[string]session
}

func random() string {
	b := make([]byte, 32)
	if _, e := rand.Read(b); e != nil {
		panic(e)
	}
	return base64.RawURLEncoding.EncodeToString(b)
}

var page = template.Must(template.New("demo").Parse(`<!doctype html><html lang="zh-CN"><head><meta charset="utf-8"><meta name="viewport" content="width=device-width,initial-scale=1"><title>Authova · {{if .AgentDemo}}Agent 身份示例{{else}}双 App 示例{{end}}</title><link rel="stylesheet" href="/demo.css"></head><body><header><a href="/">Authova <span>{{if .AgentDemo}}Agent 身份示例{{else}}双 App 示例{{end}}</span></a><a href="{{.ConsoleURL}}">管理控制台 ↗</a></header><main><p class="eyebrow">真实身份服务 / OAuth + PKCE</p>{{if .AgentDemo}}<h1>用户委托与<br>Agent 独立身份。</h1><p class="lead">在第一个 App 登录并确认 Agent 授权，查看它代表你访问资源的结果。你可以在账号安全中独立撤销授权。</p>{{else}}<h1>两个 App，<br>各自独立的账号。</h1><p class="lead">在两个 App 注册相同邮箱，查看独立身份、会话和令牌状态。</p>{{end}}<section class="apps">{{range .Apps}}<article><p class="eyebrow">独立身份域</p><h2>{{.Name}}</h2><code>{{.Issuer}}</code><a class="button" href="/apps/{{.Key}}/login">{{if .AgentInstallationID}}登录并授权 Agent →{{else}}登录或注册 →{{end}}</a></article>{{end}}</section>{{if .Session}}<section class="identity"><p class="eyebrow">当前 App 登录结果</p><h2>{{if .Session.Name}}{{.Session.Name}}{{else}}授权会话已建立{{end}}</h2><dl><dt>主体 ID</dt><dd>{{.Session.Subject}}</dd><dt>邮箱</dt><dd>{{.Session.Email}}</dd><dt>身份域</dt><dd>{{.Session.App}}</dd></dl><p>令牌保存在此示例的服务端，浏览器只持有 HttpOnly 会话 Cookie。</p><form action="/apps/{{.Session.App}}/resource" method="post"><input type="hidden" name="csrf" value="{{.Session.CSRF}}"><button>检查令牌并访问资源</button></form><form action="/apps/{{.Session.App}}/refresh" method="post"><input type="hidden" name="csrf" value="{{.Session.CSRF}}"><button class="secondary">轮换刷新令牌</button></form><a href="{{.Session.Issuer}}/ui/account">管理账号安全 →</a><a href="/apps/{{.Session.App}}/logout">退出当前 App →</a>{{if .Message}}<p role="status" class="result">{{.Message}}</p>{{end}}</section>{{end}}<footer>首次注册：打开 <a href="http://127.0.0.1:38025" target="_blank" rel="noreferrer">本地邮箱 Mailpit ↗</a> 获取验证码。示例仅监听本机地址，身份数据持久化到真实数据库。</footer></main></body></html>`))

const stylesheet = `:root{font-family:system-ui,-apple-system,"PingFang SC",sans-serif;background:#f6f7f4;color:#26362d}*{box-sizing:border-box}body{margin:0}header{display:flex;justify-content:space-between;align-items:center;padding:26px 6vw;border-bottom:1px solid #dde3db}header a{color:inherit;text-decoration:none;font-weight:750;font-size:22px}header a:last-child{font-size:14px;font-weight:500}header span{color:#718476;font-size:13px;margin-left:14px}main{max-width:1100px;margin:auto;padding:64px 28px}.eyebrow{font-size:12px;color:#64806c;letter-spacing:.1em}h1{font-size:clamp(36px,6vw,64px);font-weight:700;letter-spacing:-.045em;line-height:1.2;margin:20px 0}.lead{color:#6a7d70;line-height:1.7}.apps{display:grid;grid-template-columns:1fr 1fr;gap:44px;margin:48px 0}article{border-top:2px solid #24553b;padding-top:18px}h2{font-size:24px}code{display:block;font-size:11px;overflow-wrap:anywhere;color:#677c6c}.button,button{display:inline-block;text-decoration:none;color:white;background:#205239;border:0;padding:14px 20px;border-radius:5px;font:inherit;cursor:pointer;margin-top:24px}.button:hover,button:hover{background:#326b48}.secondary{background:#e0e8dd;color:#245039}.identity{border-top:1px solid #cbd7c7;padding-top:34px;max-width:680px}.identity p{color:#657d69;font-size:14px}.identity form{display:inline-block;margin-right:16px}dl{display:grid;grid-template-columns:100px 1fr;gap:14px;font-size:14px}dd{margin:0;overflow-wrap:anywhere}dt{color:#6b8070}.result{background:#e2eee1;padding:20px}.identity>a{display:block;margin:26px 0;color:#205239}footer{border-top:1px solid #d3ddce;margin-top:60px;padding:24px 0;color:#74816f;font-size:12px;line-height:1.8}a{color:#205239}:focus-visible{outline:3px solid #68ab79;outline-offset:4px}@media(max-width:650px){main{padding:34px 20px}.apps{grid-template-columns:1fr;gap:24px}header{padding:20px}header span{display:none}header a:last-child{font-size:12px}}`

func (p *relyingParty) render(w http.ResponseWriter, s *session, message string) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	agentDemo := false
	for _, a := range p.config.Apps {
		agentDemo = agentDemo || a.AgentInstallationID != ""
	}
	console := p.config.ConsoleURL
	if console == "" {
		console = "http://127.0.0.1:18090"
	}
	page.Execute(w, struct {
		AgentDemo  bool
		ConsoleURL string
		Apps       []app
		Session    *session
		Message    string
	}{agentDemo, console, p.config.Apps, s, message})
}
func (p *relyingParty) find(key string) (app, bool) {
	for _, a := range p.config.Apps {
		if a.Key == key {
			return a, true
		}
	}
	return app{}, false
}
func (p *relyingParty) oauth(ctx context.Context, a app) (*oidc.Provider, *oauth2.Config, error) {
	provider, e := oidc.NewProvider(ctx, a.Issuer)
	if e != nil {
		return nil, nil, e
	}
	endpoint := provider.Endpoint()
	endpoint.AuthStyle = oauth2.AuthStyleInHeader
	scopes := a.Scopes
	if len(scopes) == 0 {
		scopes = []string{"openid", "profile", "email", "offline_access"}
	}
	return provider, &oauth2.Config{ClientID: a.ClientID, ClientSecret: a.ClientSecret, Endpoint: endpoint, Scopes: scopes, RedirectURL: p.config.Origin + "/apps/" + a.Key + "/callback"}, nil
}
func (p *relyingParty) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Referrer-Policy", "strict-origin-when-cross-origin")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Content-Security-Policy", "default-src 'none'; style-src 'self'; form-action 'self'; base-uri 'none'; frame-ancestors 'none'")
	if r.URL.Path == "/demo.css" {
		w.Header().Set("Content-Type", "text/css")
		io.WriteString(w, stylesheet)
		return
	}
	if r.URL.Path == "/" {
		p.render(w, nil, "")
		return
	}
	parts := strings.Split(strings.Trim(r.URL.Path, "/"), "/")
	if len(parts) != 3 || parts[0] != "apps" {
		http.NotFound(w, r)
		return
	}
	a, ok := p.find(parts[1])
	if !ok {
		http.NotFound(w, r)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 15*time.Second)
	defer cancel()
	if parts[2] == "login" {
		_, config, e := p.oauth(ctx, a)
		if e != nil {
			http.Error(w, "Identity service unavailable", http.StatusServiceUnavailable)
			return
		}
		state := random()
		f := pending{App: a.Key, State: state, Verifier: oauth2.GenerateVerifier(), Nonce: random(), Expires: time.Now().Add(10 * time.Minute)}
		p.mu.Lock()
		for key, old := range p.flows {
			if old.Expires.Before(time.Now()) {
				delete(p.flows, key)
			}
		}
		if len(p.flows) >= 1000 {
			p.mu.Unlock()
			http.Error(w, "Too many pending logins", http.StatusTooManyRequests)
			return
		}
		p.flows[state] = f
		p.mu.Unlock()
		http.SetCookie(w, &http.Cookie{Name: "av_rp_flow_" + a.Key, Value: state, Path: "/apps/" + a.Key, HttpOnly: true, SameSite: http.SameSiteLaxMode, MaxAge: 600})
		options := []oauth2.AuthCodeOption{oauth2.SetAuthURLParam("nonce", f.Nonce), oauth2.S256ChallengeOption(f.Verifier)}
		if a.AgentInstallationID != "" {
			options = append(options, oauth2.SetAuthURLParam("installation_id", a.AgentInstallationID), oauth2.SetAuthURLParam("resource", a.Audience))
		}
		http.Redirect(w, r, config.AuthCodeURL(state, options...), http.StatusFound)
		return
	}
	if parts[2] == "callback" {
		state := r.URL.Query().Get("state")
		cookie, e := r.Cookie("av_rp_flow_" + a.Key)
		if e != nil || cookie.Value != state {
			http.Error(w, "Invalid browser binding", http.StatusForbidden)
			return
		}
		p.mu.Lock()
		f, ok := p.flows[state]
		delete(p.flows, state)
		p.mu.Unlock()
		if !ok || f.App != a.Key || f.Expires.Before(time.Now()) {
			http.Error(w, "Expired login", http.StatusForbidden)
			return
		}
		provider, config, e := p.oauth(ctx, a)
		if e != nil {
			http.Error(w, "Identity service unavailable", http.StatusServiceUnavailable)
			return
		}
		token, e := config.Exchange(ctx, r.URL.Query().Get("code"), oauth2.VerifierOption(f.Verifier))
		if e != nil {
			http.Error(w, "Code exchange failed", http.StatusUnauthorized)
			return
		}
		raw, ok := token.Extra("id_token").(string)
		if !ok {
			http.Error(w, "ID token missing", http.StatusUnauthorized)
			return
		}
		identity, e := provider.Verifier(&oidc.Config{ClientID: a.ClientID, SupportedSigningAlgs: []string{"RS256"}}).Verify(ctx, raw)
		if e != nil || identity.Nonce != f.Nonce || identity.VerifyAccessToken(token.AccessToken) != nil {
			http.Error(w, "Invalid ID/access token binding", http.StatusUnauthorized)
			return
		}
		var claims struct{ Name, Email string }
		identity.Claims(&claims)
		userinfo, e := provider.UserInfo(ctx, oauth2.StaticTokenSource(token))
		if e != nil || userinfo.Subject != identity.Subject {
			http.Error(w, "UserInfo mismatch", http.StatusUnauthorized)
			return
		}
		if e = userinfo.Claims(&claims); e != nil {
			http.Error(w, "UserInfo invalid", http.StatusUnauthorized)
			return
		}
		s := session{Issuer: a.Issuer, App: a.Key, Subject: identity.Subject, Name: claims.Name, Email: claims.Email, Access: token.AccessToken, Refresh: token.RefreshToken, Expires: time.Now().Add(time.Hour), CSRF: random()}
		id := random()
		p.mu.Lock()
		for key, old := range p.sessions {
			if old.Expires.Before(time.Now()) {
				delete(p.sessions, key)
			}
		}
		p.sessions[id] = s
		p.mu.Unlock()
		http.SetCookie(w, &http.Cookie{Name: "av_rp_session_" + a.Key, Value: id, Path: "/apps/" + a.Key, HttpOnly: true, SameSite: http.SameSiteLaxMode, MaxAge: 3600})
		http.Redirect(w, r, "/apps/"+a.Key+"/home", http.StatusSeeOther)
		return
	}
	cookie, e := r.Cookie("av_rp_session_" + a.Key)
	if e != nil {
		http.Redirect(w, r, "/", http.StatusFound)
		return
	}
	p.mu.Lock()
	s, ok := p.sessions[cookie.Value]
	p.mu.Unlock()
	if !ok || s.App != a.Key || s.Expires.Before(time.Now()) {
		http.Redirect(w, r, "/", http.StatusFound)
		return
	}
	if parts[2] == "home" && r.Method == "GET" {
		p.render(w, &s, "")
		return
	}
	if parts[2] == "logout" && r.Method == "GET" {
		http.Redirect(w, r, a.Issuer+"/ui/logout", http.StatusFound)
		return
	}
	if r.Method != "POST" || r.Header.Get("Origin") != p.config.Origin || r.ParseForm() != nil || r.Form.Get("csrf") != s.CSRF {
		http.Error(w, "Invalid CSRF", http.StatusForbidden)
		return
	}
	message := ""
	switch parts[2] {
	case "resource":
		if a.AgentResourceURL != "" {
			request, _ := http.NewRequestWithContext(ctx, "GET", a.AgentResourceURL+"/api/tickets", nil)
			request.Header.Set("Authorization", "Bearer "+s.Access)
			response, e := (&http.Client{Timeout: 5 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}).Do(request)
			if e != nil {
				http.Error(w, "Agent resource unavailable", http.StatusServiceUnavailable)
				return
			}
			defer response.Body.Close()
			body, e := io.ReadAll(io.LimitReader(response.Body, 32769))
			if e != nil || len(body) > 32768 {
				http.Error(w, "Invalid resource response", http.StatusServiceUnavailable)
				return
			}
			if response.StatusCode != 200 {
				p.render(w, &s, "Agent 访问被拒绝（HTTP "+strconv.Itoa(response.StatusCode)+"）。请核对授权、权限与到期时间。")
				return
			}
			message = "Agent 已通过实时身份和业务权限检查：" + string(body)
			break
		}

		req, _ := http.NewRequestWithContext(ctx, "POST", a.Issuer+"/oauth/introspect", strings.NewReader(url.Values{"token": {s.Access}}.Encode()))
		req.SetBasicAuth(a.ClientID, a.ClientSecret)
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		resp, e := http.DefaultClient.Do(req)
		if e != nil {
			http.Error(w, "Identity service unavailable", http.StatusServiceUnavailable)
			return
		}
		defer resp.Body.Close()
		var status struct{ Active bool }
		if resp.StatusCode != 200 || json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&status) != nil {
			http.Error(w, "Introspection failed", http.StatusServiceUnavailable)
			return
		}
		message = "令牌已失效，资源访问被拒绝。"
		if status.Active {
			message = "令牌有效，当前 App 的示例资源访问成功。"
		}
	case "refresh":
		_, config, e := p.oauth(ctx, a)
		if e != nil {
			http.Error(w, "Identity service unavailable", http.StatusServiceUnavailable)
			return
		}
		token, e := config.TokenSource(ctx, &oauth2.Token{RefreshToken: s.Refresh, Expiry: time.Now().Add(-time.Hour)}).Token()
		if e != nil {
			http.Error(w, "Refresh failed", http.StatusUnauthorized)
			return
		}
		s.Access, s.Refresh = token.AccessToken, token.RefreshToken
		p.mu.Lock()
		p.sessions[cookie.Value] = s
		p.mu.Unlock()
		message = "刷新令牌已轮换，旧令牌不能再次使用。"
	default:
		http.NotFound(w, r)
		return
	}
	p.render(w, &s, message)
}
func main() {
	data, e := os.ReadFile(os.Getenv("AUTHOVA_DEMO_CONFIG"))
	if e != nil {
		slog.Error("Demo config required")
		os.Exit(1)
	}
	var config configuration
	if json.Unmarshal(data, &config) != nil || !localOrigin(config.Origin) || (config.ConsoleURL != "" && !localOrigin(config.ConsoleURL)) {
		slog.Error("Demo accepts local configuration only")
		os.Exit(1)
	}
	for _, a := range config.Apps {
		if a.AgentResourceURL != "" && !localOrigin(a.AgentResourceURL) {
			slog.Error("Agent demo resource must be local")
			os.Exit(1)
		}
		issuer, err := url.Parse(a.Issuer)
		if err != nil || !localOrigin(issuer.Scheme+"://"+issuer.Host) || issuer.Path != "/a/"+a.Key || issuer.RawQuery != "" || issuer.Fragment != "" || issuer.User != nil {
			slog.Error("Demo issuer must be local")
			os.Exit(1)
		}
	}
	origin, _ := url.Parse(config.Origin)
	server := &http.Server{Addr: net.JoinHostPort("127.0.0.1", origin.Port()), Handler: &relyingParty{config: config, flows: map[string]pending{}, sessions: map[string]session{}}, ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 10 * time.Second, WriteTimeout: 20 * time.Second}
	slog.Info("Demo RP listening", "address", server.Addr)
	e = server.ListenAndServe()
	if !errors.Is(e, http.ErrServerClosed) {
		slog.Error("Demo stopped")
	}
}

// Demo ports may vary for isolated localhost WebAuthn acceptance. Keep the
// sample strictly on loopback and do not generalize it into a public RP.
func localOrigin(raw string) bool {
	u, e := url.Parse(raw)
	if e != nil || u.Scheme != "http" || u.User != nil || u.Path != "" || u.RawQuery != "" || u.Fragment != "" || (u.Hostname() != "localhost" && u.Hostname() != "127.0.0.1") {
		return false
	}
	port, e := strconv.Atoi(u.Port())
	return e == nil && port >= 1024 && port <= 65535
}
