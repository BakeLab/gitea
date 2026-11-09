// Copyright 2019 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package v1_12

import (
	"context"

	"gitea.dev/modelmigration/base"
)

func PrependRefsHeadsToIssueRefs(_ context.Context, x base.EngineMigration) error {
	_, err := x.Exec("UPDATE `issue` SET `ref` = 'refs/heads/' || `ref` WHERE `ref` IS NOT NULL AND `ref` <> '' AND `ref` NOT LIKE 'refs/%'")
	return err
}
