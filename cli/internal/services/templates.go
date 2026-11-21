// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package services

import (
	"context"
	"slices"
	"strings"

	"gitea.dev/cli/internal/transport"
	gitea "gitea.dev/sdk"
)

// LicenseEntry is a license that Gitea offers for new repositories.
type LicenseEntry struct {
	Key  string `json:"key"`
	Name string `json:"name"`
	URL  string `json:"url"`
}

// License is a license with its text.
type License struct {
	Key            string `json:"key"`
	Name           string `json:"name"`
	URL            string `json:"url"`
	Body           string `json:"body"`
	Implementation string `json:"implementation"`
}

// GitignoreEntry is the name of a .gitignore template.
type GitignoreEntry struct {
	Name string `json:"name"`
}

// Gitignore is a .gitignore template with its content.
type Gitignore struct {
	Name   string `json:"name"`
	Source string `json:"source"`
}

// Templates reads the license and .gitignore templates that Gitea offers for new repositories.
type Templates struct{ Client *transport.Client }

// Licenses lists the licenses.
func (s Templates) Licenses(ctx context.Context) ([]LicenseEntry, error) {
	licenses, _, err := call(ctx, s.Client, func(ctx context.Context, c *gitea.Client) ([]*gitea.LicensesTemplateListEntry, *gitea.Response, error) {
		return c.Templates.ListLicenses(ctx)
	})
	return convertAll(licenses, func(license *gitea.LicensesTemplateListEntry) LicenseEntry {
		return LicenseEntry{Key: license.Key, Name: license.Name, URL: license.URL}
	}), err
}

// License returns a license by its key, matching it without regard to case when Gitea does not know
// the key as written.
func (s Templates) License(ctx context.Context, key string) (License, error) {
	fetch := func(key string) (License, error) {
		license, _, err := call(ctx, s.Client, func(ctx context.Context, c *gitea.Client) (*gitea.LicenseTemplateInfo, *gitea.Response, error) {
			return c.Templates.GetLicense(ctx, key)
		})
		if err != nil {
			return License{}, err
		}
		return License{Key: license.Key, Name: license.Name, URL: license.URL, Body: license.Body, Implementation: license.Implementation}, nil
	}
	return fetchIgnoringCase(ctx, "license", key, fetch, Repositories(s).Licenses)
}

// Gitignores lists the names of the .gitignore templates.
func (s Templates) Gitignores(ctx context.Context) ([]GitignoreEntry, error) {
	names, err := Repositories(s).Gitignores(ctx)
	entries := make([]GitignoreEntry, len(names))
	for i, name := range names {
		entries[i] = GitignoreEntry{Name: name}
	}
	return entries, err
}

// Gitignore returns a .gitignore template by name, matching it without regard to case when Gitea
// does not know the name as written.
func (s Templates) Gitignore(ctx context.Context, name string) (Gitignore, error) {
	fetch := func(name string) (Gitignore, error) {
		template, _, err := call(ctx, s.Client, func(ctx context.Context, c *gitea.Client) (*gitea.GitignoreTemplateInfo, *gitea.Response, error) {
			return c.Templates.GetGitignore(ctx, name)
		})
		if err != nil {
			return Gitignore{}, err
		}
		return Gitignore{Name: template.Name, Source: template.Source}, nil
	}
	return fetchIgnoringCase(ctx, ".gitignore template", name, fetch, Repositories(s).Gitignores)
}

// fetchIgnoringCase fetches the template called name, or the one that list names without regard to
// case when Gitea does not know name as written.
func fetchIgnoringCase[T any](ctx context.Context, kind, name string, fetch func(string) (T, error), list func(context.Context) ([]string, error)) (T, error) {
	item, err := fetch(name)
	if !IsNotFound(err) {
		return item, Classify(err, kind, name, "")
	}
	if names, listErr := list(ctx); listErr == nil {
		if i := slices.IndexFunc(names, func(other string) bool { return strings.EqualFold(other, name) && other != name }); i >= 0 {
			return fetch(names[i])
		}
	}
	var zero T
	return zero, Classify(err, kind, name, "")
}
