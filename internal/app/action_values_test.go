// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package app

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"gitea.dev/cli/internal/clierr"
	"github.com/stretchr/testify/require"
)

func TestSecretValueFileDropsTrailingLineEnding(t *testing.T) {
	var stored string
	server := newAuthenticatedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v1/repos/alice/project/actions/secrets/BUILD_TOKEN":
			var body struct{ Data string }
			require.NoError(t, json.NewDecoder(r.Body).Decode(&body))
			stored = body.Data
			w.WriteHeader(http.StatusNoContent)
		default:
			t.Errorf("unexpected %s %s", r.Method, r.URL.Path)
		}
	}))
	defer server.Close()
	deps, _, _ := serverDeps(t, server.URL)
	deps.Streams.In = strings.NewReader("sensitive\r\n")
	require.NoError(t, Execute(context.Background(), deps, []string{"secret", "set", "BUILD_TOKEN", "-R", "alice/project", "--value-file", "-", "--yes"}))
	require.Equal(t, "sensitive", stored)
}

func TestActionValueSetReadsValuesAndReportsStoredNames(t *testing.T) {
	envFile := filepath.Join(t.TempDir(), ".env")
	require.NoError(t, os.WriteFile(envFile, []byte("# deploy\nAPI_KEY=abc\nexport region=\"eu\\nwest\"\nMODE=fast # inline\n"), 0o600))
	for _, tc := range []struct {
		name     string
		args     []string
		stdin    string
		requests []string
		out, err string
	}{
		{
			"lower-case name from --body",
			[]string{"secret", "set", "lower_name", "-b", "x"},
			"",
			[]string{`PUT actions/secrets/lower_name {"data":"x","description":""}`},
			"",
			"✓ Set Actions secret LOWER_NAME for alice/project (names are upper-cased)\n",
		},
		{
			"standard input",
			[]string{"secret", "set", "TOKEN"},
			"piped\n",
			[]string{`PUT actions/secrets/TOKEN {"data":"piped","description":""}`},
			"", "✓ Set Actions secret TOKEN for alice/project\n",
		},
		{
			"JSON names the stored secret",
			[]string{"secret", "set", "token", "--body", "x", "--json"},
			"",
			[]string{`PUT actions/secrets/token {"data":"x","description":""}`},
			`{"name":"TOKEN","description":"","created_at":""}`, "",
		},
		{
			"organization",
			[]string{"secret", "set", "TOKEN", "-o", "acme", "-b", "x"},
			"",
			[]string{`PUT api/v1/orgs/acme/actions/secrets/TOKEN {"data":"x","description":""}`},
			"", "✓ Set Actions secret TOKEN for organization acme\n",
		},
		{
			"account",
			[]string{"secret", "set", "TOKEN", "--account", "-b", "x"},
			"",
			[]string{`PUT api/v1/user/actions/secrets/TOKEN {"data":"x","description":""}`},
			"", "✓ Set Actions secret TOKEN for your account\n",
		},
		{
			"dotenv file",
			[]string{"variable", "set", "--env-file", envFile, "-o", "acme"},
			"",
			[]string{
				"GET api/v1/orgs/acme/actions/variables/API_KEY", `POST api/v1/orgs/acme/actions/variables/API_KEY {"value":"abc","description":""}`,
				"GET api/v1/orgs/acme/actions/variables/region", `POST api/v1/orgs/acme/actions/variables/region {"value":"eu\nwest","description":""}`,
				"GET api/v1/orgs/acme/actions/variables/MODE", `POST api/v1/orgs/acme/actions/variables/MODE {"value":"fast","description":""}`,
			},
			"",
			"✓ Set Actions variable API_KEY for organization acme\n✓ Set Actions variable REGION for organization acme (names are upper-cased)\n✓ Set Actions variable MODE for organization acme\n",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			server, requests := actionsAPI(t, map[string]string{
				"PUT actions/secrets/lower_name": "204 ", "PUT actions/secrets/TOKEN": "204 ", "PUT actions/secrets/token": "204 ",
				"PUT api/v1/orgs/acme/actions/secrets/TOKEN": "204 ", "PUT api/v1/user/actions/secrets/TOKEN": "204 ",
				"GET api/v1/orgs/acme/actions/variables/API_KEY": `404 {"message":"variable not found"}`, "POST api/v1/orgs/acme/actions/variables/API_KEY": "201 ",
				"GET api/v1/orgs/acme/actions/variables/region": `404 {"message":"variable not found"}`, "POST api/v1/orgs/acme/actions/variables/region": "201 ",
				"GET api/v1/orgs/acme/actions/variables/MODE": `404 {"message":"variable not found"}`, "POST api/v1/orgs/acme/actions/variables/MODE": "201 ",
			})
			deps, out, diagnostics := actionsDeps(t, server.URL, true)
			deps.Streams.In = strings.NewReader(tc.stdin)
			args := tc.args
			if !strings.Contains(strings.Join(args, " "), "-o acme") && !strings.Contains(strings.Join(args, " "), "--account") {
				args = append(args, "-R", "alice/project")
			}
			require.NoError(t, Execute(context.Background(), deps, args))
			require.Equal(t, tc.requests, *requests)
			if tc.out != "" {
				require.JSONEq(t, tc.out, out.String())
			} else {
				require.Empty(t, out.String())
			}
			require.Equal(t, tc.err, diagnostics.String())
		})
	}
}

func TestActionValueListsShowScopeAndExplainTheAccountFlags(t *testing.T) {
	server, _ := actionsAPI(t, map[string]string{
		"GET actions/secrets":   `[{"name":"DEPLOY_TOKEN","description":"Deploy token","created_at":"2028-01-01T10:00:00Z"}]`,
		"GET actions/variables": `[{"name":"ENVIRONMENT","data":"staging","description":"Target environment"}]`,
	})
	deps, out, diagnostics := actionsDeps(t, server.URL, true)
	deps.Streams.ErrTTY = true
	require.NoError(t, Execute(context.Background(), deps, []string{"secret", "list", "-R", "alice/project", "--user", "alice"}))
	require.Equal(t, "Showing 1 of 1 secrets in alice/project\n\nNAME          CREATED            DESCRIPTION\nDEPLOY_TOKEN  about 2 hours ago  Deploy token\n", out.String())
	require.Equal(t, "note: --user selects the account gt acts as (alice); Gitea cannot list your account's secrets; set or delete them with --account\n", diagnostics.String())
	out.Reset()
	require.NoError(t, Execute(context.Background(), deps, []string{"variable", "list", "-R", "alice/project"}))
	require.Equal(t, "Showing 1 of 1 variables in alice/project\n\nNAME         VALUE    DESCRIPTION\nENVIRONMENT  staging  Target environment\n", out.String())
	err := Execute(context.Background(), deps, []string{"secret", "list", "--account"})
	require.Equal(t, clierr.Usage, clierr.ExitCode(err))
	require.ErrorContains(t, err, "unknown flag: --account")
}

func TestVariableGetAndView(t *testing.T) {
	server, _ := actionsAPI(t, map[string]string{"GET actions/variables/region": `{"name":"REGION","data":"eu\nwest","description":"Where to deploy"}`})
	for _, tc := range []struct {
		args []string
		tty  bool
		want string
	}{
		{[]string{"variable", "get", "region"}, false, "eu\nwest\n"},
		{[]string{"variable", "view", "region"}, true, "REGION\nScope:       alice/project\nDescription: Where to deploy\n\neu\nwest\n"},
		{[]string{"variable", "view", "region"}, false, "title:\tREGION\nscope:\talice/project\ndescription:\tWhere to deploy\n--\neu\nwest\n"},
	} {
		deps, out, _ := actionsDeps(t, server.URL, tc.tty)
		require.NoError(t, Execute(context.Background(), deps, append(tc.args, "-R", "alice/project")))
		require.Equal(t, tc.want, out.String(), "%v tty=%t", tc.args, tc.tty)
	}
}

func TestActionValueDeleteConfirmsWithTheScope(t *testing.T) {
	server, requests := actionsAPI(t, map[string]string{"DELETE actions/secrets/deploy_token": "204 "})
	for _, accept := range []bool{false, true} {
		*requests = nil
		deps, _, diagnostics := actionsDeps(t, server.URL, true)
		prompter := &scriptedPrompter{accept: accept}
		deps.Prompter = prompter
		deps.Streams.InTTY, deps.Streams.ErrTTY = true, true
		err := Execute(context.Background(), deps, []string{"secret", "delete", "deploy_token", "-R", "alice/project"})
		require.Equal(t, []string{"Delete secret DEPLOY_TOKEN from alice/project?"}, prompter.confirmations)
		if !accept {
			require.Equal(t, clierr.Canceled, clierr.ExitCode(err))
			require.Empty(t, *requests)
			continue
		}
		require.NoError(t, err)
		require.Equal(t, []string{"DELETE actions/secrets/deploy_token"}, *requests)
		require.Equal(t, "✓ Deleted Actions secret DEPLOY_TOKEN from alice/project\n", diagnostics.String())
	}
}

func TestParseEnvFileStripsQuotesBeforeAComment(t *testing.T) {
	entries, err := parseEnvFile([]byte("A=\"s3cr3t\" # prod\nB='v' # c\nC=\"a # b\"\nD=plain # note\nE=\"x\\ny\"\nF=\"half\n"))
	require.NoError(t, err)
	require.Equal(t, []envEntry{{"A", "s3cr3t"}, {"B", "v"}, {"C", "a # b"}, {"D", "plain"}, {"E", "x\ny"}, {"F", "\"half"}}, entries)
}

func TestSecretSetChecksTheNameBeforeReadingTheValue(t *testing.T) {
	deps, _, _ := actionsDeps(t, "http://127.0.0.1:1", true)
	read := &stdinReads{}
	deps.Streams.In = read
	err := Execute(context.Background(), deps, []string{"secret", "set", "GITHUB_PAT", "-R", "alice/project"})
	require.ErrorContains(t, err, "reserved")
	require.Zero(t, read.reads, "the value must not be read for a refused name")
}

type stdinReads struct{ reads int }

func (r *stdinReads) Read([]byte) (int, error) { r.reads++; return 0, io.EOF }

func TestVariableSetCountsCharactersNotBytes(t *testing.T) {
	server, requests := actionsAPI(t, map[string]string{
		"GET api/v1/orgs/acme/actions/variables/WIDE": `404 {"message":"variable not found"}`, "POST api/v1/orgs/acme/actions/variables/WIDE": "201 ",
	})
	deps, _, _ := actionsDeps(t, server.URL, true)
	deps.Streams.In = strings.NewReader(strings.Repeat("é", 40000))
	require.NoError(t, Execute(context.Background(), deps, []string{"variable", "set", "WIDE", "-o", "acme"}))
	require.Len(t, *requests, 2)
	deps, _, _ = actionsDeps(t, server.URL, true)
	deps.Streams.In = strings.NewReader(strings.Repeat("é", 65537))
	require.ErrorContains(t, Execute(context.Background(), deps, []string{"variable", "set", "WIDE", "-o", "acme"}), "65536 characters")
}

func TestParseEnvFileKeepsEscapedQuotes(t *testing.T) {
	entries, err := parseEnvFile([]byte("A=\"say \\\"hi\\\" now\" # note\nB='it''s'\nC=\"back\\\\slash\\nline\"\n"))
	require.NoError(t, err)
	require.Equal(t, "say \"hi\" now", entries[0].value)
	require.Equal(t, "back\\slash\nline", entries[2].value)
}
