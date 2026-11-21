// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package app

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"testing"
	"time"

	"gitea.dev/cli/internal/services"

	"github.com/stretchr/testify/require"
)

// integrationRepo is a disposable repository with its first commit, and what to run against it.
type integrationRepo struct {
	services.Repository
	deps        *Dependencies
	execute     func(args ...string) []byte
	out         *bytes.Buffer
	diagnostics *bytes.Buffer
}

// newIntegrationRepo creates a private repository on the disposable instance and deletes it when
// the test ends. execute runs gt for the account of the test and requires success without
// diagnostics; scoped adds the repository and --json.
func newIntegrationRepo(t *testing.T, prefix string) integrationRepo {
	t.Helper()
	_, deps, out, diagnostics := giteaDeps(t)
	execute := outputExecutor(t, &deps, out, diagnostics)
	var repo services.Repository
	require.NoError(t, json.Unmarshal(execute("repo", "create", fmt.Sprintf("%s-%d", prefix, time.Now().UnixNano()), "--auto-init", "--private", "--json"), &repo))
	t.Cleanup(func() {
		if err := Execute(context.Background(), deps, []string{"repo", "delete", "-R", repo.FullName, "--yes"}); err != nil {
			t.Errorf("repository cleanup: %v", err)
		}
	})
	return integrationRepo{Repository: repo, deps: &deps, execute: execute, out: out, diagnostics: diagnostics}
}

// scoped runs gt for the repository with JSON output.
func (r integrationRepo) scoped(args ...string) []byte {
	return r.execute(append(args, "-R", r.FullName, "--json")...)
}

// fails runs gt for the repository and returns the error it ends with, which it requires.
func (r integrationRepo) fails(t *testing.T, args ...string) error {
	t.Helper()
	r.out.Reset()
	err := Execute(context.Background(), *r.deps, append(args, "-R", r.FullName, "--no-input"))
	require.Error(t, err, strings.Join(args, " "))
	return err
}

// diagnosed runs gt for the repository, requires success and returns what it wrote to stderr.
func (r integrationRepo) diagnosed(t *testing.T, args ...string) string {
	t.Helper()
	r.out.Reset()
	r.diagnostics.Reset()
	require.NoError(t, Execute(context.Background(), *r.deps, append(args, "-R", r.FullName, "--no-input")))
	return r.diagnostics.String()
}

// issue creates an issue and returns its number.
func (r integrationRepo) issue(t *testing.T, title string) string {
	t.Helper()
	var issue services.Issue
	require.NoError(t, json.Unmarshal(r.scoped("issue", "create", "--title", title, "--body", "Body"), &issue))
	return strconv.FormatInt(issue.Number, 10)
}

// commit writes a file to a branch through the API.
func (r integrationRepo) commit(branch, path, message string) {
	r.deps.Streams.In = strings.NewReader(fmt.Sprintf(`{"branch":%q,"content":"dGVzdAo=","message":%q}`, branch, message))
	r.execute("api", "repos/"+r.FullName+"/contents/"+path, "--input", "-")
}

// pull creates a branch with one commit and a pull request from it, and returns its number.
func (r integrationRepo) pull(t *testing.T, branch, title string) string {
	t.Helper()
	r.execute("branch", "create", branch, "-R", r.FullName)
	r.commit(branch, branch+".txt", title+" change")
	var pull services.Pull
	require.NoError(t, json.Unmarshal(r.scoped("pr", "create", "--title", title, "--body", "Body", "--head", branch, "--base", r.DefaultBranch), &pull))
	return strconv.FormatInt(pull.Number, 10)
}

// api reads a path of the repository through the API, filtered by a jq expression.
func (r integrationRepo) api(path, jq string) string {
	return strings.TrimSpace(string(r.execute("api", "repos/"+r.FullName+"/"+path, "--jq", jq)))
}
