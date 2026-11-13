package main

import (
	"bytes"
	sdk "github.com/carvebloom/authova/console/internal/coreapi"
	"io/fs"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

type panicFS struct{}

func (panicFS) Open(string) (fs.File, error) { panic("CANARY-REQUEST-PASSWORD") }
func TestConsoleRawPanicIsSafeAbort(t *testing.T) {
	var logs bytes.Buffer
	old := slog.Default()
	slog.SetDefault(slog.New(slog.NewJSONHandler(&logs, nil)))
	defer slog.SetDefault(old)
	defer func() {
		if recover() != http.ErrAbortHandler {
			t.Fatal("lost abort")
		}
		if strings.Contains(logs.String(), "CANARY") || !strings.Contains(logs.String(), "panic_unwind") || !strings.Contains(logs.String(), "console_") {
			t.Fatal("unsafe/missing Console panic diagnostics")
		}
	}()
	api, _ := sdk.New("https://core.example")
	b := &bff{api: api, staticFS: panicFS{}, development: true}
	b.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest("GET", "https://console.example/assets/file.js?password=CANARY", nil))
}
