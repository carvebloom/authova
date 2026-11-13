package diagnostic

import (
	"github.com/google/uuid"
	"net/http"
)

// HTTPGuard protects setup/raw-handler panics before application middleware is
// active. No default recovery sits inside it. Application handlers may provide
// a richer pre-write response; this last guard safely aborts an uncertain stream.
func HTTPGuard(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer func() {
			if v := recover(); v != nil {
				if v == http.ErrAbortHandler {
					panic(v)
				}
				Panic(r.Context(), "http", v)
				panic(http.ErrAbortHandler)
			}
		}()
		r = r.WithContext(WithRequest(r.Context(), uuid.NewString()))
		w.Header().Set("X-Request-ID", RequestID(r.Context()))
		next.ServeHTTP(w, r)
	})
}
