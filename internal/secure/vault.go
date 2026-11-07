package secure

import (
	"bytes"
	"crypto/aes"
	"crypto/cipher"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"regexp"
)

// The lookup root stays stable across encryption rotation. It also derives
// existing protocol/federation keys; changing it requires a separate migration.
type Vault struct {
	key        [32]byte
	active     string
	encryption map[string][32]byte
	legacy     *[32]byte
}

var ciphertextMagic = []byte("AUTHOVA\x00\x02")
var keyIDPattern = regexp.MustCompile(`^[a-zA-Z0-9_-]{1,32}$`)

func NewVault(key []byte) (*Vault, error) {
	return NewVaultKeyring(key, "initial", map[string][]byte{"initial": key}, key)
}
func NewVaultKeyring(lookup []byte, active string, encryption map[string][]byte, legacy []byte) (*Vault, error) {
	if len(lookup) != 32 || !keyIDPattern.MatchString(active) || len(encryption) < 1 || len(encryption) > 16 {
		return nil, errors.New("invalid vault keyring")
	}
	v := &Vault{active: active, encryption: make(map[string][32]byte)}
	copy(v.key[:], lookup)
	for id, key := range encryption {
		if !keyIDPattern.MatchString(id) || len(key) != 32 {
			return nil, errors.New("invalid encryption key")
		}
		var value [32]byte
		copy(value[:], key)
		v.encryption[id] = value
	}
	if _, ok := v.encryption[active]; !ok {
		return nil, errors.New("active encryption key missing")
	}
	if len(legacy) != 0 {
		if len(legacy) != 32 {
			return nil, errors.New("invalid legacy key")
		}
		var key [32]byte
		copy(key[:], legacy)
		v.legacy = &key
	}
	return v, nil
}
func derive(key [32]byte, purpose string) [32]byte {
	m := hmac.New(sha256.New, key[:])
	m.Write([]byte("authova:v1:" + purpose))
	var out [32]byte
	copy(out[:], m.Sum(nil))
	return out
}
func (v *Vault) Derive(purpose string) [32]byte { return derive(v.key, purpose) }
func (v *Vault) Digest(purpose, value string) string {
	k := v.Derive(purpose)
	m := hmac.New(sha256.New, k[:])
	m.Write([]byte(value))
	return hex.EncodeToString(m.Sum(nil))
}
func gcm(key [32]byte, purpose string) (cipher.AEAD, error) {
	k := derive(key, purpose)
	block, err := aes.NewCipher(k[:])
	if err != nil {
		return nil, err
	}
	return cipher.NewGCM(block)
}
func (v *Vault) ActiveKeyID() string { return v.active }

// CiphertextKeyID is only a format inspection. Authentication happens in Open.
// An empty ID denotes the unversioned legacy format.
func CiphertextKeyID(data []byte) (string, error) {
	if !bytes.HasPrefix(data, ciphertextMagic) {
		return "", nil
	}
	n := len(ciphertextMagic)
	if len(data) <= n || int(data[n]) < 1 || int(data[n]) > 32 || len(data) < n+1+int(data[n])+28 {
		return "", errors.New("invalid ciphertext envelope")
	}
	id := string(data[n+1 : n+1+int(data[n])])
	if !keyIDPattern.MatchString(id) {
		return "", errors.New("invalid ciphertext key ID")
	}
	return id, nil
}
func (v *Vault) Seal(purpose string, plain []byte) ([]byte, error) {
	g, err := gcm(v.encryption[v.active], purpose)
	if err != nil {
		return nil, err
	}
	header := append(append([]byte{}, ciphertextMagic...), byte(len(v.active)))
	header = append(header, []byte(v.active)...)
	nonce := make([]byte, g.NonceSize())
	if _, err = rand.Read(nonce); err != nil {
		return nil, err
	}
	aad := append(append([]byte(purpose), 0), header...)
	return g.Seal(append(header, nonce...), nonce, plain, aad), nil
}
func (v *Vault) Open(purpose string, data []byte) ([]byte, error) {
	id, err := CiphertextKeyID(data)
	if err != nil {
		return nil, err
	}
	var key [32]byte
	aad := []byte(purpose)
	if id == "" {
		if v.legacy == nil {
			return nil, errors.New("legacy encryption key unavailable")
		}
		key = *v.legacy
	} else {
		var ok bool
		key, ok = v.encryption[id]
		if !ok {
			return nil, errors.New("encryption key unavailable")
		}
		n := len(ciphertextMagic) + 1 + len(id)
		aad = append(append([]byte(purpose), 0), data[:n]...)
		data = data[n:]
	}
	g, err := gcm(key, purpose)
	if err != nil {
		return nil, err
	}
	if len(data) < g.NonceSize()+g.Overhead() {
		return nil, errors.New("invalid ciphertext")
	}
	return g.Open(nil, data[:g.NonceSize()], data[g.NonceSize():], aad)
}
