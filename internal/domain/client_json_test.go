package domain

import (
	"encoding/json"
	"testing"
)

func TestClientJSONArraysFollowPublicContract(t *testing.T) {
	data, err := json.Marshal(Client{SecretDigest: "private-digest-fixture"})
	if err != nil {
		t.Fatal(err)
	}
	var object map[string]json.RawMessage
	if err = json.Unmarshal(data, &object); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"redirect_uris", "allowed_scopes", "allowed_audiences", "allowed_introspection_audiences", "grant_types"} {
		if string(object[name]) != "[]" {
			t.Fatal("contract array became nullable", name)
		}
	}
	if _, present := object["secret_digest"]; present {
		t.Fatal("private client credential serialized")
	}
}
