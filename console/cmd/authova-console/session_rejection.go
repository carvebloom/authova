package main

import (
	"context"
	"encoding/json"
	"log/slog"

	"github.com/valkey-io/valkey-go"
)

// Remove only the rejected credential generation. Do not expire the browser's
// cookie here: a delayed 401 may arrive after a different login has already
// installed a new cookie. App refresh can also replace a token under the same
// cache key, so deletion must be fenced against that newer session value.
func (b *bff) invalidateRejectedSession(ctx context.Context, id string, rejected session) (err error) {
	defer func() {
		if err != nil {
			slog.Warn("console rejected-session cleanup unavailable")
		}
	}()
	key := "authova:console:" + id
	raw, err := b.cache.Do(ctx, b.cache.B().Get().Key(key).Build()).AsBytes()
	if valkey.IsValkeyNil(err) {
		return nil
	}
	if err != nil {
		return err
	}
	plain, err := b.crypt(raw, false)
	if err != nil {
		return err
	}
	var current session
	if err = json.Unmarshal(plain, &current); err != nil {
		return err
	}
	if current.Token != rejected.Token || current.CSRF != rejected.CSRF || current.RefreshPending != rejected.RefreshPending {
		return nil
	}
	return b.cache.Do(ctx, b.cache.B().Eval().Script("if redis.call('GET',KEYS[1])==ARGV[1] then redis.call('DEL',KEYS[1]) end; return 1").Numkeys(1).Key(key).Arg(string(raw)).Build()).Error()
}
