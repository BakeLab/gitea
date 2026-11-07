// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

// Package git contains subprocess boundaries, independent of command flags.
package git

import (
	"context"
	"errors"
	"io"
	"os"
	"os/exec"
	"strings"
)

// Runner abstracts context-aware Git subprocess execution.
type Runner interface {
	Output(context.Context, string, bool, ...string) (string, error)
}

// CommandError carries Git's exit status without including command arguments or output.
type CommandError struct{ ExitCode int }

func (*CommandError) Error() string {
	return "Git operation failed; check repository, remote, and credentials"
}

// requireWorkTree fails unless run operates inside a Git working tree. action names the operation in the error.
func requireWorkTree(run func(...string) (string, error), action string) error {
	inside, err := run("rev-parse", "--is-inside-work-tree")
	if err != nil {
		return err
	}
	if inside != "true" {
		return errors.New(action + " requires a Git working tree")
	}
	return nil
}

// System executes installed Git with explicit stream and prompting policy.
type System struct {
	// Env overrides the subprocess environment when supplied.
	Env    []string
	In     io.Reader
	ErrOut io.Writer
	// Reads, when set, spares repeated probes, which are the reads whose ErrOut is io.Discard.
	Reads *ReadCache
}

func setEnv(env []string, key, value string) []string {
	prefix := key + "="
	result := make([]string, 0, len(env)+1)
	for _, entry := range env {
		if !strings.HasPrefix(entry, prefix) {
			result = append(result, entry)
		}
	}
	return append(result, prefix+value)
}

func networkSubcommand(args []string) bool {
	for index := 0; index < len(args); index++ {
		switch {
		case args[index] == "-c" || args[index] == "-C":
			index++
		case strings.HasPrefix(args[index], "-"):
		default:
			switch args[index] {
			case "push", "fetch", "pull", "ls-remote", "clone":
				return true
			}
			return false
		}
	}
	return false
}

// batchSSHEnv makes SSH fail instead of prompting. A per-operation core.sshCommand
// is preserved, and a GIT_SSH program (such as plink) is left untouched.
func batchSSHEnv(ctx context.Context, dir string, env, args []string) []string {
	sshCommand, sshProgram, variant := "", "", ""
	for _, entry := range env {
		if value, ok := strings.CutPrefix(entry, "GIT_SSH_COMMAND="); ok {
			sshCommand = value
		} else if value, ok := strings.CutPrefix(entry, "GIT_SSH="); ok {
			sshProgram = value
		} else if value, ok := strings.CutPrefix(entry, "GIT_SSH_VARIANT="); ok {
			variant = value
		}
	}
	config := func(key string) string {
		configArgs := []string{}
		for index := 0; index+1 < len(args) && args[index] == "-c"; index += 2 {
			configArgs = append(configArgs, args[index:index+2]...)
		}
		configArgs = append(configArgs, "config", "--get", key)
		command := exec.CommandContext(ctx, "git", configArgs...)
		command.Dir = dir
		command.Env = env
		data, _ := command.Output()
		return strings.TrimSpace(string(data))
	}
	if sshCommand == "" {
		sshCommand = config("core.sshCommand")
	}
	if sshCommand == "" {
		if sshProgram != "" {
			return env
		}
		sshCommand = "ssh"
	}
	if variant == "" {
		variant = config("ssh.variant")
	}
	if plink(variant, sshCommand) {
		return setEnv(env, "GIT_SSH_COMMAND", sshCommand+" -batch")
	}
	return setEnv(env, "GIT_SSH_COMMAND", sshCommand+" -oBatchMode=yes")
}

// plink reports whether Git treats sshCommand as PuTTY's plink, which rejects OpenSSH options.
// Like Git, it honors the variant from GIT_SSH_VARIANT or ssh.variant and otherwise looks at the
// program's base name.
func plink(variant, sshCommand string) bool {
	variant = strings.ToLower(variant)
	if variant == "" || variant == "auto" {
		program, _, _ := strings.Cut(sshCommand, " ")
		if quote := sshCommand[0]; quote == '"' || quote == '\'' {
			program, _, _ = strings.Cut(sshCommand[1:], string(quote))
		}
		variant = strings.TrimSuffix(strings.ToLower(program[strings.LastIndexAny(program, `/\`)+1:]), ".exe")
	}
	return variant == "plink" || variant == "putty" || variant == "tortoiseplink"
}

// Output runs Git, suppressing credential prompts when interaction is disabled.
func (s System) Output(ctx context.Context, dir string, prompt bool, args ...string) (string, error) {
	if s.Reads == nil {
		return s.run(ctx, dir, prompt, args...)
	}
	if !cacheableRead(args) {
		s.Reads.clear()
		return s.run(ctx, dir, prompt, args...)
	}
	if prompt || s.ErrOut != io.Discard {
		return s.run(ctx, dir, prompt, args...)
	}
	return s.Reads.read(ctx, dir, args, func(args ...string) (string, error) { return s.run(ctx, dir, false, args...) })
}

func (s System) run(ctx context.Context, dir string, prompt bool, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, "git", args...)
	cmd.Dir = dir
	cmd.Stderr = s.ErrOut
	env := append([]string(nil), s.Env...)
	if s.Env == nil {
		env = os.Environ()
	}
	if prompt {
		cmd.Stdin = s.In
	} else {
		env = setEnv(env, "GIT_TERMINAL_PROMPT", "0")
		env = setEnv(env, "GIT_ASKPASS", "")
		env = setEnv(env, "SSH_ASKPASS", "")
		env = setEnv(env, "SSH_ASKPASS_REQUIRE", "never")
		if networkSubcommand(args) {
			env = batchSSHEnv(ctx, dir, env, args)
		}
	}
	cmd.Env = env
	output, err := cmd.Output()
	if ctx.Err() != nil {
		return "", ctx.Err()
	}
	if err != nil {
		if failure, ok := errors.AsType[*exec.ExitError](err); ok {
			return "", &CommandError{ExitCode: failure.ExitCode()}
		}
		return "", errors.New("Git operation failed; check repository, remote, and credentials")
	}
	return strings.TrimSpace(string(output)), nil
}
