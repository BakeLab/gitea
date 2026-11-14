// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package packages

import (
	"net/http"
	"strconv"
	"strings"

	actions_model "gitea.dev/models/actions"
	user_model "gitea.dev/models/user"
	"gitea.dev/modules/setting"
	"gitea.dev/services/actions"
	"gitea.dev/services/context"
	"github.com/golang-jwt/jwt/v5"
)

// Rules are exact owner-ID|repository-ID|ref|workflow-ref|package-name tuples.
// No rules grant access by default. Authorization is confined to this request.
func authenticateGenericOIDC(ctx *context.Context) bool {
	if ctx.Req.Method != http.MethodPut && ctx.Req.Method != http.MethodGet && ctx.Req.Method != http.MethodHead {
		return false
	}
	parts := strings.Split(strings.Trim(ctx.Req.URL.Path, "/"), "/")
	if len(parts) != 7 || parts[0] != "api" || parts[1] != "packages" || parts[3] != "generic" {
		return false
	}
	raw, ok := strings.CutPrefix(ctx.Req.Header.Get("Authorization"), "Bearer ")
	if !ok || len(setting.Packages.GenericOIDCUploadRules) == 0 {
		return false
	}
	key, err := actions.OIDCSigningKey()
	if err != nil {
		return false
	}
	claims := jwt.MapClaims{}
	token, err := jwt.ParseWithClaims(raw, claims, func(_ *jwt.Token) (any, error) { return key.VerifyKey(), nil },
		jwt.WithValidMethods([]string{actions.OIDCSigningAlgorithm}), jwt.WithIssuer(actions.OIDCIssuer()),
		jwt.WithAudience(setting.AppURL+"api/packages"), jwt.WithExpirationRequired(), jwt.WithIssuedAt())
	if err != nil || !token.Valid {
		return false
	}
	value := func(name string) string { v, _ := claims[name].(string); return v }
	switch value("event_name") {
	case "push", "workflow_dispatch", "schedule":
	default:
		return false
	}
	rule := strings.Join([]string{value("repository_owner_id"), value("repository_id"), value("ref"), value("workflow_ref"), parts[4]}, "|")
	allowed := false
	for _, configured := range setting.Packages.GenericOIDCUploadRules {
		if configured == rule {
			allowed = true
			break
		}
	}
	if !allowed {
		return false
	}
	repoID, err := strconv.ParseInt(value("repository_id"), 10, 64)
	if err != nil {
		return false
	}
	runID, err := strconv.ParseInt(value("run_id"), 10, 64)
	if err != nil {
		return false
	}
	run, err := actions_model.GetRunByRepoAndID(ctx, repoID, runID)
	if err != nil || run.Status != actions_model.StatusRunning || run.IsForkPullRequest || run.NeedApproval || run.CommitSHA != value("sha") || run.Ref != value("ref") {
		return false
	}
	if strconv.FormatInt(run.OwnerID, 10) != value("repository_owner_id") {
		return false
	}
	owner, err := user_model.GetUserByID(ctx, run.OwnerID)
	if err != nil || !strings.EqualFold(owner.Name, parts[2]) {
		return false
	}
	doer, err := user_model.GetUserByID(ctx, run.TriggerUserID)
	if err != nil || !doer.IsActive || doer.ProhibitLogin {
		return false
	}
	ctx.Doer = doer
	ctx.IsSigned = true
	ctx.Data["genericOIDCUpload"] = true
	return true
}
