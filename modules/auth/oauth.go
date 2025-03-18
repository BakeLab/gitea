// Copyright 2025 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package auth

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/tls"
	"encoding/base64"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"code.gitea.io/tea/modules/config"
	"code.gitea.io/tea/modules/task"
	"code.gitea.io/tea/modules/utils"

	"github.com/skratchdot/open-golang/open"
	"golang.org/x/oauth2"
)

// Constants for OAuth2 PKCE flow
const (
	// default client ID included in most Gitea instances
	defaultClientID = "d57cb8c4-630c-4168-8324-ec79935e18d4"

	// default scopes to request
	defaultScopes = "admin,user,issue,misc,notification,organization,package,repository"

	// length of code verifier
	codeVerifierLength = 64

	// timeout for oauth server response
	authTimeout = 60 * time.Second

	// local server settings to receive the callback
	redirectPort = 0
	redirectHost = "127.0.0.1"
)

// OAuthOptions contains options for the OAuth login flow
type OAuthOptions struct {
	Name        string
	URL         string
	Insecure    bool
	ClientID    string
	RedirectURL string
	Port        int
}

// OAuthLogin performs an OAuth2 PKCE login flow to authorize the CLI
func OAuthLogin(name, giteaURL string) error {
	return OAuthLoginWithOptions(name, giteaURL, false)
}

// OAuthLoginWithOptions performs an OAuth2 PKCE login flow with additional options
func OAuthLoginWithOptions(name, giteaURL string, insecure bool) error {
	opts := OAuthOptions{
		Name:        name,
		URL:         giteaURL,
		Insecure:    insecure,
		ClientID:    defaultClientID,
		RedirectURL: fmt.Sprintf("http://%s:%d", redirectHost, redirectPort),
		Port:        redirectPort,
	}
	return OAuthLoginWithFullOptions(opts)
}

// OAuthLoginWithFullOptions performs an OAuth2 PKCE login flow with full options control
func OAuthLoginWithFullOptions(opts OAuthOptions) error {
	// Normalize URL
	serverURL, err := utils.NormalizeURL(opts.URL)
	if err != nil {
		return fmt.Errorf("unable to parse URL: %s", err)
	}

	// Set defaults if needed
	if opts.ClientID == "" {
		opts.ClientID = defaultClientID
	}

	// If the redirect URL is specified, parse it to extract port if needed
	if opts.RedirectURL != "" {
		parsedURL, err := url.Parse(opts.RedirectURL)
		if err == nil && parsedURL.Port() != "" {
			port, err := strconv.Atoi(parsedURL.Port())
			if err == nil {
				opts.Port = port
			}
		}
	} else {
		// If no redirect URL, ensure we have a port and then set the default redirect URL
		if opts.Port == 0 {
			opts.Port = redirectPort
		}
		opts.RedirectURL = fmt.Sprintf("http://%s:%d", redirectHost, opts.Port)
	}

	// Double check that port is set
	if opts.Port == 0 {
		opts.Port = redirectPort
	}

	// Generate code verifier (random string)
	codeVerifier, err := generateCodeVerifier(codeVerifierLength)
	if err != nil {
		return fmt.Errorf("failed to generate code verifier: %s", err)
	}

	// Generate code challenge (SHA256 hash of code verifier)
	codeChallenge := generateCodeChallenge(codeVerifier)

	// Set up the OAuth2 config
	ctx := context.Background()
	ctx = context.WithValue(ctx, oauth2.HTTPClient, createHTTPClient(opts.Insecure))

	// Configure the OAuth2 endpoints
	authURL := fmt.Sprintf("%s/login/oauth/authorize", serverURL)
	tokenURL := fmt.Sprintf("%s/login/oauth/access_token", serverURL)

	oauth2Config := &oauth2.Config{
		ClientID:     opts.ClientID,
		ClientSecret: "", // No client secret for PKCE
		Endpoint: oauth2.Endpoint{
			AuthURL:  authURL,
			TokenURL: tokenURL,
		},
		RedirectURL: opts.RedirectURL,
		Scopes:      strings.Split(defaultScopes, ","),
	}

	// Set up PKCE extension options
	authCodeOpts := []oauth2.AuthCodeOption{
		oauth2.SetAuthURLParam("code_challenge", codeChallenge),
		oauth2.SetAuthURLParam("code_challenge_method", "S256"),
	}

	// Generate state parameter to protect against CSRF
	state, err := generateCodeVerifier(32)
	if err != nil {
		return fmt.Errorf("failed to generate state: %s", err)
	}

	// Get the authorization URL
	authCodeURL := oauth2Config.AuthCodeURL(state, authCodeOpts...)

	// Start a local server to receive the callback
	code, receivedState, err := startLocalServerAndOpenBrowser(authCodeURL, state, opts)
	if err != nil {
		// Check for redirect URI errors
		if strings.Contains(err.Error(), "no authorization code") ||
			strings.Contains(err.Error(), "redirect_uri") ||
			strings.Contains(err.Error(), "redirect") {
			fmt.Println("\n❌ Error: Redirect URL not registered in Gitea")
			fmt.Println("\nTo fix this, you need to register the redirect URL in Gitea:")
			fmt.Printf("1. Go to your Gitea instance: %s\n", serverURL)
			fmt.Println("2. Sign in and go to Settings > Applications")
			fmt.Println("3. Register a new OAuth2 application with:")
			fmt.Printf("   - Application Name: tea-cli (or any name)\n")
			fmt.Printf("   - Redirect URI: %s\n", opts.RedirectURL)
			fmt.Println("4. Copy the Client ID and try again with:")
			fmt.Printf("   tea login add --oauth --client-id YOUR_CLIENT_ID --redirect-url %s\n", opts.RedirectURL)
			fmt.Println("\nAlternatively, you can use a token-based login: tea login add")
		}
		return fmt.Errorf("authorization failed: %s", err)
	}

	// Verify state to prevent CSRF attacks
	if state != receivedState {
		return fmt.Errorf("state mismatch, possible CSRF attack")
	}

	// Exchange authorization code for token
	token, err := oauth2Config.Exchange(ctx, code, oauth2.SetAuthURLParam("code_verifier", codeVerifier))
	if err != nil {
		return fmt.Errorf("token exchange failed: %s", err)
	}

	// Create login with token data
	return createLoginFromToken(opts.Name, serverURL.String(), token, opts.Insecure)
}

// createHTTPClient creates an HTTP client with optional insecure setting
func createHTTPClient(insecure bool) *http.Client {
	client := &http.Client{}
	if insecure {
		client = &http.Client{
			Transport: &http.Transport{
				TLSClientConfig: &tls.Config{InsecureSkipVerify: true},
			},
		}
	}
	return client
}

// generateCodeVerifier creates a cryptographically random string for PKCE
func generateCodeVerifier(length int) (string, error) {
	bytes := make([]byte, length)
	if _, err := rand.Read(bytes); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(bytes)[:length], nil
}

// generateCodeChallenge creates a code challenge from the code verifier using SHA256
func generateCodeChallenge(codeVerifier string) string {
	hash := sha256.Sum256([]byte(codeVerifier))
	return base64.RawURLEncoding.EncodeToString(hash[:])
}

// startLocalServerAndOpenBrowser starts a local HTTP server to receive the OAuth callback
// and opens the browser to the authorization URL
func startLocalServerAndOpenBrowser(authURL, expectedState string, opts OAuthOptions) (string, string, error) {
	// Channel to receive the authorization code
	codeChan := make(chan string, 1)
	stateChan := make(chan string, 1)
	errChan := make(chan error, 1)
	portChan := make(chan int, 1)

	// Parse the redirect URL to get the path
	parsedURL, err := url.Parse(opts.RedirectURL)
	if err != nil {
		return "", "", fmt.Errorf("invalid redirect URL: %s", err)
	}

	// Path to listen for in the callback
	callbackPath := parsedURL.Path
	if callbackPath == "" {
		callbackPath = "/"
	}

	// Get the hostname from the redirect URL
	hostname := parsedURL.Hostname()
	if hostname == "" {
		hostname = redirectHost
	}

	// Ensure we have a valid port
	port := opts.Port
	if port == 0 {
		if parsedPort := parsedURL.Port(); parsedPort != "" {
			port, _ = strconv.Atoi(parsedPort)
		}
	}

	// Server address with port (may be dynamic if port=0)
	serverAddr := fmt.Sprintf("%s:%d", hostname, port)

	// Start local server
	server := &http.Server{
		Addr: serverAddr,
		Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			// Only process the callback path
			if r.URL.Path != callbackPath {
				http.NotFound(w, r)
				return
			}

			// Extract code and state from URL parameters
			code := r.URL.Query().Get("code")
			state := r.URL.Query().Get("state")
			error := r.URL.Query().Get("error")
			errorDesc := r.URL.Query().Get("error_description")

			if error != "" {
				errMsg := error
				if errorDesc != "" {
					errMsg += ": " + errorDesc
				}
				errChan <- fmt.Errorf("authorization error: %s", errMsg)
				w.WriteHeader(http.StatusBadRequest)
				fmt.Fprintf(w, "Error: %s", errMsg)
				return
			}

			if code == "" {
				errChan <- fmt.Errorf("no authorization code received")
				w.WriteHeader(http.StatusBadRequest)
				fmt.Fprintf(w, "Error: No authorization code received")
				return
			}

			// Send success response to browser
			w.WriteHeader(http.StatusOK)
			fmt.Fprintf(w, "Authorization successful! You can close this window and return to the CLI.")

			// Send code to channel
			codeChan <- code
			stateChan <- state
		}),
	}

	// Listener for getting the actual port when using port 0
	listener, err := net.Listen("tcp", serverAddr)
	if err != nil {
		return "", "", fmt.Errorf("failed to start local server: %s", err)
	}

	// Get the actual port if we used port 0
	if port == 0 {
		addr := listener.Addr().(*net.TCPAddr)
		port = addr.Port
		portChan <- port

		// Update redirect URL with actual port
		parsedURL.Host = fmt.Sprintf("%s:%d", hostname, port)
		opts.RedirectURL = parsedURL.String()

		// Update the auth URL with the new redirect URL
		authURLParsed, err := url.Parse(authURL)
		if err == nil {
			query := authURLParsed.Query()
			query.Set("redirect_uri", opts.RedirectURL)
			authURLParsed.RawQuery = query.Encode()
			authURL = authURLParsed.String()
		}
	}

	// Start server in a goroutine
	go func() {
		fmt.Printf("Starting local server on %s:%d...\n", hostname, port)
		if err := server.Serve(listener); err != nil && err != http.ErrServerClosed {
			errChan <- err
		}
	}()

	// Wait a bit for server to start
	time.Sleep(100 * time.Millisecond)

	// Open browser
	fmt.Println("Opening browser for authorization...")
	if err := openBrowser(authURL); err != nil {
		return "", "", fmt.Errorf("failed to open browser: %s", err)
	}

	// Wait for code, error, or timeout
	select {
	case code := <-codeChan:
		state := <-stateChan
		// Shut down server
		go server.Close()
		return code, state, nil
	case err := <-errChan:
		go server.Close()
		return "", "", err
	case <-time.After(authTimeout):
		go server.Close()
		return "", "", fmt.Errorf("authentication timed out after %s", authTimeout)
	}
}

// openBrowser opens the default browser to the specified URL
func openBrowser(url string) error {
	fmt.Printf("Please authorize the application by visiting this URL in your browser:\n%s\n", url)

	return open.Run(url)
}

// createLoginFromToken creates a login entry using the obtained access token
func createLoginFromToken(name, serverURL string, token *oauth2.Token, insecure bool) error {
	if name == "" {
		var err error
		name, err = task.GenerateLoginName(serverURL, "")
		if err != nil {
			return err
		}
	}

	// Create login object
	login := config.Login{
		Name:         name,
		URL:          serverURL,
		Token:        token.AccessToken,
		RefreshToken: token.RefreshToken,
		Insecure:     insecure,
		VersionCheck: true,
		Created:      time.Now().Unix(),
	}

	// Set token expiry if available
	if !token.Expiry.IsZero() {
		login.TokenExpiry = token.Expiry.Unix()
	}

	// Validate token by getting user info
	client := login.Client()
	u, _, err := client.GetMyUserInfo()
	if err != nil {
		return fmt.Errorf("failed to validate token: %s", err)
	}

	// Set user info
	login.User = u.UserName

	// Get SSH host
	parsedURL, err := url.Parse(serverURL)
	if err != nil {
		return err
	}
	login.SSHHost = parsedURL.Host

	// Add login to config
	if err := config.AddLogin(&login); err != nil {
		return err
	}

	fmt.Printf("Login as %s on %s successful. Added this login as %s\n", login.User, login.URL, login.Name)
	return nil
}

// RefreshAccessToken manually renews an expired access token using the refresh token
// Note: In most cases, tokens are automatically refreshed when using login.Client()
// This function is primarily used for manual refreshes via CLI command
func RefreshAccessToken(login *config.Login) error {
	if login.RefreshToken == "" {
		return fmt.Errorf("no refresh token available")
	}

	// Check if token actually needs refreshing
	if login.TokenExpiry > 0 && time.Now().Unix() < login.TokenExpiry {
		// Token is still valid, no need to refresh
		fmt.Println("Token is still valid, no need to refresh.")
		return nil
	}

	fmt.Println("Access token expired, refreshing...")

	// Create an expired Token object
	expiredToken := &oauth2.Token{
		AccessToken:  login.Token,
		RefreshToken: login.RefreshToken,
		// Set expiry in the past to force refresh
		Expiry: time.Unix(login.TokenExpiry, 0),
	}

	// Set up the OAuth2 config
	ctx := context.Background()
	ctx = context.WithValue(ctx, oauth2.HTTPClient, createHTTPClient(login.Insecure))

	// Configure the OAuth2 endpoints
	oauth2Config := &oauth2.Config{
		ClientID: defaultClientID,
		Endpoint: oauth2.Endpoint{
			TokenURL: fmt.Sprintf("%s/login/oauth/access_token", login.URL),
		},
	}

	// Refresh the token
	newToken, err := oauth2Config.TokenSource(ctx, expiredToken).Token()
	if err != nil {
		return fmt.Errorf("failed to refresh token: %s", err)
	}

	// Update login with new token information
	login.Token = newToken.AccessToken

	if newToken.RefreshToken != "" {
		login.RefreshToken = newToken.RefreshToken
	}

	if !newToken.Expiry.IsZero() {
		login.TokenExpiry = newToken.Expiry.Unix()
	}

	// Save updated login to config
	return config.UpdateLogin(login)
}
