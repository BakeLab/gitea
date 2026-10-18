// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package app

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"unicode"

	"gitea.dev/cli/internal/clierr"
	"gitea.dev/cli/internal/version"
	"github.com/spf13/cobra"
	"github.com/stretchr/testify/require"
)

func TestHelpWorksWithoutValidConfiguration(t *testing.T) {
	for _, args := range [][]string{{}, {"help"}, {"version"}, {"man"}, {"man", "pr", "checkout"}, {"completion", "bash"}, {"--help"}} {
		deps, out, _ := testDeps(t)
		require.NoError(t, os.WriteFile(filepath.Join(deps.Settings.Dir, "config.yml"), []byte("not: ["), 0o600))
		require.NoError(t, Execute(context.Background(), deps, args))
		require.NotEmpty(t, out.String())
	}
}

func TestAliasesAndConfigPersistence(t *testing.T) {
	deps, out, _ := testDeps(t)
	require.NoError(t, Execute(context.Background(), deps, []string{"alias", "set", "prefs", "config list"}))
	out.Reset()
	require.NoError(t, Execute(context.Background(), deps, []string{"prefs"}))
	require.Contains(t, out.String(), "git_protocol")
	require.NoError(t, Execute(context.Background(), deps, []string{"alias", "set", "preferences", "prefs"}))
	require.NoError(t, Execute(context.Background(), deps, []string{"config", "set", "prompt", "disabled"}))
	require.NoError(t, Execute(context.Background(), deps, []string{"alias", "delete", "preferences"}))
	err := Execute(context.Background(), deps, []string{"alias", "set", "config", "issue list"})
	require.Error(t, err)
	require.Equal(t, clierr.Usage, clierr.ExitCode(err))
	s, err := deps.Settings.Load()
	require.NoError(t, err)
	require.Equal(t, "disabled", s.Preferences.Prompt)
}

func TestBrokenAliasDoesNotBlockOtherCommands(t *testing.T) {
	deps, out, _ := testDeps(t)
	require.NoError(t, os.WriteFile(filepath.Join(deps.Settings.Dir, "config.yml"), []byte("version: 1\naliases:\n  status: config list\n  dangling: missing\n"), 0o600))
	require.NoError(t, Execute(context.Background(), deps, []string{"config", "list"}))
	require.Contains(t, out.String(), "git_protocol")
	err := Execute(context.Background(), deps, []string{"dangling"})
	require.Equal(t, clierr.Usage, clierr.ExitCode(err))
	require.NoError(t, Execute(context.Background(), deps, []string{"alias", "delete", "status"}))
	require.NoError(t, Execute(context.Background(), deps, []string{"alias", "delete", "dangling"}))
}

func TestCommandLineEdgeCases(t *testing.T) {
	deps, out, _ := testDeps(t)
	err := Execute(context.Background(), deps, []string{"issue", "bogus"})
	require.Equal(t, clierr.Usage, clierr.ExitCode(err))
	err = Execute(context.Background(), deps, []string{"isue"})
	require.Equal(t, clierr.Usage, clierr.ExitCode(err))
	require.ErrorContains(t, err, "did you mean issue?")
	// A help flag supplied as a flag value must not bypass validation.
	err = Execute(context.Background(), deps, []string{"config", "list", "--jq", ".[", "--remote", "-h"})
	require.ErrorContains(t, err, "invalid --jq expression")
	require.NoError(t, Execute(context.Background(), deps, []string{"--version"}))
	require.Equal(t, version.Format()+"\n", out.String())
}

func TestAliasesAfterGlobalFlagsAndNestedHelp(t *testing.T) {
	deps, out, _ := testDeps(t)
	require.NoError(t, Execute(context.Background(), deps, []string{"alias", "set", "prefs", "config list"}))
	out.Reset()
	require.NoError(t, Execute(context.Background(), deps, []string{"--no-input", "prefs"}))
	require.Contains(t, out.String(), "git_protocol")
	require.NoError(t, os.WriteFile(filepath.Join(deps.Settings.Dir, "config.yml"), []byte("broken: ["), 0o600))
	for _, args := range [][]string{{"issue", "--help"}, {"repo"}, {"auth", "login", "--help"}} {
		require.NoError(t, Execute(context.Background(), deps, args))
	}
}

func TestJoinJSONFields(t *testing.T) {
	for _, tc := range []struct{ args, want []string }{
		{[]string{"issue", "list", "--json", "number,title"}, []string{"issue", "list", "--json=number,title"}},
		{[]string{"--json", "id", "org", "list"}, []string{"--json=id", "org", "list"}},
		{[]string{"issue", "list", "--json"}, []string{"issue", "list", "--json"}},
		{[]string{"search", "repo", "foo", "--json"}, []string{"search", "repo", "foo", "--json"}},
		{[]string{"issue", "view", "--json", "5"}, []string{"issue", "view", "--json", "5"}},
		{[]string{"repo", "clone", "--json", "alice/project"}, []string{"repo", "clone", "--json", "alice/project"}},
		{[]string{"issue", "list", "--json", "--state", "open"}, []string{"issue", "list", "--json", "--state", "open"}},
		{[]string{"search", "issues", "--", "--json", "id"}, []string{"search", "issues", "--", "--json", "id"}},
		{[]string{"search", "repo", "--json", "gt"}, []string{"search", "repo", "--json", "gt"}},
		{[]string{"search", "issue", "--json", "bug"}, []string{"search", "issue", "--json", "bug"}},
		{[]string{"repo", "clone", "--json", "project"}, []string{"repo", "clone", "--json", "project"}},
		{[]string{"search", "repo", "--json", "name,url", "gt"}, []string{"search", "repo", "--json=name,url", "gt"}},
		// A leading --json keeps the command word that follows it.
		{[]string{"--json", "repo", "view", "alice/project"}, []string{"--json", "repo", "view", "alice/project"}},
		{[]string{"--json", "pr", "list"}, []string{"--json", "pr", "list"}},
		{[]string{"--json", "number", "my-alias"}, []string{"--json=number", "my-alias"}},
		// A word that cannot be an argument is a misspelled field, which then lists the available ones.
		{[]string{"issue", "list", "--json", "Number"}, []string{"issue", "list", "--json=Number"}},
		{[]string{"issue", "list", "--json", "number,titel"}, []string{"issue", "list", "--json=number,titel"}},
		{[]string{"run", "list", "--json", "workflow", "--limit", "5"}, []string{"run", "list", "--json=workflow", "--limit", "5"}},
		{[]string{"issue", "view", "--json", "titel"}, []string{"issue", "view", "--json", "titel"}},
		{[]string{"issue", "view", "5", "--json", "titel"}, []string{"issue", "view", "5", "--json=titel"}},
	} {
		deps, _, _ := testDeps(t)
		require.Equal(t, tc.want, joinJSONFields(NewRoot(deps), tc.args), tc.args)
	}
}

func TestJSONFieldsAcceptSpaceSeparatedValue(t *testing.T) {
	server := newAuthenticatedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v1/user/orgs":
			_, _ = w.Write([]byte(`[{"id":1,"username":"team","full_name":"Team"}]`))
		case "/api/v1/repos/search":
			require.Equal(t, "foo", r.URL.Query().Get("q"))
			_, _ = w.Write([]byte(`{"data":[{"id":2,"full_name":"alice/foo"}]}`))
		default:
			t.Errorf("unexpected request %s", r.URL.Path)
		}
	}))
	defer server.Close()
	deps, out, _ := serverDeps(t, server.URL)
	for _, args := range [][]string{{"org", "list", "--json", "id,name"}, {"org", "list", "--json=id,name"}, {"--json", "id,name", "org", "list"}} {
		out.Reset()
		require.NoError(t, Execute(context.Background(), deps, args), args)
		require.JSONEq(t, `[{"id":1,"name":"team"}]`, out.String(), args)
	}
	out.Reset()
	require.NoError(t, Execute(context.Background(), deps, []string{"org", "list", "--json"}))
	require.Contains(t, out.String(), `"full_name":"Team"`)
	out.Reset()
	require.NoError(t, Execute(context.Background(), deps, []string{"search", "repo", "foo", "--json"}))
	require.Contains(t, out.String(), `"full_name":"alice/foo"`)
	out.Reset()
	require.NoError(t, Execute(context.Background(), deps, []string{"search", "repo", "--json", "foo"}))
	require.Contains(t, out.String(), `"full_name":"alice/foo"`)
}

func TestSudoSendsHeaderAfterIdentityCheck(t *testing.T) {
	seen := []string{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seen = append(seen, r.URL.Path+" "+r.Header.Get("Sudo"))
		if r.URL.Path == "/api/v1/user" {
			_, _ = w.Write([]byte(`{"login":"alice"}`))
			return
		}
		_, _ = w.Write([]byte(`{"id":7,"full_name":"alice/project"}`))
	}))
	defer server.Close()
	deps, out, _ := serverDeps(t, server.URL)
	require.NoError(t, Execute(context.Background(), deps, []string{"--sudo", "bob", "repo", "view", "--repo", "alice/project", "--json=full_name"}))
	require.Equal(t, []string{"/api/v1/user ", "/api/v1/repos/alice/project bob"}, seen)
	require.JSONEq(t, `{"full_name":"alice/project"}`, out.String())
	requireUsageBeforeRequests(t, "--sudo cannot be used with auth commands", "auth", "status", "--sudo", "bob")
	requireUsageBeforeRequests(t, "--sudo requires a username", "repo", "view", "--sudo", "")
}

func TestJSONFieldErrorsListAvailableFields(t *testing.T) {
	requireUsageBeforeRequests(t, "unknown JSON field \"titel\"\navailable fields: assignees, author, body", "issue", "list", "--json", "number,titel")
	requireUsageBeforeRequests(t, "unknown JSON field \"workflw\"\navailable fields: actor, attempt, branch", "run", "list", "--json", "workflw")
	requireUsageBeforeRequests(t, "--json= needs field names; use --json alone for every field", "pr", "list", "--json=")
	requireUsageBeforeRequests(t, "version prints text; --jq is not supported", "version", "--jq", ".")
}

func TestJQImpliesJSONAndShorthands(t *testing.T) {
	server := newAuthenticatedServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`[{"id":1,"username":"team","full_name":"Team"}]`))
	}))
	defer server.Close()
	for _, args := range [][]string{{"org", "list", "--jq", ".[].name"}, {"org", "list", "-q", ".[].name"}, {"-q", ".[0].full_name", "org", "list"}} {
		deps, out, _ := serverDeps(t, server.URL)
		require.NoError(t, Execute(context.Background(), deps, args), args)
		require.Contains(t, []string{"team\n", "Team\n"}, out.String(), args)
	}
	deps, _, _ := testDeps(t)
	flags := NewRoot(deps).PersistentFlags()
	require.Equal(t, "yes", flags.ShorthandLookup("y").Name)
	require.Equal(t, "jq", flags.ShorthandLookup("q").Name)
}

func TestRawJSONCommandsKeepJQToThemselves(t *testing.T) {
	deps, out, _ := testDeps(t)
	root := NewRoot(deps)
	root.AddCommand(&cobra.Command{Use: "raw", Annotations: map[string]string{"gt.text": "raw prints text", "gt.rawjson": "true"}, RunE: func(cmd *cobra.Command, _ []string) error {
		_, err := fmt.Fprintf(cmd.OutOrStdout(), "json=%q", cmd.Flag("json").Value)
		return err
	}})
	root.SetArgs([]string{"raw", "--jq", ".x"})
	require.NoError(t, root.Execute())
	require.Equal(t, `json=""`, out.String())
	root.SetArgs([]string{"raw", "--json"})
	require.ErrorContains(t, root.Execute(), "raw prints text; --json is not supported")
}

func TestUnknownFlagsSuggestSimilarFlags(t *testing.T) {
	requireUsageBeforeRequests(t, "unknown flag: --lable; did you mean --label?", "issue", "list", "--lable", "bug")
	requireUsageBeforeRequests(t, "unknown flag: --stat; did you mean --state?", "issue", "list", "--stat", "open")
	requireUsageBeforeRequests(t, "unknown flag: --add-lable; did you mean --add-label?", "issue", "edit", "1", "--add-lable", "bug")
	deps, _, _ := testDeps(t)
	err := Execute(context.Background(), deps, []string{"issue", "list", "--zzzzzz"})
	require.Equal(t, clierr.Usage, clierr.ExitCode(err))
	require.EqualError(t, err, "unknown flag: --zzzzzz")
	for _, tc := range []struct {
		a, b     string
		distance int
	}{{"kitten", "sitting", 3}, {"", "abc", 3}, {"label", "label", 0}, {"lable", "label", 2}} {
		require.Equal(t, tc.distance, editDistance(tc.a, tc.b), tc)
	}
}

func TestRootHelpGroupsCommandsAndTopics(t *testing.T) {
	deps, out, _ := testDeps(t)
	require.NoError(t, Execute(context.Background(), deps, []string{"alias", "set", "co", "pr checkout"}))
	out.Reset()
	require.NoError(t, Execute(context.Background(), deps, []string{"--help"}))
	help := out.String()
	for _, want := range []string{
		"Work seamlessly with Gitea from the command line.\n\nUSAGE\n  gt <command> <subcommand> [flags]\n",
		"\nCORE COMMANDS\n  auth:          ",
		"\n  repo:          ",
		"\nACTIONS COMMANDS\n  run:           ",
		"\nADDITIONAL COMMANDS\n  admin:         ",
		"\n  completion:    Generate shell completion scripts\n",
		"\nALIAS COMMANDS\n  co:            Alias for \"pr checkout\"\n",
		"\nHELP TOPICS\n  environment:   ",
		"\nFLAGS\n  --help      Show help for command\n  --version   Show gt version\n",
		"\nEXAMPLES\n  $ gt issue create\n",
		"\nLEARN MORE\n  Use `gt <command> <subcommand> --help` for more information about a command.\n",
	} {
		require.Contains(t, help, want)
	}
	require.NotContains(t, help, "  help:")
	require.NotContains(t, help, "  man:")
	// Every visible top-level command belongs to one root help group.
	for _, cmd := range NewRoot(deps).Commands() {
		if cmd.IsAvailableCommand() {
			require.Contains(t, []string{"core", "actions", "additional"}, cmd.GroupID, cmd.Name())
		}
	}
}

func TestCommandHelpShowsRelevantFlagsOnly(t *testing.T) {
	deps, out, _ := testDeps(t)
	require.NoError(t, Execute(context.Background(), deps, []string{"issue", "list", "--help"}))
	help := out.String()
	for _, want := range []string{
		"\nUSAGE\n  gt issue list [flags]\n",
		"\nALIASES\n  gt issue ls\n",
		"\nFLAGS\n  -a, --assignee string ",
		"\nINHERITED FLAGS\n  -R, --repo OWNER/REPO ",
		"\n      --json fields ",
		"\n  -q, --jq expression ",
		"\n  -h, --help              Show help for command\n\n  " + globalFlagsLine + "\n",
		"\nJSON FIELDS\n  assignees, author, body, ",
	} {
		require.Contains(t, help, want)
	}
	for _, unwanted := range []string{"--sudo string", "--yes", `[="*"]`, "Global Flags:"} {
		require.NotContains(t, help, unwanted)
	}
	out.Reset()
	require.NoError(t, Execute(context.Background(), deps, []string{"label", "list", "--help"}))
	require.Contains(t, out.String(), "\nFLAGS\n  -L, --limit int          Maximum number of results (1..1000) (default 30)\n      --org organization ")
	for _, args := range [][]string{{"alias", "list", "--help"}, {"auth", "status", "--help"}, {"version", "--help"}} {
		out.Reset()
		require.NoError(t, Execute(context.Background(), deps, args))
		require.NotContains(t, out.String(), "--repo", args)
		require.Contains(t, out.String(), globalFlagsLine, args)
	}
	require.NotContains(t, out.String(), "--json fields", "version prints text")
	out.Reset()
	require.NoError(t, Execute(context.Background(), deps, []string{"issue", "--help"}))
	require.Contains(t, out.String(), "\nUSAGE\n  gt issue <command> [flags]\n\nAVAILABLE COMMANDS\n  attachment:  ")
	require.NotContains(t, out.String(), "JSON FIELDS")
}

func TestHelpConfirmAndArgumentsAnnotations(t *testing.T) {
	deps, out, _ := testDeps(t)
	root := NewRoot(deps)
	issue, _, err := root.Find([]string{"issue"})
	require.NoError(t, err)
	issue.AddCommand(&cobra.Command{Use: "drop <number>", Short: "Drop an issue", Example: "# Drop issue 7\n$ gt issue drop 7", Annotations: map[string]string{"gt.confirm": "true", "gt.arguments": "An issue is a number or a URL."}, RunE: func(*cobra.Command, []string) error { return nil }})
	root.SetArgs([]string{"issue", "drop", "--help"})
	require.NoError(t, root.Execute())
	require.Contains(t, out.String(), "\n  -y, --yes ")
	require.Contains(t, out.String(), "\nARGUMENTS\n  An issue is a number or a URL.\n\nJSON FIELDS\n")
	require.Contains(t, out.String(), "\nEXAMPLES\n  # Drop issue 7\n  $ gt issue drop 7\n")
}

func TestHelpTopicsAndUnknownTopics(t *testing.T) {
	deps, out, _ := testDeps(t)
	for args, want := range map[string]string{
		"help environment": "  GITEA_CLI_CONFIG_DIR ",
		"help exit-codes":  "  4    authentication",
		"help formatting":  "--jq runs inside gt",
		"help issue list":  "\nUSAGE\n  gt issue list [flags]\n",
		"environment":      "  -R, --repo OWNER/REPO ",
	} {
		out.Reset()
		require.NoError(t, Execute(context.Background(), deps, strings.Fields(args)), args)
		require.Contains(t, out.String(), want, args)
	}
	for args, want := range map[string]string{
		"help nope":       `unknown help topic "nope"; run gt help to list commands and topics`,
		"help isue":       `unknown help topic "isue"; did you mean issue?`,
		"help isue list":  `unknown help topic "isue list"; did you mean issue?`,
		"help env":        `unknown help topic "env"; did you mean environment?`,
		"help issue nope": `unknown help topic "issue nope"; run gt help to list commands and topics`,
	} {
		out.Reset()
		err := Execute(context.Background(), deps, strings.Fields(args))
		require.Equal(t, clierr.Usage, clierr.ExitCode(err), args)
		require.EqualError(t, err, want, args)
		require.Empty(t, out.String(), args)
	}
	values, _ := complete(t, deps, out, "help", "e")
	require.Equal(t, []string{"environment\tEnvironment variables and global flags", "exit-codes\tExit codes used by gt"}, values)
	values, _ = complete(t, deps, out, "help", "issue", "l")
	require.Equal(t, []string{"list\tList issues in a repository"}, values)
}

func TestVerbAndFlagAliases(t *testing.T) {
	deps, _, _ := testDeps(t)
	root := NewRoot(deps)
	for _, args := range [][]string{{"issue", "ls"}, {"label", "rm"}, {"pr", "review", "ls"}} {
		cmd, _, err := root.Find(args)
		require.NoError(t, err)
		require.Equal(t, map[string]string{"ls": "list", "rm": "delete"}[args[len(args)-1]], cmd.Name(), args)
	}
	// Aliases never shadow a sibling command.
	walkCommands(root, func(cmd *cobra.Command) {
		names := map[string]string{}
		for _, child := range cmd.Commands() {
			for _, name := range append([]string{child.Name()}, child.Aliases...) {
				require.Empty(t, names[name], "%s %s is both %s and %s", cmd.CommandPath(), name, names[name], child.Name())
				names[name] = child.Name()
			}
		}
	})
	var labels []string
	edit := &cobra.Command{Use: "edit", RunE: func(*cobra.Command, []string) error { return nil }}
	edit.Flags().StringSliceVar(&labels, "add-label", nil, "Add labels")
	flagAliases(edit, map[string]string{"add-labels": "add-label"})
	edit.SetArgs([]string{"--add-labels", "bug", "--add-label", "docs"})
	require.NoError(t, edit.Execute())
	require.Equal(t, []string{"bug", "docs"}, labels)
	require.NotContains(t, edit.UsageString(), "add-labels")
}

// TestHelpTextsAreComplete checks the help texts of every command: a Short line without a final period,
// a capitalized hyphenated verb ("Mark-read …") or a sibling's wording, an Example on every command and a
// Long text on every command group.
func TestHelpTextsAreComplete(t *testing.T) {
	deps, _, _ := testDeps(t)
	walkCommands(NewRoot(deps), func(cmd *cobra.Command) {
		if !cmd.HasParent() || !cmd.IsAvailableCommand() {
			return
		}
		path := strings.TrimPrefix(cmd.CommandPath(), "gt ")
		generated := strings.Contains(cmd.Name(), "-") && strings.HasPrefix(cmd.Short, titleCase(cmd.Name())+" ")
		siblings := slices.ContainsFunc(cmd.Parent().Commands(), func(other *cobra.Command) bool { return other != cmd && other.Short == cmd.Short })
		if cmd.Short == "" || strings.HasSuffix(cmd.Short, ".") || unicode.IsLower(rune(cmd.Short[0])) || generated || siblings {
			t.Errorf("%s needs a clear Short", path)
		}
		group := cmd.Annotations["gt.group"] != ""
		if cmd.Example == "" && !group {
			t.Errorf("%s needs an Example", path)
		}
		if cmd.Long == "" && group {
			t.Errorf("%s needs a Long text", path)
		}
	})
}

// walkCommands calls visit for cmd and each of its descendants.
func walkCommands(cmd *cobra.Command, visit func(*cobra.Command)) {
	visit(cmd)
	for _, child := range cmd.Commands() {
		walkCommands(child, visit)
	}
}
