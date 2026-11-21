// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package actions

import (
	stdcontext "context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	actions_model "gitea.dev/models/actions"
	"gitea.dev/models/db"
	"gitea.dev/modules/globallock"
	"gitea.dev/modules/json"
	"gitea.dev/modules/setting"
	"gitea.dev/modules/storage"
	"gitea.dev/modules/timeutil"
	"gitea.dev/modules/web"
	"gitea.dev/services/context"

	"xorm.io/builder"
)

const (
	CacheRouteBase       = "/api/actions_cache"
	maxCacheSize   int64 = 5 * 1024 * 1024 * 1024
	maxCacheChunk  int64 = 128 * 1024 * 1024
)

func CacheRoutes() *web.Router {
	m := web.NewRouter()
	m.Get("/download/{id}/{expires}/{signature}", ArtifactV4Contexter(), downloadCache)
	m.Group("/_apis/artifactcache", func() {
		m.Get("/cache", findCache)
		m.Post("/caches", reserveCache)
		m.Patch("/caches/{id}", uploadCache)
		m.Post("/caches/{id}", commitCache)
	}, ArtifactContexter())
	return m
}

func cacheRefs(ctx *ArtifactContext) ([]string, error) {
	if err := ctx.ActionTask.Job.LoadRun(ctx); err != nil {
		return nil, err
	}
	run := ctx.ActionTask.Job.Run
	if err := run.LoadRepo(ctx); err != nil {
		return nil, err
	}
	ref := run.Ref
	refs := []string{}
	if run.Event.IsPullRequest() || run.Event.IsPullRequestReview() {
		payload, err := run.GetPullRequestEventPayload()
		if err != nil || payload.Index <= 0 || payload.PullRequest == nil || payload.PullRequest.Base == nil {
			return nil, fmt.Errorf("cacheRefs: invalid pull request payload")
		}
		ref = fmt.Sprintf("refs/pull/%d/head", payload.Index)
		refs = append(refs, "refs/heads/"+payload.PullRequest.Base.Ref)
	} else if run.IsForkPullRequest {
		return nil, fmt.Errorf("cacheRefs: missing pull request scope")
	}
	if ref == "" {
		return nil, fmt.Errorf("cacheRefs: empty ref")
	}
	return append(append([]string{ref}, refs...), "refs/heads/"+run.Repo.DefaultBranch), nil
}

func cacheSignature(id, expires int64) string {
	mac := hmac.New(sha256.New, []byte(setting.GetGeneralTokenSigningSecret()))
	_, _ = fmt.Fprintf(mac, "actions-cache:%d:%d", id, expires)
	return hex.EncodeToString(mac.Sum(nil))
}

func findCache(ctx *ArtifactContext) {
	refs, err := cacheRefs(ctx)
	if err != nil {
		ctx.HTTPError(http.StatusForbidden, "Invalid cache scope")
		return
	}
	keys := strings.Split(ctx.Req.URL.Query().Get("keys"), ",")
	version := ctx.Req.URL.Query().Get("version")
	if len(keys) > 10 || version == "" || len(version) > 128 {
		ctx.HTTPError(http.StatusBadRequest, "Invalid cache keys or version")
		return
	}
	for _, ref := range refs {
		var entries []*actions_model.ActionCache
		err = db.GetEngine(ctx).Where("repo_id = ? AND ref = ? AND version = ? AND status = ? AND expires_unix > ?", ctx.ActionTask.RepoID, ref, version, actions_model.CacheComplete, timeutil.TimeStampNow()).Desc("id").Limit(1000).Find(&entries)
		if err != nil {
			ctx.HTTPError(http.StatusInternalServerError, "Cache lookup failed")
			return
		}
		for _, key := range keys {
			for _, exact := range []bool{true, false} {
				if key == "" || len(key) > 512 {
					continue
				}
				for _, entry := range entries {
					if (exact && entry.Key == key) || (!exact && strings.HasPrefix(entry.Key, key)) {
						expiry := time.Now().Add(15 * time.Minute).Unix()
						location := fmt.Sprintf("%s%s/download/%d/%d/%s", strings.TrimRight(setting.AppURL, "/"), CacheRouteBase, entry.ID, expiry, cacheSignature(entry.ID, expiry))
						ctx.JSON(http.StatusOK, map[string]any{"cacheKey": entry.Key, "scope": entry.Ref, "archiveLocation": location})
						return
					}
				}
			}
		}
	}
	ctx.Status(http.StatusNoContent)
}

func reserveCache(ctx *ArtifactContext) {
	var input struct {
		Key     string `json:"key"`
		Version string `json:"version"`
		Size    int64  `json:"cacheSize"`
	}
	if json.NewDecoder(io.LimitReader(ctx.Req.Body, 4096)).Decode(&input) != nil || input.Key == "" || len(input.Key) > 512 || input.Version == "" || len(input.Version) > 128 || input.Size <= 0 || input.Size > maxCacheSize {
		ctx.HTTPError(http.StatusBadRequest, "Invalid cache reservation")
		return
	}
	refs, err := cacheRefs(ctx)
	if err != nil {
		ctx.HTTPError(http.StatusForbidden, "Invalid cache scope")
		return
	}
	unlock, err := globallock.Lock(ctx, fmt.Sprintf("actions-cache-repo-%d", ctx.ActionTask.RepoID))
	defer unlock()
	if err != nil {
		ctx.HTTPError(http.StatusServiceUnavailable, "Cache busy")
		return
	}
	var entries []*actions_model.ActionCache
	if err := db.GetEngine(ctx).Where("repo_id = ?", ctx.ActionTask.RepoID).Find(&entries); err != nil {
		ctx.HTTPError(http.StatusInternalServerError, "Cache quota lookup failed")
		return
	}
	var total int64
	for _, entry := range entries {
		total += entry.Size
	}
	if total+input.Size > 10*1024*1024*1024 || len(entries) >= 1000 {
		ctx.HTTPError(http.StatusConflict, "Repository cache quota exhausted")
		return
	}
	hash := sha256.Sum256([]byte(refs[0] + "\x00" + input.Key + "\x00" + input.Version))
	entry := &actions_model.ActionCache{RepoID: ctx.ActionTask.RepoID, TaskID: ctx.ActionTask.ID, Ref: refs[0], Key: input.Key, Version: input.Version, KeyHash: hex.EncodeToString(hash[:]), Size: input.Size, ExpiresUnix: timeutil.TimeStampNow().Add(timeutil.Day)}
	if _, err := db.GetEngine(ctx).Insert(entry); err != nil {
		ctx.HTTPError(http.StatusConflict, "Cache already reserved")
		return
	}
	ctx.JSON(http.StatusCreated, map[string]any{"cacheId": entry.ID})
}

func writableCache(ctx *ArtifactContext) (*actions_model.ActionCache, func()) {
	id, _ := strconv.ParseInt(ctx.PathParam("id"), 10, 64)
	unlock, err := globallock.Lock(ctx, fmt.Sprintf("actions-cache-%d", id))
	if err != nil {
		unlock()
		ctx.HTTPError(http.StatusServiceUnavailable, "Cache busy")
		return nil, func() {}
	}
	entry, exists, err := db.Get[actions_model.ActionCache](ctx, builder.Eq{"id": id, "task_id": ctx.ActionTask.ID, "repo_id": ctx.ActionTask.RepoID, "status": actions_model.CachePending})
	if err != nil || !exists || entry.ExpiresUnix <= timeutil.TimeStampNow() {
		unlock()
		ctx.HTTPError(http.StatusNotFound, "Cache upload not found")
		return nil, func() {}
	}
	claimed, err := db.GetEngine(ctx).Where("id = ? AND status = ?", id, actions_model.CachePending).Cols("status").Update(&actions_model.ActionCache{Status: actions_model.CacheWriting})
	if err != nil || claimed != 1 {
		unlock()
		ctx.HTTPError(http.StatusConflict, "Cache upload busy")
		return nil, func() {}
	}
	return entry, func() {
		resetCtx, cancel := stdcontext.WithTimeout(stdcontext.WithoutCancel(ctx), 10*time.Second)
		defer cancel()
		_, _ = db.GetEngine(resetCtx).Where("id = ? AND status = ?", id, actions_model.CacheWriting).Cols("status").Update(&actions_model.ActionCache{Status: actions_model.CachePending})
		unlock()
	}
}

func uploadCache(ctx *ArtifactContext) {
	entry, unlock := writableCache(ctx)
	defer unlock()
	if entry == nil {
		return
	}
	var start, end int64
	rangeValue := ctx.Req.Header.Get("Content-Range")
	if _, err := fmt.Sscanf(rangeValue, "bytes %d-%d/*", &start, &end); err != nil || rangeValue != fmt.Sprintf("bytes %d-%d/*", start, end) || start < 0 || end < start || end >= entry.Size || end-start+1 > maxCacheChunk {
		ctx.HTTPError(http.StatusBadRequest, "Invalid content range")
		return
	}
	chunks, err := actions_model.GetCacheChunks(ctx, entry.ID)
	if err != nil {
		ctx.HTTPError(http.StatusInternalServerError, "Cache chunk lookup failed")
		return
	}
	if len(chunks) >= 1024 {
		ctx.HTTPError(http.StatusBadRequest, "Too many cache chunks")
		return
	}
	for _, chunk := range chunks {
		if start < chunk.Offset+chunk.Size && end >= chunk.Offset && !(start == chunk.Offset && end-start+1 == chunk.Size) {
			ctx.HTTPError(http.StatusConflict, "Overlapping cache chunks")
			return
		}
	}
	path := actions_model.CacheChunkPath(entry.ID, start)
	size, err := storage.ActionsArtifacts.Save(path, io.LimitReader(ctx.Req.Body, end-start+2), end-start+1)
	if err != nil || size != end-start+1 {
		_ = storage.ActionsArtifacts.Delete(path)
		ctx.HTTPError(http.StatusBadRequest, "Incomplete cache chunk")
		return
	}
	err = db.WithTx(ctx, func(txCtx stdcontext.Context) error {
		claimed, err := db.GetEngine(txCtx).Where("id = ? AND status = ?", entry.ID, actions_model.CacheWriting).Cols("status").Update(&actions_model.ActionCache{Status: actions_model.CacheWriting})
		if err != nil {
			return err
		}
		if claimed != 1 {
			return fmt.Errorf("cache upload expired")
		}
		existing, found, err := db.Get[actions_model.ActionCacheChunk](txCtx, builder.Eq{"cache_id": entry.ID, "offset": start})
		if err != nil {
			return err
		}
		if found {
			if existing.Size != size {
				return fmt.Errorf("cache chunk size changed")
			}
			return nil
		}
		_, err = db.GetEngine(txCtx).Insert(&actions_model.ActionCacheChunk{CacheID: entry.ID, Offset: start, Size: size})
		return err
	})
	if err != nil {
		_ = storage.ActionsArtifacts.Delete(path)
		ctx.HTTPError(http.StatusInternalServerError, "Cache chunk save failed")
		return
	}
	ctx.Status(http.StatusNoContent)
}

func commitCache(ctx *ArtifactContext) {
	entry, unlock := writableCache(ctx)
	defer unlock()
	if entry == nil {
		return
	}
	var input struct {
		Size int64 `json:"size"`
	}
	if json.NewDecoder(io.LimitReader(ctx.Req.Body, 1024)).Decode(&input) != nil || input.Size != entry.Size {
		ctx.HTTPError(http.StatusBadRequest, "Cache size mismatch")
		return
	}
	chunks, err := actions_model.GetCacheChunks(ctx, entry.ID)
	if err != nil {
		ctx.HTTPError(http.StatusInternalServerError, "Cache chunk lookup failed")
		return
	}
	var size int64
	for _, chunk := range chunks {
		if chunk.Offset != size {
			ctx.HTTPError(http.StatusBadRequest, "Incomplete cache chunks")
			return
		}
		size += chunk.Size
	}
	if size != entry.Size {
		ctx.HTTPError(http.StatusBadRequest, "Incomplete cache")
		return
	}
	reader := &cacheChunkReader{cacheID: entry.ID, chunks: chunks}
	defer reader.Close()
	written, err := storage.ActionsArtifacts.Save(actions_model.CachePath(entry.ID), io.LimitReader(reader, entry.Size+1), entry.Size)
	if err != nil || written != entry.Size {
		ctx.HTTPError(http.StatusInternalServerError, "Cache commit failed")
		return
	}
	updated, err := db.GetEngine(ctx).Where("id = ? AND status = ?", entry.ID, actions_model.CacheWriting).Cols("status", "expires_unix").Update(&actions_model.ActionCache{Status: actions_model.CacheComplete, ExpiresUnix: timeutil.TimeStampNow().Add(7 * timeutil.Day)})
	if err != nil || updated != 1 {
		_ = storage.ActionsArtifacts.Delete(actions_model.CachePath(entry.ID))
		ctx.HTTPError(http.StatusInternalServerError, "Cache commit failed")
		return
	}
	for _, chunk := range chunks {
		_ = storage.ActionsArtifacts.Delete(actions_model.CacheChunkPath(entry.ID, chunk.Offset))
	}
	ctx.Status(http.StatusNoContent)
}

// cacheChunkReader bounds open storage objects to one, including remote storage.
type cacheChunkReader struct {
	cacheID int64
	chunks  []*actions_model.ActionCacheChunk
	current io.ReadCloser
}

func (r *cacheChunkReader) Close() error {
	if r.current != nil {
		return r.current.Close()
	}
	return nil
}

func (r *cacheChunkReader) Read(p []byte) (int, error) {
	for {
		if r.current == nil {
			if len(r.chunks) == 0 {
				return 0, io.EOF
			}
			f, err := storage.ActionsArtifacts.Open(actions_model.CacheChunkPath(r.cacheID, r.chunks[0].Offset))
			if err != nil {
				return 0, err
			}
			r.current = f
			r.chunks = r.chunks[1:]
		}
		n, err := r.current.Read(p)
		if err == io.EOF {
			_ = r.current.Close()
			r.current = nil
			if n > 0 {
				return n, nil
			}
			continue
		}
		return n, err
	}
}

func downloadCache(ctx *ArtifactContext) {
	id, _ := strconv.ParseInt(ctx.PathParam("id"), 10, 64)
	expiry, _ := strconv.ParseInt(ctx.PathParam("expires"), 10, 64)
	if expiry < time.Now().Unix() || !hmac.Equal([]byte(ctx.PathParam("signature")), []byte(cacheSignature(id, expiry))) {
		ctx.Resp.WriteHeader(http.StatusUnauthorized)
		return
	}
	entry, exists, err := db.Get[actions_model.ActionCache](ctx, builder.Eq{"id": id, "status": actions_model.CacheComplete})
	if err != nil || !exists || entry.ExpiresUnix <= timeutil.TimeStampNow() {
		ctx.Resp.WriteHeader(http.StatusNotFound)
		return
	}
	file, err := storage.ActionsArtifacts.Open(actions_model.CachePath(id))
	if err != nil {
		ctx.Resp.WriteHeader(http.StatusNotFound)
		return
	}
	defer file.Close()
	ctx.ServeContent(file, context.ServeHeaderOptions{Filename: "cache.tzst", LastModified: entry.CreatedUnix.AsLocalTime()})
}
