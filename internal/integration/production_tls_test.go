package integration

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"os"
	"strings"
	"testing"
)

func TestProductionTLS(t *testing.T) {
	configured := false
	for _, driver := range []string{"postgres", "mysql"} {
		dsn := os.Getenv("AUTHOVA_TEST_" + strings.ToUpper(driver) + "_DSN")
		if dsn == "" {
			continue
		}
		configured = true
		t.Run(driver, func(t *testing.T) {
			f := setupTransport(t, driver, dsn, true)
			response, err := f.http.Get(f.app1.Issuer + "/.well-known/openid-configuration")
			if err != nil {
				t.Fatal(err)
			}
			defer response.Body.Close()
			if response.TLS == nil || len(response.TLS.VerifiedChains) == 0 {
				t.Fatal("TLS certificate chain was not verified")
			}
			var metadata map[string]any
			if json.NewDecoder(response.Body).Decode(&metadata) != nil || metadata["issuer"] != f.app1.Issuer {
				t.Fatal("HTTPS issuer mismatch")
			}
			captured := &cookieCapture{next: f.http.Transport}
			f.http.Transport = captured
			code, verifier := f.code()
			status, tokens := f.exchange(code, verifier)
			if status != 200 || !f.active(tokens.Access) {
				t.Fatal("production-mode TLS authorization failed")
			}
			response, err = f.http.Get(f.app1.Issuer + "/ui/account")
			if err != nil {
				t.Fatal(err)
			}
			body, _ := io.ReadAll(response.Body)
			response.Body.Close()
			csrf := csrfPattern.FindSubmatch(body)
			if len(csrf) != 2 {
				t.Fatal("secure session not accepted over HTTPS")
			}
			request, _ := http.NewRequest("POST", f.app1.Issuer+"/ui/account/password", strings.NewReader("_csrf="+string(csrf[1])+"&old_password=wrong&new_password=long+new+test+password"))
			request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
			request.Header.Set("Origin", "https://untrusted.example.test")
			response, err = f.http.Do(request)
			if err != nil {
				t.Fatal(err)
			}
			response.Body.Close()
			if response.StatusCode != 403 {
				t.Fatal("cross-origin sensitive write accepted")
			}
			// Go/browsers treat loopback as a secure origin even on HTTP. Test
			// the production host rule separately using the actual Set-Cookie flags.
			jar, _ := cookiejar.New(nil)
			secureOrigin, _ := url.Parse("https://identity.example.test/a/" + f.app1.Key)
			plainOrigin, _ := url.Parse("http://identity.example.test/a/" + f.app1.Key)
			found := false
			for _, cookie := range captured.cookies {
				if strings.HasPrefix(cookie.Name, "av_") {
					if !cookie.Secure || !cookie.HttpOnly {
						t.Fatal("production cookie lacks Secure/HttpOnly")
					}
					jar.SetCookies(secureOrigin, []*http.Cookie{cookie})
					found = true
				}
			}
			if !found || len(jar.Cookies(plainOrigin)) != 0 {
				t.Fatal("secure cookie would be sent to a non-loopback plaintext origin")
			}

		})
	}
	if !configured {
		t.Skip("real databases required")
	}
}

type cookieCapture struct {
	next    http.RoundTripper
	cookies []*http.Cookie
}

func (c *cookieCapture) RoundTrip(request *http.Request) (*http.Response, error) {
	response, err := c.next.RoundTrip(request)
	if err == nil {
		c.cookies = append(c.cookies, response.Cookies()...)
	}
	return response, err
}
