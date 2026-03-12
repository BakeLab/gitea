// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package config

import (
	"path/filepath"
	"sync"
	"time"

	"github.com/adrg/xdg"
	"github.com/go-authgate/sdk-go/credstore"
	"golang.org/x/oauth2"
)

var (
	tokenStore     *credstore.SecureStore[credstore.Token]
	tokenStoreOnce sync.Once
)

func getTokenStore() *credstore.SecureStore[credstore.Token] {
	tokenStoreOnce.Do(func() {
		filePath := filepath.Join(xdg.ConfigHome, "tea", "credentials.json")
		tokenStore = credstore.DefaultTokenSecureStore("tea-cli", filePath)
	})
	return tokenStore
}

// LoadOAuthToken loads OAuth tokens from the secure store.
func LoadOAuthToken(loginName string) (*credstore.Token, error) {
	tok, err := getTokenStore().Load(loginName)
	if err != nil {
		return nil, err
	}
	return &tok, nil
}

// SaveOAuthToken saves OAuth tokens to the secure store.
func SaveOAuthToken(loginName, accessToken, refreshToken string, expiresAt time.Time) error {
	return getTokenStore().Save(loginName, credstore.Token{
		AccessToken:  accessToken,
		RefreshToken: refreshToken,
		ExpiresAt:    expiresAt,
		ClientID:     loginName,
	})
}

// DeleteOAuthToken removes tokens from the secure store.
func DeleteOAuthToken(loginName string) error {
	return getTokenStore().Delete(loginName)
}

// SaveOAuthTokenFromOAuth2 saves an oauth2.Token to credstore, falling back to
// the existing login's values for empty refresh token or zero expiry.
func SaveOAuthTokenFromOAuth2(loginName string, token *oauth2.Token, login *Login) error {
	refreshToken := token.RefreshToken
	if refreshToken == "" {
		refreshToken = login.GetRefreshToken()
	}
	expiry := token.Expiry
	if expiry.IsZero() {
		expiry = login.GetTokenExpiry()
	}
	return SaveOAuthToken(loginName, token.AccessToken, refreshToken, expiry)
}
