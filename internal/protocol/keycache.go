package protocol

import (
	"context"
	"crypto/rsa"
	"crypto/x509"
	"encoding/json"
	"strconv"
	"sync"
	"time"

	"github.com/carvebloom/authova/internal/domain"
	"github.com/carvebloom/authova/internal/ports"
	jose "github.com/go-jose/go-jose/v4"
)

type verificationKey struct {
	public  *rsa.PublicKey
	expires time.Time
	retire  int64
	current bool
}
type verificationSet struct {
	keys    map[string]verificationKey
	expires time.Time
}

type verificationCache struct {
	mu    sync.Mutex
	sets  map[string]verificationSet
	loads map[string]*verificationLoad
}

type verificationLoad struct {
	done chan struct{}
	set  verificationSet
	err  error
}

func usableVerificationKey(key verificationKey) bool {
	now := time.Now()
	return key.public != nil && key.expires.After(now) && (key.current || key.retire > now.Unix())
}

func keyFromSet(set verificationSet, id string) (*rsa.PublicKey, error) {
	if key := set.keys[id]; usableVerificationKey(key) {
		return key.public, nil
	}
	return nil, domain.ErrUnauthenticated
}

func (p *Provider) verificationKey(ctx context.Context, id string, epoch int64) (*rsa.PublicKey, error) {
	// A complete set also answers unknown kids. Grouping by the primary epoch
	// permits different untrusted kids to join the same bounded in-flight load.
	scopeKey := req(ctx).App.ID + ":" + strconv.FormatInt(epoch, 10)
	p.cache.mu.Lock()
	if set, ok := p.cache.sets[scopeKey]; ok && set.expires.After(time.Now()) {
		p.cache.mu.Unlock()
		return keyFromSet(set, id)
	}
	if load := p.cache.loads[scopeKey]; load != nil {
		p.cache.mu.Unlock()
		select {
		case <-load.done:
			if load.err != nil {
				return nil, dependency(ctx, load.err)
			}
			return keyFromSet(load.set, id)
		case <-ctx.Done():
			return nil, dependency(ctx, ctx.Err())
		}
	}
	if len(p.cache.loads) >= 2048 {
		p.cache.mu.Unlock()
		return nil, dependency(ctx, domain.ErrUnavailable)
	}
	if p.cache.loads == nil {
		p.cache.loads = map[string]*verificationLoad{}
	}
	load := &verificationLoad{done: make(chan struct{})}
	p.cache.loads[scopeKey] = load
	p.cache.mu.Unlock()
	keys, err := p.loadVerificationKeys(ctx)
	now := time.Now()
	set := verificationSet{keys: map[string]verificationKey{}, expires: now.Add(time.Minute)}
	if err == nil {
		for _, key := range keys {
			if key.RevokedAt != 0 || (!key.Current && key.Retire <= now.Unix()) {
				continue
			}
			if len(set.keys) >= 2048 {
				err = domain.ErrUnavailable
				break
			}
			private, e := x509.ParsePKCS1PrivateKey(key.Private)
			if e != nil {
				err = domain.ErrUnavailable
				break
			}
			set.keys[key.ID] = verificationKey{public: &private.PublicKey, current: key.Current, retire: key.Retire, expires: set.expires}
		}
	}
	p.cache.mu.Lock()
	if err == nil {
		if p.cache.sets == nil {
			p.cache.sets = map[string]verificationSet{}
		}
		delete(p.cache.sets, scopeKey)
		count := len(set.keys)
		for _, old := range p.cache.sets {
			count += len(old.keys)
		}
		for name, old := range p.cache.sets {
			if !old.expires.After(now) || count > 2048 || len(p.cache.sets) >= 2048 {
				count -= len(old.keys)
				delete(p.cache.sets, name)
			}
		}
		p.cache.sets[scopeKey] = set
		load.set = set
	}
	load.err = err
	delete(p.cache.loads, scopeKey)
	close(load.done)
	p.cache.mu.Unlock()
	if err != nil {
		return nil, dependency(ctx, err)
	}
	return keyFromSet(set, id)
}

func (p *Provider) loadVerificationKeys(ctx context.Context) ([]domain.SigningKey, error) {
	if req(ctx).Tx != nil {
		return req(ctx).Tx.VerificationKeys()
	}
	if p.service == nil {
		return nil, domain.ErrUnavailable
	}
	scope, err := domain.AppScope(req(ctx).App.ID)
	if err != nil {
		return nil, err
	}
	var keys []domain.SigningKey
	err = p.service.Store.App(ctx, scope, false, func(tx ports.AppTx) error {
		var e error
		keys, e = tx.VerificationKeys()
		return e
	})
	return keys, err
}

// Reuse the request's bounded parse; the unverified payload was only a lookup hint.
// Trust claims exclusively after RSA verification and all issuer/claim checks.
func (p *Provider) cachedVerify(ctx context.Context, signed *jose.JSONWebSignature, epoch int64) (accessClaims, error) {
	var claims accessClaims
	if signed == nil || len(signed.Signatures) != 1 {
		return claims, domain.ErrUnauthenticated
	}
	header := signed.Signatures[0].Header
	if header.Algorithm != string(jose.RS256) || header.ExtraHeaders[jose.HeaderType] != "at+jwt" || header.KeyID == "" {
		return claims, domain.ErrUnauthenticated
	}
	key, e := p.verificationKey(ctx, header.KeyID, epoch)
	if e != nil {
		return claims, e
	}
	payload, e := signed.Verify(key)
	if e != nil {
		return claims, domain.ErrUnauthenticated
	}
	if json.Unmarshal(payload, &claims) != nil || claims.Issuer != req(ctx).App.Issuer || claims.Expires <= time.Now().Unix() || claims.Issued > time.Now().Add(30*time.Second).Unix() || (claims.Principal != "user" && claims.Principal != "client") || claims.ClientID == "" || claims.Subject == "" || claims.ID == "" || len(claims.Audience) == 0 {
		return claims, domain.ErrUnauthenticated
	}
	return claims, nil
}
