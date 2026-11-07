package integration

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strings"
	"sync"
	"testing"

	"github.com/carvebloom/authova/internal/protocol"
	"github.com/carvebloom/authova/internal/transport"
)

func TestTwoCoreReplicas(t *testing.T) {
	configured := false
	for _, driver := range []string{"postgres", "mysql"} {
		dsn := os.Getenv("AUTHOVA_TEST_" + strings.ToUpper(driver) + "_DSN")
		if dsn == "" {
			continue
		}
		configured = true
		t.Run(driver, func(t *testing.T) {
			f := setup(t, driver, dsn)
			secondProvider, err := protocol.New(f.service)
			if err != nil {
				t.Fatal(err)
			}
			second := httptest.NewServer((&transport.Server{Service: f.service, Provider: secondProvider, Limiter: transport.NewLocalLimiter()}).Handler())
			defer second.Close()
			code, verifier := f.code()
			origins := []string{f.server.URL, second.URL}
			var results [2]int
			var issued [2]tokens
			var wait sync.WaitGroup
			for index, origin := range origins {
				wait.Add(1)
				go func(index int, origin string) {
					defer wait.Done()
					request, _ := http.NewRequest("POST", origin+"/a/"+f.app1.Key+"/oauth/token", strings.NewReader(url.Values{"grant_type": {"authorization_code"}, "code": {code}, "code_verifier": {verifier}, "redirect_uri": {f.server.URL + "/test-callback"}}.Encode()))
					request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
					request.SetBasicAuth(f.client.ClientID, f.secret)
					response, err := f.http.Do(request)
					if err != nil {
						t.Error(err)
						return
					}
					defer response.Body.Close()
					results[index] = response.StatusCode
					json.NewDecoder(response.Body).Decode(&issued[index])
				}(index, origin)
			}
			wait.Wait()
			winner := -1
			for index, status := range results {
				if status == 200 {
					if winner != -1 {
						t.Fatal("two replicas exchanged one code twice")
					}
					winner = index
				}
			}
			if winner < 0 {
				t.Fatal("no replica exchanged the code", results)
			}
			refresh := issued[winner].Refresh
			for index, origin := range origins {
				wait.Add(1)
				go func(index int, origin string) {
					defer wait.Done()
					request, _ := http.NewRequest("POST", origin+"/a/"+f.app1.Key+"/oauth/token", strings.NewReader(mapForm("grant_type", "refresh_token", "refresh_token", refresh).Encode()))
					request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
					request.SetBasicAuth(f.client.ClientID, f.secret)
					response, err := f.http.Do(request)
					if err != nil {
						t.Error(err)
						return
					}
					defer response.Body.Close()
					results[index] = response.StatusCode
					json.NewDecoder(response.Body).Decode(&issued[index])
				}(index, origin)
			}
			wait.Wait()
			success := 0
			for _, status := range results {
				if status == 200 {
					success++
				}
			}
			if success != 1 {
				t.Fatal("refresh was not atomic across replicas", results)
			}
			for index, status := range results {
				if status == 200 && f.active(issued[index].Access) {
					t.Fatal("reuse revocation not visible across replicas")
				}
			}
		})
	}
	if !configured {
		t.Skip("real database DSNs required")
	}
}
