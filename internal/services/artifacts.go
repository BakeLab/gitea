// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package services

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"strconv"

	gitea "gitea.dev/sdk"
)

// Artifact is the stable metadata for a downloadable Actions artifact.
type Artifact struct {
	ID        int64   `json:"id"`
	Name      string  `json:"name"`
	Size      int64   `json:"size_in_bytes"`
	URL       string  `json:"url"`
	Expired   bool    `json:"expired"`
	CreatedAt string  `json:"created_at"`
	UpdatedAt string  `json:"updated_at"`
	ExpiresAt *string `json:"expires_at"`
}

func artifactFrom(artifact *gitea.ActionsArtifact) Artifact {
	return Artifact{
		ID: artifact.ID, Name: artifact.Name, Size: artifact.SizeInBytes, URL: artifact.URL, Expired: artifact.Expired,
		CreatedAt: timestamp(artifact.CreatedAt), UpdatedAt: timestamp(artifact.UpdatedAt), ExpiresAt: optionalTimestamp(&artifact.ExpiresAt),
	}
}

// Artifacts lists one page of artifacts belonging to a run, with their total number.
func (s Actions) Artifacts(ctx context.Context, run int64, limit, page int) (Page[Artifact], error) {
	owner, repo, err := splitRepository(s.Repository)
	if err != nil {
		return Page[Artifact]{}, err
	}
	result, err := instancePage(ctx, s.Client, limit, page, func(limit, page int) ([]Artifact, http.Header, error) {
		response, resp, err := call(ctx, s.Client, func(ctx context.Context, c *gitea.Client) (*gitea.ActionsArtifactsResponse, *gitea.Response, error) {
			return c.Actions.ListRepoRunArtifacts(ctx, owner, repo, run, gitea.ListActionsArtifactsOptions{ListOptions: listOptions(limit, page)})
		})
		if err != nil {
			return nil, nil, err
		}
		// The server sends its count in the body only, and caps the page size below what AllArtifacts asks for;
		// a count smaller than the page means the response lacks it.
		headers := headerOf(resp).Clone()
		if total := int(response.TotalCount); total >= len(response.Artifacts) {
			headers.Set("X-Total-Count", strconv.Itoa(total))
		}
		return convertAll(response.Artifacts, artifactFrom), headers, nil
	})
	if err != nil {
		return Page[Artifact]{}, repositoryItemError(ctx, s.Client, s.Repository, err, "run", strconv.FormatInt(run, 10))
	}
	// Gitea lists no artifacts for a missing run rather than failing.
	if len(result.Items) == 0 {
		if _, err := s.Run(ctx, run); err != nil {
			return Page[Artifact]{}, err
		}
	}
	return result, nil
}

// AllArtifacts lists every artifact belonging to a run.
func (s Actions) AllArtifacts(ctx context.Context, run int64) ([]Artifact, error) {
	artifacts := []Artifact{}
	for page := 1; page <= maxListPages; page++ {
		window, err := s.Artifacts(ctx, run, 100, page)
		if err != nil {
			return nil, err
		}
		artifacts = append(artifacts, window.Items...)
		if !window.More || len(window.Items) == 0 {
			return artifacts, nil
		}
	}
	return nil, fmt.Errorf("listing exceeded %d pages", maxListPages)
}

// DownloadArtifact streams an artifact ZIP, accepting a scoped signed redirect.
func (s Actions) DownloadArtifact(ctx context.Context, id int64) (io.ReadCloser, error) {
	body, err := downloadAPI(ctx, s.Client, fmt.Sprintf("%sartifacts/%d/zip", s.endpoint(), id))
	if err != nil {
		return nil, repositoryItemError(ctx, s.Client, s.Repository, err, "artifact", strconv.FormatInt(id, 10))
	}
	return body, nil
}
