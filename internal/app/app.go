// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

// Package app builds the gt command tree and its injected dependencies.
package app

import (
	"bytes"
	"cmp"
	"context"
	"errors"
	"fmt"
	"maps"
	"os"
	"path/filepath"
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
	"gitea.dev/cli/internal/extensions"
	"gitea.dev/cli/internal/extensions/github"
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
	ReleaseForm   terminal.ReleaseForm
	RepoForm      terminal.RepoForm
	LabelForm     terminal.LabelForm
	Getenv        func(string) string
	LookupEnv     func(string) (string, bool)
	Client        func(string, string, bool) *transport.Client
	// ServerVersion reads an instance's Gitea version; nil asks the instance.
	ServerVersion func(context.Context, *transport.Client) (string, error)
	// DataDir holds installed extensions; PolicyFile is the system policy, which may not exist.
	DataDir    string
	PolicyFile string
	// Environ is the environment extensions inherit, without gt's secrets.
	Environ      func() []string
	RunExtension func(extensions.Command) (int, error)
	GitHub       func() github.Client
}

// System connects the CLI to the current environment and terminal.
func System() Dependencies {
	streams := terminal.System()
	store := settings.New(os.Getenv("GITEA_CLI_CONFIG_DIR"))
	prompter := &terminal.FormPrompter{Streams: streams}
	return Dependencies{Browser: terminal.SystemBrowser{ErrOut: streams.ErrOut}, Editor: terminal.SystemEditor{Streams: streams}, Pager: terminal.SystemPager{}, Executable: os.Executable, Git: git.System{In: streams.In, ErrOut: streams.ErrOut, Reads: &git.ReadCache{}}, Now: time.Now, Settings: store, Credentials: &auth.Store{Dir: store.Dir, Keyring: auth.SystemKeyring{}}, Streams: streams, Prompter: prompter, IssueForm: prompter, IssueEditForm: prompter, PullForm: prompter, ReviewForm: prompter, MergeForm: prompter, MilestoneForm: prompter, ReleaseForm: prompter, RepoForm: prompter, LabelForm: prompter, Getenv: os.Getenv, LookupEnv: os.LookupEnv, Client: transport.New, DataDir: settings.DataDir(os.Getenv("GITEA_CLI_DATA_DIR")), PolicyFile: settings.PolicyPath(), Environ: os.Environ, RunExtension: extensions.Run, GitHub: github.New}
}

type options struct {
	host, user, repo, cwd, remote string
	sudo                          string
	json, jq                      string
	// csv holds the --csv fields; checkOutputFlags copies them to json, which selects them.
	csv string
	// template holds the --template Go template; checkOutputFlags makes it select every field unless --json names some.
	template                  string
	noInput, interactive, yes bool
	debug                     bool
	// dryRun withholds the write requests of commands that take --dry-run, which dryRunJSON reports
	// as JSON, filtered by jq, because --json or --jq was given.
	dryRun, dryRunJSON bool
	// editor is the command's --editor, which needs a configured editor before any request.
	editor bool
	// repoArgument is set when repo holds a command's repository argument rather than --repo.
	repoArgument bool
	// ignoresRemote is set by commands whose repository never comes from --remote.
	ignoresRemote bool
}

type application struct {
	deps    Dependencies
	options options

	// snapshot caches the settings for one run; generation is the store's generation it was loaded at.
	mu         sync.Mutex
	snapshot   *settings.Snapshot
	generation uint64
	// resolved is the last target a command resolved, which error hints repeat.
	resolved target
	// command is the command being run; serverVersions caches each instance's Gitea version.
	command        *cobra.Command
	serverVersions map[string]string
	// clients are the clients sharedClient built, by instance and token.
	clients map[string]*transport.Client
	// extensionAliases are the top-level aliases installed extensions declare that gt runs.
	extensionAliases map[string]extensionAlias
	// dryRunRequests collects the write requests that --dry-run withheld.
	dryRunRequests *transport.DryRunRecorder
	// dryRunHeld holds what a dry run prints to stdout.
	dryRunHeld *bytes.Buffer
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
	root, _ := newRoot(deps)
	return root
}

func newRoot(deps Dependencies) (*cobra.Command, *application) {
	if deps.Executable == nil {
		deps.Executable = os.Executable
	}
	if deps.Now == nil {
		deps.Now = time.Now
	}
	if deps.Environ == nil {
		deps.Environ = os.Environ
	}
	if deps.RunExtension == nil {
		deps.RunExtension = extensions.Run
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
	if getenv := deps.Getenv; getenv != nil {
		deps.Getenv = func(key string) string {
			if key == "GITEA_TOKEN" { // A token read from a file keeps its trailing newline.
				return strings.TrimSpace(getenv(key))
			}
			return getenv(key)
		}
	}
	if deps.LookupEnv == nil && deps.Getenv != nil {
		// Without LookupEnv, a variable is set when Getenv reads a value for it.
		getenv := deps.Getenv
		deps.LookupEnv = func(key string) (string, bool) {
			value := getenv(key)
			return value, value != ""
		}
	}
	a := &application{deps: deps, dryRunRequests: &transport.DryRunRecorder{}}
	if prompter, ok := deps.Prompter.(*terminal.FormPrompter); ok {
		// Forms draw on stderr, so they follow the color settings for stderr.
		prompter.Color = a.errColorEnabled
	}
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
	flags.StringVar(&a.options.template, "template", "", "Format JSON output using a Go `template`; implies --json (see gt help formatting)")
	flags.StringVar(&a.options.csv, "csv", "", "Output CSV with the specified comma-separated `fields`, or all fields")
	flags.Lookup("csv").NoOptDefVal = "*"
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
	_ = root.RegisterFlagCompletionFunc("csv", completeJSONFields)
	root.PersistentPreRunE = func(cmd *cobra.Command, _ []string) error {
		a.mu.Lock()
		a.command = cmd
		a.mu.Unlock()
		if err := a.checkOutputFlags(cmd); err != nil {
			return err
		}
		if err := validateEnums(cmd); err != nil {
			return err
		}
		if err := a.checkErrorFormat(); err != nil {
			return err
		}
		if _, err := a.colorEnvironment(); err != nil {
			return err
		}
		if a.options.cwd != "" {
			if info, err := os.Stat(a.options.cwd); err != nil || !info.IsDir() {
				return clierr.Wrap(clierr.Usage, fmt.Errorf("--cwd %s is not a directory", a.options.cwd))
			}
		}
		if cmd.Flags().Changed("user") && strings.TrimSpace(a.options.user) == "" {
			return clierr.Wrap(clierr.Usage, errors.New("--user cannot be empty"))
		}
		if cmd.Flags().Changed("sudo") {
			if strings.TrimSpace(a.options.sudo) == "" {
				return clierr.Wrap(clierr.Usage, errors.New("--sudo requires a username"))
			}
			if strings.IndexFunc(a.options.sudo, func(r rune) bool { return unicode.IsSpace(r) || unicode.IsControl(r) }) >= 0 {
				return clierr.Wrap(clierr.Usage, fmt.Errorf("invalid --sudo username %q", a.options.sudo))
			}
			for c := cmd; c.HasParent(); c = c.Parent() {
				if c.Name() == "auth" && !c.Parent().HasParent() {
					return clierr.Wrap(clierr.Usage, errors.New("--sudo cannot be combined with auth commands"))
				}
			}
		}
		a.options.editor, _ = cmd.Flags().GetBool("editor")
		switch {
		case a.options.dryRun && a.options.editor:
			return clierr.Wrap(clierr.Usage, errors.New("--dry-run cannot be combined with --editor"))
		case a.options.dryRun && a.options.interactive:
			return clierr.Wrap(clierr.Usage, errors.New("--dry-run cannot be combined with --interactive"))
		case a.options.dryRun && cmd.Flags().Changed("web"):
			return clierr.Wrap(clierr.Usage, errors.New("--dry-run cannot be combined with --web"))
		case a.options.dryRun && cmd.Flags().Changed("follow"):
			return clierr.Wrap(clierr.Usage, errors.New("--dry-run cannot be combined with --follow"))
		case a.options.dryRun:
			a.options.noInput, a.options.yes = true, true // a dry run previews the command as given
			// What the command prints after its withheld writes, such as the address of the issue it
			// "created", is not true, so stdout is held back until Execute knows whether to report.
			a.dryRunHeld = &bytes.Buffer{}
			a.deps.Streams.Out = a.dryRunHeld
			cmd.Root().SetOut(a.dryRunHeld)
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
	root.AddCommand(a.configCommand(), a.aliasCommand(), a.authCommand(), a.apiCommand(), a.repoCommand(), a.issueCommand(), a.prCommand(), a.doctorCommand(), a.workflowCommand(), a.runCommand(), a.runnerCommand(), a.labelCommand(), a.milestoneCommand(), a.projectCommand(), a.sshKeyCommand(), a.gpgKeyCommand(), a.timeCommand(), a.wikiCommand(), a.browseCommand(), a.actionValueCommand(true), a.actionValueCommand(false), a.releaseCommand(), a.organizationCommand(), a.notificationCommand(), a.teamCommand(), a.branchCommand(), a.commitCommand(), a.tagCommand(), a.webhookCommand(), a.packageCommand(), a.adminCommand(), a.searchCommand(), a.statusCommand(false), a.activityCommand(), a.extensionCommand())
	a.addDryRun(root)
	root.AddCommand(a.manCommand(), a.schemaCommand(), a.upgradeCommand())
	root.AddCommand(&cobra.Command{Use: "version", Short: "Print gt's version", Example: "$ gt version", Args: argsExact(0), Annotations: textOutput("version prints text"), RunE: func(cmd *cobra.Command, _ []string) error {
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
		&cobra.Group{ID: "extension", Title: "Extension commands"},
	)
	for _, cmd := range root.Commands() {
		if cmd.Annotations[topicAnnotation] == "" {
			cmd.GroupID = cmp.Or(rootGroups[cmd.Name()], "additional")
		}
		cmd.SuggestFor = teaCommands[cmd.Name()]
	}
	rejectUnknownSubcommands(root)
	applyGiteaVersions(root)
	addVerbAliases(root)
	completeDefaults(root)
	return root, a
}

// teaCommands are the top-level commands of tea, which gt replaces, that gt suggests its own
// command for.
var teaCommands = map[string][]string{
	"auth": {"login", "logins", "logout", "whoami"}, "browse": {"open"}, "issue": {"comment", "comments"},
	"pr": {"pulls", "pull", "comment", "comments"}, "release": {"assets"}, "repo": {"clone"},
	"run": {"actions"}, "workflow": {"actions"}, "webhook": {"hooks"},
}

// rootGroups places the core and Actions commands in their root help groups; every other
// command is additional.
var rootGroups = map[string]string{
	"auth": "core", "browse": "core", "issue": "core", "org": "core", "pr": "core", "release": "core", "repo": "core",
	"run": "actions", "runner": "actions", "secret": "actions", "variable": "actions", "workflow": "actions",
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

// outputTemplate reports whether cmd got the global --template of the output, not the --template of
// its own, which gt issue create, gt repo create and gt project create have for a template to start
// from.
func outputTemplate(cmd *cobra.Command) bool {
	flag := cmd.Flags().Lookup("template")
	return flag != nil && flag.Changed && flag == cmd.Root().PersistentFlags().Lookup("template")
}

// checkOutputFlags validates --json, --jq and --csv before a command runs. --jq alone selects every
// field, except on commands that filter their raw JSON themselves (annotation gt.rawjson). --csv
// selects its fields like --json, so that every JSON output path writes CSV instead.
func (a *application) checkOutputFlags(cmd *cobra.Command) error {
	selected := ""
	if outputTemplate(cmd) {
		for _, other := range []string{"jq", "csv"} {
			if cmd.Flags().Changed(other) {
				return clierr.Wrap(clierr.Usage, fmt.Errorf("--template cannot be combined with --%s", other))
			}
		}
		if strings.TrimSpace(a.options.template) == "" {
			return clierr.Wrap(clierr.Usage, errors.New(`--template requires a template, such as '{{.title}}{{"\n"}}'`))
		}
		if cmd.Annotations["gt.rawjson"] != "" {
			return clierr.Wrap(clierr.Usage, fmt.Errorf("%s prints the API's own JSON; --template is not supported, use --jq", cmd.CommandPath()))
		}
		if err := terminal.ValidateTemplate(a.options.template); err != nil {
			return clierr.Wrap(clierr.Usage, err)
		}
	}
	switch {
	case cmd.Flags().Changed("csv"):
		for _, other := range []string{"json", "jq"} {
			if cmd.Flags().Changed(other) {
				return clierr.Wrap(clierr.Usage, fmt.Errorf("--csv cannot be combined with --%s", other))
			}
		}
		if slices.ContainsFunc(strings.Split(a.options.csv, ","), func(name string) bool { return strings.TrimSpace(name) == "" }) {
			return clierr.Wrap(clierr.Usage, errors.New("--csv field names cannot be empty; use --csv alone for every field"))
		}
		selected, a.options.json = "--csv", a.options.csv
	case cmd.Flags().Changed("json"):
		if a.options.json == "" {
			return clierr.Wrap(clierr.Usage, errors.New("--json= requires field names; use --json alone for every field"))
		}
		selected = "--json"
	case outputTemplate(cmd):
		selected, a.options.json = "--template", "*"
	case a.options.jq != "" && cmd.Annotations["gt.rawjson"] == "":
		selected, a.options.json = "--jq", "*"
	}
	switch {
	case a.options.dryRun && selected == "--csv":
		return clierr.Wrap(clierr.Usage, errors.New("--dry-run cannot be combined with --csv"))
	case a.options.dryRun && selected != "":
		// The report of a dry run is JSON whichever fields the command would print.
		a.options.dryRunJSON, a.options.json = true, ""
		return clierr.Wrap(clierr.Usage, terminal.ValidateOutput("", a.options.jq, nil))
	}
	if reason := cmd.Annotations["gt.text"]; reason != "" && selected != "" {
		return clierr.Wrap(clierr.Usage, fmt.Errorf("%s; %s is not supported", reason, selected))
	}
	if flag := cmd.Flags().Lookup("web"); flag != nil && flag.Value.String() == "true" && (a.options.json != "" || a.options.jq != "") {
		return clierr.Wrap(clierr.Usage, errors.New("--web cannot be combined with --json, --jq or --csv"))
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
		// A user alias may take the name of an alias an extension declares.
		if cmd.Annotations["gt.alias"] != "true" && cmd.Annotations["gt.extension-alias"] != "true" {
			names[cmd.Name()] = true
		}
	}
	return names
}

// Execute expands aliases and executes the fresh root without printing errors.
func Execute(ctx context.Context, deps Dependencies, args []string) error {
	if extension := deps.Getenv("GITEA_CLI_EXTENSION"); extension != "" && deps.Credentials != nil && !extensionGranted(deps, extension) {
		// An extension without a credential grant must not get the stored ones through gt. It can
		// unset the variable: this keeps honest extensions honest, it does not stop hostile ones.
		source := "<repository>"
		if installed, err := (extensions.Store{Dir: filepath.Join(deps.DataDir, "extensions")}).Lookup(extension); err == nil {
			source = extensionOrigin(installed.Manifest)
		}
		denied := *deps.Credentials
		denied.Denied = fmt.Errorf("extension %s was not given your credentials; allow them with gt extension install --force --allow-credentials %s", extension, source)
		deps.Credentials = &denied
	}
	root, a := newRoot(deps)
	// Broken settings or aliases must not prevent help or recovery commands.
	snapshot, loadErr := deps.Settings.Load()
	names := builtins(root)
	if loadErr == nil {
		// An extension runs before gt rewrites any of its arguments. An alias may expand to an
		// extension, and cannot have the name of one.
		names = maps.Clone(names)
		maps.Copy(names, a.addExtensionCommands(root, snapshot.Preferences.Aliases))
		if ran, err := a.dispatchExtension(ctx, root, args, commandIndex(root, args)); ran {
			return err
		}
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
			if ran, err := a.dispatchExtension(ctx, root, slices.Concat(args[:commandAt], expanded), commandAt); ran {
				return err
			}
			args = joinJSONFields(root, slices.Concat(args[:commandAt], expanded))
		}
		if completion {
			args = completeJSONValue(args)
		}
	}
	root.SetArgs(args)
	notifyUpdate := a.checkForUpdate(ctx, args, deps.Client)
	cmd, err := root.ExecuteContextC(ctx)
	if a.dryRunHeld != nil {
		a.deps.Streams.Out = deps.Streams.Out
		if requests := a.dryRunRequests.Requests(); len(requests) > 0 || (err == nil && a.options.dryRunJSON) {
			err = cmp.Or(err, a.reportDryRun(requests))
		} else {
			_, copyErr := a.dryRunHeld.WriteTo(deps.Streams.Out) // nothing was withheld: the command's own output stands
			err = cmp.Or(err, copyErr)
		}
	}
	notifyUpdate(cmd, err == nil)
	if services.IsUnauthorized(err) {
		return clierr.Wrap(clierr.Authentication, err)
	}
	if services.IsNotFound(err) && a.options.sudo != "" {
		if missing := services.MissingSudoUser(ctx, a.lastTarget().client); missing != nil {
			return missing
		}
	}
	if notPull, ok := errors.AsType[*services.NotPullError](err); ok {
		if issue, _, findErr := root.Find(strings.Fields(issueCommandPath(cmd))[1:]); findErr == nil && issue.Name() == cmd.Name() {
			repo := ""
			if resolved := a.lastTarget(); resolved.Repo == notPull.Repository {
				repo = a.suggestedRepo(resolved)
			}
			return explain(err, "%s", strings.Replace(err.Error(), notPull.Error(), fmt.Sprintf("%s; use %s %d%s", notPull.Error(), issue.CommandPath(), notPull.Number, repo), 1))
		}
	}
	return err
}

var jsonFieldList = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9_]*(,[A-Za-z][A-Za-z0-9_]*)*$`)

// outputFlag names the flag that selected machine output, for usage errors about its fields.
func (a *application) outputFlag() string {
	if a.options.csv != "" {
		return "--csv"
	}
	return "--json"
}

// joinJSONFields accepts `--json number,title` like `--json=number,title`, and --csv likewise. The word
// stays a positional argument only when it is the only one and the command neither outputs any of those
// fields, nor one they resemble, nor rejects the argument, so a misspelled field lists the available
// fields rather than naming one more tag or label to change. Unknown commands, such as unexpanded
// aliases, are joined.
func joinJSONFields(root *cobra.Command, args []string) []string {
	for i := 0; i < len(args); i++ {
		if args[i] == "--" {
			break
		}
		if (args[i] != "--json" && args[i] != "--csv") || i+1 >= len(args) || !jsonFieldList.MatchString(args[i+1]) {
			continue
		}
		// A word that names a subcommand at this position is the command, as in `gt --json repo view`.
		if parent, _, err := root.Find(args[:i]); err == nil && slices.ContainsFunc(parent.Commands(), func(c *cobra.Command) bool { return c.Name() == args[i+1] || c.HasAlias(args[i+1]) }) {
			continue
		}
		cmd, _, err := root.Find(slices.Concat(args[:i+1], args[i+2:]))
		if fields := commandOutputFields(cmd); err == nil && terminal.ValidateOutput(args[i+1], "", fields) != nil && !namesField(args[i+1], fields) && !resemblesField(args[i+1], fields) && acceptsArgs(args) && len(positionalArgs(slices.Concat(args[:i+1], args[i+2:]))) == 0 {
			continue
		}
		args = slices.Concat(args[:i], []string{args[i] + "=" + args[i+1]}, args[i+2:])
	}
	return args
}

// namesField reports whether list names a field of fields, so it is a field list even when another
// of its names is misspelled, as in "number,titel".
func namesField(list string, fields []string) bool {
	return slices.ContainsFunc(strings.Split(list, ","), func(name string) bool { return slices.Contains(fields, name) })
}

// resemblesField reports whether list names a field of fields but for case and underscores, as
// "fullname" does full_name. Closer guesses would take search words such as "foo" for fork.
func resemblesField(list string, fields []string) bool {
	plain := func(name string) string { return strings.ToLower(strings.ReplaceAll(name, "_", "")) }
	return slices.ContainsFunc(strings.Split(list, ","), func(name string) bool {
		return slices.ContainsFunc(fields, func(field string) bool { return plain(name) == plain(field) })
	})
}

// acceptsArgs reports whether the command that args select accepts their positional arguments. It
// parses a separate command tree, because parsing sets the flags of the one that runs.
func acceptsArgs(args []string) bool {
	cmd, rest, err := NewRoot(Dependencies{}).Find(args)
	return err == nil && cmd.ParseFlags(rest) == nil && cmd.ValidateArgs(cmd.Flags().Args()) == nil
}

// positionalArgs returns the positional arguments of the command that args select, parsed like acceptsArgs.
func positionalArgs(args []string) []string {
	cmd, rest, err := NewRoot(Dependencies{}).Find(args)
	if err != nil || cmd.ParseFlags(rest) != nil {
		return nil
	}
	return cmd.Flags().Args()
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

// varyingJSONOutput declares fields that vary, such as config keys, which have no type.
func varyingJSONOutput(cmd *cobra.Command, fields []string) *cobra.Command {
	if cmd.Annotations == nil {
		cmd.Annotations = map[string]string{}
	}
	cmd.Annotations[fieldsAnnotation] = strings.Join(fields, ",")
	return cmd
}

// outputTypes maps the outputTypeAnnotation of a command to the type it outputs, for gt schema.
var outputTypes sync.Map

// jsonOutput declares the JSON fields of T, except omit, as those cmd and its direct subcommands
// output.
func jsonOutput[T any](cmd *cobra.Command, omit ...string) *cobra.Command {
	t := reflect.TypeFor[T]()
	key := t.PkgPath() + "." + t.Name()
	outputTypes.Store(key, t)
	varyingJSONOutput(cmd, slices.DeleteFunc(jsonFields[T](), func(name string) bool { return slices.Contains(omit, name) }))
	cmd.Annotations[outputTypeAnnotation] = key
	return cmd
}

// fieldsDeclaration returns cmd, else its parent, whichever declares the JSON fields of cmd.
func fieldsDeclaration(cmd *cobra.Command) *cobra.Command {
	for _, c := range []*cobra.Command{cmd, cmd.Parent()} {
		if c == nil {
			continue
		}
		if _, ok := c.Annotations[fieldsAnnotation]; ok {
			return c
		}
	}
	return nil
}

// commandOutputFields returns the JSON fields declared on cmd for its --local flag when set, else
// on cmd or its parent, or nil for a command group, which outputs nothing itself.
func commandOutputFields(cmd *cobra.Command) []string {
	if cmd.Annotations["gt.group"] != "" {
		return nil
	}
	if fields, ok := cmd.Annotations[localFieldsAnnotation]; ok {
		if flag := cmd.Flags().Lookup("local"); flag != nil && flag.Value.String() == "true" {
			return strings.Split(fields, ",")
		}
	}
	if c := fieldsDeclaration(cmd); c != nil {
		return strings.Split(c.Annotations[fieldsAnnotation], ",")
	}
	return nil
}

// jsonFields lists the JSON names of T's fields, which --json can select, in alphabetical order.
func jsonFields[T any]() []string {
	var fields []string
	structJSONFields(reflect.TypeFor[T](), func(name, _ string, _ reflect.Type) { fields = append(fields, name) })
	slices.Sort(fields)
	return fields
}

// structJSONFields calls visit with the JSON name, tag options and type of each of t's fields in
// order, including those of embedded structs.
func structJSONFields(t reflect.Type, visit func(name, options string, field reflect.Type)) {
	for field := range t.Fields() {
		name, options, _ := strings.Cut(field.Tag.Get("json"), ",")
		switch {
		case field.Anonymous && name == "":
			structJSONFields(field.Type, visit)
		case name != "" && name != "-":
			visit(name, options, field.Type)
		}
	}
}
