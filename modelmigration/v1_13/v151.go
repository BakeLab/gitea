// Copyright 2020 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package v1_13

import (
	"context"

	"gitea.dev/modelmigration/base"
)

func SetDefaultPasswordToArgon2(_ context.Context, x base.EngineMigration) error {
	_, err := x.Exec("ALTER TABLE `user` ALTER COLUMN passwd_hash_algo SET DEFAULT 'argon2';")
	return err
}
