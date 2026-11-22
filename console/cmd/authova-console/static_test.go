package main

import (
	"io/fs"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"testing/fstest"
	"time"

	sdk "github.com/carvebloom/authova/console/internal/coreapi"
)

type staticFailureFS struct{ fs.FS }

func (f staticFailureFS) Open(name string) (fs.File, error) {
	if name == "assets/blocked.js" || name == "blocked-route" {
		return nil, fs.ErrPermission
	}
	return f.FS.Open(name)
}

func TestStaticAssetFailuresDoNotBecomeSPAHTML(t *testing.T) {
	files := staticFailureFS{fstest.MapFS{
		"index.html":       {Data: []byte("<!doctype html><title>SPA</title>")},
		"assets/entry.js":  {Data: []byte("console.log('asset')")},
		"assets/entry.css": {Data: []byte("body{color:red}")},
	}}
	b := &bff{staticFS: files, api: &sdk.Client{HTTP: &http.Client{Timeout: time.Second}}}
	for _, tc := range []struct {
		path string
		code int
		body string
	}{
		{"/", 200, "<title>SPA</title>"},
		{"/apps/owned/config", 200, "<title>SPA</title>"},
		{"/assets/entry.js", 200, "console.log('asset')"},
		{"/assets/entry.css", 200, "body{color:red}"},
		{"/assets/missing.js", 404, "static_not_found"},
		{"/assets/missing", 404, "static_not_found"},
		{"/favicon.svg", 404, "static_not_found"},
		{"/assets/", 404, "static_not_found"},
		{"/assets/blocked.js", 503, "static_unavailable"},
		{"/blocked-route", 503, "static_unavailable"},
	} {
		t.Run(tc.path, func(t *testing.T) {
			w := httptest.NewRecorder()
			b.ServeHTTP(w, httptest.NewRequest("GET", "https://console.example"+tc.path, nil))
			if w.Code != tc.code || !strings.Contains(w.Body.String(), tc.body) {
				t.Fatalf("status=%d body=%q", w.Code, w.Body.String())
			}
			if tc.code != 200 && strings.Contains(w.Body.String(), "<title>SPA</title>") {
				t.Fatal("static failure returned SPA")
			}
			if tc.code == 200 && strings.HasSuffix(tc.path, ".css") && !strings.HasPrefix(w.Header().Get("Content-Type"), "text/css") {
				t.Fatal("wrong CSS MIME", w.Header().Get("Content-Type"))
			}
			if tc.code == 200 && strings.HasSuffix(tc.path, ".js") && !strings.HasPrefix(w.Header().Get("Content-Type"), "text/javascript") {
				t.Fatal("wrong JS MIME", w.Header().Get("Content-Type"))
			}
		})
	}
}
