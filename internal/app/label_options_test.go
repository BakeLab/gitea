// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package app

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"

	"gitea.dev/cli/internal/terminal"
	"github.com/stretchr/testify/require"
)

func TestLabelCreateAndEditExclusiveArchived(t *testing.T) {
	calls := 0
	server := newAuthenticatedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet {
			if calls > 0 && r.URL.Query().Get("page") == "1" {
				_, _ = w.Write([]byte(`[{"id":3,"name":"scoped","color":"00aa00"}]`))
				return
			}
			_, _ = w.Write([]byte(`[]`))
			return
		}
		calls++
		var payload map[string]any
		require.NoError(t, json.NewDecoder(r.Body).Decode(&payload))
		switch calls {
		case 1:
			require.Equal(t, http.MethodPost, r.Method)
			require.Equal(t, "/api/v1/repos/owner/repo/labels", r.URL.Path)
			require.Equal(t, "scoped", payload["name"])
			require.Equal(t, "00aa00", payload["color"])
			require.Equal(t, true, payload["exclusive"])
			_, _ = w.Write([]byte(`{"id":3,"name":"scoped","color":"00aa00","description":"","exclusive":true,"is_archived":false}`))
		case 2:
			require.Equal(t, http.MethodPatch, r.Method)
			require.Equal(t, "/api/v1/repos/owner/repo/labels/3", r.URL.Path)
			require.Equal(t, true, payload["is_archived"])
			require.Equal(t, false, payload["exclusive"])
			_, _ = w.Write([]byte(`{"id":3,"name":"scoped","color":"00aa00","description":"","exclusive":false,"is_archived":true}`))
		default:
			t.Fatalf("unexpected call %d %s %s", calls, r.Method, r.URL.Path)
		}
	}))
	defer server.Close()
	deps, out, diagnostics := serverDeps(t, server.URL)
	fields := "--json=id,name,color,exclusive,is_archived"
	require.NoError(t, Execute(context.Background(), deps, []string{"label", "create", "-R", "owner/repo", "--name", "scoped", "--color", "00aa00", "--exclusive", fields, "--no-input"}))
	require.JSONEq(t, `{"id":3,"name":"scoped","color":"00aa00","exclusive":true,"is_archived":false}`, out.String())
	out.Reset()
	require.NoError(t, Execute(context.Background(), deps, []string{"label", "edit", "3", "-R", "owner/repo", "--exclusive=false", "--archived", fields}))
	require.JSONEq(t, `{"id":3,"name":"scoped","color":"00aa00","exclusive":false,"is_archived":true}`, out.String())
	require.Equal(t, 2, calls)
	require.Empty(t, diagnostics.String())
}

func TestLabelCreateRefusesDuplicateNames(t *testing.T) {
	var changed []string
	server := newAuthenticatedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.Method + " " + r.URL.Path {
		case "GET /api/v1/repos/alice/project/labels":
			if r.URL.Query().Get("page") == "1" {
				_, _ = w.Write([]byte(`[{"id":11,"name":"bug","color":"ee0701","description":"Broken"}]`))
				return
			}
			_, _ = w.Write([]byte(`[]`))
		case "PATCH /api/v1/repos/alice/project/labels/11":
			requirePayload(t, `{"color":"000000"}`, r.Body)
			changed = append(changed, r.Method+" "+r.URL.Path)
			_, _ = w.Write([]byte(`{"id":11,"name":"bug","color":"000000","description":"Broken"}`))
		default:
			changed = append(changed, r.Method+" "+r.URL.Path)
			_, _ = w.Write([]byte(`{"id":12,"name":"docs"}`))
		}
	}))
	defer server.Close()
	deps, out, _ := serverDeps(t, server.URL)
	for _, name := range []string{"bug", "BUG"} {
		err := Execute(context.Background(), deps, []string{"label", "create", "-R", "alice/project", "--name", name, "--color", "000000"})
		require.EqualError(t, err, `label "bug" already exists in alice/project; pass --force to update it`)
	}
	require.Empty(t, changed)
	require.Empty(t, out.String())
	require.NoError(t, Execute(context.Background(), deps, []string{"label", "create", "-R", "alice/project", "--name", "bug", "--color", "000000", "--force", "--json=id,color"}))
	require.JSONEq(t, `{"id":11,"color":"000000"}`, out.String())
	require.Equal(t, []string{"PATCH /api/v1/repos/alice/project/labels/11"}, changed)
}

func TestLabelsByName(t *testing.T) {
	var changed []string
	labels := `[{"id":11,"name":"bug","color":"ee0701"},{"id":12,"name":"kind/feature","color":"84b6eb","exclusive":true},{"id":13,"name":"2026","color":"cccccc"}]`
	server := newAuthenticatedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.Method + " " + r.URL.Path {
		case "GET /api/v1/repos/alice/project/labels", "GET /api/v1/repos/bob/source/labels":
			if r.URL.Query().Get("page") != "1" {
				_, _ = w.Write([]byte(`[]`))
				return
			}
			if r.URL.Path == "/api/v1/repos/bob/source/labels" {
				_, _ = w.Write([]byte(`[{"id":1,"name":"bug","color":"000000"},{"id":2,"name":"docs","color":"0000ff","description":"Docs"}]`))
				return
			}
			_, _ = w.Write([]byte(labels))
		default:
			changed = append(changed, r.Method+" "+r.URL.Path+" "+requestFields(t, r, "name", "color", "description"))
			if r.Method == http.MethodDelete {
				w.WriteHeader(http.StatusNoContent)
				return
			}
			_, _ = w.Write([]byte(`{"id":20,"name":"docs","color":"0000ff"}`))
		}
	}))
	defer server.Close()
	deps, out, diagnostics := serverDeps(t, server.URL)
	deps.Getenv = serverEnv(server.URL, map[string]string{"NO_COLOR": "1"})
	deps.Streams.OutTTY = true
	run := func(args ...string) error {
		out.Reset()
		diagnostics.Reset()
		return Execute(context.Background(), deps, append(args, "-R", "alice/project", "--no-input"))
	}
	require.NoError(t, run("label", "create", "docs", "-c", "#0000FF", "-d", "Docs"))
	require.Equal(t, "✓ Created label \"docs\" in alice/project\n", diagnostics.String())
	require.NoError(t, run("label", "edit", "kind/feature", "-n", "kind/enhancement"))
	require.Equal(t, "✓ Edited label \"docs\" in alice/project\n", diagnostics.String())
	require.NoError(t, run("label", "edit", "2026", "--by-name", "-d", "Year"))
	require.NoError(t, run("label", "delete", "BUG", "12", "--yes"))
	require.Equal(t, "✓ Deleted label \"bug\" from alice/project\n✓ Deleted label \"kind/feature\" from alice/project\n", diagnostics.String())
	require.NoError(t, run("label", "clone", "bob/source"))
	require.Equal(t, "! Skipped label \"bug\" in alice/project: it exists already\n✓ Created label \"docs\" in alice/project\n", diagnostics.String())
	require.Equal(t, []string{
		"POST /api/v1/repos/alice/project/labels name=docs color=0000FF description=Docs",
		"PATCH /api/v1/repos/alice/project/labels/12 name=kind/enhancement color=<nil> description=<nil>",
		"PATCH /api/v1/repos/alice/project/labels/13 name=<nil> color=<nil> description=Year",
		"DELETE /api/v1/repos/alice/project/labels/11 ",
		"DELETE /api/v1/repos/alice/project/labels/12 ",
		"POST /api/v1/repos/alice/project/labels name=docs color=0000ff description=Docs",
	}, changed)
	for _, test := range []struct {
		args []string
		want string
	}{
		{[]string{"label", "edit", "nope", "-c", "000000"}, `label "nope" not found in alice/project`},
		{[]string{"label", "edit", "bug"}, "nothing to edit"},
		{[]string{"label", "delete", "bug"}, `confirmation required to delete label "bug" from alice/project; rerun with --yes`},
		{[]string{"label", "create", "x", "-c", "red"}, "--color requires six hexadecimal digits"},
	} {
		require.ErrorContains(t, run(test.args...), test.want, test.args)
	}
}

// requestFields lists the JSON request body's named fields as name=value, empty without a body.
func requestFields(t *testing.T, r *http.Request, names ...string) string {
	t.Helper()
	if r.ContentLength == 0 {
		return ""
	}
	var payload map[string]any
	require.NoError(t, json.NewDecoder(r.Body).Decode(&payload))
	fields := make([]string, len(names))
	for i, name := range names {
		fields[i] = fmt.Sprintf("%s=%v", name, payload[name])
	}
	return strings.Join(fields, " ")
}

func TestLabelCreatePicksAColorAndPrompts(t *testing.T) {
	var created []map[string]any
	server := newAuthenticatedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet {
			_, _ = w.Write([]byte(`[]`))
			return
		}
		var payload map[string]any
		require.NoError(t, json.NewDecoder(r.Body).Decode(&payload))
		created = append(created, payload)
		_, _ = w.Write([]byte(`{"id":1,"name":"x"}`))
	}))
	defer server.Close()
	deps, _, _ := serverDeps(t, server.URL)
	require.NoError(t, Execute(context.Background(), deps, []string{"label", "create", "triage", "-R", "alice/project"}))
	require.Contains(t, labelColors, created[0]["color"])
	prompt := &askingPrompter{scriptedPrompter: scriptedPrompter{inputs: map[string]string{"Label name": "prompted", "Color (hex, e.g. d73a4a; empty = random):": "#00AA00", "Description (optional):": "From a prompt"}}}
	deps.Prompter = prompt
	deps.Streams.InTTY, deps.Streams.ErrTTY = true, true
	require.NoError(t, Execute(context.Background(), deps, []string{"label", "create", "-R", "alice/project"}))
	require.Equal(t, "prompted", created[1]["name"])
	require.Equal(t, "00AA00", created[1]["color"])
	require.Equal(t, "From a prompt", created[1]["description"])
	require.Equal(t, []string{"Label name", "Color (hex, e.g. d73a4a; empty = random):", "Description (optional):"}, prompt.asked)
}

// labelFormPrompter answers the label form with edit applied to the current values.
type labelFormPrompter struct {
	scriptedPrompter
	heading string
	edit    func(*terminal.LabelFormValues)
}

func (p *labelFormPrompter) EditLabel(_ context.Context, heading string, current terminal.LabelFormValues) (terminal.LabelFormValues, error) {
	p.heading = heading
	p.edit(&current)
	return current, nil
}

func TestLabelEditFormStartsFromTheLabel(t *testing.T) {
	var edited string
	server := newAuthenticatedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.Method + " " + r.URL.Path {
		case "GET /api/v1/repos/alice/project/labels":
			if r.URL.Query().Get("page") != "1" {
				_, _ = w.Write([]byte(`[]`))
				return
			}
			_, _ = w.Write([]byte(`[{"id":11,"name":"bug","color":"ee0701","description":"Broken"}]`))
		case "PATCH /api/v1/repos/alice/project/labels/11":
			edited = requestFields(t, r, "name", "color", "description", "exclusive")
			_, _ = w.Write([]byte(`{"id":11,"name":"defect"}`))
		default:
			t.Errorf("unexpected %s %s", r.Method, r.URL.Path)
		}
	}))
	defer server.Close()
	deps, _, _ := serverDeps(t, server.URL)
	deps.Streams.InTTY, deps.Streams.ErrTTY = true, true
	prompt := &labelFormPrompter{edit: func(v *terminal.LabelFormValues) { v.Name, v.Exclusive = "defect", true }}
	deps.Prompter = prompt
	require.NoError(t, Execute(context.Background(), deps, []string{"label", "edit", "bug", "-R", "alice/project"}))
	require.Equal(t, `Editing label "bug" in alice/project`, prompt.heading)
	require.Equal(t, "name=defect color=ee0701 description=Broken exclusive=true", edited)
	deps.Streams.InTTY, deps.Streams.ErrTTY = false, false
	require.ErrorIs(t, Execute(context.Background(), deps, []string{"label", "edit", "bug", "-R", "alice/project"}), errNothingToEdit)
}
