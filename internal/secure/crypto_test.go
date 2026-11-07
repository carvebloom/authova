package secure

import (
	"bytes"
	"net"
	"testing"
	"time"
)

func TestVaultIsolationAndTamper(t *testing.T) {
	v, e := NewVault(bytes.Repeat([]byte{7}, 32))
	if e != nil {
		t.Fatal(e)
	}
	cipher, e := v.Seal("app1:user", []byte("private"))
	if e != nil {
		t.Fatal(e)
	}
	if _, e = v.Open("app2:user", cipher); e == nil {
		t.Fatal("cross-App ciphertext accepted")
	}
	cipher[len(cipher)-1] ^= 1
	if _, e = v.Open("app1:user", cipher); e == nil {
		t.Fatal("tampered ciphertext accepted")
	}
	if v.Digest("app1:email", "a@b.com") == v.Digest("app2:email", "a@b.com") {
		t.Fatal("digest lacks App separation")
	}
}
func TestPasswordAndTOTP(t *testing.T) {
	hash, e := HashPassword("a long test password phrase")
	if e != nil {
		t.Fatal(e)
	}
	if !CheckPassword("a long test password phrase", hash) || CheckPassword("wrong password", hash) {
		t.Fatal("password verification")
	}
	if _, e = HashPassword("short"); e == nil {
		t.Fatal("short password accepted")
	}
	now := time.Unix(1700000000, 0)
	secret := NewTOTP()
	if !ValidTOTPSecret(secret) || ValidTOTPSecret("not-a-base32-key") || ValidTOTPSecret("AAAA") {
		t.Fatal("invalid provisioning secret accepted")
	}
	step, ok := VerifyTOTP(secret, TOTP(secret, now.Unix()/30), 0, now)
	if !ok {
		t.Fatal("TOTP rejected")
	}
	if _, ok = VerifyTOTP(secret, TOTP(secret, step), step, now); ok {
		t.Fatal("TOTP replay accepted")
	}
}
func TestPublicAddresses(t *testing.T) {
	for _, raw := range []string{"127.0.0.1", "10.0.0.1", "169.254.169.254", "100.64.0.1", "::1", "fc00::1", "224.0.0.1"} {
		if PublicIP(net.ParseIP(raw)) {
			t.Fatalf("SSRF address accepted: %s", raw)
		}
	}
	if !PublicIP(net.ParseIP("8.8.8.8")) {
		t.Fatal("public address rejected")
	}
}

func TestPhoneCanonicalE164(t *testing.T) {
	for _, phone := range []string{"13800138000", "+013800138000", "+86 13800138000", "+86123456789012345", "+123"} {
		if _, err := Normalize("phone", phone); err == nil {
			t.Fatal("invalid phone accepted", phone)
		}
	}
	if phone, err := Normalize("phone", " +8613800138000 "); err != nil || phone != "+8613800138000" {
		t.Fatal("valid phone rejected", err)
	}
}
