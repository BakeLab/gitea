// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package git

import (
	"bytes"
	"encoding/base64"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"sort"
	"strconv"
	"strings"
)

type cliBackend struct{}

type cliRepository struct {
	workTree string
}

func (cliBackend) Name() string {
	return "cli"
}

func (b cliBackend) Open(path string) (RepositoryBackend, error) {
	if path == "" {
		path = "."
	}

	out, err := runGitCommand(path, nil, nil, "rev-parse", "--show-toplevel")
	if err != nil {
		return nil, classifyRepoError(err)
	}

	return &cliRepository{workTree: strings.TrimSpace(out)}, nil
}

func (b cliBackend) Clone(path, remoteURL string, auth *AuthMethod, opts CloneOptions) (RepositoryBackend, error) {
	extraConfigs := make([]string, 0, 1)
	if opts.Insecure {
		extraConfigs = append(extraConfigs, "http.sslVerify=false")
	}

	args := []string{"clone"}
	if opts.Depth > 0 {
		args = append(args, "--depth", strconv.Itoa(opts.Depth))
	}
	args = append(args, remoteURL, path)

	if _, err := runGitCommand("", auth, extraConfigs, args...); err != nil {
		return nil, err
	}
	return b.Open(path)
}

func (r *cliRepository) WorkTree() string {
	return r.workTree
}

func (r *cliRepository) Config() (*Config, error) {
	cfg := &Config{
		Remotes:  map[string]*RemoteConfig{},
		Branches: map[string]*Branch{},
	}

	remoteOut, err := r.git(nil, nil, "remote")
	if err != nil {
		return nil, err
	}
	for _, remoteName := range strings.Fields(remoteOut) {
		urlOut, err := r.git(nil, nil, "config", "--get-all", "remote."+remoteName+".url")
		if err != nil {
			return nil, err
		}
		cfg.Remotes[remoteName] = &RemoteConfig{Name: remoteName, URLs: splitNonEmptyLines(urlOut)}
	}

	branchOut, err := r.git(nil, nil, "config", "--get-regexp", `^branch\..*\.(remote|merge)$`)
	if err != nil {
		var gitErr *gitCommandError
		if !errors.As(err, &gitErr) {
			return nil, err
		}
		if strings.TrimSpace(gitErr.stderr) != "" && !strings.Contains(gitErr.stderr, "No such section or key") {
			return nil, err
		}
		return cfg, nil
	}

	for _, line := range splitNonEmptyLines(branchOut) {
		parts := strings.SplitN(line, " ", 2)
		if len(parts) != 2 {
			continue
		}
		branchName, field, ok := parseBranchConfigKey(parts[0])
		if !ok {
			continue
		}
		branch := cfg.Branches[branchName]
		if branch == nil {
			branch = &Branch{Name: branchName}
			cfg.Branches[branchName] = branch
		}
		switch field {
		case "remote":
			branch.Remote = parts[1]
		case "merge":
			branch.Merge = ReferenceName(parts[1])
		}
	}

	return cfg, nil
}

func (r *cliRepository) Head() (*Reference, error) {
	hashOut, err := r.git(nil, nil, "rev-parse", "HEAD")
	if err != nil {
		return nil, err
	}
	hash := Hash(strings.TrimSpace(hashOut))

	if refOut, err := r.git(nil, nil, "symbolic-ref", "-q", "HEAD"); err == nil {
		return &Reference{name: ReferenceName(strings.TrimSpace(refOut)), hash: hash}, nil
	}
	if tagOut, err := r.git(nil, nil, "describe", "--exact-match", "--tags", "HEAD"); err == nil {
		return &Reference{name: ReferenceName("refs/tags/" + strings.TrimSpace(tagOut)), hash: hash}, nil
	}
	return &Reference{name: ReferenceName("HEAD"), hash: hash}, nil
}

func (r *cliRepository) AddRemote(name, remoteURL string) error {
	_, err := r.git(nil, nil, "remote", "add", name, remoteURL)
	return err
}

func (r *cliRepository) SetBranchUpstream(branchName, remoteName, remoteBranch string) error {
	mergeRef := NewBranchReferenceName(remoteBranch).String()
	if _, err := r.git(nil, nil, "config", "branch."+branchName+".remote", remoteName); err != nil {
		return err
	}
	_, err := r.git(nil, nil, "config", "branch."+branchName+".merge", mergeRef)
	return err
}

func (r *cliRepository) Fetch(remoteName string, refspecs []string, auth *AuthMethod) error {
	args := []string{"fetch", remoteName}
	args = append(args, refspecs...)
	_, err := r.git(auth, nil, args...)
	return err
}

func (r *cliRepository) CreateTrackingBranch(localBranchName, remoteBranchName, remoteName string) error {
	_, err := r.git(nil, nil, "branch", "--track", localBranchName, fmt.Sprintf("%s/%s", remoteName, remoteBranchName))
	if err == nil {
		return nil
	}
	if gitErr, ok := err.(*gitCommandError); ok && strings.Contains(gitErr.stderr, "already exists") {
		return ErrBranchExists
	}
	return err
}

func (r *cliRepository) Checkout(ref ReferenceName) error {
	args := []string{"checkout"}
	switch {
	case ref.IsBranch():
		// `git checkout refs/heads/<branch>` detaches HEAD, while the short branch
		// name switches to the local branch as intended.
		args = append(args, ref.Short())
	case ref.IsRemote():
		// Be explicit about detached HEAD when checking out a remote-tracking ref.
		args = append(args, "--detach", ref.String())
	default:
		args = append(args, ref.String())
	}

	_, err := r.git(nil, nil, args...)
	return err
}

func (r *cliRepository) DeleteLocalBranch(branchName string) error {
	_, err := r.git(nil, nil, "branch", "-D", branchName)
	if err == nil {
		return nil
	}
	if gitErr, ok := err.(*gitCommandError); ok {
		stderr := strings.ToLower(gitErr.stderr)
		if strings.Contains(stderr, "not found") || strings.Contains(stderr, "not exist") {
			return nil
		}
	}
	return err
}

func (r *cliRepository) DeleteRemoteBranch(remoteName, remoteBranch string, auth *AuthMethod) error {
	_, err := r.git(auth, nil, "push", "--delete", remoteName, remoteBranch)
	return err
}

func (r *cliRepository) ListReferences(prefixes ...string) ([]*Reference, error) {
	args := []string{"for-each-ref", "--format=%(objectname)%09%(refname)"}
	args = append(args, prefixes...)
	out, err := r.git(nil, nil, args...)
	if err != nil {
		return nil, err
	}

	refs := make([]*Reference, 0)
	for _, line := range splitNonEmptyLines(out) {
		parts := strings.SplitN(line, "	", 2)
		if len(parts) != 2 {
			continue
		}
		refs = append(refs, &Reference{name: ReferenceName(parts[1]), hash: Hash(parts[0])})
	}
	return refs, nil
}

func (r *cliRepository) PushToAgitFlowPR(remoteName, head, base, topic string, pushOptions map[string]string, auth *AuthMethod) error {
	ref := fmt.Sprintf("%s:refs/for/%s/%s", head, base, topic)
	args := []string{"push", remoteName, ref}
	if len(pushOptions) > 0 {
		keys := make([]string, 0, len(pushOptions))
		for key := range pushOptions {
			keys = append(keys, key)
		}
		sort.Strings(keys)
		for _, key := range keys {
			args = append(args, "-o", key+"="+pushOptions[key])
		}
	}
	_, err := r.git(auth, nil, args...)
	return err
}

func (r *cliRepository) git(auth *AuthMethod, extraConfigs []string, args ...string) (string, error) {
	return runGitCommand(r.workTree, auth, extraConfigs, args...)
}

type gitCommandError struct {
	args   []string
	stderr string
	err    error
}

func (e *gitCommandError) Error() string {
	stderr := strings.TrimSpace(e.stderr)
	if stderr == "" {
		return fmt.Sprintf("git %s: %v", strings.Join(e.args, " "), e.err)
	}
	return fmt.Sprintf("git %s: %s", strings.Join(e.args, " "), stderr)
}

func (e *gitCommandError) Unwrap() error {
	return e.err
}

func runGitCommand(dir string, auth *AuthMethod, extraConfigs []string, args ...string) (string, error) {
	authConfigs, authEnv, cleanup, err := prepareCLIAuth(auth)
	if err != nil {
		return "", err
	}
	defer cleanup()

	fullArgs := make([]string, 0, (len(extraConfigs)+len(authConfigs))*2+len(args))
	for _, cfg := range extraConfigs {
		fullArgs = append(fullArgs, "-c", cfg)
	}
	for _, cfg := range authConfigs {
		fullArgs = append(fullArgs, "-c", cfg)
	}
	fullArgs = append(fullArgs, args...)

	cmd := exec.Command("git", fullArgs...)
	if dir != "" {
		cmd.Dir = dir
	}
	cmd.Env = append(os.Environ(), authEnv...)

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return "", &gitCommandError{args: fullArgs, stderr: stderr.String(), err: err}
	}
	return stdout.String(), nil
}

func prepareCLIAuth(auth *AuthMethod) ([]string, []string, func(), error) {
	if auth == nil {
		return nil, nil, func() {}, nil
	}

	configs := make([]string, 0, 1)
	env := make([]string, 0, 4)
	cleanup := func() {}

	switch auth.Scheme {
	case "http", "https":
		if auth.Username != "" || auth.Password != "" {
			header := "Authorization: Basic " + base64.StdEncoding.EncodeToString([]byte(auth.Username+":"+auth.Password))
			configs = append(configs, "http.extraHeader="+header)
		}
	case "ssh":
		sshCommand := "ssh"
		if auth.KeyFile != "" {
			sshCommand += " -i " + shellQuote(auth.KeyFile) + " -o IdentitiesOnly=yes"
		}
		env = append(env, "GIT_SSH_COMMAND="+sshCommand)
		if auth.KeyPassphrase != "" {
			askPassPath, err := writeAskPassScript(auth.KeyPassphrase)
			if err != nil {
				return nil, nil, cleanup, err
			}
			cleanup = func() { _ = os.Remove(askPassPath) }
			env = append(env,
				"SSH_ASKPASS="+askPassPath,
				"SSH_ASKPASS_REQUIRE=force",
				"DISPLAY=tea",
			)
		}
	}

	return configs, env, cleanup, nil
}

func writeAskPassScript(passphrase string) (string, error) {
	f, err := os.CreateTemp("", "tea-ssh-askpass-*")
	if err != nil {
		return "", err
	}
	defer f.Close()

	content := "#!/bin/sh\nprintf '%s\\n' " + shellQuote(passphrase) + "\n"
	if _, err := f.WriteString(content); err != nil {
		_ = os.Remove(f.Name())
		return "", err
	}
	if err := f.Chmod(0o700); err != nil {
		_ = os.Remove(f.Name())
		return "", err
	}
	return f.Name(), nil
}

func classifyRepoError(err error) error {
	var gitErr *gitCommandError
	if errors.As(err, &gitErr) {
		msg := strings.ToLower(gitErr.stderr)
		if strings.Contains(msg, "not a git repository") || strings.Contains(msg, "cannot change to") {
			return ErrRepositoryNotExists
		}
	}
	return err
}

func parseBranchConfigKey(key string) (branchName, field string, ok bool) {
	const prefix = "branch."
	if !strings.HasPrefix(key, prefix) {
		return "", "", false
	}
	trimmed := strings.TrimPrefix(key, prefix)
	switch {
	case strings.HasSuffix(trimmed, ".remote"):
		return strings.TrimSuffix(trimmed, ".remote"), "remote", true
	case strings.HasSuffix(trimmed, ".merge"):
		return strings.TrimSuffix(trimmed, ".merge"), "merge", true
	default:
		return "", "", false
	}
}

func splitNonEmptyLines(s string) []string {
	lines := strings.Split(strings.TrimSpace(s), "\n")
	out := make([]string, 0, len(lines))
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line != "" {
			out = append(out, line)
		}
	}
	return out
}
