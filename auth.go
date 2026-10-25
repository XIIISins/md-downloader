package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
	"sync"
	"time"
)

const tokenURL = "https://auth.mangadex.org/realms/mangadex/protocol/openid-connect/token"

type credentials struct {
	ClientID     string
	ClientSecret string
	Username     string
	Password     string
}

// loadCredentials reads `key:value` lines (id, secret, username, password) from
// path, then lets MD_* environment variables override them.
func loadCredentials(path string) (credentials, error) {
	var c credentials
	b, err := os.ReadFile(path)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return c, err
	}
	for _, line := range strings.Split(string(b), "\n") {
		k, v, ok := strings.Cut(line, ":")
		if !ok {
			continue
		}
		v = strings.TrimSpace(v)
		switch strings.ToLower(strings.TrimSpace(k)) {
		case "id", "client_id":
			c.ClientID = v
		case "secret", "client_secret":
			c.ClientSecret = v
		case "username":
			c.Username = v
		case "password":
			c.Password = v
		}
	}
	for env, dst := range map[string]*string{
		"MD_CLIENT_ID":     &c.ClientID,
		"MD_CLIENT_SECRET": &c.ClientSecret,
		"MD_USERNAME":      &c.Username,
		"MD_PASSWORD":      &c.Password,
	} {
		if v := os.Getenv(env); v != "" {
			*dst = v
		}
	}
	switch {
	case c.ClientID == "" || c.ClientSecret == "":
		return c, fmt.Errorf("client id/secret missing (expected `id:` and `secret:` lines in %s)", path)
	case c.Username == "" || c.Password == "":
		return c, errors.New("username/password missing (set MD_USERNAME and MD_PASSWORD, or add `username:`/`password:` lines to the token file)")
	}
	return c, nil
}

// authSession keeps a MangaDex OAuth token fresh. Access tokens last ~15 minutes.
type authSession struct {
	http  *http.Client
	creds credentials

	mu      sync.Mutex
	access  string
	refresh string
	expires time.Time
}

func (a *authSession) token(ctx context.Context) (string, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.access != "" && time.Now().Before(a.expires.Add(-time.Minute)) {
		return a.access, nil
	}
	if a.refresh != "" {
		err := a.request(ctx, url.Values{
			"grant_type":    {"refresh_token"},
			"refresh_token": {a.refresh},
		})
		if err == nil {
			return a.access, nil
		}
		// Refresh token expired or revoked; fall back to a fresh login.
	}
	err := a.request(ctx, url.Values{
		"grant_type": {"password"},
		"username":   {a.creds.Username},
		"password":   {a.creds.Password},
	})
	return a.access, err
}

// invalidate forces the next token() call to refresh (e.g. after a 401).
func (a *authSession) invalidate() {
	a.mu.Lock()
	a.expires = time.Time{}
	a.mu.Unlock()
}

func (a *authSession) request(ctx context.Context, form url.Values) error {
	form.Set("client_id", a.creds.ClientID)
	form.Set("client_secret", a.creds.ClientSecret)
	req, _ := http.NewRequestWithContext(ctx, http.MethodPost, tokenURL, strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("User-Agent", userAgent)
	resp, err := a.http.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)

	var r struct {
		AccessToken  string `json:"access_token"`
		RefreshToken string `json:"refresh_token"`
		ExpiresIn    int    `json:"expires_in"`
		Error        string `json:"error"`
		ErrorDesc    string `json:"error_description"`
	}
	_ = json.Unmarshal(body, &r)
	if resp.StatusCode != http.StatusOK || r.AccessToken == "" {
		return fmt.Errorf("login failed (HTTP %d): %s %s", resp.StatusCode, r.Error, r.ErrorDesc)
	}
	a.access, a.refresh = r.AccessToken, r.RefreshToken
	a.expires = time.Now().Add(time.Duration(r.ExpiresIn) * time.Second)
	return nil
}

// libraryStatuses returns manga IDs in the user's library with the given
// reading status (reading, on_hold, plan_to_read, dropped, re_reading, completed).
func (c *client) libraryStatuses(ctx context.Context, status string) ([]string, error) {
	var r struct {
		Statuses map[string]string `json:"statuses"`
	}
	u := apiBase + "/manga/status"
	if status != "" && status != "all" {
		u += "?status=" + url.QueryEscape(status)
	}
	if err := c.getJSONAuth(ctx, c.api, u, &r); err != nil {
		return nil, err
	}
	ids := make([]string, 0, len(r.Statuses))
	for id := range r.Statuses {
		ids = append(ids, id)
	}
	return ids, nil
}
