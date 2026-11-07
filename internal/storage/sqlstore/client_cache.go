package sqlstore

import (
	"crypto/sha256"
	"encoding/hex"

	"github.com/carvebloom/authova/internal/domain"
)

type parsedClient struct {
	value domain.Client
	bytes int
}

// Only parsing is cached. Every request still fetches the current ciphertext
// from the primary snapshot; changed permissions/secrets produce a new key.
func (t *tx) introspectionClient(data []byte) (domain.Client, error) {
	digest := sha256.Sum256(data)
	key := t.app + ":" + hex.EncodeToString(digest[:])
	s := t.store
	s.clientCacheMu.Lock()
	entry, exists := s.clientCache[key]
	s.clientCacheMu.Unlock()
	if exists {
		return entry.value, nil
	}
	var client domain.Client
	if err := t.open("client", data, &client); err != nil {
		return client, err
	}
	s.clientCacheMu.Lock()
	defer s.clientCacheMu.Unlock()
	if s.clientCache == nil {
		s.clientCache = make(map[string]parsedClient)
	}
	if _, exists := s.clientCache[key]; !exists && len(data) <= 1024*1024 {
		for len(s.clientCache) >= 1024 || s.clientCacheBytes+len(data) > 1024*1024 {
			for old, value := range s.clientCache {
				delete(s.clientCache, old)
				s.clientCacheBytes -= value.bytes
				break
			}
		}
		s.clientCache[key] = parsedClient{value: client, bytes: len(data)}
		s.clientCacheBytes += len(data)
	}
	return client, nil
}
