// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package extensions

import (
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
)

// Secrets are never inherited by an extension; a granted one receives its own.
var Secrets = []string{
	"GITEA_TOKEN", "GITEA_INSTANCE_URL", "GITEA_INSTANCE_INSECURE", "GITEA_INSTANCE_SSH_HOST",
	"GH_TOKEN", "GITHUB_TOKEN", "GH_ENTERPRISE_TOKEN", "GITHUB_ENTERPRISE_TOKEN", "GH_HOST",
}

// Environ returns base without Secrets and with set applied, in a stable order.
func Environ(base []string, set map[string]string) []string {
	same := func(a, b string) bool {
		// Windows treats environment variable names case-insensitively.
		if runtime.GOOS == "windows" {
			return strings.EqualFold(a, b)
		}
		return a == b
	}
	env := make([]string, 0, len(base)+len(set))
	for _, entry := range base {
		key, _, _ := strings.Cut(entry, "=")
		if slices.ContainsFunc(Secrets, func(secret string) bool { return same(key, secret) }) {
			continue
		}
		if _, overridden := set[key]; overridden {
			continue
		}
		env = append(env, entry)
	}
	keys := make([]string, 0, len(set))
	for key := range set {
		keys = append(keys, key)
	}
	slices.Sort(keys)
	for _, key := range keys {
		env = append(env, key+"="+set[key])
	}
	return env
}

// Command is one run of an extension.
type Command struct {
	Path string
	Args []string
	Dir  string
	Env  []string
	// Interpreter runs Path when the system cannot execute it directly, such as sh for a script
	// on Windows.
	Interpreter string
	Stdin       io.Reader
	Stdout      io.Writer
	Stderr      io.Writer
}

// Run runs c to completion and returns its exit code; a process ended by a signal exits with
// 128 plus the signal number, as a shell reports it. Interrupts from the terminal reach the
// extension directly through its process group, so gt waits instead of killing it, and relays
// termination signals sent to gt alone.
func Run(c Command) (int, error) {
	name, args := c.Path, c.Args
	if c.Interpreter != "" {
		name, args = c.Interpreter, slices.Concat([]string{c.Path}, c.Args)
	}
	cmd := exec.Command(name, args...)
	cmd.Dir, cmd.Env = c.Dir, c.Env
	cmd.Stdin, cmd.Stdout, cmd.Stderr = c.Stdin, c.Stdout, c.Stderr
	if err := cmd.Start(); err != nil {
		return 0, err
	}
	stop := relaySignals(cmd.Process)
	err := cmd.Wait()
	stop()
	if exit, ok := errors.AsType[*exec.ExitError](err); ok {
		return exitCode(exit.ProcessState), nil
	}
	return 0, err
}

// Interpreter returns the program that runs the script at path on this system: none where the
// system runs it itself, else sh from PATH or from Git for Windows.
func Interpreter(path string, lookPath func(string) (string, error)) (string, error) {
	if runtime.GOOS != "windows" || strings.EqualFold(filepath.Ext(path), ".exe") {
		return "", nil
	}
	if sh, err := lookPath("sh"); err == nil {
		return sh, nil
	}
	if git, err := lookPath("git"); err == nil {
		// Git for Windows installs cmd\git.exe next to bin\sh.exe.
		sh := strings.TrimSuffix(git, `\cmd\git.exe`) + `\bin\sh.exe`
		if _, err := os.Stat(sh); err == nil {
			return sh, nil
		}
	}
	return "", errors.New("running a script extension on Windows requires sh, such as the one Git for Windows installs")
}
