// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package app

import (
	"bufio"
	"bytes"
	"errors"
	"fmt"
	"io"
	"regexp"
	"strings"

	"gitea.dev/cli/internal/clierr"
	"gitea.dev/cli/internal/services"
	"gitea.dev/cli/internal/terminal"
	"github.com/spf13/cobra"
)

var actionValueName = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)

// checkActionValueName rejects names that Gitea refuses for secrets and variables.
func checkActionValueName(name string) error {
	if !actionValueName.MatchString(name) {
		return clierr.Wrap(clierr.Usage, fmt.Errorf("invalid name %q: use letters, numbers and underscores, not starting with a number", name))
	}
	if upper := strings.ToUpper(name); upper == "CI" || strings.HasPrefix(upper, "GITEA_") || strings.HasPrefix(upper, "GITHUB_") {
		return clierr.Wrap(clierr.Usage, fmt.Errorf("invalid name %q: CI and names starting with GITEA_ or GITHUB_ are reserved", name))
	}
	return nil
}

// actionScopeFlags select where secrets and variables live: a repository by default, an
// organization with --org, or the user's account with --account.
type actionScopeFlags struct {
	org     string
	account bool
}

// register adds the scope flags to cmd; account adds --account, which secret list lacks.
func (f *actionScopeFlags) register(a *application, cmd *cobra.Command, kind string, account bool) {
	cmd.Flags().StringVarP(&f.org, "org", "o", "", "Use the "+kind+"s of an organization instead of a repository")
	_ = cmd.RegisterFlagCompletionFunc("org", a.completeOrgs())
	if account {
		cmd.Flags().BoolVar(&f.account, "account", false, "Use the "+kind+"s of your user account instead of a repository or organization")
	}
}

func (f *actionScopeFlags) scope() services.ActionScope {
	return services.ActionScope{Organization: f.org, Account: f.account}
}

// resolve selects the scope and the account that manages it.
func (f *actionScopeFlags) resolve(a *application, cmd *cobra.Command) (services.ActionScope, target, error) {
	if (f.org != "" && f.account) || ((f.org != "" || f.account) && a.options.repo != "") {
		return services.ActionScope{}, target{}, clierr.Wrap(clierr.Usage, errors.New("--org, --account and --repo select different scopes; pass one"))
	}
	t, err := a.resolve(cmd.Context(), f.org == "" && !f.account)
	if err != nil {
		return services.ActionScope{}, t, err
	}
	scope := f.scope()
	if f.org == "" && !f.account {
		scope.Repository = t.Repo
	}
	return scope, t, nil
}

func (a *application) actionValueCommand(secret bool) *cobra.Command {
	kind, use := "variable", "read with ${{ vars.NAME }}"
	if secret {
		kind, use = "secret", "read with ${{ secrets.NAME }}"
	}
	group := &cobra.Command{
		Use:   kind,
		Short: fmt.Sprintf("Manage Actions %ss for a repository, organization, or your account", kind),
		Long: fmt.Sprintf(`Manage the Actions %[1]ss that workflows %[2]s.

A %[1]s belongs to a repository, to an organization (--org), whose repositories all see it, or
to your user account (--account), which all your repositories see. Without --org or --account,
gt uses the repository of the working directory or --repo. Names are stored upper-cased.

Note that the global --user flag selects the account gt acts as, not a scope.`, kind, use),
	}
	group.AddCommand(a.actionValueListCommand(secret, kind), a.actionValueSetCommand(secret, kind), a.actionValueDeleteCommand(secret, kind))
	if !secret {
		group.AddCommand(a.variableViewCommand(), a.variableGetCommand())
	}
	return group
}

func (a *application) actionValueListCommand(secret bool, kind string) *cobra.Command {
	var scope actionScopeFlags
	var limit, page int
	example := `# list the variables of the repository
$ gt variable list
# list the variables of an organization
$ gt variable list --org acme`
	if secret {
		example = `# list the secrets of the repository
$ gt secret list
# list the secrets of an organization
$ gt secret list --org acme`
	}
	cmd := &cobra.Command{
		Use:     "list",
		Short:   "List " + kind + "s",
		Example: example,
		Args:    argsExact(0),
		RunE: func(cmd *cobra.Command, _ []string) error {
			selected, t, err := scope.resolve(a, cmd)
			if err != nil {
				return err
			}
			if secret {
				a.accountNote(cmd, t, "Gitea cannot list your account's secrets; set or delete them with --account")
			} else {
				a.accountNote(cmd, t, "for your account's variables, use --account")
			}
			where := " of " + selected.String()
			if selected.Repository != "" {
				where = " in " + selected.Repository
			}
			pages := listPage{Number: page}
			if secret {
				items, err := (services.ActionSecrets{Client: t.client, Scope: selected}).ListPage(cmd.Context(), limit, page)
				if err != nil {
					return err
				}
				pages.More, pages.Total, pages.Counted = items.More, items.Total, items.Counted
				spec := listSpec[services.ActionSecret]{Columns: secretColumns, Row: secretRow, Empty: "no secrets" + where, Context: "secrets" + where}
				return writeTable(a, cmd, items.Items, spec, pages)
			}
			items, err := (services.ActionVariables{Client: t.client, Scope: selected}).ListPage(cmd.Context(), limit, page)
			if err != nil {
				return err
			}
			pages.More, pages.Total, pages.Counted = items.More, items.Total, items.Counted
			spec := listSpec[services.ActionVariable]{Columns: variableColumns, Row: variableRow, Empty: "no variables" + where, Context: "variables" + where}
			return writeTable(a, cmd, items.Items, spec, pages)
		},
	}
	pageFlags(cmd, &limit, &page)
	scope.register(a, cmd, kind, !secret)
	return cmd
}

var (
	secretColumns   = []terminal.Column{{Header: "Name"}, {Header: "Created"}, {Header: "Description", Flex: true}}
	variableColumns = []terminal.Column{{Header: "Name"}, {Header: "Value", Flex: true}, {Header: "Description", Flex: true}}
)

func secretRow(item services.ActionSecret) []terminal.Cell {
	return []terminal.Cell{{Text: item.Name}, {Time: terminal.ParseTime(item.CreatedAt)}, {Text: item.Description}}
}

func variableRow(item services.ActionVariable) []terminal.Cell {
	return []terminal.Cell{{Text: item.Name}, {Text: item.Value}, {Text: item.Description}}
}

func (a *application) actionValueSetCommand(secret bool, kind string) *cobra.Command {
	var scope actionScopeFlags
	var body, bodyFile, envFile, description string
	example := `# set a repository variable
$ gt variable set ENVIRONMENT --body staging
# set an organization variable from a file
$ gt variable set CONFIG --org acme < config.json
# set the variables of a dotenv file
$ gt variable set --env-file .env`
	if secret {
		example = `# paste the value of a repository secret at a prompt
$ gt secret set DEPLOY_TOKEN
# set an organization secret from standard input
$ echo -n "$TOKEN" | gt secret set NPM_TOKEN --org acme
# set the secrets of a dotenv file
$ gt secret set --env-file .env`
	}
	cmd := &cobra.Command{
		Use:   "set [<name>]",
		Short: "Create or update a " + kind,
		Long: fmt.Sprintf(`Create or update a %[1]s.

The value comes from --body, --body-file or standard input when it is not a terminal (one
trailing line break is removed), or else from a prompt. --env-file sets every NAME=value of a
dotenv file instead.`, kind),
		Example: example,
		Args:    usageArgs(cobra.MaximumNArgs(1)),
		RunE: func(cmd *cobra.Command, args []string) error {
			if (len(args) == 1) == (envFile != "") {
				return clierr.Wrap(clierr.Usage, &argumentError{problem: "pass a " + kind + " name or --env-file", cmd: cmd})
			}
			if envFile != "" && (cmd.Flags().Changed("body") || cmd.Flags().Changed("body-file")) {
				return clierr.Wrap(clierr.Usage, errors.New("--env-file cannot be combined with --body or --body-file"))
			}
			var entries []envEntry
			if envFile != "" {
				data, err := readFileOrStdin(a.deps.Streams.In, envFile)
				if err != nil {
					return fmt.Errorf("read --env-file: %w", err)
				}
				if entries, err = parseEnvFile(data); err != nil {
					return clierr.Wrap(clierr.Usage, fmt.Errorf("%s: %w", envFile, err))
				}
			} else {
				value, err := a.actionValue(cmd, secret, args[0], body, bodyFile)
				if err != nil {
					return err
				}
				entries = []envEntry{{args[0], value}}
			}
			for _, entry := range entries {
				if err := checkActionValueName(entry.name); err != nil {
					return err
				}
				if entry.value == "" {
					return clierr.Wrap(clierr.Usage, fmt.Errorf("the value of %s is empty", entry.name))
				}
				if !secret && len(entry.value) > 65536 {
					return clierr.Wrap(clierr.Usage, fmt.Errorf("the value of %s exceeds 64 KiB", entry.name))
				}
			}
			selected, t, err := scope.resolve(a, cmd)
			if err != nil {
				return err
			}
			a.accountNote(cmd, t, "for a "+kind+" of your account, use --account")
			given := map[string]string{}
			for _, entry := range entries {
				given[services.ActionValueName(entry.name)] = entry.name
			}
			label := func(entry envEntry) string { return entry.name }
			report := func(name string) string {
				text := fmt.Sprintf("Set Actions %s %s for %s", kind, name, selected)
				if given[name] != name {
					text += " (names are upper-cased)"
				}
				return text
			}
			if secret {
				service := services.ActionSecrets{Client: t.client, Scope: selected}
				return batchDone(a, cmd, entries, label, func(entry envEntry) (services.ActionSecret, error) {
					return service.Set(cmd.Context(), entry.name, entry.value, description)
				}, func(item services.ActionSecret) string { return report(item.Name) })
			}
			service := services.ActionVariables{Client: t.client, Scope: selected}
			return batchDone(a, cmd, entries, label, func(entry envEntry) (services.ActionVariable, error) {
				return service.Set(cmd.Context(), entry.name, entry.value, description)
			}, func(item services.ActionVariable) string { return report(item.Name) })
		},
	}
	cmd.Flags().StringVarP(&body, "body", "b", "", "The value")
	cmd.Flags().StringVarP(&bodyFile, "body-file", "F", "", "Read the value from a file, or - for standard input")
	cmd.Flags().StringVarP(&envFile, "env-file", "f", "", "Set every NAME=value of a dotenv file, or - for standard input")
	cmd.Flags().StringVarP(&description, "description", "d", "", "Description")
	flagAliases(cmd, map[string]string{"value": "body", "value-file": "body-file"})
	scope.register(a, cmd, kind, true)
	cmd.ValidArgsFunction = a.completeActionValues(secret, &scope)
	return cmd
}

// actionValue returns the value to set: --body, --body-file, standard input when it is not a
// terminal, or else what the user types at a prompt.
func (a *application) actionValue(cmd *cobra.Command, secret bool, name, body, bodyFile string) (string, error) {
	if cmd.Flags().Changed("body") || cmd.Flags().Changed("body-file") {
		return a.readSecret(cmd, body, bodyFile, "body", "body-file")
	}
	missing := clierr.Wrap(clierr.Usage, errors.New("missing value: pass --body, --body-file or standard input"))
	if !a.deps.Streams.InTTY {
		data, err := io.ReadAll(a.deps.Streams.In)
		if err != nil {
			return "", fmt.Errorf("read standard input: %w", err)
		}
		if len(data) == 0 {
			return "", missing
		}
		return strings.TrimSuffix(strings.TrimSuffix(string(data), "\n"), "\r"), nil
	}
	if !a.canPrompt() {
		return "", missing
	}
	if secret {
		return a.deps.Prompter.SecretInput(cmd.Context(), "Paste the value of "+strings.ToUpper(name)+":")
	}
	return a.deps.Prompter.Ask(cmd.Context(), "Value of "+strings.ToUpper(name)+":", nil)
}

// envEntry is one NAME=value line of a dotenv file.
type envEntry struct{ name, value string }

// parseEnvFile reads NAME=value lines, skipping blank lines and # comments. An optional export
// prefix is ignored; values may be quoted, and double-quoted ones understand \n.
func parseEnvFile(data []byte) ([]envEntry, error) {
	var entries []envEntry
	scanner := bufio.NewScanner(bytes.NewReader(data))
	scanner.Buffer(nil, 1<<20)
	for number := 1; scanner.Scan(); number++ {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		name, value, ok := strings.Cut(strings.TrimPrefix(line, "export "), "=")
		name, value = strings.TrimSpace(name), strings.TrimSpace(value)
		if !ok || name == "" {
			return nil, fmt.Errorf("line %d: use NAME=value", number)
		}
		switch {
		case len(value) >= 2 && value[0] == '"' && value[len(value)-1] == '"':
			value = strings.ReplaceAll(value[1:len(value)-1], `\n`, "\n")
		case len(value) >= 2 && value[0] == '\'' && value[len(value)-1] == '\'':
			value = value[1 : len(value)-1]
		default:
			value, _, _ = strings.Cut(value, " #")
		}
		entries = append(entries, envEntry{name, value})
	}
	if len(entries) == 0 {
		return nil, errors.New("no NAME=value lines")
	}
	return entries, scanner.Err()
}

func (a *application) actionValueDeleteCommand(secret bool, kind string) *cobra.Command {
	var scope actionScopeFlags
	cmd := &cobra.Command{
		Use:   "delete <name>",
		Short: "Delete a " + kind,
		Example: fmt.Sprintf(`# delete a repository %[1]s
$ gt %[1]s delete DEPLOY_TOKEN
# delete an organization %[1]s without confirmation
$ gt %[1]s delete DEPLOY_TOKEN --org acme --yes`, kind),
		Args:        argsExact(1),
		Annotations: map[string]string{"gt.confirm": "true", "gt.text": kind + " deletion does not return a resource"},
		RunE: func(cmd *cobra.Command, args []string) error {
			name := services.ActionValueName(args[0])
			selected, t, err := scope.resolve(a, cmd)
			if err != nil {
				return err
			}
			if err := a.confirmAction(cmd, terminal.Confirmation{Prompt: fmt.Sprintf("Delete %s %s from %s?", kind, name, selected)}); err != nil {
				return err
			}
			if secret {
				err = services.ActionSecrets{Client: t.client, Scope: selected}.Delete(cmd.Context(), args[0])
			} else {
				err = services.ActionVariables{Client: t.client, Scope: selected}.Delete(cmd.Context(), args[0])
			}
			if err != nil {
				return err
			}
			a.done(cmd, "Deleted Actions %s %s from %s", kind, name, selected)
			return nil
		},
	}
	scope.register(a, cmd, kind, true)
	cmd.ValidArgsFunction = a.completeActionValues(secret, &scope)
	return cmd
}

func (a *application) variableViewCommand() *cobra.Command {
	var scope actionScopeFlags
	cmd := &cobra.Command{
		Use:   "view <name>",
		Short: "View a variable",
		Example: `# view a variable with its description
$ gt variable view ENVIRONMENT`,
		Args: argsExact(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			selected, t, err := scope.resolve(a, cmd)
			if err != nil {
				return err
			}
			item, err := services.ActionVariables{Client: t.client, Scope: selected}.View(cmd.Context(), args[0])
			if err != nil {
				return err
			}
			if a.options.json != "" {
				return a.writeJSON(cmd, item)
			}
			d := a.display()
			d.Markdown = false
			view := terminal.View{Title: item.Name, Fields: []terminal.Field{{Label: "Scope", Value: selected.String()}, {Label: "Description", Value: item.Description}}, Body: item.Value}
			return a.writeHuman(cmd, view.Render(d))
		},
	}
	scope.register(a, cmd, "variable", true)
	cmd.ValidArgsFunction = a.completeActionValues(false, &scope)
	return cmd
}

func (a *application) variableGetCommand() *cobra.Command {
	var scope actionScopeFlags
	cmd := &cobra.Command{
		Use:   "get <name>",
		Short: "Print the value of a variable",
		Example: `# use the value of a variable in a script
$ region=$(gt variable get REGION --org acme)`,
		Args: argsExact(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			selected, t, err := scope.resolve(a, cmd)
			if err != nil {
				return err
			}
			item, err := services.ActionVariables{Client: t.client, Scope: selected}.View(cmd.Context(), args[0])
			if err != nil {
				return err
			}
			if a.options.json != "" {
				return a.writeJSON(cmd, item)
			}
			return writeTerminalSafe(cmd.OutOrStdout(), a.deps.Streams.OutTTY, strings.NewReader(item.Value+"\n"))
		},
	}
	scope.register(a, cmd, "variable", true)
	cmd.ValidArgsFunction = a.completeActionValues(false, &scope)
	return cmd
}

// completeActionValues completes the secret or variable names of the scope the flags select.
func (a *application) completeActionValues(secret bool, scope *actionScopeFlags) cobra.CompletionFunc {
	names := a.completeVariables(scope.scope)
	if secret {
		names = a.completeSecrets(scope.scope)
	}
	return firstArg(names)
}
