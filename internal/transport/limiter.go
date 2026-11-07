package transport

import (
	"context"
	"errors"
	"strconv"
	"sync"
	"time"

	"github.com/carvebloom/authova/internal/valkeyconfig"
	"github.com/valkey-io/valkey-go"
)

type rateWindow struct {
	start time.Time
	count int
}

// LocalLimiter is deliberately restricted to development single-process deployments.
type LocalLimiter struct {
	mu    sync.Mutex
	items map[string]rateWindow
}

func NewLocalLimiter() *LocalLimiter { return &LocalLimiter{items: map[string]rateWindow{}} }
func (l *LocalLimiter) Allow(ctx context.Context, key string, max int, duration time.Duration) (bool, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	now := time.Now()
	if len(l.items) > 20000 {
		for k, v := range l.items {
			if now.Sub(v.start) > time.Hour {
				delete(l.items, k)
			}
		}
		if len(l.items) > 20000 {
			return false, errors.New("rate limiter capacity reached")
		}
	}
	v := l.items[key]
	if now.Sub(v.start) >= duration {
		v = rateWindow{start: now}
	}
	v.count++
	l.items[key] = v
	return v.count <= max, nil
}

type ValkeyLimiter struct{ client valkey.Client }

func NewValkeyLimiter(config valkeyconfig.Config) (*ValkeyLimiter, error) {
	options, e := config.Options()
	if e != nil {
		return nil, e
	}
	c, e := valkey.NewClient(options)
	if e != nil {
		return nil, errors.New("valkey connection failed; check transport, trust and ACL settings")
	}
	return &ValkeyLimiter{c}, nil
}
func (l *ValkeyLimiter) Close() { l.client.Close() }
func (l *ValkeyLimiter) Allow(ctx context.Context, key string, max int, duration time.Duration) (bool, error) {
	script := "local n=redis.call('INCR',KEYS[1]); if n==1 then redis.call('PEXPIRE',KEYS[1],ARGV[1]) end; return n"
	n, e := l.client.Do(ctx, l.client.B().Eval().Script(script).Numkeys(1).Key("authova:limit:"+key).Arg(strconv.FormatInt(duration.Milliseconds(), 10)).Build()).AsInt64()
	if e != nil {
		return false, e
	}
	return n <= int64(max), nil
}
