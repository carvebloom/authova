package domain

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestAuthenticationPolicyRequiresEveryBoolean(t *testing.T) {
	valid, e := json.Marshal(DefaultAuthenticationPolicy())
	if e != nil {
		t.Fatal(e)
	}
	cases := [][]byte{[]byte(`{}`), []byte(`null`), []byte(strings.Replace(string(valid), `"registration_enabled":true,`, "", 1)), []byte(strings.Replace(string(valid), `"registration_enabled":true`, `"registration_enabled":null`, 1)), []byte(strings.Replace(string(valid), `"registration_enabled":true`, `"unknown":true`, 1))}
	for _, data := range cases {
		var p AuthenticationPolicy
		if json.Unmarshal(data, &p) == nil {
			t.Fatal("incomplete/unknown policy accepted", string(data))
		}
	}
	var p AuthenticationPolicy
	if e = json.Unmarshal(valid, &p); e != nil || p != DefaultAuthenticationPolicy() {
		t.Fatal("complete policy rejected", e)
	}
}
func TestClientJSONNeverIncludesPreviousSecret(t *testing.T) {
	b, e := json.Marshal(Client{SecretDigest: "current-secret-digest", PreviousSecretDigest: "previous-secret-digest", PreviousSecretExpires: 123})
	if e != nil || strings.Contains(string(b), "secret-digest") || !strings.Contains(string(b), `"previous_secret_expires_at":123`) {
		t.Fatal("secret lifetime metadata exposed credentials", e)
	}
}
