// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package services

import (
	"context"
	"fmt"
	"io"
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
	response, err := repoCall(ctx, s.Client, s.Repository, func(ctx context.Context, c *gitea.Client, owner, repo string) (*gitea.ActionsArtifactsResponse, *gitea.Response, error) {
		return c.Actions.ListRepoRunArtifacts(ctx, owner, repo, run, gitea.ListActionsArtifactsOptions{ListOptions: listOptions(limit, page)})
	})
	if err != nil {
		return Page[Artifact]{}, Classify(err, "run", strconv.FormatInt(run, 10), s.Repository)
	}
	total := int(response.TotalCount)
	return Page[Artifact]{Items: convertAll(response.Artifacts, artifactFrom), More: total > page*limit, Total: total, Counted: true}, nil
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
	endpoint, err := s.Client.URL(fmt.Sprintf("%sartifacts/%d/zip", s.endpoint(), id))
	if err != nil {
		return nil, err
	}
	response, err := s.Client.DownloadSigned(ctx, endpoint)
	if err != nil {
		return nil, Classify(err, "artifact", strconv.FormatInt(id, 10), s.Repository)
	}
	return response.Body, nil
}
