// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package services

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"gitea.dev/cli/internal/transport"
	gitea "gitea.dev/sdk"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestReleaseUploadStreamsAndMapsMetadata(t *testing.T) {
	client := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, http.MethodPost, r.Method)
		assert.Equal(t, "/api/v1/repos/alice/project/releases/1/assets", r.URL.Path)
		reader, err := r.MultipartReader()
		if !assert.NoError(t, err) {
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		part, err := reader.NextPart()
		if !assert.NoError(t, err) {
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		assert.Equal(t, "attachment", part.FormName())
		assert.Equal(t, "file.bin", part.FileName())
		content, err := io.ReadAll(part)
		assert.NoError(t, err)
		assert.Equal(t, "asset bytes", string(content))
		_, err = reader.NextPart()
		assert.ErrorIs(t, err, io.EOF)
		_, _ = w.Write([]byte(`{"id":2,"name":"file.bin","size":11,"browser_download_url":"https://example.test/file"}`))
	}))
	service := Releases{Client: client, Repository: "alice/project"}
	asset, err := service.UploadAsset(context.Background(), 1, "file.bin", strings.NewReader("asset bytes"))
	require.NoError(t, err)
	require.Equal(t, int64(2), asset.ID)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err = service.UploadAsset(ctx, 1, "file.bin", strings.NewReader("asset bytes"))
	require.ErrorIs(t, err, context.Canceled)
}

func TestReleaseAssetDownloadFollowsObjectStorageRedirect(t *testing.T) {
	storage := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Empty(t, r.Header.Get("Authorization"))
		_, _ = w.Write([]byte("asset bytes"))
	}))
	defer storage.Close()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/attachments/uuid" {
			http.Redirect(w, r, storage.URL+"/bucket/uuid?X-Amz-Signature=value", http.StatusSeeOther)
			return
		}
		assert.Equal(t, "/api/v1/repos/alice/project/releases/1/assets/2", r.URL.Path)
		_, _ = w.Write([]byte(`{"uuid":"uuid"}`))
	}))
	defer server.Close()
	service := Releases{Client: transport.New(server.URL, "secret", false), Repository: "alice/project"}
	body, err := service.DownloadAsset(context.Background(), 1, 2)
	require.NoError(t, err)
	defer body.Close()
	data, err := io.ReadAll(body)
	require.NoError(t, err)
	require.Equal(t, "asset bytes", string(data))
}

func TestReleaseArchiveFollowsObjectStorageRedirect(t *testing.T) {
	storage := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Empty(t, r.Header.Get("Authorization"))
		_, _ = w.Write([]byte("archive bytes"))
	}))
	defer storage.Close()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "/api/v1/repos/alice/project/archive/release/v1%231.tar.gz", r.URL.EscapedPath())
		assert.Equal(t, "token secret", r.Header.Get("Authorization"))
		http.Redirect(w, r, storage.URL+"/bucket/archive?X-Amz-Signature=value", http.StatusFound)
	}))
	defer server.Close()
	service := Releases{Client: transport.New(server.URL, "secret", false), Repository: "alice/project"}
	body, err := service.Archive(context.Background(), "release/v1#1", "tar.gz")
	require.NoError(t, err)
	defer body.Close()
	data, err := io.ReadAll(body)
	require.NoError(t, err)
	require.Equal(t, "archive bytes", string(data))
}

func TestReleaseArchiveNamesTheStorageHostThatRefusedIt(t *testing.T) {
	storage := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusForbidden)
	}))
	defer storage.Close()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, storage.URL+"/bucket/archive?X-Amz-Signature=expired", http.StatusFound)
	}))
	defer server.Close()
	service := Releases{Client: transport.New(server.URL, "secret", false), Repository: "alice/project"}
	_, err := service.Archive(context.Background(), "main", "zip")
	require.ErrorContains(t, err, "storage host "+strings.TrimPrefix(storage.URL, "http://")+" refused the signed download with HTTP 403")
}

func TestReleaseAssetDownloadRejectsUnsafeIdentifiers(t *testing.T) {
	for _, uuid := range []string{"", "../secret", "a/b", `a\b`, "a.b"} {
		t.Run(uuid, func(t *testing.T) {
			client := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				assert.Equal(t, "/api/v1/repos/alice/project/releases/1/assets/2", r.URL.Path, "download followed an unsafe identifier")
				_ = json.NewEncoder(w).Encode(map[string]string{"uuid": uuid})
			}))
			_, err := (Releases{Client: client, Repository: "alice/project"}).DownloadAsset(context.Background(), 1, 2)
			require.ErrorContains(t, err, "invalid attachment identifier")
		})
	}
}

func TestReleaseDeleteReportsCompletedStep(t *testing.T) {
	for name, test := range map[string]struct {
		deleteTag            bool
		failRelease, failTag bool
		wantCalls            []string
		wantErr              string
	}{
		"release only":    {wantCalls: []string{"/releases/5"}},
		"release and tag": {deleteTag: true, wantCalls: []string{"/releases/5", "/tags/v1%2Fbeta"}},
		"release fails":   {deleteTag: true, failRelease: true, wantCalls: []string{"/releases/5"}, wantErr: "not found"},
		"tag fails":       {deleteTag: true, failTag: true, wantCalls: []string{"/releases/5", "/tags/v1%2Fbeta"}, wantErr: "release deleted, but tag deletion failed"},
	} {
		t.Run(name, func(t *testing.T) {
			var calls []string
			client := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				assert.Equal(t, http.MethodDelete, r.Method)
				path := strings.TrimPrefix(r.URL.EscapedPath(), "/api/v1/repos/alice/project")
				calls = append(calls, path)
				if (test.failRelease && strings.HasPrefix(path, "/releases/")) || (test.failTag && strings.HasPrefix(path, "/tags/")) {
					w.WriteHeader(http.StatusNotFound)
					return
				}
				w.WriteHeader(http.StatusNoContent)
			}))
			err := (Releases{Client: client, Repository: "alice/project"}).Delete(context.Background(), Release{ID: 5, Tag: "v1/beta"}, test.deleteTag)
			if test.wantErr == "" {
				require.NoError(t, err)
			} else {
				require.ErrorContains(t, err, test.wantErr)
			}
			if test.failRelease {
				require.NotContains(t, err.Error(), "release deleted")
			}
			require.Equal(t, test.wantCalls, calls)
		})
	}
}

func TestReleaseFromMapsServerFields(t *testing.T) {
	release := decode[gitea.Release](t, `{"id":3,"tag_name":"v1","target_commitish":"main","name":"One","body":"Notes",
		"url":"https://gitea.test/api/v1/repos/alice/project/releases/3","html_url":"https://gitea.test/alice/project/releases/tag/v1",
		"draft":true,"prerelease":true,"created_at":"2026-10-01T12:00:00+02:00","published_at":"2026-10-02T12:00:00Z","author":null}`)
	published := "2026-10-02T12:00:00Z"
	require.Equal(t, Release{
		ID: 3, Tag: "v1", Target: "main", Title: "One", Body: "Notes", URL: "https://gitea.test/alice/project/releases/tag/v1",
		Draft: true, Prerelease: true, Assets: []ReleaseAsset{}, CreatedAt: "2026-10-01T12:00:00+02:00", PublishedAt: &published,
	}, releaseFrom(release))
}

func TestReleaseListAndViewEscapeTags(t *testing.T) {
	client := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/v1/repos/alice/project/releases" {
			assert.Equal(t, "10", r.URL.Query().Get("limit"))
			assert.Equal(t, "2", r.URL.Query().Get("page"))
			_, _ = w.Write([]byte(`[{"id":1,"tag_name":"v1"}]`))
			return
		}
		assert.Equal(t, "/api/v1/repos/alice/project/releases/tags/v1%2Fbeta%20one", r.URL.EscapedPath())
		_, _ = w.Write([]byte(`{"id":2,"tag_name":"v1/beta one"}`))
	}))
	service := Releases{Client: client, Repository: "alice/project"}
	items, err := service.ListPage(context.Background(), 10, 2)
	require.NoError(t, err)
	require.Equal(t, []Release{{ID: 1, Tag: "v1", Assets: []ReleaseAsset{}}}, items.Items)
	item, err := service.View(context.Background(), "v1/beta one")
	require.NoError(t, err)
	require.Equal(t, "v1/beta one", item.Tag)
}

func TestReleaseCreateAndEditPayloads(t *testing.T) {
	var payloads []map[string]any
	client := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		payload := requestJSON(t, r)
		payloads = append(payloads, payload)
		_, _ = w.Write([]byte(`{"id":7,"tag_name":"v1","name":"v1"}`))
	}))
	service := Releases{Client: client, Repository: "alice/project"}
	tag, title, body, yes := "v1", "Release one", "Notes", true
	_, err := service.Create(context.Background(), ReleaseOptions{Tag: &tag, Title: &title, Body: &body, Prerelease: &yes})
	require.NoError(t, err)
	_, err = service.Edit(context.Background(), Release{ID: 7}, ReleaseOptions{Title: &title, Draft: &yes})
	require.NoError(t, err)
	require.Len(t, payloads, 2)
	require.Equal(t, "v1", payloads[0]["tag_name"])
	require.Equal(t, "Release one", payloads[0]["name"])
	require.Equal(t, "Notes", payloads[0]["body"])
	require.Equal(t, false, payloads[0]["draft"])
	require.Equal(t, true, payloads[0]["prerelease"])
	require.Equal(t, "Release one", payloads[1]["name"])
	require.Equal(t, true, payloads[1]["draft"])
	require.Nil(t, payloads[1]["prerelease"])
	for _, field := range []string{"tag_name", "target_commitish", "body"} {
		require.Empty(t, payloads[1][field], "Gitea ignores empty edit fields")
	}
}

func TestReleaseCreateRequiresTitle(t *testing.T) {
	client := newTestClient(t, http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		t.Errorf("unexpected request %s", r.URL.Path)
	}))
	tag, title := "v1", " "
	_, err := (Releases{Client: client, Repository: "alice/project"}).Create(context.Background(), ReleaseOptions{Tag: &tag, Title: &title})
	require.ErrorContains(t, err, "title is empty")
}

func TestReleaseAssetRenameAndDelete(t *testing.T) {
	client := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "/api/v1/repos/alice/project/releases/1/assets/2", r.URL.Path)
		if r.Method == http.MethodDelete {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		assert.Equal(t, http.MethodPatch, r.Method)
		var payload map[string]string
		assert.NoError(t, json.NewDecoder(r.Body).Decode(&payload))
		assert.Equal(t, map[string]string{"name": "renamed.bin"}, payload)
		_, _ = w.Write([]byte(`{"id":2,"name":"renamed.bin","created_at":"0001-01-01T00:00:00Z"}`))
	}))
	service := Releases{Client: client, Repository: "alice/project"}
	asset, err := service.RenameAsset(context.Background(), 1, 2, "renamed.bin")
	require.NoError(t, err)
	require.Equal(t, ReleaseAsset{ID: 2, Name: "renamed.bin"}, asset)
	require.NoError(t, service.DeleteAsset(context.Background(), 1, 2))
}

func TestReleaseCreateReportsAnUnresolvableTargetNotAMissingRelease(t *testing.T) {
	client := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNotFound)
		_, _ = w.Write([]byte(`{"message":"target not found"}`))
	}))
	target, tag := "deadbeef", "v1"
	_, err := Releases{Client: client, Repository: "alice/project"}.Create(context.Background(), ReleaseOptions{Tag: &tag, Title: &tag, Target: &target})
	var notFound *NotFoundError
	require.ErrorAs(t, err, &notFound)
	require.EqualError(t, err, `target "deadbeef" not found in alice/project`)
}

func TestReleaseTagExistsDistinguishesMissingFromFailing(t *testing.T) {
	status := http.StatusNotFound
	client := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(status)
		_, _ = w.Write([]byte(`{"message":"x"}`))
	}))
	service := Releases{Client: client, Repository: "alice/project"}
	exists, err := service.TagExists(context.Background(), "v1")
	require.NoError(t, err)
	require.False(t, exists)
	status = http.StatusInternalServerError
	_, err = service.TagExists(context.Background(), "v1")
	require.Error(t, err)
}

func TestReleaseCreateInAMissingRepositoryNamesTheRepository(t *testing.T) {
	client := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNotFound)
		_, _ = w.Write([]byte(`{"message":"not found"}`))
	}))
	tag := "v9"
	_, err := Releases{Client: client, Repository: "alice/missing"}.Create(context.Background(), ReleaseOptions{Tag: &tag, Title: &tag})
	require.EqualError(t, err, "repository alice/missing not found")
}

func TestReleaseListNamesAMissingRepository(t *testing.T) {
	client := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNotFound)
		_, _ = w.Write([]byte(`{"message":"not found"}`))
	}))
	_, err := Releases{Client: client, Repository: "alice/missing"}.ListPage(context.Background(), 30, 1)
	require.EqualError(t, err, "repository alice/missing not found")
}

func TestReleaseLookupsTellAMissingRepositoryFromAMissingRelease(t *testing.T) {
	var requests []string
	client := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests = append(requests, r.URL.Path)
		if r.URL.Path == "/api/v1/repos/alice/project" {
			_, _ = w.Write([]byte(`{"id":1,"full_name":"alice/project"}`))
			return
		}
		w.WriteHeader(http.StatusNotFound)
		_, _ = w.Write([]byte(`{"message":"not found"}`))
	}))
	missing := Releases{Client: client, Repository: "alice/missing"}
	_, err := missing.View(context.Background(), "v9")
	require.EqualError(t, err, "repository alice/missing not found")
	_, err = missing.Latest(context.Background())
	require.EqualError(t, err, "repository alice/missing not found")
	requests = nil
	_, err = Releases{Client: client, Repository: "alice/project"}.View(context.Background(), "v9")
	require.EqualError(t, err, "release v9 not found in alice/project")
	require.Equal(t, []string{"/api/v1/repos/alice/project/releases/tags/v9", "/api/v1/repos/alice/project"}, requests)
}

func TestReleaseTargetInAnEmptyRepositoryAsksForACommit(t *testing.T) {
	client := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusConflict)
		_, _ = w.Write([]byte(`{"message":"Git Repository is empty."}`))
	}))
	tag, yes, target := "v1", true, "main"
	_, err := Releases{Client: client, Repository: "alice/project"}.Create(context.Background(), ReleaseOptions{Tag: &tag, Title: &tag, Draft: &yes, Target: &target})
	require.EqualError(t, err, "alice/project is empty; push a commit first")

	client = newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusUnprocessableEntity)
		_, _ = w.Write([]byte(`{"message":"repo is empty"}`))
	}))
	_, err = Releases{Client: client, Repository: "alice/project"}.Create(context.Background(), ReleaseOptions{Tag: &tag, Title: &tag})
	require.EqualError(t, err, "alice/project is empty; push a commit first")
}

func TestReleaseDraftsRequireAnExistingTarget(t *testing.T) {
	var requests []string
	client := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests = append(requests, r.Method+" "+r.URL.Path)
		switch {
		case r.URL.Path == "/api/v1/repos/alice/project/commits" && r.URL.Query().Get("sha") == "release/1.1":
			assert.Equal(t, "1", r.URL.Query().Get("limit"))
			_, _ = w.Write([]byte(`[{"sha":"abc"}]`))
		case r.URL.Path == "/api/v1/repos/alice/project/commits":
			w.WriteHeader(http.StatusNotFound)
			_, _ = w.Write([]byte(`{"message":"object does not exist [id: ` + r.URL.Query().Get("sha") + `, rel_path: ]"}`))
		case r.Method == http.MethodPatch:
			// Publishing a draft whose target does not exist; Gitea explains it only to administrators.
			w.WriteHeader(http.StatusInternalServerError)
			_, _ = w.Write([]byte(`{"message":""}`))
		default:
			_, _ = w.Write([]byte(`{"id":7,"tag_name":"v1","name":"v1","draft":true}`))
		}
	}))
	service := Releases{Client: client, Repository: "alice/project"}
	tag, yes, missing, branch := "v1", true, "nosuch", "release/1.1"
	_, err := service.Create(context.Background(), ReleaseOptions{Tag: &tag, Title: &tag, Draft: &yes, Target: &missing})
	require.EqualError(t, err, `target "nosuch" not found in alice/project`)
	require.Equal(t, []string{"GET /api/v1/repos/alice/project/commits"}, requests, "no draft is created")
	_, err = service.Create(context.Background(), ReleaseOptions{Tag: &tag, Title: &tag, Draft: &yes, Target: &branch})
	require.NoError(t, err)
	draft := Release{ID: 7, Tag: "v1", Target: "gone", Draft: true}
	_, err = service.Edit(context.Background(), draft, ReleaseOptions{Target: &missing})
	require.EqualError(t, err, `target "nosuch" not found in alice/project`)
	no := false
	requests = nil
	_, err = service.Edit(context.Background(), draft, ReleaseOptions{Draft: &no})
	require.EqualError(t, err, `target "gone" not found in alice/project`)
	require.Equal(t, []string{"PATCH /api/v1/repos/alice/project/releases/7", "GET /api/v1/repos/alice/project/commits"}, requests)
	// Another failure of a release that is no draft is no missing target.
	_, err = service.Edit(context.Background(), Release{ID: 7, Target: "gone"}, ReleaseOptions{Draft: &no})
	require.EqualError(t, err, "Gitea API returned HTTP 500 for PATCH /api/v1/repos/alice/project/releases/7")
}

func TestReleaseEditRefusesATagAnotherReleaseHas(t *testing.T) {
	var requests []string
	client := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests = append(requests, r.Method+" "+r.URL.Path)
		switch r.URL.Path {
		case "/api/v1/repos/alice/project/releases/tags/v2":
			_, _ = w.Write([]byte(`{"id":8,"tag_name":"v2"}`))
		case "/api/v1/repos/alice/project/releases/tags/v3":
			w.WriteHeader(http.StatusNotFound)
			_, _ = w.Write([]byte(`{"message":"not found"}`))
		default:
			_, _ = w.Write([]byte(`{"id":7,"tag_name":"v3"}`))
		}
	}))
	service := Releases{Client: client, Repository: "alice/project"}
	current := Release{ID: 7, Tag: "v1"}
	taken, free := "v2", "v3"
	_, err := service.Edit(context.Background(), current, ReleaseOptions{Tag: &taken})
	require.ErrorAs(t, err, new(*ConflictError))
	require.EqualError(t, err, "release v2 already exists in alice/project")
	require.Equal(t, []string{"GET /api/v1/repos/alice/project/releases/tags/v2"}, requests, "nothing is changed")
	requests = nil
	item, err := service.Edit(context.Background(), current, ReleaseOptions{Tag: &free})
	require.NoError(t, err)
	require.Equal(t, "v3", item.Tag)
	require.Equal(t, []string{"GET /api/v1/repos/alice/project/releases/tags/v3", "PATCH /api/v1/repos/alice/project/releases/7"}, requests)
}
