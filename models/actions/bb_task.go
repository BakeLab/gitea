// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package actions

import (
	"context"

	"gitea.dev/models/db"
	"gitea.dev/modules/timeutil"
)

type ActionBBTask struct {
	ID                    int64 `xorm:"pk autoincr"`
	JobID                 int64 `xorm:"UNIQUE"`
	RunnerID              int64 `xorm:"UNIQUE"`
	Endpoint              string
	Project               string
	Registration          string `xorm:"TEXT"`
	RequestJSON           string `xorm:"LONGTEXT"`
	TaskID                string
	Key                   string
	CancellationRequested timeutil.TimeStamp
	Completed             bool               `xorm:"index"`
	Created               timeutil.TimeStamp `xorm:"created"`
}

func init() {
	db.RegisterModel(new(ActionBBTask))
}

func GetBBTaskForRunner(ctx context.Context, runnerID int64) (*ActionBBTask, error) {
	row := new(ActionBBTask)
	has, err := db.GetEngine(ctx).Where("runner_id = ?", runnerID).Get(row)
	if !has || err != nil {
		return nil, err
	}
	return row, nil
}

func ListActiveBBTasks(ctx context.Context) ([]*ActionBBTask, error) {
	rows := make([]*ActionBBTask, 0)
	err := db.GetEngine(ctx).Where("completed = ?", false).Asc("id").Find(&rows)
	return rows, err
}
