// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package v29

import (
	"context"

	"gitea.dev/modelmigration/base"
	"gitea.dev/modules/timeutil"
)

func AddActionCache(_ context.Context, x base.EngineMigration) error {
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
	return x.Sync(&ActionCache{}, &ActionCacheChunk{})
}
