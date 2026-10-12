// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package services

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"
)

// Scope names the repository or organization the labels belong to.
func (s Labels) Scope() string {
	if s.Organization != "" {
		return s.Organization
	}
	return s.Repository
}

// finder addresses the labels of the scope by ID or name (D10); byName matches numeric values as
// names only.
func (s Labels) finder(byName bool) NameOrID[Label] {
	return NameOrID[Label]{Kind: "label", Scope: s.Scope(), ID: func(l Label) int64 { return l.ID }, Name: func(l Label) string { return l.Name }, ByName: byName}
}

// Find returns the labels of the scope that raws address, each by ID or name (D10); byName matches
// numeric values as names only.
func (s Labels) Find(ctx context.Context, raws []string, byName bool) ([]Label, error) {
	items, err := s.All(ctx)
	if err != nil {
		return nil, err
	}
	result := make([]Label, len(raws))
	for i, raw := range raws {
		if result[i], err = s.finder(byName).Find(items, raw); err != nil {
			return nil, err
		}
	}
	return result, nil
}

// CreateUnique creates a label unless existing, the labels of the scope, has one with its name,
// ignoring case. Then update edits that label with the supplied fields, keeping its name, and
// otherwise CreateUnique fails: Gitea would create a second label that name filters cannot tell
// apart. It reports whether it updated a label.
func (s Labels) CreateUnique(ctx context.Context, existing []Label, opts LabelOptions, update bool) (Label, bool, error) {
	current, err := s.finder(true).Find(existing, deref(opts.Name))
	if errors.As(err, new(*NotFoundError)) {
		label, err := s.Create(ctx, opts)
		return label, false, err
	}
	if err != nil {
		return Label{}, false, err
	}
	if !update {
		return Label{}, false, &ConflictError{Reason: ReasonExists, Kind: "label", Key: strconv.Quote(current.Name), Scope: s.Scope(), Hint: "pass --force to update it"}
	}
	opts.Name = nil
	label, err := s.Edit(ctx, current.ID, opts)
	return label, true, err
}

func selectLabelNames(scope string, names []string, matches map[string][]int64) ([]int64, error) {
	ids := make([]int64, 0, len(names))
	seen := map[int64]bool{}
	for _, name := range names {
		candidates := matches[name]
		if len(candidates) == 0 {
			return nil, fmt.Errorf("label %q does not exist; use gt label list", name)
		}
		if len(candidates) > 1 {
			return nil, &AmbiguousError{Kind: "label", Key: strconv.Quote(name), Scope: scope, IDs: candidates}
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
	return selectLabelNames(s.Repository, names, matches)
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
