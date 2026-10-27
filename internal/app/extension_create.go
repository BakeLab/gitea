// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package app

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"gitea.dev/cli/internal/clierr"
	"gitea.dev/cli/internal/extensions"
	"github.com/spf13/cobra"
)

const extensionScriptTemplate = `#!/bin/sh
# gt %[1]s runs this script with the arguments after its name. gt sets GITEA_CLI_EXTENSION and
# GITEA_CLI_EXECUTABLE, and passes --host, --repo and --user as GITEA_CLI_HOST, GITEA_CLI_REPO
# and GITEA_CLI_USER; with a credential grant, GITEA_TOKEN and GITEA_INSTANCE_URL as well.
set -e

echo "Hello from gt %[1]s!"
# "$GITEA_CLI_EXECUTABLE" api user --jq .login
`

const extensionGoTemplate = `package main

import (
	"fmt"
	"os"
)

// gt %[1]s runs this program with the arguments after its name. gt sets GITEA_CLI_EXTENSION and
// GITEA_CLI_EXECUTABLE, and passes --host, --repo and --user as GITEA_CLI_HOST, GITEA_CLI_REPO
// and GITEA_CLI_USER; with a credential grant, GITEA_TOKEN and GITEA_INSTANCE_URL as well.
func main() {
	fmt.Printf("Hello from gt %[1]s! Arguments: %%q\n", os.Args[1:])
}
`

const extensionReleaseTemplate = `name: release

on:
  push:
    tags: ["v*"]

jobs:
  release:
    runs-on: ubuntu-latest
    steps:
      - uses: actions/checkout@v4
      - uses: actions/setup-go@v5
        with:
          go-version: stable
      - name: build
        run: |
          mkdir dist
          for platform in linux/amd64 linux/arm64 darwin/amd64 darwin/arm64 windows/amd64; do
            os=${platform%%/*}; arch=${platform##*/}; ext=""
            [ "$os" = windows ] && ext=.exe
            CGO_ENABLED=0 GOOS=$os GOARCH=$arch go build -trimpath -o "dist/gt-%[1]s-$os-$arch$ext" .
          done
          (cd dist && sha256sum * > checksums.txt)
      - name: release
        env:
          GITEA_CLI_HOST: ${{ github.server_url }}
          GITEA_CLI_REPO: ${{ github.repository }}
          GITEA_TOKEN: ${{ secrets.GITHUB_TOKEN }}
        run: go run gitea.dev/cli/cmd/gt@latest release create "$GITHUB_REF_NAME" dist/* --title "$GITHUB_REF_NAME"
`

func (a *application) extensionCreateCommand() *cobra.Command {
	var precompiled string
	cmd := &cobra.Command{
		Use: "create <name>", Short: "Create a new extension", Args: nonBlankArgs(argsExact(1), "extension name"),
		Annotations: map[string]string{"gt.offline": "true", "gt.text": "extension create does not return a resource"},
		Long: `Create the directory gt-NAME with a new extension in a fresh Git repository: an executable
gt-NAME script, or with --precompiled=go a Go program and a workflow that publishes it as
release assets with their checksums.txt. gt extension install . runs it while you work on it;
build a Go extension with go build first.`,
		Example: `$ gt extension create hello
$ gt extension create hello --precompiled=go`,
		RunE: func(cmd *cobra.Command, args []string) error {
			name := strings.TrimPrefix(strings.TrimPrefix(args[0], "gt-"), "gh-")
			if !extensions.ValidName(name) {
				return clierr.Wrap(clierr.Usage, fmt.Errorf("invalid extension name %q: use lowercase letters, digits and hyphens", name))
			}
			if isCommand(cmd.Root(), name) {
				return clierr.Wrap(clierr.Usage, fmt.Errorf("extension %s cannot be created: gt %s is a built-in command", name, name))
			}
			if precompiled != "" && precompiled != "go" {
				return clierr.Wrap(clierr.Usage, fmt.Errorf("--precompiled must be go, not %q", precompiled))
			}
			repo := "gt-" + name
			dir := filepath.Join(a.options.cwd, repo)
			if err := os.Mkdir(dir, 0o755); errors.Is(err, fs.ErrExist) {
				return fmt.Errorf("%s already exists", dir)
			} else if err != nil {
				return err
			}
			files := map[string]string{
				"gt-extension.yml": fmt.Sprintf("description: %s\n# min_gt_version: 1.0.0\n# credentials: true\n", "Describe what gt "+name+" does"),
				"README.md":        fmt.Sprintf("# %s\n\nA gt extension. Install it with:\n\n    gt extension install OWNER/%s\n\nand run it as `gt %s`.\n", repo, repo, name),
			}
			mode := map[string]fs.FileMode{}
			if precompiled == "go" {
				files["main.go"] = fmt.Sprintf(extensionGoTemplate, name)
				files["go.mod"] = fmt.Sprintf("module %s\n\ngo 1.24\n", repo)
				files[".gitignore"] = "/dist/\n/" + repo + "\n"
				files[filepath.Join(".gitea", "workflows", "release.yml")] = fmt.Sprintf(extensionReleaseTemplate, name)
			} else {
				files[repo] = fmt.Sprintf(extensionScriptTemplate, name)
				mode[repo] = 0o755
			}
			for file, content := range files {
				path := filepath.Join(dir, file)
				if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
					return err
				}
				if err := os.WriteFile(path, []byte(content), cmpMode(mode[file])); err != nil {
					return err
				}
			}
			if _, err := a.gitIn(cmd.Context(), dir, "init", "--quiet"); err != nil {
				return err
			}
			if _, err := a.gitIn(cmd.Context(), dir, "add", "--all"); err != nil {
				return err
			}
			build := ""
			if precompiled == "go" {
				build = "go build && "
			}
			a.done(cmd, "Created extension %s in %s; try it with: cd %s && %sgt extension install .", name, dir, dir, build)
			return nil
		},
	}
	cmd.Flags().StringVar(&precompiled, "precompiled", "", "Create a precompiled extension in `go` instead of a script")
	_ = cmd.RegisterFlagCompletionFunc("precompiled", cobra.FixedCompletions([]cobra.Completion{"go"}, cobra.ShellCompDirectiveNoFileComp))
	return cmd
}

// cmpMode is mode, or the mode of a regular file.
func cmpMode(mode fs.FileMode) fs.FileMode {
	if mode == 0 {
		return 0o644
	}
	return mode
}
