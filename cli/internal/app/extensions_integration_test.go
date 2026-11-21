// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package app

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"gitea.dev/cli/internal/extensions"
	"gitea.dev/cli/internal/services"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestGiteaExtensionInstall(t *testing.T) {
	_, deps, out, diagnostics := giteaDeps(t)
	execute := outputExecutor(t, &deps, out, diagnostics)
	name := fmt.Sprintf("it%d", time.Now().UnixNano()%1_000_000_000)
	var repo services.Repository
	require.NoError(t, json.Unmarshal(execute("repo", "create", "gt-"+name, "--auto-init", "--private", "--json"), &repo))
	t.Cleanup(func() {
		if err := Execute(context.Background(), deps, []string{"repo", "delete", "-R", repo.FullName, "--yes"}); err != nil {
			t.Errorf("repository cleanup: %v", err)
		}
	})
	dir := t.TempDir()
	asset := filepath.Join(dir, "gt-"+name+"-"+runtime.GOOS+"-"+runtime.GOARCH)
	if runtime.GOOS == "windows" {
		asset += ".exe"
	}
	content := []byte("extension " + name)
	require.NoError(t, os.WriteFile(asset, content, 0o755))
	checksums := filepath.Join(dir, "checksums.txt")
	require.NoError(t, os.WriteFile(checksums, fmt.Appendf(nil, "%x  %s\n", sha256.Sum256(content), filepath.Base(asset)), 0o644))
	out.Reset()
	require.NoError(t, Execute(context.Background(), deps, []string{"release", "create", "v1.0.0", asset, checksums, "-R", repo.FullName}))

	runs := &extensionRun{}
	deps.RunExtension = runs.run
	execute("extension", "install", repo.FullName, "--yes")
	extension, err := (extensions.Store{Dir: filepath.Join(deps.DataDir, "extensions")}).Lookup(name)
	require.NoError(t, err)
	assert.Equal(t, "v1.0.0", extension.Tag)
	assert.Contains(t, []string{"server", "checksums.txt"}, extension.DigestSource)
	assert.Equal(t, fmt.Sprintf("%x", sha256.Sum256(content)), extension.SHA256)

	execute(name, "argument")
	require.Len(t, runs.commands, 1)
	assert.Equal(t, []string{"argument"}, runs.commands[0].Args)
	execute("extension", "remove", name, "--yes")
}
