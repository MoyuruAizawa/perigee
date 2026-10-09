package github

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"
)

type transport func(*http.Request) (*http.Response, error)

func (f transport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }
func response(status int, body string) *http.Response {
	return &http.Response{StatusCode: status, Body: io.NopCloser(strings.NewReader(body)), Header: http.Header{}}
}
func TestJWTAndTokenCaching(t *testing.T) {
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	installationCalls, registrationCalls := 0, 0
	c := &Client{Key: key, Org: "ExampleOrg", AppID: 1, InstallationID: 2, BaseURL: "https://api.example.test"}
	c.HTTP = &http.Client{Transport: transport(func(r *http.Request) (*http.Response, error) {
		if strings.Contains(r.URL.Path, "/app/installations/") {
			installationCalls++
			parts := strings.Split(strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer "), ".")
			if len(parts) != 3 {
				t.Fatal("invalid JWT")
			}
			claims, err := base64.RawURLEncoding.DecodeString(parts[1])
			if err != nil {
				t.Fatal(err)
			}
			var p map[string]any
			json.Unmarshal(claims, &p)
			if p["iss"] != "1" {
				t.Fatal(p)
			}
			return response(201, fmt.Sprintf(`{"token":"installation-secret","expires_at":%q}`, time.Now().Add(time.Hour).Format(time.RFC3339))), nil
		}
		registrationCalls++
		if r.Header.Get("Authorization") != "Bearer installation-secret" {
			t.Fatal("wrong token")
		}
		return response(201, `{"token":"registration-secret"}`), nil
	})}
	for i := 0; i < 2; i++ {
		token, err := c.RegistrationToken(context.Background())
		if err != nil || token != "registration-secret" {
			t.Fatalf("%s %v", token, err)
		}
	}
	if installationCalls != 1 || registrationCalls != 2 {
		t.Fatal("token cache or fresh registration failed")
	}
}
func TestOwnedDeletionPaginationAndRedaction(t *testing.T) {
	var calls []string
	c := &Client{Org: "org", BaseURL: "https://api.example.test", token: "cached-secret", expires: time.Now().Add(time.Hour)}
	c.HTTP = &http.Client{Transport: transport(func(r *http.Request) (*http.Response, error) {
		calls = append(calls, r.Method+" "+r.URL.RequestURI())
		if r.Method == "DELETE" {
			return response(404, "already gone"), nil
		}
		if r.URL.Query().Get("page") == "1" {
			rows := []string{`{"id":1,"name":"owned","labels":[{"name":"perigee-owner"}]}`, `{"id":2,"name":"foreign","labels":[]}`, `{"id":3,"name":"live","labels":[{"name":"perigee-owner"}]}`}
			for len(rows) < 100 {
				rows = append(rows, `{"id":99,"name":"other","labels":[]}`)
			}
			return response(200, `{"total_count":101,"runners":[`+strings.Join(rows, ",")+`]}`), nil
		}
		return response(200, `{"total_count":101,"runners":[{"id":4,"name":"owned-two","labels":[{"name":"perigee-owner"}]}]}`), nil
	})}
	if err := c.DeleteOwned(context.Background(), "perigee-owner", nil, map[string]bool{"live": true}); err != nil {
		t.Fatal(err)
	}
	if len(calls) != 4 || !strings.HasPrefix(calls[1], "GET") || !strings.HasSuffix(calls[2], "/1") || !strings.HasSuffix(calls[3], "/4") {
		t.Fatal(calls)
	}
	c.HTTP.Transport = transport(func(*http.Request) (*http.Response, error) { return response(403, `{"token":"cached-secret"}`), nil })
	_, err := c.RegistrationToken(context.Background())
	if err == nil || strings.Contains(err.Error(), "cached-secret") {
		t.Fatal("response leaked secret")
	}
}
func TestExactNamesAndOwnerLabel(t *testing.T) {
	deleted := []string{}
	c := &Client{Org: "org", BaseURL: "https://api.example.test", token: "token", expires: time.Now().Add(time.Hour)}
	c.HTTP = &http.Client{Transport: transport(func(r *http.Request) (*http.Response, error) {
		if r.Method == "DELETE" {
			deleted = append(deleted, r.URL.Path)
			return response(204, ""), nil
		}
		return response(200, `{"total_count":3,"runners":[{"id":1,"name":"target","labels":[]},{"id":2,"name":"target","labels":[{"name":"perigee-owner"}]},{"id":3,"name":"other","labels":[{"name":"perigee-owner"}]}]}`), nil
	})}
	if err := c.DeleteOwned(context.Background(), "perigee-owner", map[string]bool{"target": true}, nil); err != nil {
		t.Fatal(err)
	}
	if len(deleted) != 1 || !strings.HasSuffix(deleted[0], "/2") {
		t.Fatal(deleted)
	}
}
