// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package services

import (
	"context"
	"errors"
	"strings"
	"sync"

	"gitea.dev/cli/internal/transport"
	gitea "gitea.dev/sdk"
)

// accountCache holds each client's authenticated login per Sudo user; failed lookups are retried.
var accountCache sync.Map

type accountKey struct {
	client *transport.Client
	sudo   string
}

// CurrentAccount verifies the credential against the selected Gitea installation and returns
// the authenticated login. It asks the server once per client and Sudo user.
func CurrentAccount(ctx context.Context, client *transport.Client) (string, error) {
	key := accountKey{client, client.Sudo}
	if cached, ok := accountCache.Load(key); ok {
		return cached.(string), nil
	}
	user, _, err := call(ctx, client, func(ctx context.Context, c *gitea.Client) (*gitea.User, *gitea.Response, error) {
		return c.Users.GetMyUserInfo(ctx)
	})
	if err != nil {
		return "", err
	}
	account := login(user)
	if strings.TrimSpace(account) == "" {
		return "", errors.New("Gitea did not return an authenticated account")
	}
	accountCache.Store(key, account)
	return account, nil
}

// MatchAccount reports whether selected matches the authenticated login.
// An empty selection accepts any account.
func MatchAccount(selected, authenticated string) bool {
	return selected == "" || strings.EqualFold(selected, authenticated)
}
