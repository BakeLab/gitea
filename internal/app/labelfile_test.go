// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package app

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"gitea.dev/cli/internal/services"
	"github.com/stretchr/testify/require"
)

func TestLabelFileRoundTrip(t *testing.T) {
	for _, label := range []services.Label{
		{Name: "bug", Color: "ff0000", Description: "Something is broken"},
		{Name: "kind/feature request", Color: "#00FF00", Description: "Wanted; maybe"},
		{Name: "#hash", Color: "0000ff"},
		{Name: "priority/high", Color: "7057ff", Exclusive: true},
		{Name: "old", Color: "cccccc", Description: "Retired; kept", Exclusive: true, Archived: true},
		{Name: "flag-like", Color: "cccccc", Description: "ends in;archived text"},
	} {
		row, err := formatLabel(label)
		require.NoError(t, err, label.Name)
		parsed, err := parseLabelFile(strings.NewReader(row))
		require.NoError(t, err, row)
		require.Len(t, parsed, 1)
		require.Equal(t, label.Name, *parsed[0].Name)
		require.Equal(t, strings.TrimPrefix(label.Color, "#"), *parsed[0].Color)
		require.Equal(t, label.Description, *parsed[0].Description)
		require.Equal(t, label.Exclusive, parsed[0].Exclusive != nil && *parsed[0].Exclusive, row)
		require.Equal(t, label.Archived, parsed[0].Archived != nil && *parsed[0].Archived, row)
	}
	for label, row := range map[services.Label]string{
		{Name: "good first issue", Color: "7057ff"}:                            "#7057ff good first issue\n",
		{Name: "bug", Color: "ee0701", Description: "Broken"}:                  "#ee0701 bug ;Broken\n",
		{Name: "priority/high", Color: "7057ff", Exclusive: true}:              "#7057ff priority/high ; ;exclusive\n",
		{Name: "old", Color: "cccccc", Description: "Retired", Archived: true}: "#cccccc old ;Retired ;archived\n",
		{Name: "both", Color: "cccccc", Exclusive: true, Archived: true}:       "#cccccc both ; ;exclusive,archived\n",
	} {
		formatted, err := formatLabel(label)
		require.NoError(t, err)
		require.Equal(t, row, formatted)
	}
	for _, label := range []services.Label{
		{Name: "", Color: "ff0000"},
		{Name: "two  spaces", Color: "ff0000"},
		{Name: "semi;colon", Color: "ff0000"},
		{Name: "tab\tname", Color: "ff0000"},
		{Name: "bug", Color: "red"},
		{Name: "bug", Color: "ff0000", Description: "line\nbreak"},
		{Name: "bug", Color: "ff0000", Description: " padded"},
	} {
		_, err := formatLabel(label)
		require.ErrorContains(t, err, "cannot be represented", label)
	}
}

func TestLabelImportAcceptsColorPrefixedRows(t *testing.T) {
	var created []string
	server := newAuthenticatedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, "/api/v1/repos/alice/project/labels", r.URL.Path)
		if r.Method == http.MethodGet {
			_, _ = w.Write([]byte(`[]`))
			return
		}
		payload, err := io.ReadAll(r.Body)
		require.NoError(t, err)
		var label struct{ Name string }
		require.NoError(t, json.Unmarshal(payload, &label))
		created = append(created, string(payload))
		_, _ = fmt.Fprintf(w, `{"id":%d,"name":%q}`, len(created), label.Name)
	}))
	defer server.Close()
	deps, out, _ := serverDeps(t, server.URL)
	file := filepath.Join(t.TempDir(), "labels.txt")
	require.NoError(t, os.WriteFile(file, []byte("# Gitea default labels\n#ee0701 bug ; Something is not working\n84b6eb enhancement\n"), 0o600))
	require.NoError(t, Execute(context.Background(), deps, []string{"label", "import", "-R", "alice/project", file, "--yes"}))
	require.Len(t, created, 2)
	requirePayload(t, `{"name":"bug","color":"ee0701","description":"Something is not working"}`, strings.NewReader(created[0]))
	requirePayload(t, `{"name":"enhancement","color":"84b6eb"}`, strings.NewReader(created[1]))
	require.Empty(t, out.String())
}

func TestLabelImportJSONIsAnArrayForOneRow(t *testing.T) {
	server := newAuthenticatedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet {
			_, _ = w.Write([]byte(`[]`))
			return
		}
		_, _ = w.Write([]byte(`{"id":1,"name":"bug"}`))
	}))
	defer server.Close()
	deps, out, _ := serverDeps(t, server.URL)
	file := filepath.Join(t.TempDir(), "labels.txt")
	require.NoError(t, os.WriteFile(file, []byte("#ee0701 bug\n"), 0o600))
	require.NoError(t, Execute(context.Background(), deps, []string{"label", "import", file, "-R", "alice/project", "--json=id,name"}))
	require.JSONEq(t, `[{"id":1,"name":"bug"}]`, out.String())
}
