// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package auth

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
	"net"
	"net/http"
	"time"

	"gitea.dev/cli/internal/settings"
	"golang.org/x/oauth2"
)

// DefaultClientID identifies the public gt OAuth application.
const DefaultClientID = "d57cb8c4-630c-4168-8324-ec79935e18d4"

// DefaultScopes are the token scopes gt requests through OAuth: read and write access to everything
// but instance administration.
var DefaultScopes = []string{"write:issue", "write:misc", "write:notification", "write:organization", "write:package", "write:repository", "write:user"}

// refreshTimeout bounds a token refresh, which holds the settings lock, below settings.LockTimeout.
const refreshTimeout = 20 * time.Second

// OAuthOptions supplies the instance, client, and authorization interaction.
type OAuthOptions struct {
	Instance string
	ClientID string
	// HTTP sends the token exchange; nil uses http.DefaultClient.
	HTTP      *http.Client
	Authorize func(context.Context, string) error
	Timeout   time.Duration
	// Scopes are the token scopes to request; none requests DefaultScopes.
	Scopes []string
}

// OAuthLogin exchanges a PKCE authorization code through a loopback callback.
func OAuthLogin(ctx context.Context, opts OAuthOptions) (Credential, error) {
	if opts.ClientID == "" {
		opts.ClientID = DefaultClientID
	}
	if opts.Timeout == 0 {
		opts.Timeout = 2 * time.Minute
	}
	ctx, cancel := context.WithTimeout(ctx, opts.Timeout)
	defer cancel()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return Credential{}, errors.New("cannot listen for OAuth callback")
	}
	defer listener.Close()
	random := make([]byte, 32)
	if _, err := rand.Read(random); err != nil {
		return Credential{}, err
	}
	state := base64.RawURLEncoding.EncodeToString(random)
	verifier := oauth2.GenerateVerifier()
	// Gitea ignores the port of a loopback redirect but compares its path; the built-in gt application registers none.
	redirect := "http://" + listener.Addr().String()
	config := oauthConfig(opts.Instance, opts.ClientID)
	config.Scopes = opts.Scopes
	if len(config.Scopes) == 0 {
		config.Scopes = DefaultScopes
	}
	config.RedirectURL = redirect
	codes := make(chan string, 1)
	failures := make(chan error, 1)
	mux := http.NewServeMux()
	mux.HandleFunc("/{$}", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		if r.URL.Query().Get("state") != state {
			http.Error(w, "Invalid OAuth state", http.StatusBadRequest)
			return
		}
		code := r.URL.Query().Get("code")
		if code == "" {
			http.Error(w, "Authorization declined", http.StatusBadRequest)
			select {
			case failures <- errors.New("OAuth authorization declined"):
			default:
			}
			return
		}
		select {
		case codes <- code:
		default:
		}
		_, _ = fmt.Fprintln(w, "Authorization received. Return to gt.")
	})
	server := &http.Server{Handler: mux, ReadHeaderTimeout: 5 * time.Second}
	go func() {
		if err := server.Serve(listener); err != nil && !errors.Is(err, http.ErrServerClosed) && !errors.Is(err, net.ErrClosed) {
			select {
			case failures <- errors.New("OAuth callback server failed"):
			default:
			}
		}
	}()
	defer server.Close()
	authorizeURL := config.AuthCodeURL(state, oauth2.S256ChallengeOption(verifier))
	if opts.Authorize == nil {
		return Credential{}, errors.New("OAuth requires an authorization URL handler")
	}
	if err := opts.Authorize(ctx, authorizeURL); err != nil {
		return Credential{}, err
	}
	select {
	case <-ctx.Done():
		return Credential{}, ctx.Err()
	case err := <-failures:
		return Credential{}, err
	case code := <-codes:
		ctx = withHTTPClient(ctx, opts.HTTP)
		token, err := config.Exchange(ctx, code, oauth2.VerifierOption(verifier))
		if err != nil {
			if ctx.Err() != nil {
				return Credential{}, ctx.Err()
			}
			return Credential{}, errors.New("OAuth token exchange failed; verify client ID and registered loopback redirect URI")
		}
		return Credential{AccessToken: token.AccessToken, RefreshToken: token.RefreshToken, ExpiresAt: token.Expiry}, nil
	}
}

// withHTTPClient routes oauth2 requests through client, or http.DefaultClient when it is nil.
// oauth2 would dereference a nil client stored in the context.
func withHTTPClient(ctx context.Context, client *http.Client) context.Context {
	if client == nil {
		client = http.DefaultClient
	}
	return context.WithValue(ctx, oauth2.HTTPClient, client)
}

func oauthConfig(instance, clientID string) *oauth2.Config {
	return &oauth2.Config{ClientID: clientID, Endpoint: oauth2.Endpoint{AuthURL: instance + "/login/oauth/authorize", TokenURL: instance + "/login/oauth/access_token", AuthStyle: oauth2.AuthStyleInParams}}
}

// Refresh locks by instance and account, then reads again before rotating secrets.
// A nil httpClient uses http.DefaultClient.
func (m Manager) Refresh(ctx context.Context, instance, user string, httpClient *http.Client, force bool, now time.Time) (Credential, error) {
	snapshot, err := m.settings.Load()
	if err != nil {
		return Credential{}, err
	}
	host := snapshot.Hosts.Hosts[instance]
	account, exists := host.Users[user]
	if !exists {
		return Credential{}, errors.New("account is not configured")
	}
	if account.AuthMethod != settings.AuthOAuth {
		return m.credentials.Load(instance, user, account.CredentialStore)
	}
	credential, err := m.credentials.Load(instance, user, account.CredentialStore)
	if err != nil {
		return Credential{}, err
	}
	needsRefresh := func(c Credential) bool { return !c.ExpiresAt.IsZero() && !now.Add(5*time.Minute).Before(c.ExpiresAt) }
	if !force && !needsRefresh(credential) {
		return credential, nil
	}
	original := credential
	err = m.settings.Locked(func(current settings.Snapshot) error {
		if err := ctx.Err(); err != nil {
			return err
		}
		currentAccount, exists := current.Hosts.Hosts[instance].Users[user]
		if !exists || currentAccount.AuthMethod != settings.AuthOAuth {
			return errors.New("OAuth account changed while waiting to refresh")
		}
		credential, err = m.credentials.Load(instance, user, currentAccount.CredentialStore)
		if err != nil {
			return err
		}
		if credential.AccessToken != original.AccessToken && !needsRefresh(credential) {
			return nil
		}
		if !force && !needsRefresh(credential) {
			return nil
		}
		if credential.RefreshToken == "" {
			return errors.New("OAuth token cannot be refreshed; run gt auth login")
		}
		clientID := current.Hosts.Hosts[instance].OAuthClientID
		if clientID == "" {
			clientID = DefaultClientID
		}
		config := oauthConfig(instance, clientID)
		requestContext, cancel := context.WithTimeout(ctx, refreshTimeout)
		defer cancel()
		requestContext = withHTTPClient(requestContext, httpClient)
		old := &oauth2.Token{AccessToken: credential.AccessToken, RefreshToken: credential.RefreshToken, Expiry: now.Add(-time.Hour)}
		token, err := config.TokenSource(requestContext, old).Token()
		if err != nil {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			if requestContext.Err() != nil {
				return errors.New("OAuth refresh timed out; try again")
			}
			return errors.New("OAuth refresh failed; run gt auth login")
		}
		credential.AccessToken = token.AccessToken
		if token.RefreshToken != "" {
			credential.RefreshToken = token.RefreshToken
		}
		if !token.Expiry.IsZero() {
			credential.ExpiresAt = token.Expiry
		}
		return m.credentials.Save(instance, user, currentAccount.CredentialStore, credential)
	})
	return credential, err
}
