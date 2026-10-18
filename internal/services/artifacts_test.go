// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package services

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"testing"

	gitea "gitea.dev/sdk"
	"github.com/stretchr/testify/require"
)

func TestArtifactListingProjectsMetadataAndEmptyArrays(t *testing.T) {
	client := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, http.MethodGet, r.Method)
		require.Equal(t, "/api/v1/repos/owner/repo/actions/runs/7/artifacts", r.URL.Path)
		require.Equal(t, "20", r.URL.Query().Get("limit"))
		if r.URL.Query().Get("page") == "2" {
			_, _ = w.Write([]byte(`{"artifacts":null,"total_count":0}`))
			return
		}
		_, _ = w.Write([]byte(`{"artifacts":[{"id":9,"name":"build","size_in_bytes":128,"url":"https://example.test/artifact","expired":false,"expires_at":null,"workflow_run":{"head_sha":"excluded"},"archive_download_url":"excluded"}]}`))
	}))
	service := Actions{Client: client, Repository: "owner/repo"}
	page, err := service.Artifacts(context.Background(), 7, 20, 1)
	require.NoError(t, err)
	require.Len(t, page.Items, 1)
	data, err := json.Marshal(page.Items)
	require.NoError(t, err)
	require.JSONEq(t, `[{"id":9,"name":"build","size_in_bytes":128,"url":"https://example.test/artifact","expired":false,"created_at":"","updated_at":"","expires_at":null}]`, string(data))
	page, err = service.Artifacts(context.Background(), 7, 20, 2)
	require.NoError(t, err)
	require.Empty(t, page.Items)
	require.NotNil(t, page.Items)
}

func TestArtifactFromMapsServerFields(t *testing.T) {
	artifact := decode[gitea.ActionsArtifact](t, `{"id":9,"name":"build","size_in_bytes":128,"url":"https://example.test/artifact","expired":true,
		"created_at":"2028-01-02T03:04:05Z","updated_at":"2028-01-02T03:04:05Z","expires_at":"2028-01-03T03:04:05Z"}`)
	expires := "2028-01-03T03:04:05Z"
	require.Equal(t, Artifact{
		ID: 9, Name: "build", Size: 128, URL: "https://example.test/artifact", Expired: true,
		CreatedAt: "2028-01-02T03:04:05Z", UpdatedAt: "2028-01-02T03:04:05Z", ExpiresAt: &expires,
	}, artifactFrom(artifact))
}

func TestAllArtifactsReadsPastTheServersPageCap(t *testing.T) {
	const total, serverCap = 130, 50
	client := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
		page, _ := strconv.Atoi(r.URL.Query().Get("page"))
		size := min(limit, serverCap)
		items := []string{}
		for id := (page-1)*size + 1; id <= min(page*size, total); id++ {
			items = append(items, fmt.Sprintf(`{"id":%d,"name":"a%d"}`, id, id))
		}
		_, _ = fmt.Fprintf(w, `{"total_count":%d,"artifacts":[%s]}`, total, strings.Join(items, ","))
	}))
	artifacts, err := Actions{Client: client, Repository: "owner/repo"}.AllArtifacts(context.Background(), 7)
	require.NoError(t, err)
	require.Len(t, artifacts, total)
	require.EqualValues(t, total, artifacts[total-1].ID)
}
