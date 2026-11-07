package secure

import "testing"

func TestTOTPRejectsInvalidKeyAndNegativeCounter(t *testing.T) {
	if TOTP(NewTOTP(), -1) != "" {
		t.Fatal("negative counter accepted")
	}
	for _, secret := range []string{"", "INVALID!", "AAAA"} {
		if TOTP(secret, 1) != "" {
			t.Fatal("invalid/weak secret accepted")
		}
	}
	if len(TOTP(NewTOTP(), 1)) != 6 {
		t.Fatal("valid TOTP rejected")
	}
}
