// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package git

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestOpenClassifiesLocalizedNotRepoError(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("test uses a POSIX shell script as fake git")
	}

	fakeGitDir := t.TempDir()
	fakeGit := filepath.Join(fakeGitDir, "git")
	script := `#!/bin/sh
locale="${LC_ALL:-${LANG:-C}}"
if [ "$locale" = "C" ]; then
	echo "fatal: not a git repository (or any of the parent directories): .git" >&2
else
	echo "fatal: Kein Git-Repository (oder irgendein Elternverzeichnis bis zum Einhangepunkt /)" >&2
fi
exit 128
`
	require.NoError(t, os.WriteFile(fakeGit, []byte(script), 0o755))

	t.Setenv("PATH", fakeGitDir+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("LC_ALL", "de_DE.UTF-8")
	t.Setenv("LANG", "de_DE.UTF-8")
	t.Setenv("LANGUAGE", "de_DE:de")

	_, err := (cliBackend{}).Open(t.TempDir())
	require.ErrorIs(t, err, ErrRepositoryNotExists)
}
