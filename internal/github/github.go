package github

import (
	"context"
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"

	"perigee/internal/config"
)

type Client struct {
	HTTP                  *http.Client
	BaseURL               string
	Org                   string
	AppID, InstallationID int64
	Key                   *rsa.PrivateKey
	token                 string
	expires               time.Time
}
type Runner struct {
	ID     int64  `json:"id"`
	Name   string `json:"name"`
	Labels []struct {
		Name string `json:"name"`
	} `json:"labels"`
}

func (r Runner) HasLabel(label string) bool {
	for _, l := range r.Labels {
		if l.Name == label {
			return true
		}
	}
	return false
}

func New(cfg config.GitHub) (*Client, error) {
	path, err := config.ExpandHome(cfg.PEM)
	if err != nil {
		return nil, err
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read GitHub App private key: %w", err)
	}
	block, _ := pem.Decode(data)
	if block == nil {
		return nil, fmt.Errorf("invalid GitHub App PEM")
	}
	key, err := x509.ParsePKCS1PrivateKey(block.Bytes)
	if err != nil {
		parsed, err := x509.ParsePKCS8PrivateKey(block.Bytes)
		if err != nil {
			return nil, fmt.Errorf("invalid GitHub App private key")
		}
		var ok bool
		key, ok = parsed.(*rsa.PrivateKey)
		if !ok {
			return nil, fmt.Errorf("GitHub App key must be RSA")
		}
	}
	if err := key.Validate(); err != nil {
		return nil, fmt.Errorf("invalid RSA private key")
	}
	return &Client{HTTP: &http.Client{Timeout: 30 * time.Second}, BaseURL: "https://api.github.com", Org: cfg.Org, AppID: cfg.AppID, InstallationID: cfg.InstallationID, Key: key}, nil
}

func (c *Client) jwt(now time.Time) (string, error) {
	header := base64.RawURLEncoding.EncodeToString([]byte(`{"alg":"RS256","typ":"JWT"}`))
	claims, _ := json.Marshal(map[string]any{"iat": now.Add(-time.Minute).Unix(), "exp": now.Add(9 * time.Minute).Unix(), "iss": strconv.FormatInt(c.AppID, 10)})
	input := header + "." + base64.RawURLEncoding.EncodeToString(claims)
	hash := sha256.Sum256([]byte(input))
	sig, err := rsa.SignPKCS1v15(rand.Reader, c.Key, crypto.SHA256, hash[:])
	if err != nil {
		return "", fmt.Errorf("sign GitHub App JWT: %w", err)
	}
	return input + "." + base64.RawURLEncoding.EncodeToString(sig), nil
}

// Response bodies and request authorization never enter diagnostics.
func (c *Client) request(ctx context.Context, method, path, token string, out any) error {
	req, err := http.NewRequestWithContext(ctx, method, c.BaseURL+path, nil)
	if err != nil {
		return fmt.Errorf("create GitHub request")
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("X-GitHub-Api-Version", "2022-11-28")
	resp, err := c.HTTP.Do(req)
	if err != nil {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		return fmt.Errorf("GitHub %s request failed (network error or timeout)", method)
	}
	defer resp.Body.Close()
	if method == http.MethodDelete && resp.StatusCode == http.StatusNotFound {
		return nil
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		if resp.StatusCode == http.StatusUnauthorized {
			c.expires = time.Time{}
		}
		return fmt.Errorf("GitHub %s request returned HTTP %d", method, resp.StatusCode)
	}
	if out != nil {
		if err := json.NewDecoder(io.LimitReader(resp.Body, 4<<20)).Decode(out); err != nil {
			return fmt.Errorf("invalid GitHub response JSON")
		}
	}
	return nil
}
func (c *Client) installationToken(ctx context.Context) (string, error) {
	if c.token != "" && time.Now().Add(time.Minute).Before(c.expires) {
		return c.token, nil
	}
	jwt, err := c.jwt(time.Now())
	if err != nil {
		return "", err
	}
	var out struct {
		Token   string    `json:"token"`
		Expires time.Time `json:"expires_at"`
	}
	if err := c.request(ctx, http.MethodPost, fmt.Sprintf("/app/installations/%d/access_tokens", c.InstallationID), jwt, &out); err != nil {
		return "", err
	}
	if out.Token == "" || !out.Expires.After(time.Now()) {
		return "", fmt.Errorf("GitHub returned an empty or expired installation token")
	}
	c.token, c.expires = out.Token, out.Expires
	return c.token, nil
}
func (c *Client) RegistrationToken(ctx context.Context) (string, error) {
	token, err := c.installationToken(ctx)
	if err != nil {
		return "", err
	}
	var out struct {
		Token string `json:"token"`
	}
	if err := c.request(ctx, http.MethodPost, "/orgs/"+url.PathEscape(c.Org)+"/actions/runners/registration-token", token, &out); err != nil {
		return "", err
	}
	if out.Token == "" || strings.ContainsAny(out.Token, "\r\n\x00") {
		return "", fmt.Errorf("GitHub returned an invalid registration token")
	}
	return out.Token, nil
}

// DeleteOwned excludes live names during crash recovery. A nil selection deletes
// all owned runners except keep; otherwise only selected exact names are deleted.
func (c *Client) DeleteOwned(ctx context.Context, label string, selection, keep map[string]bool) error {
	token, err := c.installationToken(ctx)
	if err != nil {
		return err
	}
	var found []Runner
	for page := 1; ; page++ {
		var out struct {
			Runners    []Runner `json:"runners"`
			TotalCount *int     `json:"total_count"`
		}
		path := fmt.Sprintf("/orgs/%s/actions/runners?per_page=100&page=%d", url.PathEscape(c.Org), page)
		if err := c.request(ctx, http.MethodGet, path, token, &out); err != nil {
			return err
		}
		if out.TotalCount == nil || out.Runners == nil {
			return fmt.Errorf("invalid GitHub runner list")
		}
		found = append(found, out.Runners...)
		if len(out.Runners) < 100 {
			break
		}
		if page >= 10000 {
			return fmt.Errorf("GitHub runner pagination exceeded limit")
		}
	}
	// Collect pages before deleting so mutations do not shift pagination.
	var failures []string
	for _, r := range found {
		if !r.HasLabel(label) || keep[r.Name] || (selection != nil && !selection[r.Name]) {
			continue
		}
		if r.ID <= 0 || r.Name == "" {
			failures = append(failures, "invalid owned runner identity")
			continue
		}
		if err := c.request(ctx, http.MethodDelete, fmt.Sprintf("/orgs/%s/actions/runners/%d", url.PathEscape(c.Org), r.ID), token, nil); err != nil {
			failures = append(failures, fmt.Sprintf("remove runner %s: %v", r.Name, err))
		}
	}
	if len(failures) > 0 {
		return fmt.Errorf("%s", strings.Join(failures, "; "))
	}
	return nil
}
