// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package packages

import (
	"net/http"
	"strconv"
	"strings"

	actions_model "gitea.dev/models/actions"
	"gitea.dev/models/perm"
	user_model "gitea.dev/models/user"
	"gitea.dev/modules/auth/httpauth"
	"gitea.dev/modules/setting"
	"gitea.dev/services/actions"
	"gitea.dev/services/context"

	"github.com/golang-jwt/jwt/v5"
)

func authenticatePackageOIDC(ctx *context.Context) bool {
	parts := strings.Split(strings.Trim(strings.TrimPrefix(ctx.Req.URL.Path, setting.AppSubURL), "/"), "/")
	if len(parts) < 4 || parts[0] != "api" || parts[1] != "packages" {
		return false
	}
	kind := parts[3]
	read := ctx.Req.Method == http.MethodGet || ctx.Req.Method == http.MethodHead
	upload := (kind == "generic" && ctx.Req.Method == http.MethodPut && len(parts) == 7) || (kind == "pypi" && ctx.Req.Method == http.MethodPost && len(parts) == 4)
	if !upload && !(read && ((kind == "generic" && len(parts) == 7) || (kind == "pypi" && ((len(parts) == 6 && parts[4] == "simple") || (len(parts) == 8 && parts[4] == "files"))))) {
		return false
	}
	authorization, ok := httpauth.ParseAuthorizationHeader(ctx.Req.Header.Get("Authorization"))
	if !ok || len(setting.Packages.OIDCUploadRules) == 0 {
		return false
	}
	raw := ""
	if authorization.BearerToken != nil {
		raw = authorization.BearerToken.Token
	}
	if authorization.BasicAuth != nil && authorization.BasicAuth.Username == "__token__" {
		raw = authorization.BasicAuth.Password
	}
	if raw == "" {
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
	repoID, err := strconv.ParseInt(value("repository_id"), 10, 64)
	if err != nil {
		return false
	}
	runID, err := strconv.ParseInt(value("run_id"), 10, 64)
	if err != nil {
		return false
	}
	run, err := actions_model.GetRunByRepoAndID(ctx, repoID, runID)
	if err != nil || run.Status != actions_model.StatusRunning || run.IsForkPullRequest || run.NeedApproval || run.CommitSHA != value("sha") || run.Ref != value("ref") || run.TriggerEvent != value("event_name") {
		return false
	}
	if strconv.FormatInt(run.OwnerID, 10) != value("repository_owner_id") {
		return false
	}
	attempt, hasAttempt, err := run.GetLatestAttempt(ctx)
	if err != nil {
		return false
	}
	triggerUserID := run.TriggerUserID
	if hasAttempt {
		if strconv.FormatInt(attempt.Attempt, 10) != value("run_attempt") || attempt.Status != actions_model.StatusRunning {
			return false
		}
		triggerUserID = attempt.TriggerUserID
	} else if value("run_attempt") != "1" {
		return false
	}
	owner, err := user_model.GetUserByName(ctx, parts[2])
	if err != nil {
		return false
	}
	prefix := strings.Join([]string{strconv.FormatInt(owner.ID, 10), value("repository_id"), value("ref"), value("workflow_ref"), kind}, "|") + "|"
	candidates := []string{}
	for _, rule := range setting.Packages.OIDCUploadRules {
		if name, found := strings.CutPrefix(rule, prefix); found && name != "" && !strings.Contains(name, "|") {
			candidates = append(candidates, name)
		}
	}
	if len(candidates) == 0 {
		return false
	}
	name := ""
	if kind == "generic" {
		name = parts[4]
	} else if read {
		name = parts[5]
	} else {
		// Authenticate the job and destination before reading the multipart upload.
		if err := ctx.Req.ParseMultipartForm(context.ParseMultipartFormMaxMemory); err != nil {
			return false
		}
		name = ctx.Req.FormValue("name")
	}
	if kind == "pypi" {
		name = strings.NewReplacer("_", "-", ".", "-").Replace(strings.ToLower(name))
	}
	allowed := false
	for _, candidate := range candidates {
		if candidate == name {
			allowed = true
			break
		}
	}
	if !allowed {
		return false
	}
	doer, err := user_model.GetUserByID(ctx, triggerUserID)
	if err != nil || !doer.IsActive || doer.ProhibitLogin {
		return false
	}
	ctx.Doer = doer
	ctx.IsSigned = true
	ctx.Data["packageOIDCUpload"] = true
	return true
}

func reqPyPIPackageRead(ctx *context.Context) {
	if ctx.Doer == nil && (ctx.Req.Method == http.MethodGet || ctx.Req.Method == http.MethodHead) {
		ownerID := strconv.FormatInt(ctx.Package.Owner.ID, 10)
		for _, allowed := range setting.Packages.AnonymousPyPIOwners {
			if allowed == ownerID {
				return
			}
		}
	}
	reqPackageAccess(perm.AccessModeRead)(ctx)
}
