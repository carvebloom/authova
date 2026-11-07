package main

import "testing"

func TestLocalDemoOriginBoundary(t *testing.T) {
	for _, origin := range []string{"http://localhost:18188", "http://127.0.0.1:18088"} {
		if !localOrigin(origin) {
			t.Fatal("valid local origin rejected", origin)
		}
	}
	for _, origin := range []string{"https://localhost:18188", "http://example.com:18188", "http://localhost:18188/path", "http://localhost:18188?callback=example.com", "http://user@localhost:18188", "http://localhost:80", "http://localhost:99999", "http://localhost"} {
		if localOrigin(origin) {
			t.Fatal("noncanonical origin accepted", origin)
		}
	}
}
