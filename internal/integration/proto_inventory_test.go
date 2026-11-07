package integration

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"testing"

	"github.com/carvebloom/authova/internal/application"
)

func TestProtoInventoryHTTP(t *testing.T) {
	agentDatabases(t, func(t *testing.T, f *fixture) {
		request := func(method, token, key string, body []byte, suffix ...string) (int, map[string]json.RawMessage) {
			t.Helper()
			path := f.server.URL + "/api/v1/apps"
			if len(suffix) > 0 {
				path += suffix[0]
			}
			r, err := http.NewRequest(method, path, bytes.NewReader(body))
			if err != nil {
				t.Fatal(err)
			}
			if token != "" {
				r.Header.Set("Authorization", "Bearer "+token)
			}
			if key != "" {
				r.Header.Set("Idempotency-Key", key)
			}
			r.Header.Set("Content-Type", "application/json")
			response, err := f.http.Do(r)
			if err != nil {
				t.Fatal(err)
			}
			defer response.Body.Close()
			data, err := io.ReadAll(response.Body)
			if err != nil {
				t.Fatal(err)
			}
			var out map[string]json.RawMessage
			if err = json.Unmarshal(data, &out); err != nil {
				t.Fatalf("HTTP %d invalid JSON: %v", response.StatusCode, err)
			}
			return response.StatusCode, out
		}
		if status, _ := request("POST", "", "", []byte(`{`)); status != 401 {
			t.Fatal("body decode changed authentication priority", status)
		}
		if status, _ := request("POST", f.operatorToken, "key-"+application.ID(), []byte(`{"name":"Invalid","slug":"invalid","unknown":false}`)); status != 400 {
			t.Fatal("unknown field accepted", status)
		}
		key := "proto-" + application.ID()
		body, _ := json.Marshal(map[string]string{"name": "Proto management vertical slice", "slug": "proto-" + application.ID()})
		status, created := request("POST", f.operatorToken, key, body)
		if status != 201 || len(created["id"]) == 0 {
			t.Fatal("creation status/projection changed", status)
		}
		status, repeated := request("POST", f.operatorToken, key, body)
		if status != 201 || !bytes.Equal(created["id"], repeated["id"]) {
			t.Fatal("idempotent create changed", status)
		}
		if status, alias := request("POST", f.operatorToken, key, body, "/"); status != 201 || !bytes.Equal(alias["id"], created["id"]) {
			t.Fatal("trailing slash create compatibility", status)
		}
		if status, _ := request("GET", f.operatorToken, "", nil, "/"); status != 200 {
			t.Fatal("trailing slash list compatibility", status)
		}
		// Earlier acceptance cases legitimately leave more than one page of Apps.
		// Read after this fixture's first App rather than assuming an empty suite DB.
		status, listed := request("GET", f.operatorToken, "", nil, "?cursor="+f.app1.ID)
		if status != 200 || !bytes.Equal(listed["next_cursor"], []byte("null")) {
			t.Fatal("nullable page cursor changed", status)
		}
		var items []map[string]json.RawMessage
		if err := json.Unmarshal(listed["items"], &items); err != nil {
			t.Fatal(err)
		}
		found := false
		for _, item := range items {
			if bytes.Equal(item["id"], created["id"]) {
				found = true
			}
		}
		if !found {
			t.Fatal("committed App missing from listing")
		}
		appToken := f.managementToken()
		if status, _ := request("POST", appToken, key, []byte(`{`)); status != 403 {
			t.Fatal("App credential escaped platform boundary or body priority", status)
		}
		status, restricted := request("GET", appToken, "", nil)
		if status != 200 {
			t.Fatal(status)
		}
		if err := json.Unmarshal(restricted["items"], &items); err != nil || len(items) != 1 {
			t.Fatal("App inventory boundary changed", err)
		}
		var id string
		_ = json.Unmarshal(items[0]["id"], &id)
		if id != f.app1.ID {
			t.Fatal("cross-App list")
		}
	})
}
