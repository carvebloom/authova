// demo-sms is a loopback-only development mailbox, never a production SMS provider.
package main

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"github.com/carvebloom/authova/internal/runtimehttp"
	"html/template"
	"log"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"sync"
	"syscall"
	"time"
)

type message struct {
	ID          string    `json:"message_id"`
	AppID       string    `json:"app_id"`
	Destination string    `json:"destination"`
	Body        string    `json:"body"`
	Received    time.Time `json:"received_at"`
}
type mailbox struct {
	mu    sync.Mutex
	token string
	items []message
}

var page = template.Must(template.New("mailbox").Parse(`<!doctype html><html lang="zh-CN"><head><meta charset="utf-8"><meta name="viewport" content="width=device-width,initial-scale=1"><meta http-equiv="refresh" content="5"><title>Authova 本地短信收件箱</title></head><body><main><h1>本地短信收件箱</h1><p>仅模拟送达，没有发送真实短信。每 5 秒刷新，最多保留 15 分钟。</p>{{range .}}<article><h2>{{.Destination}}</h2><p>App: {{.AppID}}</p><pre>{{.Body}}</pre><p>{{.Received}}</p></article>{{else}}<p>暂无短信。请在登录页面选择手机短信并发送验证码。</p>{{end}}</main></body></html>`))

func (m *mailbox) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Content-Security-Policy", "default-src 'none'; frame-ancestors 'none'; base-uri 'none'")
	if r.Host != "127.0.0.1:18089" && r.Host != "localhost:18089" {
		http.Error(w, "loopback Host required", http.StatusForbidden)
		return
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	live := m.items[:0]
	for _, item := range m.items {
		if time.Since(item.Received) < 15*time.Minute {
			live = append(live, item)
		}
	}
	m.items = live
	if r.Method == "GET" && (r.URL.Path == "/" || r.URL.Path == "/messages") {
		if r.URL.Path == "/messages" {
			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(m.items)
		} else {
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			page.Execute(w, m.items)
		}
		return
	}
	if r.Method != "POST" || r.URL.Path != "/send" {
		http.NotFound(w, r)
		return
	}
	if subtle.ConstantTimeCompare([]byte(r.Header.Get("Authorization")), []byte("Bearer "+m.token)) != 1 {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	var item message
	reader := http.MaxBytesReader(w, r.Body, 8192)
	decoder := json.NewDecoder(reader)
	decoder.DisallowUnknownFields()
	if decoder.Decode(&item) != nil || item.ID == "" || len(item.ID) > 128 || item.AppID == "" || !strings.HasPrefix(item.Destination, "+") || item.Body == "" || r.Header.Get("Idempotency-Key") != item.ID {
		http.Error(w, "invalid message", http.StatusBadRequest)
		return
	}
	for _, old := range m.items {
		if old.ID == item.ID {
			if old.AppID != item.AppID || old.Destination != item.Destination || old.Body != item.Body {
				http.Error(w, "idempotency conflict", http.StatusConflict)
				return
			}
			w.WriteHeader(http.StatusAccepted)
			return
		}
	}
	if len(m.items) >= 1024 {
		http.Error(w, "mailbox full", http.StatusServiceUnavailable)
		return
	}
	item.Received = time.Now().UTC()
	m.items = append(m.items, item)
	w.WriteHeader(http.StatusAccepted)
}
func main() {
	token := os.Getenv("AUTHOVA_SMS_TOKEN")
	if os.Getenv("AUTHOVA_MODE") != "development" || token == "" {
		log.Fatal("demo-sms requires development mode and a configured adapter token")
	}
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()
	server := &http.Server{Addr: "127.0.0.1:18089", Handler: &mailbox{token: token, items: []message{}}, ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 5 * time.Second, WriteTimeout: 5 * time.Second, IdleTimeout: 30 * time.Second}
	if err := runtimehttp.Serve(ctx, server); err != nil {
		log.Fatal("local SMS mailbox stopped")
	}
}
