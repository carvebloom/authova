package secure

import (
	"bytes"
	"testing"
)

func TestEncryptionRotationPreservesLookupsAndAuthenticatesHeader(t *testing.T) {
	old := bytes.Repeat([]byte{1}, 32)
	next := bytes.Repeat([]byte{2}, 32)
	original, _ := NewVault(old)
	// A genuine pre-envelope ciphertext, using the historical derivation/AAD.
	g, _ := gcm(original.key, "app:user")
	nonce := bytes.Repeat([]byte{3}, 12)
	legacy := g.Seal(append([]byte{}, nonce...), nonce, []byte("private"), []byte("app:user"))
	first, err := original.Seal("app:user", []byte("private"))
	if err != nil {
		t.Fatal(err)
	}
	rotated, err := NewVaultKeyring(old, "second", map[string][]byte{"initial": old, "second": next}, old)
	if err != nil {
		t.Fatal(err)
	}
	if rotated.Digest("app:identifier", "same") != original.Digest("app:identifier", "same") || rotated.Derive("federation") != original.Derive("federation") {
		t.Fatal("encryption rotation changed stable lookups")
	}
	for _, data := range [][]byte{legacy, first} {
		if p, e := rotated.Open("app:user", data); e != nil || string(p) != "private" {
			t.Fatal("legacy decode failed")
		}
	}
	current, err := rotated.Seal("app:user", []byte("private"))
	if err != nil {
		t.Fatal(err)
	}
	retired, _ := NewVaultKeyring(old, "second", map[string][]byte{"second": next}, nil)
	if _, e := retired.Open("app:user", current); e != nil {
		t.Fatal(e)
	}
	for _, data := range [][]byte{legacy, first} {
		if _, e := retired.Open("app:user", data); e == nil {
			t.Fatal("removed encryption key accepted")
		}
	}
	tampered := bytes.Clone(current)
	tampered[len(ciphertextMagic)+1] = 'X'
	if _, e := rotated.Open("app:user", tampered); e == nil {
		t.Fatal("tampered key ID accepted")
	}
	if _, e := rotated.Open("other:user", current); e == nil {
		t.Fatal("wrong scope accepted")
	}
}
