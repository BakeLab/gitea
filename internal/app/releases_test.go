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
	"testing"

	"gitea.dev/cli/internal/terminal"
	"github.com/stretchr/testify/require"
)

func TestReleaseCreateWithTagFlagReturnsSingleJSONObject(t *testing.T) {
	server := newAuthenticatedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, http.MethodPost, r.Method)
		require.Equal(t, "/api/v1/repos/alice/project/releases", r.URL.Path)
		_, _ = w.Write([]byte(`{"id":7,"tag_name":"v1","name":"v1"}`))
	}))
	defer server.Close()
	deps, out, _ := serverDeps(t, server.URL)
	require.NoError(t, Execute(context.Background(), deps, []string{"release", "create", "v1", "-R", "alice/project", "--json=id,tag"}))
	require.JSONEq(t, `{"id":7,"tag":"v1"}`, out.String())
}

const firstRelease = `{"id":1,"tag_name":"v0.1.0","name":"First release","body":"First release.\n\n- login","target_commitish":"main",
	"html_url":"https://gitea.test/alice/project/releases/tag/v0.1.0","author":{"login":"alice"},"published_at":"2026-10-11T18:00:00Z",
	"assets":[{"id":3,"name":"notes.txt","size":13}]}`

func TestReleaseViewShowsTheLatestReleaseWithNotesAndAssets(t *testing.T) {
	server := newAuthenticatedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, "/api/v1/repos/alice/project/releases/latest", r.URL.Path)
		_, _ = w.Write([]byte(firstRelease))
	}))
	defer server.Close()
	deps, out, _ := repoTTYDeps(t, server.URL)
	rawMarkdown(t, deps)
	require.NoError(t, Execute(context.Background(), deps, []string{"release", "view", "-R", "alice/project"}))
	require.Equal(t, `First release v0.1.0
Latest • alice released this about 2 hours ago · target main
Assets: notes.txt (13 B)

First release.

- login

View this release on Gitea: https://gitea.test/alice/project/releases/tag/v0.1.0
`, out.String())
}

func TestPipedReleaseViewShowsDraftAndPrerelease(t *testing.T) {
	server := newAuthenticatedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, "/api/v1/repos/alice/project/releases/tags/v0.3.0", r.URL.Path)
		_, _ = w.Write([]byte(`{"id":4,"tag_name":"v0.3.0","draft":true,"prerelease":true,"body":"Soon."}`))
	}))
	defer server.Close()
	deps, out, _ := serverDeps(t, server.URL)
	require.NoError(t, Execute(context.Background(), deps, []string{"release", "view", "v0.3.0", "-R", "alice/project"}))
	require.Contains(t, out.String(), "tag:\tv0.3.0\ndraft:\ttrue\nprerelease:\ttrue\nassets:\t\n")
}

func TestReleaseListShowsTypes(t *testing.T) {
	server := newAuthenticatedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`[
			{"id":4,"tag_name":"v0.3.0","draft":true,"created_at":"2026-10-11T19:00:00Z"},
			{"id":3,"tag_name":"v0.2.0-rc1","name":"v0.2.0 RC1","prerelease":true,"published_at":"2026-10-11T18:00:00Z"},
			{"id":1,"tag_name":"v0.1.0","name":"First release","published_at":"2026-10-10T18:00:00Z"}]`))
	}))
	defer server.Close()
	deps, out, _ := repoTTYDeps(t, server.URL)
	require.NoError(t, Execute(context.Background(), deps, []string{"release", "list", "-R", "alice/project"}))
	require.Equal(t, `Showing 3 of 3 releases in alice/project

TITLE          TYPE         TAG         PUBLISHED
v0.3.0         Draft        v0.3.0      about 1 hour ago
v0.2.0 RC1     Pre-release  v0.2.0-rc1  about 2 hours ago
First release  Latest       v0.1.0      about 1 day ago
`, out.String())
}

func TestReleaseDeleteSaysWhatHappenedToTheTag(t *testing.T) {
	server := newAuthenticatedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet {
			_, _ = w.Write([]byte(`{"id":5,"tag_name":"v2.0.0"}`))
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}))
	defer server.Close()
	deps, out, diagnostics := repoTTYDeps(t, server.URL)
	require.NoError(t, Execute(context.Background(), deps, []string{"release", "delete", "v2.0.0", "--yes", "-R", "alice/project"}))
	require.Empty(t, out.String())
	require.Equal(t, "✓ Deleted release v2.0.0 from alice/project (tag kept; use --delete-tag)\n", diagnostics.String())
}

func TestReleaseBatchesNameAMissingReleaseOnce(t *testing.T) {
	server := newAuthenticatedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v1/repos/alice/project":
			_, _ = w.Write([]byte(`{"id":1,"full_name":"alice/project"}`))
		case "/api/v1/repos/alice/project/releases/tags/v1":
			_, _ = w.Write([]byte(`{"id":5,"tag_name":"v1"}`))
		default:
			if r.Method == http.MethodGet {
				w.WriteHeader(http.StatusNotFound)
				_, _ = w.Write([]byte(`{"message":"not found"}`))
				return
			}
			w.WriteHeader(http.StatusNoContent)
		}
	}))
	defer server.Close()
	deps, _, _ := repoTTYDeps(t, server.URL)
	for _, args := range [][]string{
		{"release", "delete", "v1", "nosuch", "--yes"},
		{"release", "edit", "nosuch", "--title", "x"},
		{"release", "publish", "nosuch"},
	} {
		err := Execute(context.Background(), deps, append(args, "-R", "alice/project"))
		require.EqualError(t, Explain(context.Background(), deps, err), "release nosuch: not found in alice/project", args)
	}
	// A missing repository is blamed, not the release.
	err := Execute(context.Background(), deps, []string{"release", "edit", "v9", "--title", "x", "-R", "alice/missing"})
	require.EqualError(t, Explain(context.Background(), deps, err), "release v9: repository alice/missing not found")
}

func TestReleaseCreateUploadsPositionalFiles(t *testing.T) {
	var requests []string
	server := newAuthenticatedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests = append(requests, r.Method+" "+r.URL.Path)
		if r.URL.Path == "/api/v1/repos/alice/project/releases" {
			requirePayload(t, `{"tag_name":"v1.1.0","name":"v1.1.0","body":"Notes","draft":true}`, r.Body)
			_, _ = w.Write([]byte(`{"id":9,"tag_name":"v1.1.0","draft":true,"html_url":"https://gitea.test/alice/project/releases/tag/v1.1.0"}`))
			return
		}
		_, _ = w.Write([]byte(`{"id":1,"name":"gt.bin","size":2}`))
	}))
	defer server.Close()
	deps, out, diagnostics := repoTTYDeps(t, server.URL)
	file := filepath.Join(t.TempDir(), "gt.bin")
	require.NoError(t, os.WriteFile(file, []byte("hi"), 0o600))
	require.NoError(t, Execute(context.Background(), deps, []string{"release", "new", "v1.1.0", file, "-n", "Notes", "-d", "-R", "alice/project"}))
	require.Equal(t, []string{"POST /api/v1/repos/alice/project/releases", "POST /api/v1/repos/alice/project/releases/9/assets"}, requests)
	require.Equal(t, "https://gitea.test/alice/project/releases/tag/v1.1.0\n", out.String())
	require.Equal(t, "✓ Created draft release v1.1.0 in alice/project\n✓ Uploaded gt.bin (2 B)\n", diagnostics.String())
}

func TestReleaseCreateWithUnknownTargetNamesTheTargetAndHintsAtPushing(t *testing.T) {
	server := newAuthenticatedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
		_, _ = w.Write([]byte(`{"message":"target not found"}`))
	}))
	defer server.Close()
	deps, _, _ := serverDeps(t, server.URL)
	clone := map[string]string{"remote": "origin", "remote get-url origin": server.URL + "/alice/project.git", "rev-parse --verify --quiet refs/tags/v2": "abc"}
	deps.Git = &fakeGit{results: clone}
	err := Execute(context.Background(), deps, []string{"release", "create", "v1", "--target", "v2"})
	require.EqualError(t, Explain(context.Background(), deps, err), `target "v2" not found in alice/project; push it first: git push origin v2`)
	// A clone of another repository says nothing about the one -R selects.
	err = Execute(context.Background(), deps, []string{"release", "create", "v1", "--target", "v2", "-R", "alice/other"})
	require.EqualError(t, Explain(context.Background(), deps, err), `target "v2" not found in alice/other`)
	deps.Git = &fakeGit{}
	err = Execute(context.Background(), deps, []string{"release", "create", "v1", "--target", "nowhere", "-R", "alice/project"})
	require.EqualError(t, Explain(context.Background(), deps, err), `target "nowhere" not found in alice/project`)
}

func TestReleaseCreateRefusesATagOnlyTheCloneHas(t *testing.T) {
	var requests []string
	server := newAuthenticatedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests = append(requests, r.Method+" "+r.URL.Path)
		w.WriteHeader(http.StatusNotFound)
		_, _ = w.Write([]byte(`{"message":"tag does not exist"}`))
	}))
	defer server.Close()
	deps, _, _ := serverDeps(t, server.URL)
	deps.Git = &fakeGit{results: map[string]string{"remote": "origin", "remote get-url origin": server.URL + "/alice/project.git", "rev-parse --verify --quiet refs/tags/v1": "abc"}}
	err := Execute(context.Background(), deps, []string{"release", "create", "v1", "--verify-tag"})
	require.EqualError(t, err, "tag v1 not found in alice/project; push it first: git push origin v1")
	require.Equal(t, []string{"GET /api/v1/repos/alice/project/tags/v1"}, requests)
	// Without --verify-tag, Gitea would create the tag at the default branch rather than at the local commit.
	requests = nil
	err = Execute(context.Background(), deps, []string{"release", "create", "v1"})
	require.EqualError(t, err, "tag v1 not found in alice/project; push it first: git push origin v1, or pass --target to create it on the server")
	require.Equal(t, []string{"GET /api/v1/repos/alice/project/tags/v1"}, requests)
	// Selecting the clone's own repository with -R keeps the guard.
	deps.Git = &fakeGit{results: map[string]string{`config --get-regexp ^remote\..*\.url$`: "remote.upstream.url " + server.URL + "/alice/project.git", "rev-parse --verify --quiet refs/tags/v1": "abc"}}
	err = Execute(context.Background(), deps, []string{"release", "create", "v1", "-R", "alice/project"})
	require.EqualError(t, err, "tag v1 not found in alice/project; push it first: git push upstream v1, or pass --target to create it on the server")
}

func TestReleaseCreateTagsTheCommitOfTheClone(t *testing.T) {
	const local, server = "1111111111111111111111111111111111111111", "2222222222222222222222222222222222222222"
	var requests []string
	var target any
	api := newAuthenticatedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests = append(requests, r.Method+" "+r.URL.Path)
		switch r.URL.Path {
		case "/api/v1/repos/alice/project":
			_, _ = w.Write([]byte(`{"full_name":"alice/project","default_branch":"main"}`))
		case "/api/v1/repos/alice/project/branches/main":
			_, _ = w.Write([]byte(`{"name":"main","commit":{"id":"` + server + `"}}`))
		case "/api/v1/repos/alice/project/releases":
			var body map[string]any
			require.NoError(t, json.NewDecoder(r.Body).Decode(&body))
			target = body["target_commitish"]
			_, _ = w.Write([]byte(`{"id":9,"tag_name":"v1"}`))
		default:
			w.WriteHeader(http.StatusNotFound)
			_, _ = w.Write([]byte(`{"message":"not found"}`))
		}
	}))
	defer api.Close()
	deps, _, _ := serverDeps(t, api.URL)
	clone := map[string]string{
		"remote": "origin", "remote get-url origin": api.URL + "/alice/project.git",
		"rev-parse --verify --quiet --end-of-options HEAD^{commit}":    local,
		"rev-parse --verify --quiet --end-of-options 1111111^{commit}": local,
		"rev-parse --verify --quiet --end-of-options main^{commit}":    local,
	}
	deps.Git = &fakeGit{results: clone}
	// Without --target, Gitea would tag the head of the default branch, which is not the local HEAD.
	err := Execute(context.Background(), deps, []string{"release", "create", "v1"})
	require.EqualError(t, err, "tag v1 not found in alice/project, and Gitea would create it at main (2222222), not at HEAD (1111111); pass --target HEAD to create it at HEAD, or --target main")
	require.Equal(t, []string{"GET /api/v1/repos/alice/project/tags/v1", "GET /api/v1/repos/alice/project", "GET /api/v1/repos/alice/project/branches/main"}, requests)
	// Revisions Gitea would resolve on the server name the commit of the clone; a server branch stays.
	for ref, want := range map[string]string{"HEAD": local, "1111111": local, "main": "main"} {
		require.NoError(t, Execute(context.Background(), deps, []string{"release", "create", "v1", "--target", ref}), ref)
		require.Equal(t, want, target, ref)
	}
	clone["rev-parse --verify --quiet --end-of-options HEAD^{commit}"] = server
	target = "unset"
	require.NoError(t, Execute(context.Background(), deps, []string{"release", "create", "v1"}))
	require.Empty(t, target)
	// The form starts from the commit of the clone.
	deps.Streams.InTTY, deps.Streams.ErrTTY = true, true
	deps.ReleaseForm = releaseFormFunc(func(_ string, current terminal.ReleaseFormValues) (terminal.ReleaseFormValues, error) {
		require.Equal(t, "HEAD", current.Target)
		return terminal.ReleaseFormValues{Tag: "v1", Target: current.Target}, nil
	})
	require.NoError(t, Execute(context.Background(), deps, []string{"release", "create"}))
	require.Equal(t, server, target)
}

type releaseFormFunc func(heading string, current terminal.ReleaseFormValues) (terminal.ReleaseFormValues, error)

func (f releaseFormFunc) EditRelease(_ context.Context, heading string, current terminal.ReleaseFormValues) (terminal.ReleaseFormValues, error) {
	return f(heading, current)
}

func TestReleaseCreateAsksInAFormWithoutTagAndFlags(t *testing.T) {
	payloads := []string{
		`{"tag_name":"v1.1.0","name":"v1.1.0","body":"Notes","target_commitish":"main","draft":false,"prerelease":true}`,
		`{"tag_name":"v2","name":"v2","draft":false,"prerelease":false}`,
	}
	server := newAuthenticatedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, "POST /api/v1/repos/alice/project/releases", r.Method+" "+r.URL.Path)
		requirePayload(t, payloads[0], r.Body)
		payloads = payloads[1:]
		_, _ = w.Write([]byte(`{"id":9,"tag_name":"v1.1.0"}`))
	}))
	defer server.Close()
	deps, _, _ := serverDeps(t, server.URL)
	deps.Streams.InTTY, deps.Streams.ErrTTY = true, true
	asked := 0
	deps.ReleaseForm = releaseFormFunc(func(string, terminal.ReleaseFormValues) (terminal.ReleaseFormValues, error) {
		asked++
		return terminal.ReleaseFormValues{Tag: "v1.1.0", Notes: "Notes", Target: "main", Prerelease: true}, nil
	})
	require.NoError(t, Execute(context.Background(), deps, []string{"release", "create", "-R", "alice/project"}))
	// A tag skips the form.
	require.NoError(t, Execute(context.Background(), deps, []string{"release", "create", "v2", "-R", "alice/project"}))
	require.Equal(t, 1, asked)
	require.Empty(t, payloads)
}

func TestReleaseEditWithoutFlagsStartsAFormFromTheRelease(t *testing.T) {
	var patches []string
	server := newAuthenticatedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.Method + " " + r.URL.Path {
		case "GET /api/v1/repos/alice/project/releases/tags/v1":
			_, _ = w.Write([]byte(`{"id":5,"tag_name":"v1","name":"One","body":"Notes","target_commitish":"main","draft":true}`))
		case "PATCH /api/v1/repos/alice/project/releases/5":
			body, err := io.ReadAll(r.Body)
			require.NoError(t, err)
			patches = append(patches, string(body))
			_, _ = w.Write([]byte(`{"id":5,"tag_name":"v1"}`))
		default:
			t.Errorf("unexpected %s %s", r.Method, r.URL.Path)
		}
	}))
	defer server.Close()
	deps, _, diagnostics := serverDeps(t, server.URL)
	deps.Streams.InTTY, deps.Streams.ErrTTY = true, true
	var heading string
	var current terminal.ReleaseFormValues
	edit := func(values *terminal.ReleaseFormValues) {}
	deps.ReleaseForm = releaseFormFunc(func(h string, c terminal.ReleaseFormValues) (terminal.ReleaseFormValues, error) {
		heading, current = h, c
		edit(&c)
		return c, nil
	})
	edit = func(values *terminal.ReleaseFormValues) { values.Title, values.Draft = "", false }
	require.NoError(t, Execute(context.Background(), deps, []string{"release", "edit", "v1", "-R", "alice/project"}))
	require.Equal(t, "Editing release v1 in alice/project", heading)
	require.Equal(t, terminal.ReleaseFormValues{Tag: "v1", Title: "One", Notes: "Notes", Target: "main", Draft: true}, current)
	require.Len(t, patches, 1)
	require.JSONEq(t, `{"tag_name":"","target_commitish":"","name":"v1","body":"","draft":false,"prerelease":null}`, patches[0], "only what changed is sent, and an emptied title is the tag")
	// An unchanged form sends nothing.
	edit = func(*terminal.ReleaseFormValues) {}
	diagnostics.Reset()
	require.NoError(t, Execute(context.Background(), deps, []string{"release", "edit", "v1", "-R", "alice/project"}))
	require.Len(t, patches, 1)
	require.Equal(t, "! No changes to release v1 in alice/project\n", withoutANSI(diagnostics.String()))
	// Emptied notes are refused, since Gitea would keep them.
	edit = func(values *terminal.ReleaseFormValues) { values.Notes = "" }
	err := Execute(context.Background(), deps, []string{"release", "edit", "v1", "-R", "alice/project"})
	require.ErrorContains(t, err, "release notes cannot be cleared")
	require.Len(t, patches, 1)
}

func TestReleaseUploadsRefuseFilesWithTheSameName(t *testing.T) {
	dir := t.TempDir()
	for _, sub := range []string{"a", "b"} {
		require.NoError(t, os.MkdirAll(filepath.Join(dir, sub), 0o700))
		require.NoError(t, os.WriteFile(filepath.Join(dir, sub, "gt"), []byte(sub), 0o600))
	}
	first, second := filepath.Join(dir, "a", "gt"), filepath.Join(dir, "b", "gt")
	requireUsageBeforeRequests(t, "would both be uploaded as gt", "release", "upload", "v1", first, second)
	requireUsageBeforeRequests(t, "would both be uploaded as gt", "release", "create", "v1", "--asset", first, "--asset", second)
}
