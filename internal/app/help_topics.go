// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package app

import (
	"cmp"
	"fmt"
	"io"
	"slices"
	"strings"

	"gitea.dev/cli/internal/clierr"
	"gitea.dev/cli/internal/terminal"
	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
)

// Help pages use gh-style sections: USAGE, the command lists, FLAGS, INHERITED FLAGS, ARGUMENTS,
// JSON FIELDS, EXAMPLES and LEARN MORE. Commands document themselves through these fields:
//
//   - Short: one line, a capitalized verb phrase without a final period ("List issues").
//   - Long: optional paragraphs shown instead of Short on the command's own page.
//   - Example: unindented lines; "$ gt …" for commands and "# …" for comments above them.
//   - Annotations["gt.arguments"]: the ARGUMENTS section, describing accepted argument formats.
//   - Annotations["gt.confirm"]: "true" when the command asks for confirmation, which shows --yes.
//   - Annotations["gt.jsonfields"]: the JSON FIELDS section of a command whose fields vary.

const (
	// topicAnnotation marks a hidden command that only holds a help topic.
	topicAnnotation = "gt.topic"
	// globalFlagsLine names the global flags that every help page leaves out of its flag lists.
	globalFlagsLine = "Global flags: --host, --user, --sudo, --no-input, --interactive, --debug (see gt help environment)"
)

// noRepoCommands are command paths, with their subcommands, that never select a repository, so their
// help omits --repo, --remote and --cwd.
var noRepoCommands = []string{
	"gt admin", "gt alias", "gt auth", "gt completion", "gt config", "gt help", "gt man", "gt org",
	"gt repo create", "gt repo list", "gt repo migrate", "gt search repo", "gt ssh-key", "gt team", "gt version",
}

// writeHelp prints the help page of cmd.
func (a *application) writeHelp(cmd *cobra.Command, _ []string) {
	if cmd.Annotations[topicAnnotation] != "" {
		_, _ = fmt.Fprintln(cmd.OutOrStdout(), strings.TrimSpace(cmd.Long))
		return
	}
	color := a.deps.Streams.OutTTY && a.colorEnabled()
	var page strings.Builder
	section := func(title, body string) {
		if body = strings.Trim(body, "\n"); body != "" {
			fmt.Fprintf(&page, "%s\n%s\n\n", terminal.Emphasis(title, color), indent(body))
		}
	}
	if description := cmp.Or(cmd.Long, cmd.Short); description != "" {
		page.WriteString(description + "\n\n")
	}
	section("USAGE", usageLines(cmd))
	section("ALIASES", aliasLines(cmd))
	commands := availableCommands(cmd)
	width := 0
	for _, children := range commands {
		for _, child := range children.commands {
			width = max(width, len(child.Name())+3)
		}
	}
	for _, children := range commands {
		var lines strings.Builder
		for _, child := range children.commands {
			fmt.Fprintf(&lines, "%-*s%s\n", width, child.Name()+":", child.Short)
		}
		section(children.title, lines.String())
	}
	if !cmd.HasParent() {
		section("FLAGS", "--help      Show help for command\n--version   Show gt version")
	} else {
		local, inherited := helpFlags(cmd)
		section("FLAGS", local)
		section("INHERITED FLAGS", inherited+"\n\n"+globalFlagsLine)
	}
	section("ARGUMENTS", cmd.Annotations["gt.arguments"])
	if outputsJSON(cmd) {
		section("JSON FIELDS", cmp.Or(cmd.Annotations["gt.jsonfields"], wrap(strings.Join(commandOutputFields(cmd), ", "), 78)))
	}
	if !cmd.HasParent() {
		section("EXAMPLES", "$ gt issue create\n$ gt issue list --label bug\n$ gt pr checkout 13\n$ gt repo clone owner/name")
	} else {
		section("EXAMPLES", cmd.Example)
	}
	section("LEARN MORE", "Use `gt <command> <subcommand> --help` for more information about a command.\n"+
		"Read `gt help environment` for global flags and environment variables.\n"+
		"Read `gt help formatting` for --json and --jq, and `gt help exit-codes` for exit codes.")
	_, _ = io.WriteString(cmd.OutOrStdout(), strings.TrimRight(page.String(), "\n")+"\n")
}

// indent indents every non-empty line of text by two spaces.
func indent(text string) string {
	lines := strings.Split(text, "\n")
	for i, line := range lines {
		if line != "" {
			lines[i] = "  " + line
		}
	}
	return strings.Join(lines, "\n")
}

// usageLines shows how to run cmd; command groups take a subcommand.
func usageLines(cmd *cobra.Command) string {
	switch {
	case !cmd.HasParent():
		return "gt <command> <subcommand> [flags]"
	case cmd.Annotations["gt.group"] != "":
		return cmd.CommandPath() + " <command> [flags]"
	case cmd.HasAvailableSubCommands():
		return cmd.UseLine() + "\n" + cmd.CommandPath() + " <command> [flags]"
	}
	return cmd.UseLine()
}

// aliasLines shows the other names of cmd.
func aliasLines(cmd *cobra.Command) string {
	lines := make([]string, 0, len(cmd.Aliases))
	for _, alias := range cmd.Aliases {
		lines = append(lines, cmd.Parent().CommandPath()+" "+alias)
	}
	return strings.Join(lines, "\n")
}

// commandList is one titled list of commands on a help page.
type commandList struct {
	title    string
	commands []*cobra.Command
}

// availableCommands lists the subcommands of cmd by group, the ungrouped ones, then help topics.
func availableCommands(cmd *cobra.Command) []commandList {
	var lists []commandList
	for _, group := range cmd.Groups() {
		lists = append(lists, commandList{title: strings.ToUpper(group.Title)})
	}
	other, topics := commandList{title: "AVAILABLE COMMANDS"}, commandList{title: "HELP TOPICS"}
	for _, child := range cmd.Commands() {
		switch at := slices.IndexFunc(cmd.Groups(), func(g *cobra.Group) bool { return g.ID == child.GroupID }); {
		case child.Annotations[topicAnnotation] != "":
			topics.commands = append(topics.commands, child)
		case !child.IsAvailableCommand():
		case at >= 0:
			lists[at].commands = append(lists[at].commands, child)
		default:
			other.commands = append(other.commands, child)
		}
	}
	return append(lists, other, topics)
}

// helpFlags renders the flags of cmd, counting parents' persistent flags such as --org as its own, and
// the global flags that apply to it: --repo, --remote and --cwd, --json and --jq, and --yes.
func helpFlags(cmd *cobra.Command) (local, inherited string) {
	globals := cmd.Root().PersistentFlags()
	var own []*pflag.Flag
	for _, flags := range []*pflag.FlagSet{cmd.LocalFlags(), cmd.InheritedFlags()} {
		flags.VisitAll(func(flag *pflag.Flag) {
			if flag.Name != "help" && globals.Lookup(flag.Name) != flag {
				own = append(own, flag)
			}
		})
	}
	slices.SortFunc(own, func(x, y *pflag.Flag) int { return strings.Compare(x.Name, y.Name) })
	path := cmd.CommandPath()
	repo := !slices.ContainsFunc(noRepoCommands, func(prefix string) bool { return path == prefix || strings.HasPrefix(path, prefix+" ") })
	// A command's own --remote, as in repo fork, hides the global flag of that name.
	// repo clone takes its repository as an argument only, so --repo and --remote do nothing there.
	selects := repo && path != "gt repo clone"
	shown := map[string]bool{"repo": selects, "remote": selects && cmd.LocalFlags().Lookup("remote") == nil, "cwd": repo, "json": outputsJSON(cmd), "jq": outputsJSON(cmd) || cmd.Annotations["gt.rawjson"] != "", "yes": cmd.Annotations["gt.confirm"] != ""}
	var shared []*pflag.Flag
	for _, name := range []string{"repo", "remote", "cwd", "json", "jq", "yes"} {
		if shown[name] {
			shared = append(shared, globals.Lookup(name))
		}
	}
	if help := cmd.Flags().Lookup("help"); help != nil {
		shared = append(shared, help)
	}
	return flagUsages(own), flagUsages(shared)
}

// outputsJSON reports whether the --json flag applies to cmd.
func outputsJSON(cmd *cobra.Command) bool {
	return cmd.Annotations["gt.text"] == "" && cmd.Annotations["gt.group"] == "" && (commandOutputFields(cmd) != nil || cmd.Annotations["gt.jsonfields"] != "")
}

// flagUsages renders flags in order as aligned usage lines without their common indentation. The
// optional --json value and the help flag read as in gh: "--json fields", "Show help for command".
func flagUsages(flags []*pflag.Flag) string {
	set := pflag.NewFlagSet("help", pflag.ContinueOnError)
	set.SortFlags = false
	for _, flag := range flags {
		shown := *flag
		switch flag.Name {
		case "json":
			shown.NoOptDefVal = ""
		case "help":
			shown.Usage = "Show help for command"
		}
		set.AddFlag(&shown)
	}
	lines := strings.Split(strings.TrimRight(set.FlagUsages(), "\n"), "\n")
	common := -1
	for _, line := range lines {
		if strings.TrimSpace(line) != "" {
			spaces := len(line) - len(strings.TrimLeft(line, " "))
			if common < 0 || spaces < common {
				common = spaces
			}
		}
	}
	for i, line := range lines {
		lines[i] = line[min(max(common, 0), len(line)):]
	}
	return strings.Join(lines, "\n")
}

// wrap breaks text at spaces into lines of at most width characters.
func wrap(text string, width int) string {
	var lines []string
	line := ""
	for _, word := range strings.Fields(text) {
		if line != "" && len(line)+1+len(word) > width {
			lines, line = append(lines, line), ""
		}
		line = strings.TrimPrefix(line+" "+word, " ")
	}
	return strings.Join(append(lines, line), "\n")
}

// helpCommand prints the help of a command or topic, and rejects unknown ones.
func (a *application) helpCommand() *cobra.Command {
	return &cobra.Command{
		Use: "help [command...]", Short: "Show help for a command or topic",
		Annotations: textOutput("help prints text"),
		ValidArgsFunction: func(cmd *cobra.Command, args []string, toComplete string) ([]cobra.Completion, cobra.ShellCompDirective) {
			target, _, err := cmd.Root().Find(args)
			if err != nil {
				return nil, cobra.ShellCompDirectiveNoFileComp
			}
			var names []cobra.Completion
			for _, child := range target.Commands() {
				if (child.IsAvailableCommand() || child.Annotations[topicAnnotation] != "") && strings.HasPrefix(child.Name(), toComplete) {
					names = append(names, cobra.CompletionWithDesc(child.Name(), child.Short))
				}
			}
			return names, cobra.ShellCompDirectiveNoFileComp
		},
		RunE: func(cmd *cobra.Command, args []string) error {
			target, rest, err := cmd.Root().Find(args)
			if err == nil && (len(rest) == 0 || !target.HasSubCommands()) {
				target.InitDefaultHelpFlag()
				return target.Help()
			}
			if len(rest) == 0 {
				rest = args[len(args)-1:]
			}
			return unknownHelpTopic(target, args, rest[0])
		},
	}
}

// unknownHelpTopic reports a help request for a command or topic that does not exist; name is the
// first word that parent does not know.
func unknownHelpTopic(parent *cobra.Command, args []string, name string) error {
	err := fmt.Errorf("unknown help topic %q", strings.Join(args, " "))
	suggestions := parent.SuggestionsFor(name)
	for _, child := range parent.Commands() {
		if child.Annotations[topicAnnotation] != "" && strings.HasPrefix(child.Name(), name) {
			suggestions = append(suggestions, child.Name())
		}
	}
	if len(suggestions) > 0 {
		err = fmt.Errorf("%w; did you mean %s?", err, strings.Join(suggestions, " or "))
	} else {
		err = fmt.Errorf("%w; run gt help to list commands and topics", err)
	}
	return clierr.Wrap(clierr.Usage, err)
}

// helpTopics returns the help topic commands; globals are the flags that every command accepts.
func helpTopics(globals *pflag.FlagSet) []*cobra.Command {
	var flags []*pflag.Flag
	globals.VisitAll(func(flag *pflag.Flag) { flags = append(flags, flag) })
	topics := []*cobra.Command{
		{Use: "environment", Short: "Environment variables and global flags", Long: `Global flags work with every command, before or after the command name:

` + indent(flagUsages(flags)) + `

gt selects the repository from --repo, GITEA_CLI_REPO, git config gt.repository (gt repo
set-default) or a Git remote (--remote, else the base remote pr checkout recorded, the
current branch's remote, upstream or origin, skipping remotes on instances without an
account, or not on --host or the GITEA_TOKEN instance). Like gh, a clone with upstream
and origin targets upstream: a branch tracking origin does not select the fork.

gt selects the instance from the repository URL, --host, GITEA_CLI_HOST or
GITEA_INSTANCE_URL, default_host, the only instance with an account, or, among several or
none, the instance of the clone's repository.

Environment variables:

  GITEA_CLI_CONFIG_DIR                configuration directory; gt config path prints the files
  GITEA_CLI_HOST, GITEA_INSTANCE_URL  instance URL, overriding default_host
  GITEA_TOKEN                         access token for that instance instead of stored credentials
  GITEA_INSTANCE_INSECURE             with GITEA_TOKEN, skip TLS verification when true
  GITEA_INSTANCE_SSH_HOST             with GITEA_TOKEN, SSH host that identifies the instance's remotes
  GITEA_CLI_USER                      account on the selected instance, like --user
  GITEA_CLI_REPO                      repository as OWNER/REPO or URL, like --repo
  GITEA_CLI_EDITOR                    editor, overriding config.yml
  GITEA_CLI_PAGER                     pager, overriding config.yml
  GITEA_CLI_COLOR                     color preference: auto, always or never
  GITEA_CLI_HTTP_TIMEOUT              longest wait for an answer, overriding config.yml
  NO_COLOR                            disables automatic color when set, even with CLICOLOR_FORCE
  CLICOLOR_FORCE                      unless empty or 0, colors stderr, even when redirected, and
                                      a terminal stdout despite CLICOLOR=0 or TERM=dumb
  CLICOLOR                            disables automatic color on a terminal when 0
  VISUAL, EDITOR, PAGER, BROWSER      editor (VISUAL first), pager and browser when config.yml sets none
  TERM                                dumb asks each prompt as a line of text, for screen readers,
                                      and turns off automatic color

Piped or redirected stdout is data: the color settings never color it. Only a
command's own --color always does, such as gt pr diff --color always.

Preferences in config.yml are described in gt config --help.`},
		{Use: "exit-codes", Short: "Exit codes used by gt", Long: `gt exits with one of these codes:

  0    success, including an empty list
  1    the command failed
  2    usage error: an unknown command or flag, or a missing or invalid argument
  3    canceled: a declined confirmation, a prompt left with Esc (or Ctrl-D with TERM=dumb),
       or a timeout
  4    authentication is missing or was rejected
  8    gt pr checks: some checks are still pending
  130  interrupted with Ctrl-C, also at a prompt

A command may document further codes in its help.`},
		{Use: "formatting", Short: "JSON output with --json and --jq", Long: `gt prints text for people by default. Scripts should use JSON: the columns and
wording of text output may change between releases.

  --json             print every field
  --json FIELDS      print only the comma-separated FIELDS, such as --json number,title
  -q, --jq EXPR      filter the JSON with a jq expression; implies --json

The JSON FIELDS section of a command's help lists its fields, and shell completion
offers them after --json. An unknown field reports the available ones.

--jq runs inside gt, so jq does not need to be installed. String results print
without quotes:

  $ gt issue list --json number,title --jq '.[] | "\(.number)\t\(.title)"'

Some fields cost extra requests, such as the reviewers of pr list, the blocked_by and blocks
of issue view and issue edit, and the visibility of org member list. gt makes them only when
the output includes the field: --json names it, or --json selects every field and the --jq
expression names it. An expression that reaches such a field without naming it, as
to_entries does, sees it empty; name the field in --json instead.

On a terminal, JSON is indented and colored; otherwise each value prints on one line.
Commands that return no resource, such as deletes, reject --json.`},
	}
	for _, topic := range topics {
		topic.Hidden, topic.Annotations = true, map[string]string{topicAnnotation: "true"}
	}
	return topics
}
