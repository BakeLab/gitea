// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package app

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"gitea.dev/cli/internal/services"

	"github.com/stretchr/testify/require"
	"golang.org/x/crypto/ssh"
)

func TestGiteaRepositoryAdministration(t *testing.T) {
	_, deps, out, diagnostics := giteaDeps(t)
	execute := outputExecutor(t, &deps, out, diagnostics)
	suffix := time.Now().UnixNano()
	orgName := fmt.Sprintf("gt-admin-org-%d", suffix)
	execute("org", "create", orgName, "--visibility", "private")
	var repo services.Repository
	require.NoError(t, json.Unmarshal(execute("repo", "create", fmt.Sprintf("gt-admin-%d", suffix), "--auto-init", "--private", "--json"), &repo))
	// Registered first so it runs last: the organization cannot be deleted while it owns repositories.
	t.Cleanup(func() {
		if err := Execute(context.Background(), deps, []string{"org", "delete", orgName, "--yes"}); err != nil {
			t.Errorf("organization cleanup: %v", err)
		}
	})
	t.Cleanup(func() {
		if err := Execute(context.Background(), deps, []string{"repo", "delete", "-R", repo.FullName, "--yes"}); err != nil {
			t.Errorf("repository cleanup: %v", err)
		}
	})

	var teams []services.Team
	require.NoError(t, json.Unmarshal(execute("team", "list", orgName, "--json"), &teams))
	require.Len(t, teams, 1)
	require.Equal(t, "Owners", teams[0].Name)
	var team services.Team
	require.NoError(t, json.Unmarshal(execute("team", "view", orgName+"/owners", "--json"), &team))
	require.Equal(t, teams[0].ID, team.ID)

	var fork services.Repository
	require.NoError(t, json.Unmarshal(execute("repo", "fork", repo.FullName, "--org", orgName, "--fork-name", "forked", "--json"), &fork))
	t.Cleanup(func() {
		if err := Execute(context.Background(), deps, []string{"repo", "delete", "-R", fork.FullName, "--yes"}); err != nil {
			t.Errorf("fork cleanup: %v", err)
		}
	})
	require.Equal(t, orgName+"/forked", fork.FullName)
	require.True(t, fork.Fork)
	out.Reset()
	diagnostics.Reset()
	require.NoError(t, Execute(context.Background(), deps, []string{"repo", "fork", repo.FullName, "--org", orgName, "--fork-name", "forked", "--json"}))
	require.Contains(t, diagnostics.String(), orgName+"/forked already exists")
	require.NoError(t, json.Unmarshal(out.Bytes(), &fork))
	require.Equal(t, orgName+"/forked", fork.FullName)

	_, privateKey, err := ed25519.GenerateKey(rand.Reader)
	require.NoError(t, err)
	publicKey, err := ssh.NewPublicKey(privateKey.Public())
	require.NoError(t, err)
	keyFile := filepath.Join(t.TempDir(), "deploy.pub")
	require.NoError(t, os.WriteFile(keyFile, append(bytes.TrimSpace(ssh.MarshalAuthorizedKey(publicKey)), " deployer\n"...), 0o600))
	var key services.DeployKey
	require.NoError(t, json.Unmarshal(execute("repo", "deploy-key", "add", keyFile, "-R", repo.FullName, "--json"), &key))
	require.Equal(t, "deployer", key.Title)
	require.True(t, key.ReadOnly)
	var keys []services.DeployKey
	require.NoError(t, json.Unmarshal(execute("repo", "deploy-key", "list", "-R", repo.FullName, "--json"), &keys))
	require.Equal(t, []services.DeployKey{key}, keys)
	execute("repo", "deploy-key", "delete", strconv.FormatInt(key.ID, 10), "-R", repo.FullName, "--yes")
	require.Equal(t, "[]\n", string(execute("repo", "deploy-key", "list", "-R", repo.FullName, "--json")))

	execute("repo", "archive", repo.FullName, "--yes")
	require.NoError(t, json.Unmarshal(execute("repo", "view", "-R", repo.FullName, "--json"), &repo))
	require.True(t, repo.Archived)
	execute("repo", "unarchive", repo.FullName, "--yes")
	require.NoError(t, json.Unmarshal(execute("repo", "view", "-R", repo.FullName, "--json"), &repo))
	require.False(t, repo.Archived)

	renamed := fmt.Sprintf("gt-renamed-%d", suffix)
	execute("repo", "rename", renamed, "-R", repo.FullName, "--yes")
	owner, _, _ := strings.Cut(repo.FullName, "/")
	require.NoError(t, json.Unmarshal(execute("repo", "view", "-R", owner+"/"+renamed, "--json"), &repo))
	require.Equal(t, renamed, repo.Name)
	execute("repo", "transfer", orgName, "-R", repo.FullName, "--yes")
	require.NoError(t, json.Unmarshal(execute("repo", "view", "-R", orgName+"/"+renamed, "--json"), &repo))
	require.Equal(t, orgName+"/"+renamed, repo.FullName, "a transfer into an organization the account owns completes at once")

	file := filepath.Join(t.TempDir(), "tool.bin")
	require.NoError(t, os.WriteFile(file, []byte("package bytes"), 0o600))
	pkg := fmt.Sprintf("gt-tool-%d", suffix)
	execute("package", "upload", pkg, "1.0.0", file, "--owner", orgName)
	out.Reset()
	require.ErrorContains(t, Execute(context.Background(), deps, []string{"package", "upload", pkg, "1.0.0", file, "--owner", orgName}), "tool.bin", "a version keeps its files")
	execute("package", "upload", pkg, "1.1.0", file, "--owner", orgName)
	var packages []services.Package
	require.NoError(t, json.Unmarshal(execute("package", "list", "--owner", orgName, "--type", "generic", "--search", pkg, "--json"), &packages))
	require.Len(t, packages, 2)
	var versions []services.Package
	require.NoError(t, json.Unmarshal(execute("package", "versions", "generic", pkg, "--owner", orgName, "--json"), &versions))
	require.ElementsMatch(t, []string{"1.0.0", "1.1.0"}, []string{versions[0].Version, versions[1].Version})
	var details packageDetails
	require.NoError(t, json.Unmarshal(execute("package", "view", "generic", pkg, "1.0.0", "--owner", orgName, "--json"), &details))
	require.Equal(t, "1.0.0", details.Version)
	require.Len(t, details.Files, 1)
	require.Equal(t, "tool.bin", details.Files[0].Name)
	require.EqualValues(t, len("package bytes"), details.Files[0].Size)
	execute("package", "link", "generic", pkg, renamed, "--owner", orgName)
	require.NoError(t, json.Unmarshal(execute("package", "view", "generic", pkg, "1.0.0", "--owner", orgName, "--json"), &details))
	require.Equal(t, orgName+"/"+renamed, details.Repository)
	execute("package", "unlink", "generic", pkg, "--owner", orgName)
	require.NoError(t, json.Unmarshal(execute("package", "view", "generic", pkg, "1.0.0", "--owner", orgName, "--json"), &details))
	require.Empty(t, details.Repository)
	execute("package", "delete", "generic", pkg, "1.0.0", "--owner", orgName, "--yes")
	require.NoError(t, json.Unmarshal(execute("package", "versions", "generic", pkg, "--owner", orgName, "--json"), &versions))
	require.Len(t, versions, 1)
	execute("package", "delete", "generic", pkg, "--all", "--owner", orgName, "--yes")
	require.Equal(t, "[]\n", string(execute("package", "list", "--owner", orgName, "--search", pkg, "--json")))
}
