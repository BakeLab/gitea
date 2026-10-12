// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package app

import (
	"context"
	"errors"
	"fmt"
	"math/rand/v2"
	"strconv"
	"strings"

	"gitea.dev/cli/internal/clierr"
	"gitea.dev/cli/internal/services"
	"gitea.dev/cli/internal/terminal"
	"github.com/spf13/cobra"
)

// labelScope returns the labels a label command manages: the repository's, or those of --org.
type labelScope func(*cobra.Command) (services.Labels, error)

// labelColors are the colors of new labels created without --color, from Gitea's default labels.
var labelColors = []string{"ee0701", "fbca04", "84b6eb", "cc317c", "c2e0c6", "0e8a16", "1d76db", "5319e7", "d4c5f9", "e99695", "bfd4f2", "006b75"}

const labelArguments = `A label can be supplied by its name, such as "bug" or "kind/feature", or by its ID. A
number is an ID first, then a name.`

func (a *application) labelCommand() *cobra.Command {
	group := &cobra.Command{Use: "label", Short: "Manage labels", Long: `Work with the labels of a repository or, with --org, of an organization. Labels are
addressed by name or ID.`}
	var org string
	group.PersistentFlags().StringVar(&org, "org", "", "Manage the labels of an `organization` instead of a repository")
	_ = group.RegisterFlagCompletionFunc("org", a.completeOrgs())
	scope := func(cmd *cobra.Command) (services.Labels, error) {
		if org != "" && a.options.repo != "" {
			return services.Labels{}, clierr.Wrap(clierr.Usage, errors.New("--org and --repo are mutually exclusive"))
		}
		t, err := a.resolve(cmd.Context(), org == "")
		return services.Labels{Client: t.client, Repository: t.Repo, Organization: org}, err
	}
	complete := a.completeScopedLabels(&org)
	group.AddCommand(a.labelListCommand(scope), a.labelCreateCommand(scope), a.labelEditCommand(scope, complete), a.labelDeleteCommand(scope, complete),
		a.labelImportCommand(scope), a.labelExportCommand(scope))
	return group
}

func (a *application) labelListCommand(scope labelScope) *cobra.Command {
	var limit, page int
	cmd := &cobra.Command{Use: "list", Short: "List labels", Example: `$ gt label list
$ gt label list --org acme
$ gt label list --json name,color`, Args: argsExact(0), RunE: func(cmd *cobra.Command, _ []string) error {
		s, err := scope(cmd)
		if err != nil {
			return err
		}
		items, err := s.ListPage(cmd.Context(), limit, page)
		if err != nil {
			return err
		}
		return writeTable(a, cmd, items.Items, listSpec[services.Label]{
			Columns: []terminal.Column{{Header: "ID"}, {Header: "Name"}, {Header: "Color", Prefix: "● "}, {Header: "Description", Flex: true}, {Header: "Flags"}},
			Row:     labelRow, Empty: "no labels in " + s.Scope(), Context: "labels in " + s.Scope(),
		}, servicePage(items, page))
	}}
	pageFlags(cmd, &limit, &page)
	return cmd
}

func labelRow(item services.Label) []terminal.Cell {
	var flags []string
	if item.Exclusive {
		flags = append(flags, "exclusive")
	}
	if item.Archived {
		flags = append(flags, "archived")
	}
	return []terminal.Cell{
		{Text: strconv.FormatInt(item.ID, 10)},
		{Text: item.Name},
		{Text: item.Color, Color: item.Color},
		{Text: item.Description},
		{Text: strings.Join(flags, ", ")},
	}
}

// labelName names a label in messages: `label "bug" in alice/x`.
func labelName(s services.Labels, item services.Label) string {
	return fmt.Sprintf("label %q in %s", item.Name, s.Scope())
}

type labelFields struct {
	name, color, description string
	exclusive, archived      bool
}

func (f *labelFields) register(cmd *cobra.Command, nameUsage string) {
	flags := cmd.Flags()
	flags.StringVarP(&f.color, "color", "c", "", "Color as six hexadecimal `digits`, such as d73a4a")
	flags.StringVarP(&f.description, "description", "d", "", "Description of the label")
	flags.BoolVar(&f.exclusive, "exclusive", false, "Allow only one label of its scope, such as priority/high, per issue")
	flags.BoolVar(&f.archived, "archived", false, "Archive the label so that it cannot be applied anymore")
	flags.StringVarP(&f.name, "name", "n", "", nameUsage)
}

// options returns the fields that were passed, checking them before any request.
func (f *labelFields) options(cmd *cobra.Command) (services.LabelOptions, error) {
	var opts services.LabelOptions
	if cmd.Flags().Changed("name") {
		if strings.TrimSpace(f.name) == "" {
			return opts, clierr.Wrap(clierr.Usage, errors.New("label name cannot be empty"))
		}
		opts.Name = &f.name
	}
	if cmd.Flags().Changed("color") {
		f.color = strings.TrimPrefix(f.color, "#")
		if !labelColor.MatchString(f.color) {
			return opts, clierr.Wrap(clierr.Usage, errors.New("--color requires six hexadecimal digits, such as d73a4a"))
		}
		opts.Color = &f.color
	}
	if cmd.Flags().Changed("description") {
		opts.Description = &f.description
	}
	if cmd.Flags().Changed("exclusive") {
		opts.Exclusive = &f.exclusive
	}
	if cmd.Flags().Changed("archived") {
		opts.Archived = &f.archived
	}
	return opts, nil
}

func (a *application) labelCreateCommand(scope labelScope) *cobra.Command {
	var fields labelFields
	var file string
	var force bool
	cmd := &cobra.Command{Use: "create <name>", Aliases: []string{"new"}, Short: "Create a label", Long: `Create a label. Without --color, the label gets a random color. A label with the same
name, ignoring case, is refused unless --force updates it. Without a name on a terminal,
prompts ask for the name, color and description.`, Example: `$ gt label create bug --color d73a4a --description "Something is broken"
$ gt label create "priority/high" --exclusive
# Update the label if it exists
$ gt label create bug -c ee0701 --force`, Args: usageArgs(cobra.MaximumNArgs(1)), RunE: func(cmd *cobra.Command, args []string) error {
		if file != "" {
			return a.importLabels(cmd, scope, file, force)
		}
		if len(args) == 1 {
			if cmd.Flags().Changed("name") {
				return clierr.Wrap(clierr.Usage, errors.New("pass the label name as an argument or --name, not both"))
			}
			if err := cmd.Flags().Set("name", args[0]); err != nil {
				return err
			}
		}
		opts, err := fields.options(cmd)
		if err != nil {
			return err
		}
		prompted, err := a.requireInput(cmd, &fields.name, "name", "Label name", nil)
		if err != nil {
			return err
		}
		opts.Name = &fields.name
		if prompted {
			if err := a.askLabelDetails(cmd, &opts); err != nil {
				return err
			}
		}
		if opts.Color == nil {
			color := labelColors[rand.IntN(len(labelColors))]
			opts.Color = &color
		}
		s, err := scope(cmd)
		if err != nil {
			return err
		}
		existing, err := s.All(cmd.Context())
		if err != nil {
			return err
		}
		item, updated, err := s.CreateUnique(cmd.Context(), existing, opts, force)
		if err != nil {
			return forceHint(err)
		}
		verb := "Created"
		if updated {
			verb = "Updated"
		}
		a.done(cmd, "%s %s", verb, labelName(s, item))
		return a.jsonResult(cmd, item)
	}}
	fields.register(cmd, "Label `name`")
	_ = cmd.Flags().MarkHidden("name")
	cmd.Flags().BoolVarP(&force, "force", "f", false, "Update the label if one with the same name exists")
	cmd.Flags().StringVar(&file, "file", "", "Import labels from a `file`, like gt label import")
	_ = cmd.Flags().MarkHidden("file")
	return cmd
}

// askLabelDetails asks for the optional color and description of a label whose name was prompted.
func (a *application) askLabelDetails(cmd *cobra.Command, opts *services.LabelOptions) error {
	if opts.Color == nil {
		color, err := a.deps.Prompter.Ask(cmd.Context(), "Color (hex, e.g. d73a4a; empty = random):", func(s string) error {
			if s != "" && !labelColor.MatchString(strings.TrimPrefix(s, "#")) {
				return errors.New("use six hexadecimal digits")
			}
			return nil
		})
		if err != nil {
			return err
		}
		if color = strings.TrimPrefix(color, "#"); color != "" {
			opts.Color = &color
		}
	}
	if opts.Description == nil {
		description, err := a.deps.Prompter.Ask(cmd.Context(), "Description (optional):", nil)
		if err != nil {
			return err
		}
		opts.Description = &description
	}
	return nil
}

// forceHint points at --force when a label exists already.
func forceHint(err error) error {
	if conflict, ok := errors.AsType[*services.ConflictError](err); ok && conflict.Reason == services.ReasonExists {
		conflict.Hint = "pass --force to update it"
	}
	return err
}

func (a *application) labelEditCommand(scope labelScope, complete cobra.CompletionFunc) *cobra.Command {
	var fields labelFields
	var byName bool
	cmd := &cobra.Command{Use: "edit <label>", Short: "Edit a label", Example: `$ gt label edit bug --name defect --color b60205
$ gt label edit "kind/question" --description "Further information is requested"
$ gt label edit old-stuff --archived`, Annotations: map[string]string{"gt.arguments": labelArguments}, Args: argsExact(1), RunE: func(cmd *cobra.Command, args []string) error {
		opts, err := fields.options(cmd)
		if err != nil {
			return err
		}
		if opts == (services.LabelOptions{}) {
			return errNothingToEdit
		}
		s, err := scope(cmd)
		if err != nil {
			return err
		}
		found, err := s.Find(cmd.Context(), args, byName)
		if err != nil {
			return err
		}
		item, err := s.Edit(cmd.Context(), found[0].ID, opts)
		if err != nil {
			return err
		}
		a.done(cmd, "Edited %s", labelName(s, item))
		return a.jsonResult(cmd, item)
	}}
	fields.register(cmd, "Rename the label")
	cmd.Flags().BoolVar(&byName, "by-name", false, "Match a numeric argument as a label name rather than an ID")
	cmd.ValidArgsFunction = complete
	return cmd
}

func (a *application) labelDeleteCommand(scope labelScope, complete cobra.CompletionFunc) *cobra.Command {
	var byName bool
	cmd := &cobra.Command{Use: "delete <label>...", Short: "Delete labels", Example: `$ gt label delete bug
$ gt label delete wontfix duplicate --yes`, Annotations: map[string]string{"gt.text": "label deletion does not return a resource", "gt.confirm": "true", "gt.arguments": labelArguments}, Args: usageArgs(cobra.MinimumNArgs(1)), RunE: func(cmd *cobra.Command, args []string) error {
		s, err := scope(cmd)
		if err != nil {
			return err
		}
		found, err := s.Find(cmd.Context(), args, byName)
		if err != nil {
			return err
		}
		names := make([]string, len(found))
		for i, item := range found {
			names[i] = strconv.Quote(item.Name)
		}
		subject := "label " + names[0]
		if len(found) > 1 {
			subject = fmt.Sprintf("%d labels (%s)", len(found), strings.Join(names, ", "))
		}
		if err := a.confirmAction(cmd, terminal.Confirmation{Prompt: "Delete " + subject + " from " + s.Scope() + "?"}); err != nil {
			return err
		}
		return batchDone(a, cmd, found, func(item services.Label) string { return "label " + strconv.Quote(item.Name) }, func(item services.Label) (services.Label, error) {
			return item, s.Delete(cmd.Context(), item.ID)
		}, func(item services.Label) string { return fmt.Sprintf("Deleted label %q from %s", item.Name, s.Scope()) })
	}}
	cmd.Flags().BoolVar(&byName, "by-name", false, "Match numeric arguments as label names rather than IDs")
	cmd.ValidArgsFunction = complete
	return cmd
}

func (a *application) labelImportCommand(scope labelScope) *cobra.Command {
	var force bool
	cmd := &cobra.Command{Use: "import <file>", Short: "Create labels from a file", Long: `Create the labels of a label file, one per line: "#color name", optionally followed by
" ;description" and " ;exclusive", " ;archived" or both. gt label export writes this
format, and Gitea's label templates use it. Existing labels are refused unless --force
updates them.`, Example: `$ gt label import labels.txt
$ gt label export --repo alice/old | gt label import - --repo alice/new`, Args: argsExact(1), ValidArgsFunction: completeFiles, RunE: func(cmd *cobra.Command, args []string) error {
		return a.importLabels(cmd, scope, args[0], force)
	}}
	cmd.Flags().BoolVarP(&force, "force", "f", false, "Update labels that exist already")
	return cmd
}

func (a *application) importLabels(cmd *cobra.Command, scope labelScope, file string, force bool) error {
	data, err := readFileOrStdin(cmd.InOrStdin(), file)
	if err != nil {
		return err
	}
	rows, err := parseLabelFile(strings.NewReader(string(data)))
	if err != nil {
		return err
	}
	s, err := scope(cmd)
	if err != nil {
		return err
	}
	// Each row is created, or updated with force, continuing past failures.
	existing, err := s.All(cmd.Context())
	if err != nil {
		return err
	}
	outcome := map[string]string{}
	results, failures := applyBatch(cmd, rows, func(row services.LabelOptions) string { return "label " + strconv.Quote(*row.Name) }, func(row services.LabelOptions) (services.Label, error) {
		item, updated, err := s.CreateUnique(cmd.Context(), existing, row, force)
		err = forceHint(err)
		switch {
		case err != nil:
			return item, err
		case updated:
			outcome[item.Name] = "Updated"
		default:
			outcome[item.Name] = "Created"
			existing = append(existing, item)
		}
		return item, nil
	}, func(item services.Label) {
		a.done(cmd, "%s %s", outcome[item.Name], labelName(s, item))
	})
	return errors.Join(append(failures, writeBatchJSON(a, cmd, len(rows), results))...)
}

// completeScopedLabels completes the label names of the repository or, with --org, of the
// organization that *org names.
func (a *application) completeScopedLabels(org *string) cobra.CompletionFunc {
	return a.apiCompletion(false, func(ctx context.Context, t target, _ string) ([]cobra.Completion, error) {
		if *org == "" && t.Repo == "" {
			return nil, errRepositoryRequired
		}
		items, err := (services.Labels{Client: t.client, Repository: t.Repo, Organization: *org}).List(ctx, completionLimit, 1)
		return describe(items, func(item services.Label) (string, string) { return item.Name, item.Description }), err
	})
}
