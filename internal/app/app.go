// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

// Package app builds the gt command tree and its injected dependencies.
package app

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"os"
	"reflect"
	"regexp"
	"slices"
	"strings"
	"sync"
	"time"
	"unicode"

	"gitea.dev/cli/internal/aliases"
	"gitea.dev/cli/internal/auth"
	"gitea.dev/cli/internal/clierr"
	"gitea.dev/cli/internal/git"
	"gitea.dev/cli/internal/services"
	"gitea.dev/cli/internal/settings"
	"gitea.dev/cli/internal/terminal"
	"gitea.dev/cli/internal/transport"
	"gitea.dev/cli/internal/version"
	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
)

// Dependencies supplies external boundaries for a fresh CLI invocation.
type Dependencies struct {
	Browser       terminal.Browser
	Editor        terminal.Editor
	Pager         terminal.Pager
	Executable    func() (string, error)
	Git           git.Runner
	Now           func() time.Time
	Settings      *settings.Store
	Credentials   *auth.Store
	Streams       terminal.Streams
	Prompter      terminal.Prompter
	IssueForm     terminal.IssueForm
	IssueEditForm terminal.IssueEditForm
	PullForm      terminal.PullForm
	ReviewForm    terminal.ReviewForm
	MergeForm     terminal.MergeForm
	MilestoneForm terminal.MilestoneForm
	Getenv        func(string) string
	Client        func(string, string, bool) *transport.Client
}

// System connects the CLI to the current environment and terminal.
func System() Dependencies {
	streams := terminal.System()
	store := settings.New(os.Getenv("GITEA_CLI_CONFIG_DIR"))
	prompter := &terminal.FormPrompter{Streams: streams}
	return Dependencies{Browser: terminal.SystemBrowser{ErrOut: streams.ErrOut}, Editor: terminal.SystemEditor{Streams: streams}, Pager: terminal.SystemPager{}, Executable: os.Executable, Git: git.System{In: streams.In, ErrOut: streams.ErrOut}, Now: time.Now, Settings: store, Credentials: &auth.Store{Dir: store.Dir, Keyring: auth.SystemKeyring{}}, Streams: streams, Prompter: prompter, IssueForm: prompter, IssueEditForm: prompter, PullForm: prompter, ReviewForm: prompter, MergeForm: prompter, MilestoneForm: prompter, Getenv: os.Getenv, Client: transport.New}
}

type options struct {
	host, user, repo, cwd, remote string
	sudo                          string
	json, jq                      string
	noInput, interactive, yes     bool
	debug                         bool
}

type application struct {
	deps    Dependencies
	options options

	// snapshot caches the settings for one run; generation is the store's generation it was loaded at.
	mu         sync.Mutex
	snapshot   *settings.Snapshot
	generation uint64
}

// settings returns the settings, loaded once per run and again after gt changed them.
func (a *application) settings() (settings.Snapshot, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	generation := a.deps.Settings.Generation()
	if a.snapshot != nil && a.generation == generation {
		return *a.snapshot, nil
	}
	snapshot, err := a.deps.Settings.Load()
	if err != nil {
		return snapshot, err
	}
	a.snapshot, a.generation = &snapshot, generation
	return snapshot, nil
}

// NewRoot builds fresh commands. Construction performs no disk, network, or Git IO.
func NewRoot(deps Dependencies) *cobra.Command {
	if deps.Executable == nil {
		deps.Executable = os.Executable
	}
	if deps.Now == nil {
		deps.Now = time.Now
	}
	if deps.Git == nil {
		deps.Git = git.System{In: deps.Streams.In, ErrOut: deps.Streams.ErrOut}
	}
	if deps.Browser == nil {
		deps.Browser = terminal.SystemBrowser{ErrOut: deps.Streams.ErrOut}
	}
	if deps.Editor == nil {
		deps.Editor = terminal.SystemEditor{Streams: deps.Streams}
	}
	a := &application{deps: deps}
	if client := deps.Client; client != nil {
		a.deps.Client = func(instance, token string, insecure bool) *transport.Client {
			if a.options.debug {
				return client(instance, token, insecure).Debug(deps.Streams.ErrOut)
			}
			return client(instance, token, insecure)
		}
	}
	root := &cobra.Command{Use: "gt", Short: "Work with Gitea from your terminal", Long: "Work seamlessly with Gitea from the command line.", Version: version.Format(), SilenceErrors: true, SilenceUsage: true}
	root.SetIn(deps.Streams.In)
	root.SetOut(deps.Streams.Out)
	root.SetErr(deps.Streams.ErrOut)
	root.SetFlagErrorFunc(func(cmd *cobra.Command, err error) error { return clierr.Wrap(clierr.Usage, suggestFlags(cmd, err)) })
	flags := root.PersistentFlags()
	flags.StringVar(&a.options.host, "host", "", "Gitea instance `URL`, including any base path")
	flags.StringVar(&a.options.user, "user", "", "Account `name` on the selected instance")
	flags.StringVar(&a.options.sudo, "sudo", "", "Perform API requests as this `user` (requires an administrator account)")
	flags.StringVarP(&a.options.repo, "repo", "R", "", "Select a repository as `OWNER/REPO` or URL")
	flags.StringVar(&a.options.cwd, "cwd", "", "Working `directory` for repository discovery")
	flags.StringVar(&a.options.remote, "remote", "", "Git `remote` used for repository discovery")
	flags.StringVar(&a.options.json, "json", "", "Output JSON with the specified comma-separated `fields`, or all fields")
	flags.Lookup("json").NoOptDefVal = "*"
	flags.StringVarP(&a.options.jq, "jq", "q", "", "Filter JSON output using a jq `expression`; implies --json")
	flags.BoolVar(&a.options.noInput, "no-input", false, "Disable prompts and implicit editor/browser interactions")
	flags.BoolVar(&a.options.interactive, "interactive", false, "Allow prompts with machine output")
	flags.BoolVarP(&a.options.yes, "yes", "y", false, "Accept confirmations without prompting")
	flags.BoolVar(&a.options.debug, "debug", false, "Log the method, URL, status, and duration of each HTTP request to stderr")
	_ = root.RegisterFlagCompletionFunc("host", a.completeHosts)
	_ = root.RegisterFlagCompletionFunc("user", a.completeAccounts)
	_ = root.RegisterFlagCompletionFunc("repo", a.completeRepos())
	_ = root.RegisterFlagCompletionFunc("remote", a.completeRemotes)
	_ = root.RegisterFlagCompletionFunc("cwd", completeDirectories)
	_ = root.RegisterFlagCompletionFunc("json", completeJSONFields)
	root.PersistentPreRunE = func(cmd *cobra.Command, _ []string) error {
		if err := a.checkOutputFlags(cmd); err != nil {
			return err
		}
		if err := validateEnums(cmd); err != nil {
			return err
		}
		if cmd.Flags().Changed("sudo") {
			if a.options.sudo == "" || strings.IndexFunc(a.options.sudo, func(r rune) bool { return unicode.IsSpace(r) || unicode.IsControl(r) }) >= 0 {
				return clierr.Wrap(clierr.Usage, errors.New("--sudo requires a username"))
			}
			for c := cmd; c.HasParent(); c = c.Parent() {
				if c.Name() == "auth" && !c.Parent().HasParent() {
					return clierr.Wrap(clierr.Usage, errors.New("--sudo cannot be used with auth commands"))
				}
			}
		}
		if offline(cmd) {
			return nil
		}
		snapshot, err := a.settings()
		if err != nil {
			return err
		}
		return a.policy(snapshot).Validate(a.deps.Streams)
	}
	root.AddCommand(a.configCommand(), a.aliasCommand(), a.authCommand(), a.apiCommand(), a.repoCommand(), a.issueCommand(), a.prCommand(), a.doctorCommand(), a.workflowCommand(), a.runCommand(), a.labelCommand(), a.milestoneCommand(), a.sshKeyCommand(), a.timeCommand(), a.wikiCommand(), a.actionValueCommand(true), a.actionValueCommand(false), a.releaseCommand(), a.organizationCommand(), a.notificationCommand(), a.teamCommand(), a.branchCommand(), a.webhookCommand(), a.adminCommand(), a.searchCommand(), a.statusCommand(false))
	root.AddCommand(a.manCommand())
	root.AddCommand(&cobra.Command{Use: "version", Short: "Print gt's version", Example: "$ gt version", Annotations: textOutput("version prints text"), RunE: func(cmd *cobra.Command, _ []string) error {
		_, err := fmt.Fprintln(cmd.OutOrStdout(), version.Format())
		return err
	}})
	root.AddCommand(helpTopics(flags)...)
	root.SetHelpCommand(a.helpCommand())
	root.InitDefaultHelpCmd()
	root.InitDefaultCompletionCmd()
	completionInstallHelp(root)
	root.SetVersionTemplate("{{.Version}}\n")
	root.SetHelpFunc(a.writeHelp)
	root.CompletionOptions.SetDefaultShellCompDirective(cobra.ShellCompDirectiveNoFileComp)
	root.AddGroup(
		&cobra.Group{ID: "core", Title: "Core commands"},
		&cobra.Group{ID: "actions", Title: "Actions commands"},
		&cobra.Group{ID: "additional", Title: "Additional commands"},
		&cobra.Group{ID: "alias", Title: "Alias commands"},
	)
	for _, cmd := range root.Commands() {
		if cmd.Annotations[topicAnnotation] == "" {
			cmd.GroupID = cmp.Or(rootGroups[cmd.Name()], "additional")
		}
	}
	rejectUnknownSubcommands(root)
	addVerbAliases(root)
	completeDefaults(root)
	return root
}

// rootGroups places the core and Actions commands in their root help groups; every other
// command is additional.
var rootGroups = map[string]string{
	"auth": "core", "issue": "core", "org": "core", "pr": "core", "release": "core", "repo": "core",
	"run": "actions", "secret": "actions", "variable": "actions", "workflow": "actions",
}

// verbAliases are the gh-style short names of common verbs; commands add others to their own Aliases.
var verbAliases = map[string]string{"list": "ls", "delete": "rm"}

// addVerbAliases gives every list and delete command its short name.
func addVerbAliases(cmd *cobra.Command) {
	if alias, ok := verbAliases[cmd.Name()]; ok && cmd.HasParent() && !slices.Contains(cmd.Aliases, alias) {
		cmd.Aliases = append(cmd.Aliases, alias)
	}
	for _, child := range cmd.Commands() {
		addVerbAliases(child)
	}
}

// suggestFlags adds the flags of cmd with a similar name to an unknown flag error, as in
// "unknown flag: --lable; did you mean --label?".
func suggestFlags(cmd *cobra.Command, err error) error {
	name, unknown := strings.CutPrefix(err.Error(), "unknown flag: --")
	if !unknown || len(name) < 3 {
		return err
	}
	var suggestions []string
	cmd.Flags().VisitAll(func(flag *pflag.Flag) {
		if !flag.Hidden && (strings.HasPrefix(flag.Name, name) || editDistance(name, flag.Name) <= 2) {
			suggestions = append(suggestions, "--"+flag.Name)
		}
	})
	if len(suggestions) == 0 {
		return err
	}
	return fmt.Errorf("%w; did you mean %s?", err, strings.Join(suggestions, " or "))
}

// editDistance counts the single-character insertions, deletions and substitutions that turn a into b.
func editDistance(a, b string) int {
	row := make([]int, len(b)+1)
	for j := range row {
		row[j] = j
	}
	for i := 1; i <= len(a); i++ {
		diagonal := row[0]
		row[0] = i
		for j := 1; j <= len(b); j++ {
			substitution := diagonal
			if a[i-1] != b[j-1] {
				substitution++
			}
			diagonal = row[j]
			row[j] = min(row[j]+1, row[j-1]+1, substitution)
		}
	}
	return row[len(b)]
}

// checkOutputFlags validates --json and --jq before a command runs. --jq alone selects every field,
// except on commands that filter their raw JSON themselves (annotation gt.rawjson).
func (a *application) checkOutputFlags(cmd *cobra.Command) error {
	selected := ""
	switch {
	case cmd.Flags().Changed("json"):
		if a.options.json == "" {
			return clierr.Wrap(clierr.Usage, errors.New("--json= needs field names; use --json alone for every field"))
		}
		selected = "--json"
	case a.options.jq != "" && cmd.Annotations["gt.rawjson"] == "":
		selected, a.options.json = "--jq", "*"
	}
	if reason := cmd.Annotations["gt.text"]; reason != "" && selected != "" {
		return clierr.Wrap(clierr.Usage, fmt.Errorf("%s; %s is not supported", reason, selected))
	}
	if flag := cmd.Flags().Lookup("web"); flag != nil && flag.Value.String() == "true" && (a.options.json != "" || a.options.jq != "") {
		return clierr.Wrap(clierr.Usage, errors.New("--web cannot be combined with --json or --jq"))
	}
	return clierr.Wrap(clierr.Usage, terminal.ValidateOutput(a.options.json, a.options.jq, commandOutputFields(cmd)))
}

// offline reports commands that must work without valid settings.
func offline(cmd *cobra.Command) bool {
	if cmd.Annotations["gt.group"] == "true" || cmd.Annotations["gt.offline"] == "true" {
		return true
	}
	switch cmd.Name() {
	case "help", "version", "man", cobra.ShellCompRequestCmd, cobra.ShellCompNoDescRequestCmd:
		return true
	}
	return cmd.HasParent() && cmd.Parent().Name() == "completion"
}

// rejectUnknownSubcommands makes `gt bogus` and `gt issue bogus` usage errors instead of printing help.
func rejectUnknownSubcommands(cmd *cobra.Command) {
	if cmd.HasSubCommands() && !cmd.Runnable() {
		if cmd.Annotations == nil {
			cmd.Annotations = map[string]string{}
		}
		cmd.Annotations["gt.group"] = "true"
		cmd.Args, cmd.SuggestionsMinimumDistance = cobra.ArbitraryArgs, 2
		cmd.RunE = func(cmd *cobra.Command, args []string) error {
			if len(args) == 0 {
				return cmd.Help()
			}
			err := fmt.Errorf("unknown command %q for %q", args[0], cmd.CommandPath())
			if suggestions := cmd.SuggestionsFor(args[0]); len(suggestions) > 0 {
				err = fmt.Errorf("%w; did you mean %s?", err, strings.Join(suggestions, " or "))
			}
			return clierr.Wrap(clierr.Usage, err)
		}
	}
	for _, child := range cmd.Commands() {
		rejectUnknownSubcommands(child)
	}
}

func builtins(root *cobra.Command) map[string]bool {
	names := map[string]bool{"help": true, "completion": true}
	for _, cmd := range root.Commands() {
		if cmd.Annotations["gt.alias"] != "true" {
			names[cmd.Name()] = true
		}
	}
	return names
}

// Execute expands aliases and executes the fresh root without printing errors.
func Execute(ctx context.Context, deps Dependencies, args []string) error {
	root := NewRoot(deps)
	// Broken settings or aliases must not prevent help or recovery commands.
	snapshot, loadErr := deps.Settings.Load()
	names := builtins(root)
	if loadErr == nil {
		for name, expansion := range snapshot.Preferences.Aliases {
			if aliases.Check(name, snapshot.Preferences.Aliases, names) == nil {
				root.AddCommand(&cobra.Command{Use: name, Short: fmt.Sprintf("Alias for %q", expansion), GroupID: "alias", Annotations: map[string]string{"gt.alias": "true"}, RunE: func(*cobra.Command, []string) error {
					return clierr.Wrap(clierr.Usage, errors.New("alias must be expanded before execution"))
				}})
			}
		}
	}
	if at := commandIndex(root, args); at == len(args) || (args[at] != cobra.ShellCompRequestCmd && args[at] != cobra.ShellCompNoDescRequestCmd) {
		args = joinJSONFields(root, args)
	}
	if commandAt := commandIndex(root, args); commandAt < len(args) {
		name := args[commandAt]
		completion := name == cobra.ShellCompRequestCmd || name == cobra.ShellCompNoDescRequestCmd
		switch {
		case loadErr != nil:
			// An alias cannot be expanded without settings; report why instead of "unknown command".
			if !names[name] && !completion {
				return loadErr
			}
		case completion:
			completionArgs := args[commandAt+1:]
			if aliasAt := commandIndex(root, completionArgs); aliasAt < len(completionArgs) {
				if expanded, err := aliases.ExpandCompletion(completionArgs[aliasAt:], snapshot.Preferences.Aliases, names); err == nil {
					args = slices.Concat(args[:commandAt+1], completionArgs[:aliasAt], expanded)
				}
			}
		default:
			expanded, err := aliases.Expand(args[commandAt:], snapshot.Preferences.Aliases, names)
			if err != nil {
				return clierr.Wrap(clierr.Usage, err)
			}
			args = joinJSONFields(root, slices.Concat(args[:commandAt], expanded))
		}
		if completion {
			args = completeJSONValue(args)
		}
	}
	root.SetArgs(args)
	err := root.ExecuteContext(ctx)
	if services.IsUnauthorized(err) {
		return clierr.Wrap(clierr.Authentication, err)
	}
	return err
}

var jsonFieldList = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9_]*(,[A-Za-z][A-Za-z0-9_]*)*$`)

// joinJSONFields accepts `--json number,title` like `--json=number,title`. The word stays a positional
// argument only when the command neither outputs those fields nor rejects the argument, so a
// misspelled field lists the available fields. Unknown commands, such as unexpanded aliases, are joined.
func joinJSONFields(root *cobra.Command, args []string) []string {
	for i := 0; i < len(args); i++ {
		if args[i] == "--" {
			break
		}
		if args[i] != "--json" || i+1 >= len(args) || !jsonFieldList.MatchString(args[i+1]) {
			continue
		}
		// A word that names a subcommand at this position is the command, as in `gt --json repo view`.
		if parent, _, err := root.Find(args[:i]); err == nil && slices.ContainsFunc(parent.Commands(), func(c *cobra.Command) bool { return c.Name() == args[i+1] || c.HasAlias(args[i+1]) }) {
			continue
		}
		cmd, _, err := root.Find(slices.Concat(args[:i+1], args[i+2:]))
		if err == nil && terminal.ValidateOutput(args[i+1], "", commandOutputFields(cmd)) != nil && acceptsArgs(args) {
			continue
		}
		args = slices.Concat(args[:i], []string{"--json=" + args[i+1]}, args[i+2:])
	}
	return args
}

// acceptsArgs reports whether the command that args select accepts their positional arguments. It
// parses a separate command tree, because parsing sets the flags of the one that runs.
func acceptsArgs(args []string) bool {
	cmd, rest, err := NewRoot(Dependencies{}).Find(args)
	return err == nil && cmd.ParseFlags(rest) == nil && cmd.ValidateArgs(cmd.Flags().Args()) == nil
}

func (a *application) policy(snapshot settings.Snapshot) terminal.Policy {
	return terminal.Policy{Enabled: snapshot.Preferences.Prompt == "enabled", NoInput: a.options.noInput, Machine: a.options.json != "", Interactive: a.options.interactive}
}

// titleCase capitalizes an ASCII command word for help and status text.
func titleCase(s string) string {
	if s == "" {
		return s
	}
	return strings.ToUpper(s[:1]) + s[1:]
}

// textOutput marks a command whose output is not a JSON resource, so --json is rejected up front.
func textOutput(reason string) map[string]string {
	return map[string]string{"gt.text": reason}
}

// commandIndex skips persistent flags without parsing or mutating command state.
func commandIndex(root *cobra.Command, args []string) int {
	for i := 0; i < len(args); i++ {
		arg := args[i]
		if arg == "--" {
			return i + 1
		}
		if !strings.HasPrefix(arg, "-") || arg == "-" {
			return i
		}
		name := strings.TrimLeft(arg, "-")
		if strings.Contains(name, "=") {
			continue
		}
		flag := root.PersistentFlags().Lookup(name)
		if flag == nil && len(name) > 0 && !strings.HasPrefix(arg, "--") {
			flag = root.PersistentFlags().ShorthandLookup(name[:1])
			if len(name) > 1 {
				continue
			}
		}
		if flag != nil && flag.NoOptDefVal == "" {
			i++
		}
	}
	return len(args)
}

// fieldsAnnotation holds the comma-separated fields that --json selects for a command and, unless
// they declare their own, its direct subcommands.
const fieldsAnnotation = "gt.fields"

// jsonOutput declares the JSON fields cmd and its direct subcommands output.
func jsonOutput(cmd *cobra.Command, fields []string) *cobra.Command {
	if cmd.Annotations == nil {
		cmd.Annotations = map[string]string{}
	}
	cmd.Annotations[fieldsAnnotation] = strings.Join(fields, ",")
	return cmd
}

// commandOutputFields returns the JSON fields declared on cmd or its parent, or nil for a command
// group, which outputs nothing itself.
func commandOutputFields(cmd *cobra.Command) []string {
	if cmd.Annotations["gt.group"] != "" {
		return nil
	}
	for _, c := range []*cobra.Command{cmd, cmd.Parent()} {
		if c == nil {
			continue
		}
		if fields, ok := c.Annotations[fieldsAnnotation]; ok {
			return strings.Split(fields, ",")
		}
	}
	return nil
}

// jsonFields lists the JSON names of T's fields, which --json can select, in alphabetical order.
func jsonFields[T any]() []string {
	t := reflect.TypeFor[T]()
	fields := make([]string, 0, t.NumField())
	for field := range t.Fields() {
		if name, _, _ := strings.Cut(field.Tag.Get("json"), ","); name != "" && name != "-" {
			fields = append(fields, name)
		}
	}
	slices.Sort(fields)
	return fields
}
