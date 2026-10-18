// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package app

import (
	"bytes"
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"maps"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"gitea.dev/cli/internal/auth"
	"gitea.dev/cli/internal/clierr"
	"gitea.dev/cli/internal/git"
	"gitea.dev/cli/internal/settings"
	"gitea.dev/cli/internal/terminal"
	"gitea.dev/cli/internal/transport"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// testDeps returns hermetic dependencies: no environment, no system keyring and a Git runner without results.
func testDeps(t *testing.T) (Dependencies, *bytes.Buffer, *bytes.Buffer) {
	t.Helper()
	out, diagnostics := &bytes.Buffer{}, &bytes.Buffer{}
	store := settings.New(t.TempDir())
	return Dependencies{
		Git:         &fakeGit{},
		Executable:  os.Executable,
		Settings:    store,
		Credentials: &auth.Store{Dir: store.Dir, Keyring: testNoKeyring{}},
		Streams:     terminal.Streams{In: &bytes.Buffer{}, Out: out, ErrOut: diagnostics},
		Getenv:      envFunc(nil),
		Client:      transport.New,
	}, out, diagnostics
}

// serverDeps returns testDeps authenticated against serverURL through the environment.
func serverDeps(t *testing.T, serverURL string) (Dependencies, *bytes.Buffer, *bytes.Buffer) {
	t.Helper()
	deps, out, diagnostics := testDeps(t)
	deps.Getenv = serverEnv(serverURL, nil)
	return deps, out, diagnostics
}

func envFunc(vars map[string]string) func(string) string {
	return func(key string) string { return vars[key] }
}

// serverEnv authenticates against serverURL through the environment, plus extra variables.
func serverEnv(serverURL string, extra map[string]string) func(string) string {
	vars := map[string]string{"GITEA_CLI_HOST": serverURL, "GITEA_TOKEN": "token"}
	maps.Copy(vars, extra)
	return envFunc(vars)
}

// requireUsageBeforeRequests runs args against a fully configured target and asserts
// that the command fails with a usage error before any request is sent.
func requireUsageBeforeRequests(t *testing.T, want string, args ...string) {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Errorf("unexpected request %s %s", r.Method, r.URL.Path)
		w.WriteHeader(http.StatusTeapot)
	}))
	defer server.Close()
	deps, out, diagnostics := testDeps(t)
	deps.Getenv = serverEnv(server.URL, map[string]string{"GITEA_CLI_REPO": "alice/project"})
	err := Execute(context.Background(), deps, args)
	require.Equal(t, clierr.Usage, clierr.ExitCode(err), "%v", err)
	require.ErrorContains(t, err, want)
	require.Empty(t, out.String())
	require.Empty(t, diagnostics.String())
}

// requirePayload checks that a JSON request body sends every wanted field with its value and
// leaves every other field null or empty: SDK option structs send unset fields that way.
func requirePayload(t *testing.T, want string, body io.Reader) {
	t.Helper()
	var wanted, sent map[string]any
	require.NoError(t, json.Unmarshal([]byte(want), &wanted))
	require.NoError(t, json.NewDecoder(body).Decode(&sent))
	for key, value := range wanted {
		require.Contains(t, sent, key)
		require.Equal(t, value, sent[key], key)
	}
	for key, value := range sent {
		if _, ok := wanted[key]; !ok {
			require.Empty(t, value, key)
		}
	}
}

// newAuthenticatedServer supplies the common identity endpoint for workflow fixtures.
// Tests of identity failures use an explicit httptest server instead.
func newAuthenticatedServer(handler http.Handler) *httptest.Server {
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet && strings.HasSuffix(r.URL.Path, "/api/v1/user") {
			if r.Header.Get("Authorization") == "" {
				w.WriteHeader(http.StatusUnauthorized)
				return
			}
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"login":"alice"}`))
			return
		}
		handler.ServeHTTP(w, r)
	}))
}

type testNoKeyring struct{}

var errNoTestKeyring = errors.New("system keyring is unavailable in tests")

func (testNoKeyring) Get(string, string) (string, error) { return "", errNoTestKeyring }
func (testNoKeyring) Set(string, string, string) error   { return errNoTestKeyring }
func (testNoKeyring) Delete(string, string) error        { return errNoTestKeyring }

type fakeGit struct {
	results map[string]string
	calls   []string
	dirs    []string
	prompt  bool
}

func (f *fakeGit) Output(_ context.Context, dir string, prompt bool, args ...string) (string, error) {
	key := strings.Join(args, " ")
	f.calls = append(f.calls, key)
	f.dirs = append(f.dirs, dir)
	f.prompt = prompt
	value, ok := f.results[key]
	if !ok {
		return "", errors.New("missing fake Git result")
	}
	return value, nil
}

// scriptedPrompter answers prompts with canned values and records every prompt it receives.
// Input answers from inputs by field, then from input; a prompt without an answer fails.
type scriptedPrompter struct {
	inputs        map[string]string
	input, secret string
	accept        bool
	asked         []string
	secrets       []string
	confirmations []string
	texts         []string
	defaults      []bool
}

func (p *scriptedPrompter) Ask(_ context.Context, field string, validate func(string) error) (string, error) {
	p.asked = append(p.asked, field)
	value, ok := p.inputs[field]
	if !ok {
		if p.input == "" {
			return "", errors.New("unexpected input prompt " + field)
		}
		value = p.input
	}
	// A form re-prompts until the answer is valid; the script has no other answer, so it gives up.
	if validate != nil && validate(value) != nil {
		return "", terminal.ErrCanceled
	}
	return value, nil
}

// AskText records the title and initial text, and answers like Ask, else with "typed body".
func (p *scriptedPrompter) AskText(_ context.Context, title, initial string) (string, error) {
	p.asked = append(p.asked, title)
	p.texts = append(p.texts, title+" "+initial)
	if value, ok := p.inputs[title]; ok {
		return value, nil
	}
	return cmp.Or(p.input, "typed body"), nil
}

func (p *scriptedPrompter) SecretInput(_ context.Context, title string) (string, error) {
	p.secrets = append(p.secrets, title)
	if p.secret == "" {
		return "", errors.New("unexpected secret prompt " + title)
	}
	return p.secret, nil
}

func (p *scriptedPrompter) ConfirmDefault(_ context.Context, description string, defaultYes bool) (bool, error) {
	p.confirmations = append(p.confirmations, description)
	p.defaults = append(p.defaults, defaultYes)
	return p.accept, nil
}

// testManager returns the account manager for the stores in deps.
func testManager(t *testing.T, deps Dependencies) auth.Manager {
	t.Helper()
	manager, err := auth.NewManager(deps.Settings, deps.Credentials)
	require.NoError(t, err)
	return manager
}

// configureHost stores host as the settings of the instance at url.
func configureHost(t *testing.T, deps Dependencies, url string, host settings.Host) {
	t.Helper()
	require.NoError(t, deps.Settings.Update(settings.TargetHosts, func(s *settings.Snapshot) error {
		s.Hosts.Hosts[url] = host
		return nil
	}))
}

// tokenAccounts returns one token account per user, kept in store.
func tokenAccounts(store string, users ...string) map[string]settings.Account {
	accounts := map[string]settings.Account{}
	for _, user := range users {
		accounts[user] = settings.Account{AuthMethod: settings.AuthToken, CredentialStore: store}
	}
	return accounts
}

// configureAccounts configures token accounts at url with active selected,
// and saves the file credential "<user>-token" for each of them.
func configureAccounts(t *testing.T, deps Dependencies, url, active string, users ...string) {
	t.Helper()
	configureHost(t, deps, url, settings.Host{User: active, Users: tokenAccounts(settings.StoreFile, users...)})
	for _, user := range users {
		require.NoError(t, deps.Credentials.Save(url, user, settings.StoreFile, auth.Credential{AccessToken: user + "-token"}))
	}
}

// credentialCall returns the fakeGit key of a Git call authenticated through the gt credential helper.
func credentialCall(t *testing.T, deps Dependencies, host, user string, args ...string) string {
	t.Helper()
	executable, err := deps.Executable()
	require.NoError(t, err)
	credentialArgs, err := git.CredentialArguments(host, executable, user, deps.Settings.Dir)
	require.NoError(t, err)
	return strings.Join(append(credentialArgs, args...), " ")
}

type draftEditor struct {
	draft   terminal.Draft
	err     error
	command string
	initial string
}

func (e *draftEditor) Edit(_ context.Context, command, initial string) (terminal.Draft, error) {
	e.command = command
	e.initial = initial
	return e.draft, e.err
}

func (*draftEditor) EditPath(context.Context, string, string) error {
	return errors.New("unexpected file edit")
}

// newDraftEditor returns an editor whose session saved body to a new draft file, and that file's path.
func newDraftEditor(t *testing.T, body string) (*draftEditor, string) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "draft.md")
	require.NoError(t, os.WriteFile(path, []byte(body), 0o600))
	return &draftEditor{draft: terminal.Draft{Path: path, Body: body}}, path
}

// requireDraft asserts whether the draft file at path was kept.
func requireDraft(t *testing.T, path string, kept bool) {
	t.Helper()
	_, err := os.Stat(path)
	if kept {
		require.NoError(t, err)
	} else {
		require.ErrorIs(t, err, os.ErrNotExist)
	}
}

// selectablesHandler serves the alice/project repository with the given push permission, its issue template,
// assignees, open milestones and labels, as offered by interactive forms, and passes other requests to next.
func selectablesHandler(t *testing.T, push bool, next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		firstPage := func(items string) {
			if r.URL.Query().Get("page") != "1" {
				items = `[]`
			}
			_, _ = w.Write([]byte(items))
		}
		switch r.URL.Path {
		case "/api/v1/repos/alice/project":
			if r.Method != http.MethodGet {
				next(w, r)
				return
			}
			_, _ = fmt.Fprintf(w, `{"full_name":"alice/project","default_branch":"main","permissions":{"push":%t,"pull":true}}`, push)
		case "/api/v1/orgs/alice":
			w.WriteHeader(http.StatusNotFound)
			_, _ = w.Write([]byte(`{"message":"not found"}`))
		case "/api/v1/repos/alice/project/issue_templates":
			_, _ = w.Write([]byte(`[{"name":"Bug","about":"Report","title":"bug: ","content":"## Steps"}]`))
		case "/api/v1/repos/alice/project/assignees":
			_, _ = w.Write([]byte(`[{"login":"alice"},{"login":"bob"}]`))
		case "/api/v1/repos/alice/project/milestones":
			assert.Equal(t, "open", r.URL.Query().Get("state"))
			firstPage(`[{"id":4,"title":"Release","state":"open"}]`)
		case "/api/v1/repos/alice/project/labels":
			firstPage(`[{"id":2,"name":"bug","color":"ff0000"},{"id":3,"name":"docs","color":"00ff00"}]`)
		default:
			next(w, r)
		}
	}
}

var ansiSequence = regexp.MustCompile("\x1b\\[[0-9;]*m")

// withoutANSI drops SGR color sequences, such as those in pretty JSON on a terminal.
func withoutANSI(s string) string {
	return ansiSequence.ReplaceAllString(s, "")
}

// rawMarkdown keeps Markdown bodies unrendered so terminal views can be compared byte for byte.
func rawMarkdown(t *testing.T, deps Dependencies) {
	t.Helper()
	require.NoError(t, deps.Settings.Update(settings.TargetPreferences, func(s *settings.Snapshot) error {
		s.Preferences.Markdown = "raw"
		return nil
	}))
}

// plainOutput keeps terminal output free of rendered Markdown and hyperlinks so it compares as text.
func plainOutput(t *testing.T, deps Dependencies) {
	t.Helper()
	require.NoError(t, deps.Settings.Update(settings.TargetPreferences, func(s *settings.Snapshot) error {
		s.Preferences.Markdown, s.Preferences.Hyperlinks = "raw", "never"
		return nil
	}))
}

// writeFirstPage answers the first page of a listing with page and every later page with [].
func writeFirstPage(w http.ResponseWriter, r *http.Request, page string) {
	if r.URL.Query().Get("page") != "1" {
		page = `[]`
	}
	_, _ = w.Write([]byte(page))
}

// testNow is the clock of tests that show relative times.
var testNow = time.Date(2026, 10, 11, 20, 0, 0, 0, time.UTC)
