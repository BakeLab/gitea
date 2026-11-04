// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package app

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"

	"gitea.dev/cli/internal/git"
	"gitea.dev/cli/internal/services"
	"gitea.dev/cli/internal/settings"
	"gitea.dev/cli/internal/terminal"
	"github.com/kballard/go-shellquote"
	"github.com/stretchr/testify/require"
	"golang.org/x/crypto/ssh"
)

// giteaTarget is the disposable instance configured for TestGitea* integration tests.
type giteaTarget struct{ url, token string }

// giteaDeps skips unless a disposable Gitea instance is configured and returns
// dependencies authenticated against it that run the system Git binary.
func giteaDeps(t *testing.T) (giteaTarget, Dependencies, *bytes.Buffer, *bytes.Buffer) {
	t.Helper()
	instance, tokenFile := os.Getenv("GITEA_CLI_TEST_URL"), os.Getenv("GITEA_CLI_TEST_TOKEN_FILE")
	if instance == "" || tokenFile == "" {
		t.Skip("disposable Gitea environment is not configured")
	}
	gitea := giteaTarget{url: instance, token: readTokenFile(t, tokenFile)}
	deps, out, diagnostics := testDeps(t)
	deps.Git = nil
	deps.ServerVersion = nil // ask the instance, so the version gates run
	deps.Getenv = gitea.env(gitea.token)
	return gitea, deps, out, diagnostics
}

func (g giteaTarget) env(token string) func(string) string {
	return envFunc(map[string]string{"GITEA_CLI_HOST": g.url, "GITEA_TOKEN": token})
}

func readTokenFile(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	require.NoError(t, err)
	token := strings.TrimSpace(string(data))
	require.NotEmpty(t, token)
	return token
}

// outputExecutor runs commands with the current value of *deps and returns their
// standard output, requiring success without diagnostics.
func outputExecutor(t *testing.T, deps *Dependencies, out, diagnostics *bytes.Buffer) func(args ...string) []byte {
	return func(args ...string) []byte {
		t.Helper()
		out.Reset()
		diagnostics.Reset()
		require.NoError(t, Execute(context.Background(), *deps, args))
		require.Empty(t, diagnostics.String())
		return bytes.Clone(out.Bytes())
	}
}

// withEnv overlays vars on an existing environment lookup.
func withEnv(getenv func(string) string, vars map[string]string) func(string) string {
	return func(key string) string {
		if value, ok := vars[key]; ok {
			return value
		}
		return getenv(key)
	}
}

func verifyResourceBrowser(t *testing.T, deps Dependencies, resource string, args ...string) {
	t.Helper()
	require.NotEmpty(t, resource)
	browser := &recordingBrowser{}
	deps.Browser = browser
	out, diagnostics := &bytes.Buffer{}, &bytes.Buffer{}
	deps.Streams.Out, deps.Streams.ErrOut = out, diagnostics
	require.NoError(t, Execute(context.Background(), deps, append(args, "--web", "--no-input")))
	require.Equal(t, resource, browser.resource)
	require.Empty(t, out.String())
	require.Empty(t, diagnostics.String())
}

func verifyEditorSubmission(t *testing.T, deps Dependencies, repository, number string, review bool) {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Log("POSIX editor integration fixture is unavailable on Windows")
		return
	}
	directory := t.TempDir()
	program := filepath.Join(directory, "editor with spaces")
	require.NoError(t, os.WriteFile(program, []byte("#!/bin/sh\nprintf 'Editor integration body' > \"$1\"\n"), 0o700))
	out, diagnostics := &bytes.Buffer{}, &bytes.Buffer{}
	deps.Streams.Out, deps.Streams.ErrOut = out, diagnostics
	deps.Editor = terminal.SystemEditor{Directory: directory, Streams: deps.Streams}
	deps.Getenv = withEnv(deps.Getenv, map[string]string{"GITEA_CLI_EDITOR": shellquote.Join(program)})
	args := []string{"issue", "comment", "create", number}
	if review {
		args = []string{"pr", "review", number, "--pending"}
	}
	require.NoError(t, Execute(context.Background(), deps, append(args, "--repo", repository, "--editor", "--no-input", "--json")))
	var result struct {
		ID   int64  `json:"id"`
		Body string `json:"body"`
	}
	require.NoError(t, json.Unmarshal(out.Bytes(), &result))
	require.Equal(t, "Editor integration body", result.Body)
	require.Positive(t, result.ID)
	defer func() {
		args := []string{"issue", "comment", "delete", fmt.Sprint(result.ID)}
		if review {
			args = []string{"pr", "review", "delete", number, fmt.Sprint(result.ID)}
		}
		if err := Execute(context.Background(), deps, append(args, "--repo", repository, "--yes")); err != nil {
			t.Errorf("editor submission cleanup: %v", err)
		}
	}()
	files, err := filepath.Glob(filepath.Join(directory, "gt-draft-*.md"))
	require.NoError(t, err)
	require.Empty(t, files)
	require.Empty(t, diagnostics.String())
}

func verifyBodyEditorIntegration(t *testing.T, deps Dependencies, group, repository, number, previous string) {
	t.Helper()
	out, diagnostics := &bytes.Buffer{}, &bytes.Buffer{}
	deps.Streams.Out, deps.Streams.ErrOut = out, diagnostics
	base := []string{group, "edit", number, "--repo", repository, "--no-input"}
	require.NoError(t, Execute(context.Background(), deps, append(base, "--body", "Original editor body")))
	defer func() {
		if err := Execute(context.Background(), deps, append(base, "--body", previous)); err != nil {
			t.Errorf("editor body restoration: %v", err)
		}
	}()
	editor, path := newDraftEditor(t, "Edited server body")
	deps.Editor = editor
	deps.Getenv = withEnv(deps.Getenv, map[string]string{"GITEA_CLI_EDITOR": "integration-editor"})
	out.Reset()
	require.NoError(t, Execute(context.Background(), deps, append(base, "--editor", "--json=body")))
	require.Equal(t, "Original editor body", editor.initial)
	require.JSONEq(t, `{"body":"Edited server body"}`, out.String())
	requireDraft(t, path, false)
	require.Empty(t, diagnostics.String())
}

func executeWithEditorIntegration(t *testing.T, deps Dependencies, initial, body string, args ...string) []byte {
	t.Helper()
	out, diagnostics := &bytes.Buffer{}, &bytes.Buffer{}
	deps.Streams.Out, deps.Streams.ErrOut = out, diagnostics
	editor, path := newDraftEditor(t, body)
	deps.Editor = editor
	deps.Getenv = withEnv(deps.Getenv, map[string]string{"GITEA_CLI_EDITOR": "integration-editor"})
	require.NoError(t, Execute(context.Background(), deps, append(args, "--editor", "--no-input", "--json")))
	require.Equal(t, initial, editor.initial)
	require.Equal(t, "integration-editor", editor.command)
	requireDraft(t, path, false)
	require.Empty(t, diagnostics.String())
	return bytes.Clone(out.Bytes())
}

func verifyIssueAttachmentLifecycle(t *testing.T, deps Dependencies, group, repository, number string) {
	t.Helper()
	out, diagnostics := &bytes.Buffer{}, &bytes.Buffer{}
	deps.Streams.Out, deps.Streams.ErrOut = out, diagnostics
	run := outputExecutor(t, &deps, out, diagnostics)
	execute := func(args ...string) []byte {
		t.Helper()
		return run(slices.Concat([]string{group, "attachment"}, args, []string{"--repo", repository, "--no-input", "--json"})...)
	}
	path := filepath.Join(t.TempDir(), "attachment.txt")
	require.NoError(t, os.WriteFile(path, []byte("Disposable attachment content\n"), 0o600))
	var item services.IssueAttachment
	require.NoError(t, json.Unmarshal(execute("upload", number, path), &item))
	require.Positive(t, item.ID)
	require.Equal(t, "attachment.txt", item.Name)
	require.EqualValues(t, 30, item.Size)
	id := fmt.Sprint(item.ID)
	downloaded := filepath.Join(t.TempDir(), "downloaded.txt")
	require.NoError(t, Execute(context.Background(), deps, []string{group, "attachment", "download", number, id, "--repo", repository, "--no-input", "--output", downloaded}))
	content, err := os.ReadFile(downloaded)
	require.NoError(t, err)
	require.Equal(t, "Disposable attachment content\n", string(content))
	info, err := os.Stat(downloaded)
	require.NoError(t, err)
	require.Equal(t, umaskMode(t), info.Mode().Perm())
	deleted := false
	defer func() {
		if !deleted {
			if err := Execute(context.Background(), deps, []string{group, "attachment", "delete", number, id, "--repo", repository, "--yes", "--no-input"}); err != nil {
				t.Errorf("attachment cleanup: %v", err)
			}
		}
	}()
	require.NoError(t, json.Unmarshal(execute("edit", number, id, "--name", "renamed.txt"), &item))
	require.Equal(t, "renamed.txt", item.Name)
	require.NoError(t, json.Unmarshal(execute("view", number, id), &item))
	require.Equal(t, "renamed.txt", item.Name)
	var items []services.IssueAttachment
	require.NoError(t, json.Unmarshal(execute("list", number), &items))
	require.Len(t, items, 1)
	require.Equal(t, item.ID, items[0].ID)
	require.NoError(t, Execute(context.Background(), deps, []string{group, "attachment", "delete", number, id, "--repo", repository, "--yes", "--no-input"}))
	deleted = true
	require.JSONEq(t, `[]`, string(execute("list", number)))
}

func verifySSHCloneAndCheckout(t *testing.T, deps Dependencies, repo services.Repository, pull services.Pull, user string, environment []string, endpoint string) {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("SSH fixture uses a POSIX wrapper")
	}
	directory := t.TempDir()
	public, private, err := ed25519.GenerateKey(rand.Reader)
	require.NoError(t, err)
	block, err := ssh.MarshalPrivateKey(private, "disposable gitea-cli SSH fixture")
	require.NoError(t, err)
	keyFile := filepath.Join(directory, "key with spaces")
	require.NoError(t, os.WriteFile(keyFile, pem.EncodeToMemory(block), 0o600))
	publicKey, err := ssh.NewPublicKey(public)
	require.NoError(t, err)
	out := &bytes.Buffer{}
	deps.Streams.Out = out
	deps.Streams.In = bytes.NewReader(ssh.MarshalAuthorizedKey(publicKey))
	require.NoError(t, Execute(context.Background(), deps, []string{"ssh-key", "create", "-", "--title", "Disposable SSH transport", "--json"}))
	var registered services.SSHKey
	require.NoError(t, json.Unmarshal(out.Bytes(), &registered))
	t.Cleanup(func() {
		if err := Execute(context.Background(), deps, []string{"ssh-key", "delete", fmt.Sprint(registered.ID), "--yes"}); err != nil {
			t.Errorf("SSH key cleanup: %v", err)
		}
	})
	host, port, err := net.SplitHostPort(endpoint)
	require.NoError(t, err)
	scan := exec.CommandContext(context.Background(), "ssh-keyscan", "-4", "-T", "5", "-p", port, host)
	known, err := scan.Output()
	require.NoError(t, err)
	require.NotEmpty(t, known)
	knownFile := filepath.Join(directory, "known_hosts")
	require.NoError(t, os.WriteFile(knownFile, known, 0o600))
	realSSH, err := exec.LookPath("ssh")
	require.NoError(t, err)
	wrapper := "#!/bin/sh\nexec " + shellquote.Join(realSSH, "-4", "-oUserKnownHostsFile="+knownFile, "-oStrictHostKeyChecking=yes") + " \"$@\"\n"
	require.NoError(t, os.WriteFile(filepath.Join(directory, "ssh"), []byte(wrapper), 0o700))
	env := []string{}
	for _, value := range environment {
		key, _, _ := strings.Cut(value, "=")
		if key != "PATH" && key != "GIT_SSH_COMMAND" && key != "GIT_SSH" {
			env = append(env, value)
		}
	}
	env = append(env, "PATH="+directory+string(os.PathListSeparator)+os.Getenv("PATH"), "GIT_SSH_VARIANT=ssh")
	deps.Git = git.System{Env: env, ErrOut: deps.Streams.ErrOut}
	instance := deps.Getenv("GITEA_CLI_HOST")
	require.NoError(t, deps.Settings.Update(settings.TargetHosts, func(s *settings.Snapshot) error {
		h := s.Hosts.Hosts[instance]
		h.SSHHost, h.GitProtocol = endpoint, "ssh"
		account := h.Users[user]
		account.SSHKey, account.SSHAgent = keyFile, false
		h.Users[user] = account
		s.Hosts.Hosts[instance] = h
		return nil
	}))
	clone := filepath.Join(directory, "private-clone")
	out.Reset()
	require.NoError(t, Execute(context.Background(), deps, []string{"repo", "clone", repo.FullName, clone, "--no-input", "--json=id"}))
	require.JSONEq(t, fmt.Sprintf(`{"id":%d}`, repo.ID), out.String())
	out.Reset()
	require.NoError(t, Execute(context.Background(), deps, []string{"pr", "checkout", fmt.Sprint(pull.Number), "--repo", repo.FullName, "--cwd", clone, "--detach", "--no-input", "--json"}))
	var checkout git.CheckoutResult
	require.NoError(t, json.Unmarshal(out.Bytes(), &checkout))
	require.Equal(t, pull.HeadSHA, checkout.Commit)
	require.True(t, checkout.Detached)
	_, err = deps.Git.Output(context.Background(), clone, false, "config", "--local", "--get", "core.sshCommand")
	var missing *git.CommandError
	require.ErrorAs(t, err, &missing)
	require.Equal(t, 1, missing.ExitCode)
}
