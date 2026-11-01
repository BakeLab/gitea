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
	// extensionFlagsLine replaces globalFlagsLine for commands that run an extension, which take
	// only the global flags of extensionFlags.
	extensionFlagsLine = "Global flags: --host, --user, --no-input, --yes; global flags go before the extension's name (see gt help extensions)"
)

// noRepoCommands are command paths, with their subcommands, that never select a repository, so their
// help omits --repo, --remote and --cwd.
var noRepoCommands = []string{
	"gt admin", "gt alias", "gt auth", "gt completion", "gt config", "gt help", "gt man", "gt org",
	"gt repo create", "gt repo list", "gt repo migrate", "gt search repo", "gt ssh-key", "gt team", "gt upgrade", "gt version",
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
		globals := globalFlagsLine
		if runsExtension(cmd) {
			globals = extensionFlagsLine
		}
		section("INHERITED FLAGS", inherited+"\n\n"+globals)
	}
	for _, extra := range extraSections(cmd) {
		section(extra.title, extra.body)
	}
	if !cmd.HasParent() {
		section("EXAMPLES", "$ gt issue create\n$ gt issue list --label bug\n$ gt pr checkout 13\n$ gt repo clone owner/name")
	} else {
		section("EXAMPLES", cmd.Example)
	}
	section("LEARN MORE", "Use `gt <command> <subcommand> --help` for more information about a command.\n"+
		"Read `gt help environment` for global flags and environment variables.\n"+
		"Read `gt help formatting` for --json, --jq and --csv, and `gt help exit-codes` for exit codes.")
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
// the global flags that apply to it: --repo, --remote and --cwd, --json, --jq and --csv, and --yes.
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
	shown := sharedFlags(cmd)
	var shared []*pflag.Flag
	for _, name := range sharedFlagNames {
		if shown[name] {
			shared = append(shared, globals.Lookup(name))
		}
	}
	if help := cmd.Flags().Lookup("help"); help != nil {
		shared = append(shared, help)
	}
	return flagUsages(own), flagUsages(shared)
}

// sharedFlagNames are the global flags that help and the manual list only where they apply.
var sharedFlagNames = []string{"repo", "remote", "cwd", "json", "jq", "csv", "yes"}

// sharedFlags reports which of sharedFlagNames apply to cmd.
func sharedFlags(cmd *cobra.Command) map[string]bool {
	path := cmd.CommandPath()
	repo := !slices.ContainsFunc(noRepoCommands, func(prefix string) bool { return path == prefix || strings.HasPrefix(path, prefix+" ") })
	// repo clone takes its repository as an argument only, so --repo and --remote do nothing there.
	// Among gt extension's commands, only those running an extension take a repository.
	selects := repo && path != "gt repo clone" && (!strings.HasPrefix(path, "gt extension") || runsExtension(cmd))
	return map[string]bool{"repo": selects, "remote": selects && !runsExtension(cmd), "cwd": repo, "json": outputsJSON(cmd), "jq": outputsJSON(cmd) || cmd.Annotations["gt.rawjson"] != "", "csv": outputsJSON(cmd), "yes": cmd.Annotations["gt.confirm"] != ""}
}

// helpSection is a titled section of a help page.
type helpSection struct{ title, body string }

// extraSections are the ARGUMENTS and JSON FIELDS sections of cmd, which the manual shows too.
func extraSections(cmd *cobra.Command) []helpSection {
	var sections []helpSection
	if arguments := cmd.Annotations["gt.arguments"]; arguments != "" {
		sections = append(sections, helpSection{"ARGUMENTS", arguments})
	}
	if outputsJSON(cmd) {
		sections = append(sections, helpSection{"JSON FIELDS", cmp.Or(cmd.Annotations["gt.jsonfields"], wrap(strings.Join(commandOutputFields(cmd), ", "), 78))})
	}
	return sections
}

// runsExtension reports whether cmd runs an extension.
func runsExtension(cmd *cobra.Command) bool {
	return cmd.Annotations["gt.extension"] == "true" || cmd.CommandPath() == "gt extension exec"
}

// outputsJSON reports whether the --json and --csv flags apply to cmd.
func outputsJSON(cmd *cobra.Command) bool {
	return cmd.Annotations["gt.text"] == "" && cmd.Annotations["gt.group"] == "" && (commandOutputFields(cmd) != nil || cmd.Annotations["gt.jsonfields"] != "")
}

// flagUsages renders flags in order as aligned usage lines without their common indentation. The
// optional --json and --csv values and the help flag read as in gh: "--json fields", "Show help for command".
func flagUsages(flags []*pflag.Flag) string {
	set := pflag.NewFlagSet("help", pflag.ContinueOnError)
	set.SortFlags = false
	for _, flag := range flags {
		shown := *flag
		switch flag.Name {
		case "json", "csv":
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
		{Use: "environment", Short: "Environment variables and global flags", Long: `Global flags work with every built-in command, before or after the command name. An
extension takes only --host, --repo, --user, --cwd, --no-input and --yes, before its name
(see gt help extensions):

` + indent(flagUsages(flags)) + `

gt selects the repository from --repo, GITEA_CLI_REPO, the Git remote --remote, git config
gt.repository (gt repo set-default) or a Git remote (the remote preference, the base remote pr
checkout recorded, the current branch's remote, upstream or origin, skipping remotes on
instances without an account, or not on --host or the GITEA_TOKEN instance). Like gh,
a clone with upstream and origin targets upstream: a branch tracking origin
does not select the fork.

gt selects the instance from the repository URL, --host, GITEA_CLI_HOST or
GITEA_INSTANCE_URL, default_host, the only instance with an account, or, among several or
none, the instance of the clone's repository. gt repo clone --find-instance looks up a
repository named without an instance on each instance with an account.

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
  GITEA_CLI_DATA_DIR                  data directory; installed extensions are in its extensions/
  GITEA_CLI_NO_EXTENSIONS             when set, turns extensions off
  GITEA_CLI_NO_UPDATE_NOTIFIER        when set, gt does not check for a new release (gt upgrade --help)
  GITEA_CLI_DOWNLOAD_URL              where gt upgrade downloads releases from
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
  1    the command failed, also when the instance did not respond within http_timeout
  2    usage error: an unknown command or flag, or a missing or invalid argument
  3    canceled: a declined confirmation, a prompt left with Esc (or Ctrl-D with TERM=dumb),
       a wait that ran out of its --timeout, such as gt run watch or gt pr checks --watch,
       or a browser authorization of gt auth login not completed within 2 minutes
  4    authentication is missing or was rejected
  5    gt run watch and gt workflow run --follow: the run completed without success
  6    gt run watch --expect-head: the run is for another commit
  8    gt pr checks: some checks are still pending
  130  interrupted with Ctrl-C, also at a prompt

A command may document further codes in its help; an extension exits with its own code.`},
		{Use: "extensions", Short: "How gt runs extensions, and what they may do", Long: `An extension is an executable named gt-NAME, or gh-NAME for a GitHub CLI extension, that
gt extension install puts in the extensions directory of $GITEA_CLI_DATA_DIR, or of gitea-cli
under the XDG data directory, and gt NAME runs. Built-in commands always win over an extension.

Installing: gt installs the release asset NAME-OS-ARCH or REPO-OS-ARCH (.exe on Windows) of the
latest release, else one whose name ends in OS-ARCH, or else clones the repository, which must
hold an executable gt-NAME at its root. It checks an asset against the SHA-256 GitHub recorded
at upload, else against the release's checksums.txt, in sha256sum format; without
either it asks with a default of no, or installs with --yes. An optional gt-extension.yml in
the repository declares:

  description      what the extension does, shown in gt help
  min_gt_version   the oldest gt that runs it
  platforms        such as linux/amd64 or darwin; none means every platform
  credentials      true when it needs your Gitea token
  completion       cobra, when it answers Cobra's __complete protocol for shell completion
  aliases          top-level commands that run the extension with fixed arguments: with
                   "user-group: user-group", gt user-group runs gt NAME user-group. Built-in
                   commands, extension names and your own aliases win over them, and of two
                   extensions the first by name does; gt warns about an alias it ignores and
                   installs the extension anyway.

Running: gt checks the executable against the SHA-256 recorded at install and refuses one that
changed. The extension inherits gt's terminal and environment, except for GITEA_TOKEN,
GITEA_INSTANCE_*, GH_TOKEN, GITHUB_TOKEN, GH_ENTERPRISE_TOKEN, GITHUB_ENTERPRISE_TOKEN and
GH_HOST. It gets:

  GITEA_CLI_EXTENSION          its name
  GITEA_CLI_EXECUTABLE         the path of gt, to call gt commands such as gt api
  GITEA_CLI_HOST, _REPO, _USER  from --host, --repo and --user before its name
  GITEA_TOKEN, GITEA_INSTANCE_URL  your token and its instance, only when you allowed it at
                                   install; gh extensions also get GH_HOST, GH_TOKEN and
                                   GH_ENTERPRISE_TOKEN

Global flags go before the extension's name; everything after it is the extension's own.
--cwd sets its working directory, and --no-input and --yes are accepted but not passed on;
other global flags, such as --json, are refused.

gt itself refuses your stored credentials to an extension that was not given them. This is
hygiene, not a sandbox: an extension runs with your privileges, can read gt's configuration
directory, and can change its own files and manifest. The digest check notices a corrupted or
replaced executable, not a deliberate change: for a script extension it covers the gt-NAME
script, not other files of its clone. Install extensions only from sources you trust.

Upgrading: gt extension upgrade shows the old and new version and digest first, refuses a
repository that was transferred or recreated and a release tag whose asset changed, and never
upgrades in the background.

Compatibility: GitHub CLI extensions that only use gh api and the REST endpoints Gitea shares
with GitHub can work; those that use GraphQL or other GitHub-only features do not. With
--compat-gh, an extension's gh calls reach gt, which answers gh api, gh auth token and
gh repo view, whose --json takes gt's fields, such as full_name, not gh's.

Policy: extensions (enabled or disabled), extension_hosts and extension_owners in config.yml
restrict extensions for you. An administrator sets the same, and extension_credentials: denied,
in /etc/gitea-cli/policy.yml (%ProgramData%\gitea-cli\policy.yml on Windows), which you
cannot loosen.`},
		{Use: "formatting", Short: "JSON and CSV output with --json, --jq and --csv", Long: `gt prints text for people by default. Scripts should use JSON or CSV: the columns
and wording of text output may change between releases.

  --json             print every field
  --json FIELDS      print only the comma-separated FIELDS, such as --json number,title
  -q, --jq EXPR      filter the JSON with a jq expression; implies --json
  --csv              print every field as CSV
  --csv FIELDS       print only the comma-separated FIELDS as CSV, such as --csv number,title

The JSON FIELDS section of a command's help lists its fields, which --csv selects too,
and shell completion offers them after --json and --csv. An unknown field reports the
available ones. A word after --json or --csv is read as an argument only when the
command takes no other, as in gt release view --json v1; write --json=FIELDS to make
it a field list.

--csv prints a header row of the field names, then one row per item; a command that
returns one item prints one row. Strings print as they are, null as an empty cell,
and numbers, booleans, objects and arrays as compact JSON. --csv cannot be combined
with --json or --jq.

--jq runs inside gt, so jq does not need to be installed. String results print
without quotes:

  $ gt issue list --json number,title --jq '.[] | "\(.number)\t\(.title)"'

Some fields cost extra requests, such as the reviewers of pr list, the blocked_by and blocks
of issue view and issue edit, and the visibility of org member list. gt makes them only when
the output includes the field: --json or --csv names it, or selects every field and the --jq
expression names it. An expression that reaches such a field without naming it, as
to_entries does, sees it empty; name the field in --json instead.

Commands that take several arguments, such as issue close or milestone issue add, print an
object for one argument and an array of the successful results for several. A script that
needs one shape passes one argument per call, or several and reads the array, such as with
--jq '.[]'.

On a terminal, JSON is indented and colored; otherwise each value prints on one line.
Commands that return no resource, such as deletes, reject --json and --csv.`},
	}
	for _, topic := range topics {
		topic.Hidden, topic.Annotations = true, map[string]string{topicAnnotation: "true"}
	}
	return topics
}
