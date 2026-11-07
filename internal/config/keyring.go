package config

import (
	"encoding/json"
	"errors"
	"io"
	"os"

	"github.com/carvebloom/authova/internal/secure"
)

// Encryption key files hold raw 32-byte values. The JSON keyring contains only
// IDs and file paths, so secrets need not appear in process args or logs.
// MasterKey remains the stable lookup/protocol root, never a rotation switch.
func (c Config) Vault() (*secure.Vault, error) {
	if c.EncryptionKeyringFile == "" {
		return secure.NewVault(c.MasterKey)
	}
	file, err := os.Open(c.EncryptionKeyringFile)
	if err != nil {
		return nil, errors.New("cannot read encryption keyring")
	}
	defer file.Close()
	var spec struct {
		Active        string            `json:"active"`
		Keys          map[string]string `json:"keys"`
		LegacyKeyFile string            `json:"legacy_key_file"`
	}
	d := json.NewDecoder(io.LimitReader(file, 16385))
	d.DisallowUnknownFields()
	if err = d.Decode(&spec); err != nil {
		return nil, errors.New("invalid encryption keyring JSON")
	}
	if d.Decode(new(any)) != io.EOF {
		return nil, errors.New("invalid encryption keyring JSON suffix")
	}
	keys := make(map[string][]byte)
	if len(spec.Keys) > 16 {
		return nil, errors.New("too many encryption keys")
	}
	for id, path := range spec.Keys {
		key, e := os.ReadFile(path)
		if e != nil || len(key) != 32 {
			return nil, errors.New("cannot read 32-byte encryption key")
		}
		keys[id] = key
	}
	var legacy []byte
	if spec.LegacyKeyFile != "" {
		legacy, err = os.ReadFile(spec.LegacyKeyFile)
		if err != nil || len(legacy) != 32 {
			return nil, errors.New("cannot read legacy encryption key")
		}
	}
	return secure.NewVaultKeyring(c.MasterKey, spec.Active, keys, legacy)
}
