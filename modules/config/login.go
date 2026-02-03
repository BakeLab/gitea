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
	"code.gitea.io/tea/modules/theme"
	"code.gitea.io/tea/modules/utils"
	"github.com/charmbracelet/huh"
	"golang.org/x/oauth2"
)

// TokenRefreshThreshold is how far before expiry we should refresh OAuth tokens.
// This is used by config.Login.Client() for automatic token refresh.
const TokenRefreshThreshold = 5 * time.Minute

// DefaultClientID is the default OAuth2 client ID included in most Gitea instances
const DefaultClientID = "d57cb8c4-630c-4168-8324-ec79935e18d4"

// Login represents a login to a gitea server, you even could add multiple logins for one gitea server
type Login struct {
	Name    string `yaml:"name"`
	URL     string `yaml:"url"`
	Token   string `yaml:"token"`
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
	// RefreshToken is used to renew the access token when it expires
	RefreshToken string `yaml:"refresh_token"`
	// TokenExpiry is when the token expires (unix timestamp)
	TokenExpiry int64 `yaml:"token_expiry"`
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
	if err := loadConfig(); err != nil {
		return err
	}

	loginExist := false
	for i := range config.Logins {
		config.Logins[i].Default = false
		if strings.ToLower(config.Logins[i].Name) == strings.ToLower(name) {
			config.Logins[i].Default = true
			loginExist = true
		}
	}

	if !loginExist {
		return fmt.Errorf("login '%s' not found", name)
	}

	return saveConfig()
}

// GetLoginByName get login by name (case insensitive)
func GetLoginByName(name string) *Login {
	err := loadConfig()
	if err != nil {
		log.Fatal(err)
	}

	for _, l := range config.Logins {
		if strings.ToLower(l.Name) == strings.ToLower(name) {
			return &l
		}
	}
	return nil
}

// GetLoginByToken get login by token
func GetLoginByToken(token string) *Login {
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
	idx := -1
	for i, l := range config.Logins {
		if l.Name == name {
			idx = i
			break
		}
	}
	if idx == -1 {
		return fmt.Errorf("can not delete login '%s', does not exist", name)
	}

	config.Logins = append(config.Logins[:idx], config.Logins[idx+1:]...)

	return saveConfig()
}

// AddLogin save a login to config
func AddLogin(login *Login) error {
	if err := loadConfig(); err != nil {
		return err
	}

	// Check for duplicate login names
	for _, existing := range config.Logins {
		if strings.EqualFold(existing.Name, login.Name) {
			return fmt.Errorf("login name '%s' already exists", login.Name)
		}
	}

	// save login to global var
	config.Logins = append(config.Logins, *login)

	// save login to config file
	return saveConfig()
}

// UpdateLogin updates an existing login in the config
func UpdateLogin(login *Login) error {
	if err := loadConfig(); err != nil {
		return err
	}

	// Find and update the login
	found := false
	for i, l := range config.Logins {
		if l.Name == login.Name {
			config.Logins[i] = *login
			found = true
			break
		}
	}

	if !found {
		return fmt.Errorf("login %s not found", login.Name)
	}

	// Save updated config
	return saveConfig()
}

// RefreshOAuthTokenIfNeeded refreshes the OAuth token if it's expired or near expiry.
// Returns nil without doing anything if no refresh is needed.
func (l *Login) RefreshOAuthTokenIfNeeded() error {
	if l.RefreshToken == "" || l.TokenExpiry == 0 {
		return nil
	}
	expiryTime := time.Unix(l.TokenExpiry, 0)
	if time.Now().Add(TokenRefreshThreshold).After(expiryTime) {
		return l.RefreshOAuthToken()
	}
	return nil
}

// RefreshOAuthToken refreshes the OAuth access token using the refresh token.
// It updates the login with new token information and saves it to config.
func (l *Login) RefreshOAuthToken() error {
	if l.RefreshToken == "" {
		return fmt.Errorf("no refresh token available")
	}

	// Create a Token object with current values
	currentToken := &oauth2.Token{
		AccessToken:  l.Token,
		RefreshToken: l.RefreshToken,
		Expiry:       time.Unix(l.TokenExpiry, 0),
	}

	// Set up the OAuth2 config
	ctx := context.Background()

	// Create HTTP client, respecting the login's TLS settings
	httpClient := &http.Client{
		Transport: &http.Transport{
			TLSClientConfig: &tls.Config{InsecureSkipVerify: l.Insecure},
		},
	}
	ctx = context.WithValue(ctx, oauth2.HTTPClient, httpClient)

	// Configure the OAuth2 endpoints
	oauth2Config := &oauth2.Config{
		ClientID: DefaultClientID,
		Endpoint: oauth2.Endpoint{
			TokenURL: fmt.Sprintf("%s/login/oauth/access_token", l.URL),
		},
	}

	// Refresh the token
	newToken, err := oauth2Config.TokenSource(ctx, currentToken).Token()
	if err != nil {
		return fmt.Errorf("failed to refresh token: %w", err)
	}

	// Update login with new token information
	l.Token = newToken.AccessToken

	if newToken.RefreshToken != "" {
		l.RefreshToken = newToken.RefreshToken
	}

	if !newToken.Expiry.IsZero() {
		l.TokenExpiry = newToken.Expiry.Unix()
	}

	// Save updated login to config
	return UpdateLogin(l)
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

	options = append(options, gitea.SetToken(l.Token), gitea.SetHTTPClient(httpClient))
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
