// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package actions

import (
	"context"
	"fmt"
	"os"

	actions_model "gitea.dev/models/actions"
	"gitea.dev/models/db"
	"gitea.dev/modules/globallock"
	"gitea.dev/modules/storage"
	"gitea.dev/modules/timeutil"

	"xorm.io/builder"
)

// CleanupCaches removes abandoned uploads, expired caches and deleted repositories.
// Cache lifetime is independent of the workflow run that originally populated it.
func CleanupCaches(ctx context.Context) error {
	var entries []*actions_model.ActionCache
	cond := builder.Or(builder.Lte{"expires_unix": timeutil.TimeStampNow()}, builder.NotIn("repo_id", builder.Select("id").From("repository")))
	if err := db.GetEngine(ctx).Where(cond).Find(&entries); err != nil {
		return err
	}
	for _, entry := range entries {
		if err := globallock.LockAndDo(ctx, fmt.Sprintf("actions-cache-%d", entry.ID), func(ctx context.Context) error {
			exists, err := db.GetEngine(ctx).Where(cond).And("id = ?", entry.ID).Exist(new(actions_model.ActionCache))
			if err != nil || !exists {
				return err
			}
			claimed, err := db.GetEngine(ctx).Where(cond).And("id = ?", entry.ID).Cols("status").Update(&actions_model.ActionCache{Status: actions_model.CacheDeleting})
			if err != nil || claimed != 1 { return err }
			chunks, err := actions_model.GetCacheChunks(ctx, entry.ID)
			if err != nil {
				return err
			}
			paths := []string{actions_model.CachePath(entry.ID)}
			for _, chunk := range chunks {
				paths = append(paths, actions_model.CacheChunkPath(entry.ID, chunk.Offset))
			}
			for _, path := range paths {
				if err := storage.ActionsArtifacts.Delete(path); err != nil && !os.IsNotExist(err) {
					return err
				}
			}
			return db.WithTx(ctx, func(ctx context.Context) error {
				if _, err := db.GetEngine(ctx).Where("cache_id = ?", entry.ID).Delete(new(actions_model.ActionCacheChunk)); err != nil {
					return err
				}
				_, err := db.GetEngine(ctx).ID(entry.ID).Delete(new(actions_model.ActionCache))
				return err
			})
		}); err != nil {
			return err
		}
	}
	return nil
}
