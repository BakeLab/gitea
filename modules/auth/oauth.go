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
		ClientID:    config.DefaultClientID,
		RedirectURL: fmt.Sprintf("http://%s:%d", redirectHost, redirectPort),
		Port:        redirectPort,
	}
	return OAuthLoginWithFullOptions(opts)
}

// OAuthLoginWithFullOptions performs an OAuth2 PKCE login flow with full options control
func OAuthLoginWithFullOptions(opts OAuthOptions) error {
	serverURL, token, err := performBrowserOAuthFlow(opts)
	if err != nil {
		return err
	}

	return createLoginFromToken(opts.Name, serverURL, token, opts.Insecure)
}

// performBrowserOAuthFlow performs the browser-based OAuth2 PKCE flow and returns the token.
// This is the shared implementation used by both new logins and re-authentication.
func performBrowserOAuthFlow(opts OAuthOptions) (serverURL string, token *oauth2.Token, err error) {
	// Normalize URL
	normalizedURL, err := utils.NormalizeURL(opts.URL)
	if err != nil {
		return "", nil, fmt.Errorf("unable to parse URL: %s", err)
	}
	serverURL = normalizedURL.String()

	// Set defaults if needed
	if opts.ClientID == "" {
		opts.ClientID = config.DefaultClientID
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
		return "", nil, fmt.Errorf("failed to generate code verifier: %s", err)
	}

	// Generate code challenge (SHA256 hash of code verifier)
	codeChallenge := generateCodeChallenge(codeVerifier)

	// Set up the OAuth2 config
	ctx := context.Background()
	ctx = context.WithValue(ctx, oauth2.HTTPClient, createHTTPClient(opts.Insecure))

	// Configure the OAuth2 endpoints
	authURL := fmt.Sprintf("%s/login/oauth/authorize", normalizedURL)
	tokenURL := fmt.Sprintf("%s/login/oauth/access_token", normalizedURL)

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
		return "", nil, fmt.Errorf("failed to generate state: %s", err)
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
			fmt.Println("\nError: Redirect URL not registered in Gitea")
			fmt.Println("\nTo fix this, you need to register the redirect URL in Gitea:")
			fmt.Printf("1. Go to your Gitea instance: %s\n", normalizedURL)
			fmt.Println("2. Sign in and go to Settings > Applications")
			fmt.Println("3. Register a new OAuth2 application with:")
			fmt.Printf("   - Application Name: tea-cli (or any name)\n")
			fmt.Printf("   - Redirect URI: %s\n", opts.RedirectURL)
			fmt.Println("4. Copy the Client ID and try again with:")
			fmt.Printf("   tea login add --oauth --client-id YOUR_CLIENT_ID --redirect-url %s\n", opts.RedirectURL)
			fmt.Println("\nAlternatively, you can use a token-based login: tea login add")
		}
		return "", nil, fmt.Errorf("authorization failed: %s", err)
	}

	// Verify state to prevent CSRF attacks
	if state != receivedState {
		return "", nil, fmt.Errorf("state mismatch, possible CSRF attack")
	}

	// Exchange authorization code for token
	token, err = oauth2Config.Exchange(ctx, code, oauth2.SetAuthURLParam("code_verifier", codeVerifier))
	if err != nil {
		return "", nil, fmt.Errorf("token exchange failed: %s", err)
	}

	return serverURL, token, nil
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
		fmt.Println("Failed to open browser: ", err)
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

// RefreshAccessToken manually renews an access token using the refresh token.
// This is used by the "tea login oauth-refresh" command for explicit token refresh.
// For automatic threshold-based refresh, use login.Client() which handles it internally.
func RefreshAccessToken(login *config.Login) error {
	return login.RefreshOAuthToken()
}

// ReauthenticateLogin performs a full browser-based OAuth flow to get new tokens
// for an existing login. This is used when the refresh token is expired or invalid.
func ReauthenticateLogin(login *config.Login) error {
	opts := OAuthOptions{
		Name:        login.Name,
		URL:         login.URL,
		Insecure:    login.Insecure,
		ClientID:    config.DefaultClientID,
		RedirectURL: fmt.Sprintf("http://%s:%d", redirectHost, redirectPort),
		Port:        redirectPort,
	}

	_, token, err := performBrowserOAuthFlow(opts)
	if err != nil {
		return err
	}

	// Update the existing login with new token data
	login.Token = token.AccessToken
	if token.RefreshToken != "" {
		login.RefreshToken = token.RefreshToken
	}
	if !token.Expiry.IsZero() {
		login.TokenExpiry = token.Expiry.Unix()
	}

	// Save updated login
	return config.SaveLoginTokens(login)
}
