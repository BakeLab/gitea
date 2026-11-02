// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package app

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"maps"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"strconv"
	"strings"

	"gitea.dev/cli/internal/clierr"
	"gitea.dev/cli/internal/extensions"
	"gitea.dev/cli/internal/settings"
	"gitea.dev/cli/internal/terminal"
	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
)

// extensionFlags are the global flags an extension accepts, with the environment variables gt
// itself reads them from; --cwd becomes its working directory, and --no-input and --yes, which
// every gt command accepts, have no variable to pass.
var extensionFlags = map[string]string{"host": "GITEA_CLI_HOST", "repo": "GITEA_CLI_REPO", "user": "GITEA_CLI_USER", "cwd": "", "no-input": "", "yes": ""}

// defaultExtensionIndex lists the officially supported extensions.
const defaultExtensionIndex = "https://gitea.com/gitea/cli-extensions"

func (a *application) extensionStore() extensions.Store {
	return extensions.Store{Dir: filepath.Join(a.deps.DataDir, "extensions")}
}

// extensionGranted reports whether the installed extension name was given the stored credentials
// and the system policy does not deny them.
func extensionGranted(deps Dependencies, name string) bool {
	installed, err := (extensions.Store{Dir: filepath.Join(deps.DataDir, "extensions")}).Lookup(name)
	if err != nil || !installed.Credentials {
		return false
	}
	policy, err := settings.LoadPolicy(deps.PolicyFile)
	return err == nil && policy.ExtensionCredentials != "denied"
}

// isCommand reports whether name selects a built-in command of root, which always wins over an
// extension.
func isCommand(root *cobra.Command, name string) bool {
	if name == "help" || name == "completion" {
		return true
	}
	return slices.ContainsFunc(root.Commands(), func(cmd *cobra.Command) bool {
		return cmd.Annotations["gt.alias"] != "true" && cmd.Annotations["gt.extension"] != "true" && (cmd.Name() == name || cmd.HasAlias(name))
	})
}

// extensionPolicy is what config.yml, the environment and the system policy allow extensions.
type extensionPolicy struct {
	// disabled names the setting that turned extensions off.
	disabled string
	// hosts and owners are allowlists that all must admit an extension's source.
	hosts, owners []extensionAllowlist
	// noCredentials names the setting that denies extensions credentials.
	noCredentials string
}

type extensionAllowlist struct {
	source  string
	entries []string
}

func (a *application) extensionPolicy() (extensionPolicy, error) {
	snapshot, err := a.settings()
	if err != nil {
		return extensionPolicy{}, err
	}
	system, err := settings.LoadPolicy(a.deps.PolicyFile)
	if err != nil {
		return extensionPolicy{}, err
	}
	var p extensionPolicy
	switch {
	case system.Extensions == "disabled":
		p.disabled = "the system policy " + system.Path
	case a.deps.Getenv("GITEA_CLI_NO_EXTENSIONS") != "":
		p.disabled = "GITEA_CLI_NO_EXTENSIONS"
	case snapshot.Preferences.Extensions == "disabled":
		p.disabled = "extensions in config.yml"
	}
	if system.ExtensionCredentials == "denied" {
		p.noCredentials = "the system policy " + system.Path
	}
	for _, list := range []struct {
		source        string
		hosts, owners []string
	}{{"config.yml", snapshot.Preferences.ExtensionHosts, snapshot.Preferences.ExtensionOwners}, {"the system policy " + system.Path, system.ExtensionHosts, system.ExtensionOwners}} {
		if len(list.hosts) > 0 {
			p.hosts = append(p.hosts, extensionAllowlist{list.source, list.hosts})
		}
		if len(list.owners) > 0 {
			p.owners = append(p.owners, extensionAllowlist{list.source, list.owners})
		}
	}
	return p, nil
}

// allows reports why extensions from owner on host are not allowed, or nil.
func (p extensionPolicy) allows(host, owner string) error {
	if p.disabled != "" {
		return fmt.Errorf("extensions are disabled by %s", p.disabled)
	}
	for _, list := range p.hosts {
		if !slices.Contains(list.entries, host) {
			return fmt.Errorf("extensions from %s are not allowed by extension_hosts in %s", host, list.source)
		}
	}
	for _, list := range p.owners {
		if !slices.ContainsFunc(list.entries, func(entry string) bool {
			allowedHost, allowedOwner, err := settings.ParseExtensionOwner(entry)
			return err == nil && allowedHost == host && strings.EqualFold(allowedOwner, owner)
		}) {
			return fmt.Errorf("extensions from %s/%s are not allowed by extension_owners in %s", host, owner, list.source)
		}
	}
	return nil
}

// allowsInstalled is allows for an installed extension; one developed locally has no source.
func (p extensionPolicy) allowsInstalled(extension extensions.Extension) error {
	if extension.Kind == extensions.KindLocal {
		if p.disabled != "" {
			return fmt.Errorf("extensions are disabled by %s", p.disabled)
		}
		if len(p.hosts)+len(p.owners) > 0 {
			return errors.New("local extensions are not allowed while extension_hosts or extension_owners are set")
		}
		return nil
	}
	return p.allows(extension.Host, extension.Owner)
}

// addExtensionCommands lists the installed extensions and their aliases the policy allows in help
// and completion, and returns the names of all extensions. Running one goes through runExtension
// before Cobra parses any flags.
func (a *application) addExtensionCommands(root *cobra.Command, userAliases map[string]string) map[string]bool {
	installed, _ := a.extensionStore().List()
	a.extensionAliases = resolveExtensionAliases(root, installed, userAliases)
	policy, policyErr := a.extensionPolicy()
	allowed := func(extension extensions.Extension) bool {
		return policyErr != nil || policy.allowsInstalled(extension) == nil
	}
	names := map[string]bool{}
	for _, extension := range installed {
		names[extension.Name] = true
		if isCommand(root, extension.Name) || slices.ContainsFunc(root.Commands(), func(c *cobra.Command) bool { return c.Name() == extension.Name }) || !allowed(extension) {
			continue
		}
		short := extension.Metadata.Description
		if short == "" {
			short = "Extension from " + extensionOrigin(extension.Manifest)
		}
		root.AddCommand(&cobra.Command{
			Use: extension.Name, Short: terminal.SafeLine(short), GroupID: "extension", DisableFlagParsing: true,
			Annotations: map[string]string{"gt.extension": "true"},
			RunE: func(*cobra.Command, []string) error {
				return clierr.Wrap(clierr.Usage, errors.New("extension must be run before parsing"))
			},
			ValidArgsFunction: func(cmd *cobra.Command, args []string, toComplete string) ([]cobra.Completion, cobra.ShellCompDirective) {
				return a.completeExtension(extension, append(slices.Clone(args), toComplete))
			},
		})
	}
	for _, name := range slices.Sorted(maps.Keys(a.extensionAliases)) {
		alias := a.extensionAliases[name]
		if !allowed(alias.extension) {
			continue
		}
		root.AddCommand(&cobra.Command{
			Use: name, Short: terminal.SafeLine(fmt.Sprintf("Alias for gt %s %s", alias.extension.Name, alias.extension.Metadata.Aliases[name])), GroupID: "extension", DisableFlagParsing: true,
			Annotations: map[string]string{"gt.extension": "true", "gt.extension-alias": "true"},
			RunE: func(*cobra.Command, []string) error {
				return clierr.Wrap(clierr.Usage, errors.New("extension must be run before parsing"))
			},
			ValidArgsFunction: func(cmd *cobra.Command, args []string, toComplete string) ([]cobra.Completion, cobra.ShellCompDirective) {
				return a.completeExtension(alias.extension, slices.Concat(alias.args, args, []string{toComplete}))
			},
		})
	}
	return names
}

// extensionAlias is the extension a top-level alias runs and the arguments it passes.
type extensionAlias struct {
	extension extensions.Extension
	args      []string
}

// resolveExtensionAliases returns the usable aliases of installed. Built-in commands, extension
// names and aliases the user set win; of two extensions the first by name does.
func resolveExtensionAliases(root *cobra.Command, installed []extensions.Extension, userAliases map[string]string) map[string]extensionAlias {
	resolved := map[string]extensionAlias{}
	for _, extension := range installed {
		for name := range extension.Metadata.Aliases {
			_, owned := resolved[name]
			_, userDefined := userAliases[name]
			if owned || userDefined || isCommand(root, name) || slices.ContainsFunc(installed, func(e extensions.Extension) bool { return e.Name == name }) {
				continue
			}
			if args, err := extension.Metadata.Alias(name); err == nil {
				resolved[name] = extensionAlias{extension, args}
			}
		}
	}
	return resolved
}

// dispatchExtension runs the extension args[at] names, or the one whose alias it is, unless a
// built-in command has that name, and reports whether it did.
func (a *application) dispatchExtension(ctx context.Context, root *cobra.Command, args []string, at int) (bool, error) {
	if at >= len(args) || isCommand(root, args[at]) {
		return false, nil
	}
	if alias, ok := a.extensionAliases[args[at]]; ok {
		return true, a.runExtension(ctx, root, alias.extension, args[:at], slices.Concat(alias.args, args[at+1:]))
	}
	extension, err := a.extensionStore().Lookup(args[at])
	if errors.Is(err, extensions.ErrNotInstalled) {
		return false, nil
	}
	if err != nil {
		return true, err
	}
	return true, a.runExtension(ctx, root, extension, args[:at], args[at+1:])
}

// extensionOrigin names where an extension came from: the repository URL, or a local directory.
func extensionOrigin(m extensions.Manifest) string {
	if m.Kind == extensions.KindLocal {
		return filepath.Dir(m.Path)
	}
	return strings.TrimSuffix(m.Host, "/") + "/" + m.Repository()
}

// runExtension runs extension with args; leading are the global flags before its name.
func (a *application) runExtension(ctx context.Context, root *cobra.Command, extension extensions.Extension, leading, args []string) error {
	set, dir, err := a.extensionFlagEnv(root, extension.Name, leading)
	if err != nil {
		return err
	}
	policy, err := a.extensionPolicy()
	if err != nil {
		return err
	}
	if err := policy.allowsInstalled(extension); err != nil {
		return fmt.Errorf("cannot run extension %s: %w", extension.Name, err)
	}
	if err := verifyExtension(extension); err != nil {
		return err
	}
	if extension.Credentials && policy.noCredentials == "" {
		credentials, err := a.extensionCredentials(ctx, extension)
		if err != nil {
			a.warn(root, "Could not pass your credentials to extension %s: %v", extension.Name, err)
		}
		maps.Copy(set, credentials)
	}
	command, err := a.prepareExtension(extension, args, set)
	if err != nil {
		return err
	}
	command.Dir = dir
	command.Stdin, command.Stdout, command.Stderr = a.deps.Streams.In, a.deps.Streams.Out, a.deps.Streams.ErrOut
	code, err := a.deps.RunExtension(command)
	if err != nil {
		return fmt.Errorf("run extension %s: %w", extension.Name, err)
	}
	if code != 0 {
		return clierr.Quiet(code)
	}
	return nil
}

// extensionFlagEnv turns the global flags before an extension's name into the variables of
// extensionFlags, and returns the working directory --cwd selects. Other global flags have no
// meaning for an extension.
func (a *application) extensionFlagEnv(root *cobra.Command, name string, leading []string) (map[string]string, string, error) {
	flags := root.PersistentFlags()
	if err := flags.Parse(leading); err != nil {
		return nil, "", clierr.Wrap(clierr.Usage, err)
	}
	set, dir := map[string]string{}, ""
	var unsupported []string
	flags.Visit(func(flag *pflag.Flag) {
		variable, ok := extensionFlags[flag.Name]
		switch {
		case !ok:
			unsupported = append(unsupported, "--"+flag.Name)
		case flag.Name == "cwd":
			dir = flag.Value.String()
		case variable != "":
			set[variable] = flag.Value.String()
		}
	})
	if len(unsupported) > 0 {
		return nil, "", clierr.Wrap(clierr.Usage, fmt.Errorf("extension %s cannot be combined with %s", name, strings.Join(unsupported, ", ")))
	}
	return set, dir, nil
}

// verifyExtension refuses an executable that changed since it was installed.
func verifyExtension(extension extensions.Extension) error {
	if extension.Kind == extensions.KindLocal {
		return nil
	}
	digest, err := extensions.FileSHA256(extension.Path())
	if err != nil {
		return fmt.Errorf("cannot read extension %s: %w; reinstall it with gt extension install --force %s", extension.Name, err, extensionOrigin(extension.Manifest))
	}
	if digest != extension.SHA256 {
		return fmt.Errorf("extension %s changed since it was installed; reinstall it with gt extension install --force %s", extension.Name, extensionOrigin(extension.Manifest))
	}
	return nil
}

// prepareExtension prepares running extension with args and the environment variables set, which
// it gets instead of gt's secrets.
func (a *application) prepareExtension(extension extensions.Extension, args []string, set map[string]string) (extensions.Command, error) {
	set["GITEA_CLI_EXTENSION"] = extension.Name
	if executable, err := a.deps.Executable(); err == nil {
		set["GITEA_CLI_EXECUTABLE"] = executable
	}
	env := a.deps.Environ()
	if extension.CompatGH {
		shims, err := a.ghShim()
		if err != nil {
			return extensions.Command{}, fmt.Errorf("prepare the gh shim for extension %s: %w", extension.Name, err)
		}
		// Windows names the variable Path and compares names without case.
		key, path := "PATH", ""
		for _, entry := range env {
			if name, value, _ := strings.Cut(entry, "="); name == "PATH" || (runtime.GOOS == "windows" && strings.EqualFold(name, "PATH")) {
				key, path = name, value
			}
		}
		set[key] = shims + string(os.PathListSeparator) + path
	}
	command := extensions.Command{Path: extension.Path(), Args: args, Env: extensions.Environ(env, set)}
	if extension.Kind != extensions.KindBinary {
		interpreter, err := extensions.Interpreter(command.Path, exec.LookPath)
		if err != nil {
			return command, err
		}
		command.Interpreter = interpreter
	}
	return command, nil
}

// extensionCredentials returns the variables that give a granted extension the selected account:
// GITEA_TOKEN with its instance, and GH_HOST and GH_TOKEN for a gh extension. It returns none
// without a stored token, such as for an SSH-signed account, and never prompts; the error says
// why the selected account's token could not be read.
func (a *application) extensionCredentials(ctx context.Context, extension extensions.Extension) (map[string]string, error) {
	set := map[string]string{}
	if token := a.deps.Getenv("GITEA_TOKEN"); token != "" {
		// GITEA_TOKEN belongs to the instance the environment names and to an account
		// gt cannot check without a request; pass it on only when --host selects that instance and
		// no account is named.
		host, key := a.envHost()
		if host == "" {
			return set, errors.New("GITEA_TOKEN requires GITEA_CLI_HOST or GITEA_INSTANCE_URL naming its instance")
		}
		snapshot, err := a.settings()
		if err != nil {
			return set, err
		}
		instance, err := settings.ResolveHost(host, snapshot.Hosts.Hosts)
		if err != nil {
			return set, fmt.Errorf("invalid %s: %w", key, err)
		}
		if a.options.host != "" {
			if selected, err := settings.ResolveHost(a.options.host, snapshot.Hosts.Hosts); err != nil || selected != instance {
				return set, nil
			}
		}
		if user, _ := a.selectedUser(); user != "" {
			return set, nil
		}
		set["GITEA_TOKEN"], set["GITEA_INSTANCE_URL"], set["GITEA_CLI_HOST"] = token, instance, instance
		for _, key := range []string{"GITEA_INSTANCE_INSECURE", "GITEA_INSTANCE_SSH_HOST"} {
			if value := a.deps.Getenv(key); value != "" {
				set[key] = value
			}
		}
	} else {
		t, err := a.resolveRequest(ctx, targetRequest{authenticate: true, offline: true, refresh: true})
		if err != nil || t.client == nil || t.client.Token == "" {
			return set, err
		}
		set["GITEA_TOKEN"], set["GITEA_INSTANCE_URL"] = t.client.Token, t.Host
		// The token belongs to this instance even when the default changes; --host already names it.
		if a.options.host == "" {
			set["GITEA_CLI_HOST"] = t.Host
		}
		if t.host.Insecure {
			set["GITEA_INSTANCE_INSECURE"] = "true"
		}
		if t.host.SSHHost != "" {
			set["GITEA_INSTANCE_SSH_HOST"] = t.host.SSHHost
		}
	}
	if extension.Prefix == extensions.PrefixGH {
		host := strings.TrimPrefix(strings.TrimPrefix(set["GITEA_INSTANCE_URL"], "https://"), "http://")
		set["GH_HOST"], set["GH_TOKEN"], set["GH_ENTERPRISE_TOKEN"] = host, set["GITEA_TOKEN"], set["GITEA_TOKEN"]
	}
	return set, nil
}

// completeExtension asks an extension that declares Cobra completion to complete args, the last
// of which is the word being completed. Its answer counts only when it has Cobra's format.
func (a *application) completeExtension(extension extensions.Extension, args []string) ([]cobra.Completion, cobra.ShellCompDirective) {
	none := cobra.ShellCompDirectiveNoFileComp
	if extension.Metadata.Completion != "cobra" {
		return nil, none
	}
	policy, err := a.extensionPolicy()
	if err != nil || policy.allowsInstalled(extension) != nil || verifyExtension(extension) != nil {
		return nil, none
	}
	command, err := a.prepareExtension(extension, slices.Concat([]string{cobra.ShellCompRequestCmd}, args), map[string]string{})
	if err != nil {
		return nil, none
	}
	var out bytes.Buffer
	command.Stdin, command.Stdout, command.Stderr = strings.NewReader(""), &out, nil
	if code, err := a.deps.RunExtension(command); err != nil || code != 0 {
		return nil, none
	}
	lines := strings.Split(strings.TrimRight(out.String(), "\n"), "\n")
	directive, err := strconv.Atoi(strings.TrimPrefix(lines[len(lines)-1], ":"))
	if !strings.HasPrefix(lines[len(lines)-1], ":") || err != nil {
		return nil, none
	}
	var completions []cobra.Completion
	for _, line := range lines[:len(lines)-1] {
		value, description, described := strings.Cut(line, "\t")
		if value = terminal.SafeLine(value); described {
			value = cobra.CompletionWithDesc(value, terminal.SafeLine(description))
		}
		completions = append(completions, value)
	}
	return completions, cobra.ShellCompDirective(directive)
}

// ghShim returns the directory holding gh, a link to gt that runs the gh commands extensions
// installed with --compat-gh call, creating or refreshing it when gt moved.
func (a *application) ghShim() (string, error) {
	executable, err := a.deps.Executable()
	if err != nil {
		return "", err
	}
	if resolved, err := filepath.EvalSymlinks(executable); err == nil {
		executable = resolved
	}
	dir := filepath.Join(a.deps.DataDir, "shims")
	name := "gh"
	if runtime.GOOS == "windows" {
		name += ".exe"
	}
	shim := filepath.Join(dir, name)
	if same(shim, executable) {
		return dir, nil
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", err
	}
	temp := shim + ".new"
	_ = os.Remove(temp)
	if err := os.Link(executable, temp); err != nil {
		// Hard links fail across file systems; a copy works anywhere.
		if err := copyFile(executable, temp); err != nil {
			return "", err
		}
	}
	return dir, os.Rename(temp, shim)
}

// same reports whether the files at a and b have the same content.
func same(a, b string) bool {
	infoA, errA := os.Stat(a)
	infoB, errB := os.Stat(b)
	if errA != nil || errB != nil {
		return false
	}
	if os.SameFile(infoA, infoB) {
		return true
	}
	digestA, errA := extensions.FileSHA256(a)
	digestB, errB := extensions.FileSHA256(b)
	return errA == nil && errB == nil && digestA == digestB
}

func copyFile(from, to string) error {
	data, err := os.ReadFile(from)
	if err != nil {
		return err
	}
	return os.WriteFile(to, data, 0o755)
}
