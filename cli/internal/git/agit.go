// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package git

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"strings"
	"unicode"
)

// AgitOptions creates a pull request through Gitea's AGit push convention.
type AgitOptions struct {
	Directory, Remote, Head, Base, Topic, Title, Body string
	CredentialArgs                                    []string
	Prompt                                            bool
}

// PushAgitPR pushes head to refs/for/<base>/<topic> with optional title/description options.
func PushAgitPR(ctx context.Context, runner Runner, opts AgitOptions) error {
	if opts.Remote == "" || opts.Head == "" || opts.Base == "" || opts.Topic == "" {
		return errors.New("AGit pull creation requires remote, head, base, and topic")
	}
	if opts.Topic == opts.Base {
		return fmt.Errorf("the AGit topic %s must differ from the base branch", opts.Topic)
	}
	run := func(args ...string) (string, error) {
		return runner.Output(ctx, opts.Directory, opts.Prompt, args...)
	}
	if err := requireWorkTree(run, "AGit pull creation"); err != nil {
		return err
	}
	head := opts.Head
	if !strings.HasPrefix(head, "refs/") {
		head = "refs/heads/" + head
	}
	refspec := fmt.Sprintf("%s:refs/for/%s/%s", head, opts.Base, opts.Topic)
	args := append([]string{}, opts.CredentialArgs...)
	args = append(args, "push")
	if opts.Title != "" {
		args = append(args, "-o", "title="+agitPushOption(opts.Title))
	}
	if opts.Body != "" {
		args = append(args, "-o", "description="+agitPushOption(opts.Body))
	}
	args = append(args, "--", opts.Remote, refspec)
	_, err := run(args...)
	return err
}

func agitPushOption(value string) string {
	if strings.Contains(value, "\n") || !isASCII(value) {
		return "{base64}" + base64.StdEncoding.EncodeToString([]byte(value))
	}
	return value
}

func isASCII(value string) bool {
	for _, r := range value {
		if r > unicode.MaxASCII {
			return false
		}
	}
	return true
}
