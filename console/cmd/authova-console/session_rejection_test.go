package main

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestRejectedWriteCannotRehydrateOnPageReload(t *testing.T) {
	b, _ := fixtureBFF(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/problem+json")
		w.WriteHeader(401)
		w.Write([]byte(`{"status":401,"code":"authentication_required"}`))
	}))
	id, _ := fixtureSession(t, b, session{Token: "rejected-credential", CSRF: "fixture-csrf", Expires: time.Now().Add(time.Hour)})
	r := httptest.NewRequest("PATCH", "https://console.example.test/bff/apps/app", strings.NewReader(`{"name":"changed"}`))
	r.AddCookie(&http.Cookie{Name: "av_console", Value: id})
	r.Header.Set("Origin", b.origin)
	r.Header.Set("X-CSRF-Token", "fixture-csrf")
	w := httptest.NewRecorder()
	b.ServeHTTP(w, r)
	if w.Code != 401 {
		t.Fatal("rejection rewritten", w.Code)
	}
	if len(w.Header().Values("Set-Cookie")) != 0 {
		t.Fatal("delayed rejection could overwrite a newer browser cookie")
	}
	if _, _, err := b.load(r); err != errSessionMissing {
		t.Fatal("rejected credential survived in the BFF cache")
	}
}

func TestDelayedRejectionPreservesNewCredentialGeneration(t *testing.T) {
	b, _ := fixtureBFF(t, http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	old := session{Token: "old-credential", CSRF: "fixture-csrf", Expires: time.Now().Add(time.Hour)}
	id, request := fixtureSession(t, b, old)
	claimed := old
	claimed.RefreshPending = true
	if err := b.saveSession(context.Background(), id, claimed); err != nil {
		t.Fatal(err)
	}
	if err := b.invalidateRejectedSession(context.Background(), id, old); err != nil {
		t.Fatal(err)
	}
	if current, _, err := b.load(request); err != nil || !current.RefreshPending {
		t.Fatal("late rejection removed an in-flight refresh claim")
	}
	fresh := old
	fresh.Token = "refreshed-credential"
	if err := b.saveSession(context.Background(), id, fresh); err != nil {
		t.Fatal(err)
	}
	if err := b.invalidateRejectedSession(context.Background(), id, old); err != nil {
		t.Fatal(err)
	}
	current, _, err := b.load(request)
	if err != nil || current.Token != fresh.Token {
		t.Fatal("late rejection removed a refreshed session")
	}
	if err := b.invalidateRejectedSession(context.Background(), id, fresh); err != nil {
		t.Fatal(err)
	}
	if _, _, err = b.load(request); err != errSessionMissing {
		t.Fatal("current rejected generation not invalidated")
	}
}
