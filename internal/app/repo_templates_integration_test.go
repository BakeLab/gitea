// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package app

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"
	"time"

	"gitea.dev/cli/internal/services"
	"github.com/stretchr/testify/require"
)

func TestGiteaRepositoryLicenseAndGitignoreTemplates(t *testing.T) {
	_, deps, out, diagnostics := giteaDeps(t)
	execute := outputExecutor(t, &deps, out, diagnostics)

	var licenses []services.LicenseEntry
	require.NoError(t, json.Unmarshal(execute("repo", "license", "list", "--json"), &licenses))
	require.NotEmpty(t, licenses)
	var mit services.LicenseEntry
	for _, license := range licenses {
		if license.Key == "MIT" {
			mit = license
		}
	}
	require.Equal(t, "MIT", mit.Key, "Gitea offers the MIT license")
	require.Contains(t, string(execute("repo", "license", "list")), "MIT\t", "piped, the keys come first")

	text := string(execute("repo", "license", "view", "MIT"))
	require.Contains(t, text, "MIT License")
	require.Contains(t, text, "Permission is hereby granted")
	require.True(t, len(text) > 0 && text[len(text)-1] == '\n', "the text ends in a line break, for a file written from it")
	require.Equal(t, text, string(execute("repo", "license", "view", "mit")), "a key is matched without regard to case")
	var license services.License
	require.NoError(t, json.Unmarshal(execute("repo", "license", "view", "MIT", "--json"), &license))
	require.Equal(t, services.License{Key: "MIT", Name: license.Name, URL: license.URL, Body: license.Body, Implementation: license.Implementation}, license)
	require.Contains(t, license.Body, "MIT License")
	require.NotEmpty(t, license.Name)

	var templates []services.GitignoreEntry
	require.NoError(t, json.Unmarshal(execute("repo", "gitignore", "list", "--json"), &templates))
	require.Contains(t, templates, services.GitignoreEntry{Name: "Go"})
	source := string(execute("repo", "gitignore", "view", "Go"))
	require.Contains(t, source, "*.exe")
	require.Equal(t, source, string(execute("repo", "gitignore", "view", "go")), "a name is matched without regard to case")
	var gitignore services.Gitignore
	require.NoError(t, json.Unmarshal(execute("repo", "gitignore", "view", "Go", "--json"), &gitignore))
	require.Equal(t, "Go", gitignore.Name)
	require.Equal(t, source, gitignore.Source)

	require.ErrorContains(t, Execute(context.Background(), deps, []string{"repo", "license", "view", "no-such-license"}), "no-such-license")
	require.ErrorContains(t, Execute(context.Background(), deps, []string{"repo", "gitignore", "view", "NoSuchTemplate"}), "NoSuchTemplate")

	// The keys and names are the values that repo create takes.
	var repo services.Repository
	require.NoError(t, json.Unmarshal(execute("repo", "create", fmt.Sprintf("gt-license-%d", time.Now().UnixNano()), "--private", "--auto-init", "--license", "MIT", "--gitignores", "Go", "--json"), &repo))
	t.Cleanup(func() {
		if err := Execute(context.Background(), deps, []string{"repo", "delete", "-R", repo.FullName, "--yes"}); err != nil {
			t.Errorf("repository cleanup: %v", err)
		}
	})
	var file struct{ Content string }
	require.NoError(t, json.Unmarshal(execute("api", "repos/"+repo.FullName+"/contents/LICENSE"), &file))
	require.NotEmpty(t, file.Content, "the repository got the license that was listed")
}
