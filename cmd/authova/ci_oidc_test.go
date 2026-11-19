package main

import (
	"math"
	"strings"
	"testing"
)

func TestCIOIDCPolicyBeforeNetwork(t *testing.T) {
	base := ciOIDCRequest{
		Issuer:      "https://host.docker.internal:43100/a/" + strings.Repeat("a", 32),
		RedirectURI: "https://host.docker.internal:43100/ci/callback",
		ClientID:    "own-ci-client", ClientSecret: "OWN-SYNTHETIC-NOT-REAL-SECRET", Code: "own-code",
		ExpectedState: strings.Repeat("s", 32), CallbackState: strings.Repeat("s", 32), Nonce: "own-nonce",
		Verifier: strings.Repeat("v", 43), ExpectedSubject: "own-private-subject",
		FlowOrigin: 900, StateDeadline: 1200, CodeObserved: 1000, CodeDeadline: 1060,
	}
	if _, err := ciOIDCParameters(base, 1000); err != nil {
		t.Fatal("valid OWN request rejected before network")
	}
	cases := map[string]func(*ciOIDCRequest){
		"foreign-issuer":  func(r *ciOIDCRequest) { r.Issuer = "https://auth.carvebloom.localhost/a/" + strings.Repeat("a", 32) },
		"wrong-callback":  func(r *ciOIDCRequest) { r.RedirectURI = "https://host.docker.internal:43101/ci/callback" },
		"wrong-state":     func(r *ciOIDCRequest) { r.CallbackState = strings.Repeat("x", 32) },
		"state-renewed":   func(r *ciOIDCRequest) { r.StateDeadline = 1201 },
		"code-renewed":    func(r *ciOIDCRequest) { r.CodeDeadline = 1061 },
		"nonfinite":       func(r *ciOIDCRequest) { r.CodeObserved = math.Inf(1) },
		"missing-subject": func(r *ciOIDCRequest) { r.ExpectedSubject = "" },
		"short-verifier":  func(r *ciOIDCRequest) { r.Verifier = "short" },
	}
	for name, change := range cases {
		t.Run(name, func(t *testing.T) {
			r := base
			change(&r)
			if _, err := ciOIDCParameters(r, 1000); err == nil {
				t.Fatal("unsafe request accepted")
			}
		})
	}
	if _, err := ciOIDCParameters(base, 1019.999); err != nil {
		t.Fatal("remaining full40 should fit")
	}
	for _, now := range []float64{999, 1020, 1060} {
		if _, err := ciOIDCParameters(base, now); err == nil {
			t.Fatal("late/future code or insufficient full40 accepted")
		}
	}
}
