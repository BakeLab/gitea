// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package services

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"net/http"
	"path"
	"slices"
	"strconv"
	"strings"

	"gitea.dev/cli/internal/transport"
	gitea "gitea.dev/sdk"
	"gopkg.in/yaml.v3"
)

// Workflow is the stable command representation of an Actions workflow. ID is its file name,
// such as ci.yml.
type Workflow struct {
	ID    string `json:"id"`
	Name  string `json:"name"`
	Path  string `json:"path"`
	State string `json:"state"`
	URL   string `json:"url"`
}

func workflowFrom(workflow *gitea.ActionsWorkflow) Workflow {
	return Workflow{ID: workflow.ID, Name: workflow.Name, Path: workflow.Path, State: workflow.State, URL: workflow.HTMLURL}
}

// Enabled reports whether the workflow runs; Gitea reports a disabled one as disabled_manually.
func (w Workflow) Enabled() bool { return !strings.HasPrefix(w.State, "disabled") }

// Label names the workflow for people: "CI (ci.yml)".
func (w Workflow) Label() string {
	if w.Name == "" || w.Name == w.ID {
		return w.ID
	}
	return w.Name + " (" + w.ID + ")"
}

// DispatchResult identifies a workflow run created by dispatch when the server returns details.
type DispatchResult struct {
	ID  int64  `json:"id"`
	URL string `json:"url,omitempty"`
}

// Actions implements repository workflow and run operations.
type Actions struct {
	Client     *transport.Client
	Repository string
}

func (s Actions) endpoint() string { return "repos/" + s.Repository + "/actions/" }

// Workflows lists the repository workflow definitions.
func (s Actions) Workflows(ctx context.Context) ([]Workflow, error) {
	response, err := repoCall(ctx, s.Client, s.Repository, func(ctx context.Context, c *gitea.Client, owner, repo string) (*gitea.ActionsWorkflowResponse, *gitea.Response, error) {
		return c.Actions.ListRepoWorkflows(ctx, owner, repo)
	})
	if err != nil {
		return nil, Classify(err, "repository", s.Repository, "")
	}
	return convertAll(response.Workflows, workflowFrom), nil
}

// Workflow retrieves one workflow definition by its file name.
func (s Actions) Workflow(ctx context.Context, id string) (Workflow, error) {
	return repoConvert(ctx, s.Client, s.Repository, workflowFrom, func(ctx context.Context, c *gitea.Client, owner, repo string) (*gitea.ActionsWorkflow, *gitea.Response, error) {
		return c.Actions.GetRepoWorkflow(ctx, owner, repo, id)
	})
}

// FindWorkflow returns the workflow that raw names in the repository: its file name, path, file
// name without extension, or name, matched exactly and then case-insensitively.
func (s Actions) FindWorkflow(ctx context.Context, raw string) (Workflow, error) {
	items, err := s.Workflows(ctx)
	if err != nil {
		return Workflow{}, err
	}
	return FindWorkflow(items, raw, s.Repository)
}

// FindWorkflow returns the workflow among items that raw names, like Actions.FindWorkflow. It
// fails with a *NotFoundError when none matches and lists the files when several names match.
func FindWorkflow(items []Workflow, raw, scope string) (Workflow, error) {
	raw = strings.TrimSpace(raw)
	keys := func(w Workflow) []string {
		return []string{w.ID, w.Path, strings.TrimSuffix(w.ID, path.Ext(w.ID)), w.Name}
	}
	for _, fold := range []bool{false, true} {
		var matches []Workflow
		for _, item := range items {
			if slices.ContainsFunc(keys(item), func(key string) bool { return key == raw || (fold && strings.EqualFold(key, raw)) }) {
				matches = append(matches, item)
			}
		}
		switch len(matches) {
		case 0:
			continue
		case 1:
			return matches[0], nil
		}
		files := make([]string, len(matches))
		for i, match := range matches {
			files[i] = match.ID
		}
		return Workflow{}, fmt.Errorf("workflow %q is ambiguous in %s (%s); pass the file name", raw, scope, strings.Join(files, ", "))
	}
	return Workflow{}, NotFound("workflow", strconv.Quote(raw), scope)
}

// Dispatch triggers a workflow and reports the run it created.
func (s Actions) Dispatch(ctx context.Context, id, ref string, inputs map[string]string) (DispatchResult, error) {
	details, err := repoCall(ctx, s.Client, s.Repository, func(ctx context.Context, c *gitea.Client, owner, repo string) (*gitea.RunDetails, *gitea.Response, error) {
		return c.Actions.DispatchRepoWorkflow(ctx, owner, repo, id, gitea.CreateActionsWorkflowDispatchOption{Ref: ref, Inputs: inputs}, true)
	})
	if api, ok := errors.AsType[*transport.APIError](err); ok && api.Status == http.StatusNotFound && strings.Contains(api.Message, "ref") {
		// Gitea resolves a bare ref as a branch only; a tag needs its full name.
		if !strings.HasPrefix(ref, "refs/") {
			if retried, retryErr := s.Dispatch(ctx, id, "refs/tags/"+ref, inputs); retryErr == nil {
				return retried, nil
			}
		}
		return DispatchResult{}, &NotFoundError{Kind: "ref", Key: strconv.Quote(ref), Scope: s.Repository, Err: err}
	}
	if err != nil {
		return DispatchResult{}, Classify(err, "workflow", id, s.Repository)
	}
	return DispatchResult{ID: details.WorkflowRunID, URL: details.HTMLURL}, nil
}

// SetWorkflowEnabled updates whether a workflow can run.
func (s Actions) SetWorkflowEnabled(ctx context.Context, id string, enabled bool) error {
	return repoDo(ctx, s.Client, s.Repository, func(ctx context.Context, c *gitea.Client, owner, repo string) (*gitea.Response, error) {
		if enabled {
			return c.Actions.EnableRepoWorkflow(ctx, owner, repo, id)
		}
		return c.Actions.DisableRepoWorkflow(ctx, owner, repo, id)
	})
}

// WorkflowFile returns the workflow's YAML definition at ref, a branch, tag or commit, or on the
// default branch when ref is empty.
func (s Actions) WorkflowFile(ctx context.Context, workflow Workflow, ref string) ([]byte, error) {
	contents, err := repoCall(ctx, s.Client, s.Repository, func(ctx context.Context, c *gitea.Client, owner, repo string) (*gitea.ContentsResponse, *gitea.Response, error) {
		return c.Repositories.GetContents(ctx, owner, repo, ref, workflow.Path)
	})
	if api, ok := errors.AsType[*transport.APIError](err); ok && api.Status == http.StatusNotFound {
		// Gitea reports an unknown ref as "object does not exist [id: REF, rel_path: ]".
		if ref != "" && strings.Contains(api.Message, "[id: "+ref+",") {
			return nil, &NotFoundError{Kind: "ref", Key: strconv.Quote(ref), Scope: s.Repository, Err: err}
		}
		return nil, &NotFoundError{Kind: "workflow file", Key: workflow.Path, Scope: s.Repository, Err: err}
	}
	if err != nil {
		return nil, err
	}
	if contents.Content == nil {
		return nil, fmt.Errorf("%s is not a file in %s", workflow.Path, s.Repository)
	}
	data, err := base64.StdEncoding.DecodeString(*contents.Content)
	if err != nil {
		return nil, fmt.Errorf("invalid contents of %s: %w", workflow.Path, err)
	}
	return data, nil
}

// WorkflowInput is one input that a workflow's workflow_dispatch trigger declares.
type WorkflowInput struct {
	Name, Description string
	// Type is string, choice, boolean, number or environment; empty means string.
	Type, Default string
	Required      bool
	Options       []string
}

// ErrNoDispatch reports a workflow that has no workflow_dispatch trigger.
var ErrNoDispatch = errors.New("workflow has no workflow_dispatch trigger")

// DispatchInputs parses the inputs that the workflow_dispatch trigger of a workflow definition
// declares, in their order. It fails with ErrNoDispatch when the workflow cannot be dispatched.
func DispatchInputs(definition []byte) ([]WorkflowInput, error) {
	var workflow struct {
		On yaml.Node `yaml:"on"`
	}
	if err := yaml.Unmarshal(definition, &workflow); err != nil {
		return nil, fmt.Errorf("invalid workflow definition: %w", err)
	}
	on := workflow.On
	switch on.Kind {
	case yaml.ScalarNode:
		if on.Value == "workflow_dispatch" {
			return []WorkflowInput{}, nil
		}
	case yaml.SequenceNode:
		if slices.ContainsFunc(on.Content, func(event *yaml.Node) bool { return event.Value == "workflow_dispatch" }) {
			return []WorkflowInput{}, nil
		}
	case yaml.MappingNode:
		if trigger := mappingValue(&on, "workflow_dispatch"); trigger != nil {
			return dispatchInputs(mappingValue(trigger, "inputs"))
		}
	}
	return nil, ErrNoDispatch
}

func dispatchInputs(node *yaml.Node) ([]WorkflowInput, error) {
	inputs := []WorkflowInput{}
	if node == nil || node.Kind != yaml.MappingNode {
		return inputs, nil
	}
	for i := 0; i+1 < len(node.Content); i += 2 {
		var spec struct {
			Description string    `yaml:"description"`
			Type        string    `yaml:"type"`
			Default     yaml.Node `yaml:"default"`
			Required    bool      `yaml:"required"`
			Options     []string  `yaml:"options"`
		}
		if err := node.Content[i+1].Decode(&spec); err != nil {
			return nil, fmt.Errorf("invalid workflow input %s: %w", node.Content[i].Value, err)
		}
		inputs = append(inputs, WorkflowInput{
			Name: node.Content[i].Value, Description: spec.Description, Type: spec.Type,
			Default: spec.Default.Value, Required: spec.Required, Options: spec.Options,
		})
	}
	return inputs, nil
}

// mappingValue returns the value of key in a YAML mapping, or nil.
func mappingValue(node *yaml.Node, key string) *yaml.Node {
	if node == nil || node.Kind != yaml.MappingNode {
		return nil
	}
	for i := 0; i+1 < len(node.Content); i += 2 {
		if node.Content[i].Value == key {
			return node.Content[i+1]
		}
	}
	return nil
}
