// Copyright 2020 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package task

import (
	"fmt"

	"code.gitea.io/sdk/gitea"
	"code.gitea.io/tea/modules/utils"
)

// ResolveLabelNames returns a list of label IDs for a given list of label names
func ResolveLabelNames(client *gitea.Client, owner, repo string, labelNames []string) ([]int64, error) {
	labelIDs := make([]int64, 0, len(labelNames))
	page := 1
	for {
		labels, resp, err := client.ListRepoLabels(owner, repo, gitea.ListLabelsOptions{
			ListOptions: gitea.ListOptions{Page: page, PageSize: 50},
		})
		if err != nil {
			return nil, err
		}
		for _, l := range labels {
			if utils.Contains(labelNames, l.Name) {
				labelIDs = append(labelIDs, l.ID)
			}
		}
		if resp == nil || resp.NextPage == 0 {
			break
		}
		page = resp.NextPage
	}
	return labelIDs, nil
}

// ResolveLabelOpts resolves label names to IssueLabelsOption. Returns nil if names is empty.
func ResolveLabelOpts(client *gitea.Client, owner, repo string, names []string) (*gitea.IssueLabelsOption, error) {
	if len(names) == 0 {
		return nil, nil
	}
	ids, err := ResolveLabelNames(client, owner, repo, names)
	if err != nil {
		return nil, err
	}
	return &gitea.IssueLabelsOption{Labels: ids}, nil
}

// ApplyLabelChanges adds and removes labels on an issue or pull request.
func ApplyLabelChanges(client *gitea.Client, owner, repo string, index int64, add, rm *gitea.IssueLabelsOption) error {
	if rm != nil {
		// NOTE: as of 1.17, there is no API to remove multiple labels at once.
		for _, id := range rm.Labels {
			_, err := client.DeleteIssueLabel(owner, repo, index, id)
			if err != nil {
				return fmt.Errorf("could not remove labels: %s", err)
			}
		}
	}
	if add != nil {
		_, _, err := client.AddIssueLabels(owner, repo, index, *add)
		if err != nil {
			return fmt.Errorf("could not add labels: %s", err)
		}
	}
	return nil
}

// ApplyReviewerChanges adds and removes reviewers on a pull request.
func ApplyReviewerChanges(client *gitea.Client, owner, repo string, index int64, add, rm []string) error {
	if len(rm) != 0 {
		_, err := client.DeleteReviewRequests(owner, repo, index, gitea.PullReviewRequestOptions{
			Reviewers: rm,
		})
		if err != nil {
			return fmt.Errorf("could not remove reviewers: %w", err)
		}
	}
	if len(add) != 0 {
		_, err := client.CreateReviewRequests(owner, repo, index, gitea.PullReviewRequestOptions{
			Reviewers: add,
		})
		if err != nil {
			return fmt.Errorf("could not add reviewers: %w", err)
		}
	}
	return nil
}

// ResolveMilestoneID resolves a milestone name to its ID. Returns 0 for empty name.
func ResolveMilestoneID(client *gitea.Client, owner, repo, name string) (int64, error) {
	if name == "" {
		return 0, nil
	}
	ms, _, err := client.GetMilestoneByName(owner, repo, name)
	if err != nil {
		return 0, fmt.Errorf("could not resolve milestone '%s': %w", name, err)
	}
	return ms.ID, nil
}
