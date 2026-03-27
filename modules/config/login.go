// Copyright 2020 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package config

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"log"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"os"
	"strings"
	"time"

	"code.gitea.io/sdk/gitea"
	"code.gitea.io/tea/modules/debug"
	"code.gitea.io/tea/modules/httputil"
	"code.gitea.io/tea/modules/theme"
	"code.gitea.io/tea/modules/utils"

	"charm.land/huh/v2"
	"golang.org/x/oauth2"
)

// TokenRefreshThreshold is how far before expiry we should refresh OAuth tokens.
// This is used by config.Login.Client() for automatic token refresh.
const TokenRefreshThreshold = 5 * time.Minute

// DefaultClientID is the default OAuth2 client ID included in most Gitea instances
const DefaultClientID = "d57cb8c4-630c-4168-8324-ec79935e18d4"

// AuthMethodOAuth marks a login as using OAuth with secure credential storage.
const AuthMethodOAuth = "oauth"

// Login represents a login to a gitea server, you even could add multiple logins for one gitea server
type Login struct {
	Name    string `yaml:"name"`
	URL     string `yaml:"url"`
	Token   string `yaml:"token,omitempty"`
	Default bool   `yaml:"default"`
	SSHHost string `yaml:"ssh_host"`
	// optional path to the private key
	SSHKey            string `yaml:"ssh_key"`
	Insecure          bool   `yaml:"insecure"`
	SSHCertPrincipal  string `yaml:"ssh_certificate_principal"`
	SSHAgent          bool   `yaml:"ssh_agent"`
	SSHKeyFingerprint string `yaml:"ssh_key_agent_pub"`
	SSHPassphrase     string `yaml:"-"`
	VersionCheck      bool   `yaml:"version_check"`
	// User is username from gitea
	User string `yaml:"user"`
	// Created is auto created unix timestamp
	Created int64 `yaml:"created"`
	// AuthMethod indicates the authentication method ("oauth" for OAuth with credstore)
	AuthMethod string `yaml:"auth_method,omitempty"`
	// RefreshToken is used to renew the access token when it expires
	RefreshToken string `yaml:"refresh_token,omitempty"`
	// TokenExpiry is when the token expires (unix timestamp)
	TokenExpiry int64 `yaml:"token_expiry,omitempty"`
}

// IsOAuth returns true if this login uses OAuth with secure credential storage.
func (l *Login) IsOAuth() bool {
	return l.AuthMethod == AuthMethodOAuth
}

// loadOAuthToken loads the OAuth token from credstore, returning nil if
// this is not an OAuth login or if the load fails (caller should fallback).
func (l *Login) loadOAuthToken() *OAuthToken {
	if !l.IsOAuth() {
		return nil
	}
	tok, err := LoadOAuthToken(l.Name)
	if err != nil {
		return nil
	}
	return &OAuthToken{
		AccessToken:  tok.AccessToken,
		RefreshToken: tok.RefreshToken,
		ExpiresAt:    tok.ExpiresAt,
	}
}

// OAuthToken holds the token fields loaded from credstore.
type OAuthToken struct {
	AccessToken  string
	RefreshToken string
	ExpiresAt    time.Time
}

// GetAccessToken returns the effective access token.
// For OAuth logins, reads from credstore. For others, returns l.Token directly.
func (l *Login) GetAccessToken() string {
	if tok := l.loadOAuthToken(); tok != nil {
		return tok.AccessToken
	}
	return l.Token
}

// GetRefreshToken returns the refresh token.
func (l *Login) GetRefreshToken() string {
	if tok := l.loadOAuthToken(); tok != nil {
		return tok.RefreshToken
	}
	return l.RefreshToken
}

// GetTokenExpiry returns the token expiry time.
func (l *Login) GetTokenExpiry() time.Time {
	if tok := l.loadOAuthToken(); tok != nil {
		return tok.ExpiresAt
	}
	return time.Unix(l.TokenExpiry, 0)
}

// GetLogins return all login available by config
func GetLogins() ([]Login, error) {
	if err := loadConfig(); err != nil {
		return nil, err
	}
	return config.Logins, nil
}

// GetDefaultLogin return the default login
func GetDefaultLogin() (*Login, error) {
	if err := loadConfig(); err != nil {
		return nil, err
	}

	if len(config.Logins) == 0 {
		return nil, errors.New("No available login")
	}
	for _, l := range config.Logins {
		if l.Default {
			return &l, nil
		}
	}

	return &config.Logins[0], nil
}

// SetDefaultLogin set the default login by name (case insensitive)
func SetDefaultLogin(name string) error {
	return withConfigLock(func() error {
		loginExist := false
		for i := range config.Logins {
			config.Logins[i].Default = false
			if strings.EqualFold(config.Logins[i].Name, name) {
				config.Logins[i].Default = true
				loginExist = true
			}
		}

		if !loginExist {
			return fmt.Errorf("login '%s' not found", name)
		}

		return saveConfigUnsafe()
	})
}

// GetLoginByName get login by name (case insensitive)
func GetLoginByName(name string) (*Login, error) {
	if err := loadConfig(); err != nil {
		return nil, err
	}

	for i := range config.Logins {
		if strings.EqualFold(config.Logins[i].Name, name) {
			return &config.Logins[i], nil
		}
	}
	return nil, nil
}

// GetLoginByToken get login by token
func GetLoginByToken(token string) *Login {
	if token == "" {
		return nil
	}
	err := loadConfig()
	if err != nil {
		log.Fatal(err)
	}

	for _, l := range config.Logins {
		if l.Token == token {
			return &l
		}
	}
	return nil
}

// GetLoginByHost finds a login by it's server URL
func GetLoginByHost(host string) *Login {
	logins := GetLoginsByHost(host)
	if len(logins) > 0 {
		return logins[0]
	}
	return nil
}

// GetLoginsByHost returns all logins matching a host
func GetLoginsByHost(host string) []*Login {
	err := loadConfig()
	if err != nil {
		log.Fatal(err)
	}

	var matches []*Login
	for i := range config.Logins {
		loginURL, err := url.Parse(config.Logins[i].URL)
		if err != nil {
			log.Fatal(err)
		}
		if loginURL.Host == host {
			matches = append(matches, &config.Logins[i])
		}
	}
	return matches
}

// DeleteLogin delete a login by name from config
func DeleteLogin(name string) error {
	return withConfigLock(func() error {
		idx := -1
		for i, l := range config.Logins {
			if strings.EqualFold(l.Name, name) {
				idx = i
				break
			}
		}
		if idx == -1 {
			return fmt.Errorf("can not delete login '%s', does not exist", name)
		}

		isOAuth := config.Logins[idx].IsOAuth()
		config.Logins = append(config.Logins[:idx], config.Logins[idx+1:]...)

		// Clean up credstore tokens for OAuth logins
		if isOAuth {
			_ = DeleteOAuthToken(name)
		}

		return saveConfigUnsafe()
	})
}

// AddLogin save a login to config
func AddLogin(login *Login) error {
	return withConfigLock(func() error {
		// Check for duplicate login names
		for _, existing := range config.Logins {
			if strings.EqualFold(existing.Name, login.Name) {
				return fmt.Errorf("login name '%s' already exists", login.Name)
			}
		}

		// save login to global var
		config.Logins = append(config.Logins, *login)

		// save login to config file
		return saveConfigUnsafe()
	})
}

// SaveLoginTokens updates the token fields for an existing login.
// This is used after browser-based re-authentication to save new tokens.
func SaveLoginTokens(login *Login) error {
	if login.IsOAuth() {
		return SaveOAuthToken(login.Name, login.GetAccessToken(), login.GetRefreshToken(), login.GetTokenExpiry())
	}
	return withConfigLock(func() error {
		for i, l := range config.Logins {
			if strings.EqualFold(l.Name, login.Name) {
				config.Logins[i].Token = login.Token
				config.Logins[i].RefreshToken = login.RefreshToken
				config.Logins[i].TokenExpiry = login.TokenExpiry
				return saveConfigUnsafe()
			}
		}
		return fmt.Errorf("login %s not found", login.Name)
	})
}

// RefreshOAuthTokenIfNeeded refreshes the OAuth token if it's expired or near expiry.
// Returns nil without doing anything if no refresh is needed.
func (l *Login) RefreshOAuthTokenIfNeeded() error {
	// Load once to avoid multiple credstore reads
	if tok := l.loadOAuthToken(); tok != nil {
		if tok.RefreshToken == "" || tok.ExpiresAt.IsZero() {
			return nil
		}
		if time.Now().Add(TokenRefreshThreshold).After(tok.ExpiresAt) {
			return l.RefreshOAuthToken()
		}
		return nil
	}
	// Non-OAuth path: use YAML fields
	if l.RefreshToken == "" || l.TokenExpiry == 0 {
		return nil
	}
	if time.Now().Add(TokenRefreshThreshold).After(time.Unix(l.TokenExpiry, 0)) {
		return l.RefreshOAuthToken()
	}
	return nil
}

// RefreshOAuthToken refreshes the OAuth access token using the refresh token.
// It updates the login with new token information and saves it to config.
// Uses double-checked locking to avoid unnecessary refresh calls when multiple
// processes race to refresh the same token.
func (l *Login) RefreshOAuthToken() error {
	if l.GetRefreshToken() == "" {
		return fmt.Errorf("no refresh token available")
	}

	return withConfigLock(func() error {
		// Double-check: after acquiring lock, re-read config and check if
		// another process already refreshed the token
		for i, login := range config.Logins {
			if login.Name == l.Name {
				// Check if token was refreshed by another process
				currentExpiry := login.GetTokenExpiry()
				ourExpiry := l.GetTokenExpiry()
				if currentExpiry != ourExpiry && !currentExpiry.IsZero() {
					if time.Now().Add(TokenRefreshThreshold).Before(currentExpiry) {
						// Token was refreshed by another process, update our copy
						if !login.IsOAuth() {
							l.Token = login.Token
							l.RefreshToken = login.RefreshToken
							l.TokenExpiry = login.TokenExpiry
						}
						// For OAuth logins, credstore already has the latest tokens
						return nil
					}
				}

				// Still need to refresh - proceed with OAuth call
				newToken, err := doOAuthRefresh(l)
				if err != nil {
					return err
				}

				if l.IsOAuth() {
					// Save tokens to credstore; no YAML changes needed
					return SaveOAuthTokenFromOAuth2(l.Name, newToken, l)
				}

				// Update login with new token information (legacy path)
				l.Token = newToken.AccessToken
				if newToken.RefreshToken != "" {
					l.RefreshToken = newToken.RefreshToken
				}
				if !newToken.Expiry.IsZero() {
					l.TokenExpiry = newToken.Expiry.Unix()
				}
				config.Logins[i] = *l
				return saveConfigUnsafe()
			}
		}

		return fmt.Errorf("login %s not found", l.Name)
	})
}

// doOAuthRefresh performs the actual OAuth token refresh API call.
func doOAuthRefresh(l *Login) (*oauth2.Token, error) {
	// Build current token from credstore (single load) or YAML fields
	var accessToken, refreshToken string
	var expiry time.Time
	if tok := l.loadOAuthToken(); tok != nil {
		accessToken = tok.AccessToken
		refreshToken = tok.RefreshToken
		expiry = tok.ExpiresAt
	} else {
		accessToken = l.Token
		refreshToken = l.RefreshToken
		expiry = time.Unix(l.TokenExpiry, 0)
	}
	currentToken := &oauth2.Token{
		AccessToken:  accessToken,
		RefreshToken: refreshToken,
		Expiry:       expiry,
	}

	ctx := context.Background()

	httpClient := &http.Client{
		Transport: httputil.WrapTransport(&http.Transport{
			TLSClientConfig: &tls.Config{InsecureSkipVerify: l.Insecure},
		}),
	}
	ctx = context.WithValue(ctx, oauth2.HTTPClient, httpClient)

	oauth2Config := &oauth2.Config{
		ClientID: DefaultClientID,
		Endpoint: oauth2.Endpoint{
			TokenURL: fmt.Sprintf("%s/login/oauth/access_token", l.URL),
		},
	}

	newToken, err := oauth2Config.TokenSource(ctx, currentToken).Token()
	if err != nil {
		return nil, fmt.Errorf("failed to refresh token: %w", err)
	}

	return newToken, nil
}

// Client returns a client to operate Gitea API. You may provide additional modifiers
// for the client like gitea.SetBasicAuth() for customization
func (l *Login) Client(options ...gitea.ClientOption) *gitea.Client {
	// Refresh OAuth token if expired or near expiry
	if err := l.RefreshOAuthTokenIfNeeded(); err != nil {
		log.Fatalf("Failed to refresh token: %s\nPlease use 'tea login oauth-refresh %s' to manually refresh the token.\n", err, l.Name)
	}

	httpClient := &http.Client{}
	if l.Insecure {
		cookieJar, _ := cookiejar.New(nil)

		httpClient = &http.Client{
			Jar: cookieJar,
			Transport: &http.Transport{
				TLSClientConfig: &tls.Config{InsecureSkipVerify: true},
			},
		}
	}

	// versioncheck must be prepended in options to make sure we don't hit any version checks in the sdk
	if !l.VersionCheck {
		options = append([]gitea.ClientOption{gitea.SetGiteaVersion("")}, options...)
	}

	options = append(options, gitea.SetToken(l.GetAccessToken()), gitea.SetHTTPClient(httpClient), gitea.SetUserAgent(httputil.UserAgent()))
	if debug.IsDebug() {
		options = append(options, gitea.SetDebugMode())
	}

	if l.SSHCertPrincipal != "" {
		l.askForSSHPassphrase()
		options = append(options, gitea.UseSSHCert(l.SSHCertPrincipal, l.SSHKey, l.SSHPassphrase))
	}

	if l.SSHKeyFingerprint != "" {
		l.askForSSHPassphrase()
		options = append(options, gitea.UseSSHPubkey(l.SSHKeyFingerprint, l.SSHKey, l.SSHPassphrase))
	}

	client, err := gitea.NewClient(l.URL, options...)
	if err != nil {
		var versionError *gitea.ErrUnknownVersion
		if !errors.As(err, &versionError) {
			log.Fatal(err)
		}
		fmt.Fprintf(os.Stderr, "WARNING: could not detect gitea version: %s\nINFO: set gitea version: to last supported one\n", versionError)
	}
	return client
}

func (l *Login) askForSSHPassphrase() {
	if ok, err := utils.IsKeyEncrypted(l.SSHKey); ok && err == nil && l.SSHPassphrase == "" {
		if err := huh.NewInput().
			Title("ssh-key is encrypted please enter the passphrase: ").
			Validate(huh.ValidateNotEmpty()).
			EchoMode(huh.EchoModePassword).
			Value(&l.SSHPassphrase).
			WithTheme(theme.GetTheme()).
			Run(); err != nil {
			log.Fatal(err)
		}
	}
}

// GetSSHHost returns SSH host name
func (l *Login) GetSSHHost() string {
	if l.SSHHost != "" {
		return l.SSHHost
	}

	u, err := url.Parse(l.URL)
	if err != nil {
		return ""
	}

	return u.Host
}
