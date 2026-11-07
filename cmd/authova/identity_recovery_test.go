package main

import (
	"strings"
	"testing"
)

func TestPrivateRecoveryInputBoundary(t *testing.T) {
	for _, tc := range []struct {
		name, body string
		valid      bool
	}{
		{"object", `{"confirm":"approved"}`, true},
		{"unknown", `{"confirm":"approved","unexpected":true}`, false},
		{"two objects", `{"confirm":"approved"}{"confirm":"other"}`, false},
		{"null", `null`, false},
		{"array", `[]`, false},
		{"oversize trailing", `{"confirm":"approved"}` + strings.Repeat(" ", 1024*1024) + `{}`, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var value struct {
				Confirm string `json:"confirm"`
			}
			err := decodePrivateInput(strings.NewReader(tc.body), &value)
			if (err == nil) != tc.valid {
				t.Fatalf("accepted=%v want=%v", err == nil, tc.valid)
			}
		})
	}
}
