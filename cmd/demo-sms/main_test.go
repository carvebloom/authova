package main

import (
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestMailboxBoundary(t *testing.T) {
	m := &mailbox{token: "test-only", items: []message{}}
	body := `{"message_id":"id","app_id":"app","destination":"+8613800138000","body":"code: 123456 <script>"}`
	send := func(host, token, body string) int {
		r := httptest.NewRequest("POST", "http://"+host+"/send", strings.NewReader(body))
		r.Header.Set("Authorization", "Bearer "+token)
		r.Header.Set("Idempotency-Key", "id")
		w := httptest.NewRecorder()
		m.ServeHTTP(w, r)
		return w.Code
	}
	if send("127.0.0.1:18089", "bad", body) != 401 || send("evil.example:18089", "test-only", body) != 403 {
		t.Fatal("mailbox auth/host bypass")
	}
	first := send("127.0.0.1:18089", "test-only", body)
	second := send("127.0.0.1:18089", "test-only", body)
	if first != 202 || second != 202 || len(m.items) != 1 {
		t.Fatal("message duplicated")
	}
	if send("127.0.0.1:18089", "test-only", strings.ReplaceAll(body, "123456", "654321")) != 409 {
		t.Fatal("payload replay allowed")
	}
	w := httptest.NewRecorder()
	m.ServeHTTP(w, httptest.NewRequest("GET", "http://127.0.0.1:18089/", nil))
	if strings.Contains(w.Body.String(), "<script>") || w.Header().Get("Cache-Control") != "no-store" {
		t.Fatal("mailbox HTML unsafe")
	}
	m.items[0].Received = time.Now().Add(-16 * time.Minute)
	w = httptest.NewRecorder()
	m.ServeHTTP(w, httptest.NewRequest("GET", "http://127.0.0.1:18089/messages", nil))
	if len(m.items) != 0 {
		t.Fatal("stale OTP retained")
	}
}
