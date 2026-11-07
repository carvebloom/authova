package config

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestKeyringConfiguration(t *testing.T) {
	dir := t.TempDir()
	old := filepath.Join(dir, "old")
	next := filepath.Join(dir, "next")
	spec := filepath.Join(dir, "ring.json")
	if e := os.WriteFile(old, bytes.Repeat([]byte{1}, 32), 0600); e != nil {
		t.Fatal(e)
	}
	if e := os.WriteFile(next, bytes.Repeat([]byte{2}, 32), 0600); e != nil {
		t.Fatal(e)
	}
	data, _ := json.Marshal(map[string]any{"active": "next", "keys": map[string]string{"initial": old, "next": next}, "legacy_key_file": old})
	if e := os.WriteFile(spec, data, 0600); e != nil {
		t.Fatal(e)
	}
	c := Config{MasterKey: bytes.Repeat([]byte{1}, 32), EncryptionKeyringFile: spec}
	vault, e := c.Vault()
	if e != nil || vault.ActiveKeyID() != "next" {
		t.Fatal("keyring not applied", e)
	}
	for _, bad := range []string{`{"active":"missing","keys":{}}`, string(data) + `{}`, `{"active":"next","unexpected":"secret"}`} {
		if e := os.WriteFile(spec, []byte(bad), 0600); e != nil {
			t.Fatal(e)
		}
		if _, e = c.Vault(); e == nil {
			t.Fatal("bad keyring accepted")
		}
	}
}
