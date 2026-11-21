// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package integration

import (
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"testing"

	actions_model "gitea.dev/models/actions"
	"gitea.dev/models/db"
	"gitea.dev/modules/timeutil"
	actions_service "gitea.dev/services/actions"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestActionsPersistentCache(t *testing.T) {
	defer prepareTestEnvActionsArtifacts(t)()
	task := getArtifactFixtureTask(t)
	token := artifactFixtureRuntimeToken(t)
	const base = "/api/actions_cache/_apis/artifactcache"
	reserve := NewRequestWithJSON(t, "POST", base+"/caches", map[string]any{"key": "uv-%_v1", "version": "v1", "cacheSize": 6}).AddTokenAuth(token)
	response := MakeRequest(t, reserve, http.StatusCreated)
	var reservation struct {
		CacheID int64 `json:"cacheId"`
	}
	DecodeJSON(t, response, &reservation)
	path := fmt.Sprintf("%s/caches/%d", base, reservation.CacheID)
	upload := func(start int, body string, status int) {
		MakeRequest(t, NewRequestWithBody(t, "PATCH", path, strings.NewReader(body)).AddTokenAuth(token).SetHeader("Content-Range", fmt.Sprintf("bytes %d-%d/*", start, start+len(body)-1)), status)
	}
	upload(3, "def", http.StatusNoContent)
	MakeRequest(t, NewRequestWithJSON(t, "POST", path, map[string]int{"size": 6}).AddTokenAuth(token), http.StatusBadRequest)
	upload(2, "xx", http.StatusConflict)
	upload(0, "abc", http.StatusNoContent)
	MakeRequest(t, NewRequestWithJSON(t, "POST", path, map[string]int{"size": 6}).AddTokenAuth(token), http.StatusNoContent)
	// A distinct task obtains its own runtime token, without transferring cache credentials.
	other := *task
	other.ID = 0
	other.Token = "cache-test-new-task"
	_, err := db.GetEngine(t.Context()).Insert(&other)
	require.NoError(t, err)
	otherToken, err := actions_service.CreateAuthorizationToken(other.ID, other.Job.RunID, other.JobID)
	require.NoError(t, err)
	result := MakeRequest(t, NewRequest(t, "GET", base+"/cache?keys=uv-%25_&version=v1").AddTokenAuth(otherToken), http.StatusOK)
	var hit struct {
		CacheKey        string `json:"cacheKey"`
		ArchiveLocation string `json:"archiveLocation"`
	}
	DecodeJSON(t, result, &hit)
	assert.Equal(t, "uv-%_v1", hit.CacheKey)
	location, err := url.Parse(hit.ArchiveLocation)
	require.NoError(t, err)
	downloaded := MakeRequest(t, NewRequest(t, "GET", location.RequestURI()), http.StatusOK)
	assert.Equal(t, "abcdef", downloaded.Body.String())
	partial := MakeRequest(t, NewRequest(t, "GET", location.RequestURI()).SetHeader("Range", "bytes=2-4"), http.StatusPartialContent)
	assert.Equal(t, "cde", partial.Body.String())
	MakeRequest(t, NewRequest(t, "GET", location.RequestURI()+"bad"), http.StatusUnauthorized)
	MakeRequest(t, NewRequestWithBody(t, "PATCH", path, strings.NewReader("abc")).AddTokenAuth(otherToken).SetHeader("Content-Range", "bytes 0-2/*"), http.StatusNotFound)
	_, err = db.GetEngine(t.Context()).ID(reservation.CacheID).Cols("expires_unix").Update(&actions_model.ActionCache{ExpiresUnix: timeutil.TimeStampNow() - 1})
	require.NoError(t, err)
	require.NoError(t, actions_service.CleanupCaches(t.Context()))
	MakeRequest(t, NewRequest(t, "GET", location.RequestURI()), http.StatusNotFound)
}
