// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package services

import (
	"context"
	"fmt"
	"strings"
)

func selectLabelNames(names []string, matches map[string][]int64) ([]int64, error) {
	ids := make([]int64, 0, len(names))
	seen := map[int64]bool{}
	for _, name := range names {
		candidates := matches[name]
		if len(candidates) == 0 {
			return nil, fmt.Errorf("label %q does not exist; use gt label list", name)
		}
		if len(candidates) > 1 {
			return nil, fmt.Errorf("label %q is ambiguous", name)
		}
		if !seen[candidates[0]] {
			ids = append(ids, candidates[0])
			seen[candidates[0]] = true
		}
	}
	return ids, nil
}

// ResolveIssueNames includes labels shared by the repository's organization.
// Duplicate names require an explicit scope to avoid choosing a different label.
func (s Labels) ResolveIssueNames(ctx context.Context, names []string) ([]int64, error) {
	if len(names) == 0 {
		return []int64{}, nil
	}
	for _, name := range names {
		if strings.TrimSpace(name) == "" {
			return nil, fmt.Errorf("label names cannot be empty")
		}
	}
	scopes := []Labels{s}
	owner, _, _ := strings.Cut(s.Repository, "/")
	_, isOrg, err := organization(ctx, s.Client, owner)
	if err != nil {
		return nil, err
	}
	if isOrg {
		scopes = append(scopes, Labels{Client: s.Client, Organization: owner})
	}
	type labelKey struct{ scope, name string }
	index := map[labelKey][]int64{}
	for _, scope := range scopes {
		scopeName := "repo"
		if scope.Organization != "" {
			scopeName = "org"
		}
		labels, err := scope.All(ctx)
		if err != nil {
			return nil, err
		}
		for _, label := range labels {
			for _, key := range []labelKey{{scopeName, label.Name}, {"", label.Name}} {
				index[key] = append(index[key], label.ID)
			}
		}
	}
	matches := map[string][]int64{}
	for _, input := range names {
		scope, name := splitLabelScope(input)
		matches[input] = index[labelKey{scope, name}]
	}
	return selectLabelNames(names, matches)
}

func splitLabelScope(input string) (scope, name string) {
	for _, prefix := range []string{"repo:", "org:"} {
		if after, ok := strings.CutPrefix(input, prefix); ok {
			return strings.TrimSuffix(prefix, ":"), after
		}
	}
	return "", input
}

// RequireFilterNames rejects names Gitea's repository issue filter cannot match: it
// matches repository labels by exact name and silently ignores every other name.
func (s Labels) RequireFilterNames(ctx context.Context, names []string) error {
	labels, err := s.All(ctx)
	if err != nil {
		return err
	}
	known := make(map[string]bool, len(labels))
	for _, label := range labels {
		known[label.Name] = true
	}
	for _, name := range names {
		if !known[name] {
			return fmt.Errorf("label %q does not exist in %s; issue filters match repository labels only, see gt label list", name, s.Repository)
		}
	}
	return nil
}
