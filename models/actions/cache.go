// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package actions

import (
	"context"
	"fmt"

	"gitea.dev/models/db"
	"gitea.dev/modules/timeutil"
)

const (
	CachePending = iota
	CacheComplete
	CacheWriting
	CacheDeleting
)

type ActionCache struct {
	ID          int64  `xorm:"pk autoincr"`
	RepoID      int64  `xorm:"index unique(repo_key)"`
	KeyHash     string `xorm:"VARCHAR(64) unique(repo_key)"`
	TaskID      int64
	Ref         string `xorm:"TEXT"`
	Key         string `xorm:"TEXT"`
	Version     string `xorm:"TEXT"`
	Size        int64
	Status      int
	CreatedUnix timeutil.TimeStamp `xorm:"created"`
	ExpiresUnix timeutil.TimeStamp `xorm:"index"`
}

type ActionCacheChunk struct {
	ID      int64 `xorm:"pk autoincr"`
	CacheID int64 `xorm:"unique(cache_offset)"`
	Offset  int64 `xorm:"unique(cache_offset)"`
	Size    int64
}

func init() {
	db.RegisterModel(new(ActionCache))
	db.RegisterModel(new(ActionCacheChunk))
}

func CachePath(id int64) string              { return fmt.Sprintf("cache/%d/archive", id) }
func CacheChunkPath(id, offset int64) string { return fmt.Sprintf("cache/%d/chunk-%d", id, offset) }
func GetCacheChunks(ctx context.Context, id int64) ([]*ActionCacheChunk, error) {
	var chunks []*ActionCacheChunk
	err := db.GetEngine(ctx).Where("cache_id = ?", id).Asc("offset").Find(&chunks)
	return chunks, err
}
