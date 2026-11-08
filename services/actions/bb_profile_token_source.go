// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package actions

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"golang.org/x/oauth2"
)

const bbOIDCMaxBody = 64 * 1024

type bbOIDCProvider struct {
	Issuer        string `json:"issuer"`
	TokenEndpoint string `json:"token_endpoint"`
}

type bbProfileTokenSource struct {
	ctx    context.Context
	store  *bbProfileTokenStore
	config *oauth2.Config
	mu     sync.Mutex
}

func newBBProfileTokenSource(ctx context.Context, profile bbExecutorProfileAuth, store *bbProfileTokenStore) (*bbProfileTokenSource, error) {
	issuer, err := bbHTTPS(profile.Issuer)
	if err != nil || issuer.RawPath != "" {
		return nil, errors.New("BB profile issuer must be an HTTPS issuer URL")
	}
	if profile.ClientID == "" || profile.Resource == "" || store == nil {
		return nil, errors.New("BB profile authentication requires client ID, resource and token store")
	}
	base := &http.Client{
		Timeout: 10 * time.Second,
		CheckRedirect: func(*http.Request, []*http.Request) error {
			return errors.New("OIDC redirects are disabled")
		},
	}
	metaURL := *issuer
	metaURL.Path = strings.TrimRight(metaURL.Path, "/") + "/.well-known/openid-configuration"
	metaURL.RawPath = ""
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, metaURL.String(), nil)
	if err != nil {
		return nil, errors.New("invalid BB profile issuer URL")
	}
	res, err := base.Do(req)
	if err != nil {
		return nil, errors.New("BB profile OIDC discovery failed")
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK || res.ContentLength > bbOIDCMaxBody {
		return nil, errors.New("BB profile OIDC discovery returned an invalid response")
	}
	data, err := io.ReadAll(io.LimitReader(res.Body, bbOIDCMaxBody+1))
	if err != nil || len(data) > bbOIDCMaxBody {
		return nil, errors.New("BB profile OIDC discovery document is too large")
	}
	var provider bbOIDCProvider
	if err := json.Unmarshal(data, &provider); err != nil || provider.Issuer != profile.Issuer || provider.TokenEndpoint == "" {
		return nil, errors.New("BB profile OIDC discovery document is invalid")
	}
	tokenURL, err := bbHTTPS(provider.TokenEndpoint)
	if err != nil || !bbSameOrigin(issuer, tokenURL) {
		return nil, errors.New("BB profile token endpoint must share the issuer origin")
	}
	refreshClient := *base
	refreshClient.Transport = bbResourceTransport{base: http.DefaultTransport, resource: profile.Resource, scope: strings.Join(profile.Scopes, " ")}
	refreshCtx := context.WithValue(ctx, oauth2.HTTPClient, &refreshClient)
	return &bbProfileTokenSource{
		ctx:   refreshCtx,
		store: store,
		config: &oauth2.Config{
			ClientID: profile.ClientID,
			Scopes:   append([]string(nil), profile.Scopes...),
			Endpoint: oauth2.Endpoint{TokenURL: provider.TokenEndpoint, AuthStyle: oauth2.AuthStyleInParams},
		},
	}, nil
}

func (s *bbProfileTokenSource) Token() (*oauth2.Token, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.ctx.Err(); err != nil {
		return nil, err
	}
	var token *oauth2.Token
	err := s.store.WithLock(s.ctx, func() error {
		stored, err := s.store.Load(s.ctx)
		if err != nil {
			return errors.New("BB profile credentials could not be loaded")
		}
		if stored.AccessToken != "" && stored.ExpiresAt.After(time.Now().Add(30*time.Second)) {
			token = &oauth2.Token{AccessToken: stored.AccessToken, TokenType: "Bearer", RefreshToken: stored.RefreshToken, Expiry: stored.ExpiresAt}
			return nil
		}
		if stored.RefreshToken == "" {
			return errors.New("BB profile refresh token is unavailable; sign-in is required")
		}
		fresh, err := s.config.TokenSource(s.ctx, &oauth2.Token{RefreshToken: stored.RefreshToken}).Token()
		if err != nil {
			return errors.New("BB profile token refresh failed; sign-in may be required")
		}
		if fresh.AccessToken == "" || !strings.EqualFold(fresh.Type(), "Bearer") || fresh.Expiry.IsZero() || time.Until(fresh.Expiry) > 366*24*time.Hour {
			return errors.New("BB profile issuer returned an invalid bearer token")
		}
		if fresh.RefreshToken == "" {
			fresh.RefreshToken = stored.RefreshToken
		}
		if err := s.store.Save(s.ctx, bbStoredToken{AccessToken: fresh.AccessToken, RefreshToken: fresh.RefreshToken, ExpiresAt: fresh.Expiry}); err != nil {
			return fmt.Errorf("BB profile refreshed credentials could not be saved: %w", err)
		}
		token = fresh
		return nil
	})
	if err != nil {
		return nil, err
	}
	return token, nil
}

func newBBProfileHTTPClient(base *http.Client, endpoint string, source oauth2.TokenSource) (*http.Client, error) {
	apiURL, err := bbHTTPS(endpoint)
	if err != nil || source == nil {
		return nil, errors.New("BB profile API requires an HTTPS endpoint and token source")
	}
	client := &http.Client{Timeout: 20 * time.Second}
	if base != nil {
		*client = *base
	}
	transport := client.Transport
	if transport == nil {
		transport = http.DefaultTransport
	}
	client.Transport = bbProfileTransport{base: transport, endpoint: apiURL, source: source}
	client.CheckRedirect = func(*http.Request, []*http.Request) error {
		return errors.New("BB authenticated redirects are disabled")
	}
	return client, nil
}

type bbProfileTransport struct {
	base     http.RoundTripper
	endpoint *url.URL
	source   oauth2.TokenSource
}

func (t bbProfileTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	prefix := strings.TrimRight(t.endpoint.Path, "/")
	if !bbSameOrigin(req.URL, t.endpoint) || (prefix != "" && req.URL.Path != prefix && !strings.HasPrefix(req.URL.Path, prefix+"/")) {
		return nil, errors.New("refusing to send BB profile credentials outside the configured API endpoint")
	}
	if req.Header.Get("Authorization") != "" {
		return nil, errors.New("BB profile request already has authorization")
	}
	token, err := t.source.Token()
	if err != nil {
		return nil, err
	}
	if token == nil || token.AccessToken == "" || !strings.EqualFold(token.Type(), "Bearer") || strings.ContainsAny(token.AccessToken, "\r\n\t ") {
		return nil, errors.New("BB profile returned an invalid bearer token")
	}
	clone := req.Clone(req.Context())
	clone.Header.Set("Authorization", "Bearer "+token.AccessToken)
	return t.base.RoundTrip(clone)
}

type bbResourceTransport struct {
	base     http.RoundTripper
	resource string
	scope    string
}

func (t bbResourceTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	if req.Method != http.MethodPost || req.Body == nil {
		return t.base.RoundTrip(req)
	}
	data, err := io.ReadAll(io.LimitReader(req.Body, bbOIDCMaxBody+1))
	_ = req.Body.Close()
	if err != nil || len(data) > bbOIDCMaxBody {
		return nil, errors.New("invalid BB profile OAuth request")
	}
	form, err := url.ParseQuery(string(data))
	if err != nil {
		return nil, errors.New("invalid BB profile OAuth request")
	}
	if form.Get("grant_type") == "refresh_token" {
		form.Set("resource", t.resource)
		if t.scope != "" {
			form.Set("scope", t.scope)
		}
	}
	encoded := form.Encode()
	clone := req.Clone(req.Context())
	clone.Body = io.NopCloser(strings.NewReader(encoded))
	clone.ContentLength = int64(len(encoded))
	clone.GetBody = func() (io.ReadCloser, error) { return io.NopCloser(strings.NewReader(encoded)), nil }
	return t.base.RoundTrip(clone)
}

func bbSameOrigin(a, b *url.URL) bool {
	port := func(u *url.URL) string {
		if u.Port() != "" {
			return u.Port()
		}
		if u.Scheme == "https" {
			return "443"
		}
		return "80"
	}
	return a.Scheme == b.Scheme && strings.EqualFold(a.Hostname(), b.Hostname()) && port(a) == port(b)
}
