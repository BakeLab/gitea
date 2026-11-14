// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package cmd

import (
	"context"
	"fmt"

	"gitea.dev/models/db"
	packages_model "gitea.dev/models/packages"
	user_model "gitea.dev/models/user"

	"github.com/urfave/cli/v3"
)

func newBreadboardWheelCleanupCommand() *cli.Command {
	return &cli.Command{
		Name:   "configure-breadboard-wheel-cleanup",
		Usage:  "Create a Breadboard PyPI cleanup rule retaining 10 versions and 30 days",
		Action: runConfigureBreadboardWheelCleanup,
		Flags: []cli.Flag{
			&cli.StringFlag{Name: "owner", Required: true, Usage: "Package owner name"},
			&cli.BoolFlag{Name: "enable", Usage: "Enable deletion by the existing package cleanup cron"},
		},
	}
}

func breadboardWheelCleanupRule(ownerID int64, enabled bool) *packages_model.PackageCleanupRule {
	return &packages_model.PackageCleanupRule{
		OwnerID:       ownerID,
		Type:          packages_model.TypePyPI,
		Enabled:       enabled,
		KeepCount:     10,
		RemoveDays:    30,
		MatchFullName: true,
		RemovePattern: `breadboard/.*`,
	}
}

func runConfigureBreadboardWheelCleanup(ctx context.Context, c *cli.Command) error {
	if err := initDB(ctx); err != nil {
		return err
	}
	owner, err := user_model.GetUserByName(ctx, c.String("owner"))
	if err != nil {
		return fmt.Errorf("runConfigureBreadboardWheelCleanup: %w", err)
	}
	err = db.WithTx(ctx, func(ctx context.Context) error {
		exists, err := packages_model.HasOwnerCleanupRuleForPackageType(ctx, owner.ID, packages_model.TypePyPI)
		if err != nil {
			return err
		}
		if exists {
			return fmt.Errorf("owner %q already has a PyPI cleanup rule; review it in package settings", owner.Name)
		}
		_, err = packages_model.InsertCleanupRule(ctx, breadboardWheelCleanupRule(owner.ID, c.Bool("enable")))
		return err
	})
	if err != nil {
		return fmt.Errorf("runConfigureBreadboardWheelCleanup: %w", err)
	}
	fmt.Printf("Created PyPI cleanup rule for %s/breadboard: keep 10 versions and all versions younger than 30 days; enabled=%t\n", owner.Name, c.Bool("enable"))
	return nil
}
