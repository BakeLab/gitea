// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package api

import (
	"crypto/tls"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"strings"

	"code.gitea.io/tea/modules/config"
)

// Client provides direct HTTP access to Gitea API
type Client struct {
	baseURL    string
	token      string
	httpClient *http.Client
}

// NewClient creates a new API client from a Login config
func NewClient(login *config.Login) *Client {
	// Refresh OAuth token if expired or near expiry
	if err := login.RefreshOAuthTokenIfNeeded(); err != nil {
		log.Printf("Warning: failed to refresh OAuth token: %v", err)
	}

	httpClient := &http.Client{
		Transport: &http.Transport{
			TLSClientConfig: &tls.Config{InsecureSkipVerify: login.Insecure},
		},
	}

	return &Client{
		baseURL:    strings.TrimSuffix(login.URL, "/"),
		token:      login.Token,
		httpClient: httpClient,
	}
}

// Do executes an HTTP request with authentication headers
func (c *Client) Do(method, endpoint string, body io.Reader, headers map[string]string) (*http.Response, error) {
	// Build the full URL
	reqURL, err := c.buildURL(endpoint)
	if err != nil {
		return nil, err
	}

	req, err := http.NewRequest(method, reqURL, body)
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	// Set authentication header
	if c.token != "" {
		req.Header.Set("Authorization", "token "+c.token)
	}

	// Set default content type for requests with body
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}

	// Apply custom headers (can override defaults)
	for key, value := range headers {
		req.Header.Set(key, value)
	}

	return c.httpClient.Do(req)
}

// buildURL constructs the full URL from an endpoint
func (c *Client) buildURL(endpoint string) (string, error) {
	// If endpoint is already a full URL, validate it matches the login's host
	if strings.HasPrefix(endpoint, "http://") || strings.HasPrefix(endpoint, "https://") {
		endpointURL, err := url.Parse(endpoint)
		if err != nil {
			return "", fmt.Errorf("invalid URL: %w", err)
		}
		baseURL, err := url.Parse(c.baseURL)
		if err != nil {
			return "", fmt.Errorf("invalid base URL: %w", err)
		}
		if endpointURL.Host != baseURL.Host {
			return "", fmt.Errorf("URL host %q does not match login host %q (token would be sent to wrong server)", endpointURL.Host, baseURL.Host)
		}
		return endpoint, nil
	}

	// Ensure endpoint starts with /
	if !strings.HasPrefix(endpoint, "/") {
		endpoint = "/" + endpoint
	}

	// Auto-prefix /api/v1/ if not present
	if !strings.HasPrefix(endpoint, "/api/") {
		endpoint = "/api/v1" + endpoint
	}

	return c.baseURL + endpoint, nil
}
