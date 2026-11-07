// Package secure holds purpose-separated cryptographic primitives.
package secure

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha1" // TOTP interoperates with RFC 6238 SHA-1 authenticators.
	"crypto/subtle"
	"encoding/base32"
	"encoding/base64"
	"encoding/binary"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"golang.org/x/crypto/argon2"
)

func Random(n int) string {
	b := make([]byte, n)
	if _, e := rand.Read(b); e != nil {
		panic(e)
	}
	return base64.RawURLEncoding.EncodeToString(b)
}
func Equal(a, b string) bool { return subtle.ConstantTimeCompare([]byte(a), []byte(b)) == 1 }
func Normalize(kind, value string) (string, error) {
	v := strings.TrimSpace(value)
	switch kind {
	case "email":
		if len(v) > 254 || strings.Count(v, "@") != 1 || strings.ContainsAny(v, "\r\n\x00 ") {
			return "", errors.New("invalid email")
		}
		parts := strings.SplitN(v, "@", 2)
		if parts[0] == "" || !strings.Contains(parts[1], ".") {
			return "", errors.New("invalid email")
		}
		return strings.ToLower(v), nil
	case "phone":
		if len(v) < 8 || len(v) > 16 || v[0] != '+' || v[1] == '0' {
			return "", errors.New("phone must be E.164")
		}
		for _, c := range v[1:] {
			if c < '0' || c > '9' {
				return "", errors.New("phone must be E.164")
			}
		}
		return v, nil
	default:
		return "", errors.New("invalid identifier kind")
	}
}
func ValidPassword(p string) bool { return utf8.RuneCountInString(p) >= 15 && len(p) <= 1024 }
func HashPassword(p string) (string, error) {
	if !ValidPassword(p) {
		return "", errors.New("password must contain at least 15 characters")
	}
	salt := make([]byte, 16)
	if _, e := rand.Read(salt); e != nil {
		return "", e
	}
	h := argon2.IDKey([]byte(p), salt, 2, 64*1024, 1, 32)
	return fmt.Sprintf("$argon2id$v=19$m=65536,t=2,p=1$%s$%s", base64.RawStdEncoding.EncodeToString(salt), base64.RawStdEncoding.EncodeToString(h)), nil
}
func CheckPassword(p, encoded string) bool {
	if len(p) > 1024 {
		return false
	}
	parts := strings.Split(encoded, "$")
	if len(parts) != 6 || parts[1] != "argon2id" || parts[2] != "v=19" || parts[3] != "m=65536,t=2,p=1" {
		return false
	}
	s, e := base64.RawStdEncoding.DecodeString(parts[4])
	if e != nil || len(s) != 16 {
		return false
	}
	h, e := base64.RawStdEncoding.DecodeString(parts[5])
	if e != nil || len(h) != 32 {
		return false
	}
	got := argon2.IDKey([]byte(p), s, 2, 64*1024, 1, 32)
	return subtle.ConstantTimeCompare(got, h) == 1
}
func NewTOTP() string {
	b := make([]byte, 20)
	if _, e := rand.Read(b); e != nil {
		panic(e)
	}
	return base32.StdEncoding.WithPadding(base32.NoPadding).EncodeToString(b)
}

// ValidTOTPSecret requires an unpadded Base32 key with at least 160 bits.
func ValidTOTPSecret(secret string) bool {
	key, err := base32.StdEncoding.WithPadding(base32.NoPadding).DecodeString(secret)
	return err == nil && len(key) >= 20 && len(key) <= 64
}
func TOTP(secret string, step int64) string {
	if step < 0 || !ValidTOTPSecret(secret) {
		return ""
	}
	key, e := base32.StdEncoding.WithPadding(base32.NoPadding).DecodeString(secret)
	if e != nil {
		return ""
	}
	var b [8]byte
	binary.BigEndian.PutUint64(b[:], uint64(step))
	m := hmac.New(sha1.New, key)
	m.Write(b[:])
	h := m.Sum(nil)
	o := h[len(h)-1] & 15
	v := binary.BigEndian.Uint32(h[o:o+4]) & 0x7fffffff
	return fmt.Sprintf("%06d", v%1000000)
}
func VerifyTOTP(secret, code string, last int64, now time.Time) (int64, bool) {
	if len(code) != 6 {
		return 0, false
	}
	if _, e := strconv.Atoi(code); e != nil {
		return 0, false
	}
	step := now.Unix() / 30
	for _, d := range []int64{0, -1, 1} {
		s := step + d
		if s > last && Equal(TOTP(secret, s), code) {
			return s, true
		}
	}
	return 0, false
}
func OTP() string {
	var b [4]byte
	for {
		if _, e := rand.Read(b[:]); e != nil {
			panic(e)
		}
		v := binary.BigEndian.Uint32(b[:])
		if v < 4294000000 {
			return fmt.Sprintf("%06d", v%1000000)
		}
	}
}
func Mask(value string) string {
	if i := strings.IndexByte(value, '@'); i > 0 {
		return value[:1] + "***" + value[i:]
	}
	if len(value) > 4 {
		return "***" + value[len(value)-4:]
	}
	return "***"
}
