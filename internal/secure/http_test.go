package secure

import (
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
)

func TestUpstreamRedirectAndBodyLimits(t *testing.T) {
	var followed atomic.Int64
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { followed.Add(1); w.Write([]byte("unexpected")) }))
	defer target.Close()
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/redirect" {
			http.Redirect(w, r, target.URL, http.StatusTemporaryRedirect)
			return
		}
		w.WriteHeader(200)
		w.(http.Flusher).Flush()
		io.Copy(w, strings.NewReader(strings.Repeat("x", int(upstreamBodyLimit)+2)))
	}))
	defer upstream.Close()
	client := PublicHTTPClient(true)
	defer client.CloseIdleConnections()
	response, err := client.Get(upstream.URL + "/redirect")
	if err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	if response.StatusCode != 307 || followed.Load() != 0 {
		t.Fatal("provider redirect was followed")
	}
	response, err = client.Get(upstream.URL + "/large")
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	data, err := io.ReadAll(response.Body)
	if err == nil || int64(len(data)) > upstreamBodyLimit+1 {
		t.Fatal("unbounded provider response accepted")
	}
}

func TestSpecialAddressDeny(t *testing.T) {
	for _, raw := range []string{"not-an-ip", "64:ff9b::7f00:1", "2002:7f00:1::", "2001::7f00:1", "::ffff:127.0.0.1"} {
		if PublicIP(net.ParseIP(raw)) {
			t.Fatal("special/invalid address accepted", raw)
		}
	}
}
