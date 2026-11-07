package integration

import (
	"io"
	"net/http"
	"testing"
)

func TestProtoManagementLogoutWire(t *testing.T) {
	agentDatabases(t, func(t *testing.T, f *fixture) {
		request, err := http.NewRequest("DELETE", f.server.URL+"/api/v1/platform/sessions", nil)
		if err != nil {
			t.Fatal(err)
		}
		request.Header.Set("Authorization", "Bearer "+f.operatorToken)
		response, err := f.http.Do(request)
		if err != nil {
			t.Fatal(err)
		}
		defer response.Body.Close()
		data, err := io.ReadAll(response.Body)
		if err != nil {
			t.Fatal(err)
		}
		if response.StatusCode != http.StatusNoContent || len(data) != 0 {
			t.Fatalf("logout status/body changed: %d %q", response.StatusCode, data)
		}
		request, err = http.NewRequest("GET", f.server.URL+"/api/v1/platform/me", nil)
		if err != nil {
			t.Fatal(err)
		}
		request.Header.Set("Authorization", "Bearer "+f.operatorToken)
		response, err = f.http.Do(request)
		if err != nil {
			t.Fatal(err)
		}
		defer response.Body.Close()
		if response.StatusCode != http.StatusUnauthorized {
			t.Fatal("session revocation was not durable", response.StatusCode)
		}
	})
}
