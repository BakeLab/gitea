// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package app

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"

	"gitea.dev/cli/internal/auth"
	"gitea.dev/cli/internal/clierr"
	"gitea.dev/cli/internal/git"
	"gitea.dev/cli/internal/services"
	"gitea.dev/cli/internal/settings"
	"gitea.dev/cli/internal/transport"
)

type target struct {
	Host       string `json:"host"`
	User       string `json:"user"`
	Repo       string `json:"repo"`
	HostSource string `json:"host_source"`
	UserSource string `json:"user_source"`
	RepoSource string `json:"repo_source"`
	client     *transport.Client
	// host holds the selected instance's settings with the GITEA_TOKEN environment overrides applied.
	host settings.Host
	// gitProtocol is the instance's git_protocol, else the git_protocol preference.
	gitProtocol string
}

// ownerName splits the selected OWNER/REPO repository.
func (t target) ownerName() (owner, name string) {
	owner, name, _ = strings.Cut(t.Repo, "/")
	return owner, name
}

// repoFromClone reports whether the repository was found in the working directory's clone.
func (t target) repoFromClone() bool {
	return strings.HasPrefix(t.RepoSource, "remote ") || strings.HasPrefix(t.RepoSource, "git config")
}

// owner returns the owner of the selected repository.
func (t target) owner() string {
	owner, _ := t.ownerName()
	return owner
}

var errRepositoryRequired = errors.New("repository required: use --repo owner/name")

// errEnvironmentAccount must not reveal which account the GITEA_TOKEN belongs to.
var errEnvironmentAccount = errors.New("GITEA_TOKEN does not belong to the selected account; supply its matching --user or remove the environment token to use stored credentials")

func (a *application) resolve(ctx context.Context, requireRepo bool) (target, error) {
	return a.resolveTarget(ctx, requireRepo, true)
}

// envHost returns the instance named by GITEA_CLI_HOST, else GITEA_INSTANCE_URL, and the variable
// that named it. A GITEA_TOKEN belongs to this instance.
func (a *application) envHost() (host, key string) {
	for _, key := range []string{"GITEA_CLI_HOST", "GITEA_INSTANCE_URL"} {
		if value := a.deps.Getenv(key); value != "" {
			return value, key
		}
	}
	return "", ""
}

// tokenInstance returns the instance a GITEA_TOKEN belongs to.
func (a *application) tokenInstance() string {
	host, _ := a.envHost()
	return host
}

// environmentHosts applies GITEA_INSTANCE_INSECURE and GITEA_INSTANCE_SSH_HOST to the instance
// of a GITEA_TOKEN login, without persisting them.
func (a *application) environmentHosts(hosts map[string]settings.Host) (map[string]settings.Host, error) {
	insecure, sshHost := a.deps.Getenv("GITEA_INSTANCE_INSECURE"), a.deps.Getenv("GITEA_INSTANCE_SSH_HOST")
	if a.deps.Getenv("GITEA_TOKEN") == "" || (insecure == "" && sshHost == "") {
		return hosts, nil
	}
	instance, err := settings.ResolveHost(a.tokenInstance(), hosts)
	if err != nil {
		return hosts, nil // The token's instance check reports this.
	}
	host := hosts[instance]
	if insecure != "" {
		if host.Insecure, err = strconv.ParseBool(insecure); err != nil {
			return nil, clierr.Wrap(clierr.Usage, errors.New("GITEA_INSTANCE_INSECURE must be true or false"))
		}
	}
	if sshHost != "" {
		if _, err := settings.SSHEndpoint(sshHost); err != nil {
			return nil, clierr.Wrap(clierr.Usage, fmt.Errorf("invalid GITEA_INSTANCE_SSH_HOST: %w", err))
		}
		host.SSHHost = sshHost
	}
	result := maps.Clone(hosts)
	if result == nil {
		result = map[string]settings.Host{}
	}
	result[instance] = host
	return result, nil
}

// targetRequest says what a command needs resolved.
type targetRequest struct {
	requireRepo, authenticate bool
	// repo names the repository a command argument selected, such as the repository of an
	// issue URL; it takes precedence over discovery and must agree with --repo.
	repo, repoSource string
	// offline authenticates without prompts, token refreshes or identity checks, as shell
	// completion needs: an expired OAuth token or an encrypted SSH key fails instead.
	offline bool
}

func (a *application) resolveTarget(ctx context.Context, requireRepo, authenticate bool) (target, error) {
	return a.resolveRequest(ctx, targetRequest{requireRepo: requireRepo, authenticate: authenticate})
}

func (a *application) resolveRequest(ctx context.Context, req targetRequest) (target, error) {
	t, err := a.connect(ctx, req)
	if err != nil {
		return t, err
	}
	// A bare repository NAME belongs to the account gt acts as, which is known only now.
	if t.Repo != "" && !strings.Contains(t.Repo, "/") {
		// A name that is also a directory here may be meant as that checkout, as in gt -R ./project.
		if info, statErr := os.Stat(filepath.Join(cmp.Or(a.options.cwd, "."), t.Repo)); statErr == nil && info.IsDir() && t.RepoSource == "--repo" {
			return t, clierr.Wrap(clierr.Usage, fmt.Errorf("%q is both a repository name and a directory here; use OWNER/%s for the repository, or --cwd %s for that checkout", t.Repo, t.Repo, t.Repo))
		}
		owner := cmp.Or(a.options.sudo, t.User)
		if owner == "" {
			return t, clierr.Wrap(clierr.Usage, fmt.Errorf("cannot tell who owns repository %q; use OWNER/%s", t.Repo, t.Repo))
		}
		t.Repo = owner + "/" + t.Repo
	}
	return t, nil
}

// connect selects the instance, account and repository and builds the authenticated client.
func (a *application) connect(ctx context.Context, req targetRequest) (target, error) {
	s, err := a.settings()
	if err != nil {
		return target{}, err
	}
	if s.Hosts.Hosts, err = a.environmentHosts(s.Hosts.Hosts); err != nil {
		return target{}, err
	}
	t := target{}
	rawRepo, err := a.repositorySource(ctx, &t, s, req)
	if err != nil {
		return t, err
	}
	if rawRepo != "" {
		if err := a.selectRepository(ctx, &t, s, rawRepo); err != nil {
			return t, err
		}
	}
	if err := a.selectHost(&t, s); err != nil {
		return t, err
	}
	host := s.Hosts.Hosts[t.Host]
	t.host = host
	t.gitProtocol = cmp.Or(host.GitProtocol, s.Preferences.GitProtocol)
	if err := a.selectAccount(ctx, &t, s, req.authenticate && !req.offline); err != nil {
		return t, err
	}
	if !req.authenticate {
		return t, nil
	}
	token := a.deps.Getenv("GITEA_TOKEN")
	environmentCredential := token != ""
	if token != "" {
		normalized, err := settings.ResolveHost(a.tokenInstance(), s.Hosts.Hosts)
		if err != nil || normalized != t.Host {
			return t, clierr.Wrap(clierr.Authentication, errors.New("GITEA_TOKEN requires GITEA_CLI_HOST or GITEA_INSTANCE_URL matching the selected instance"))
		}
	} else {
		account, exists := host.Users[t.User]
		if !exists {
			return t, clierr.Wrap(clierr.Authentication, t.accountError(t.User))
		}
		if account.Tokenless() {
			passphrase := a.sshPassphrase(ctx, a.policy(s), account.SSHKey)
			if req.offline {
				passphrase = nil
			}
			signer, err := auth.SignerForAccount(account, passphrase)
			if err != nil {
				return t, clierr.Wrap(clierr.Authentication, err)
			}
			client := a.deps.Client(t.Host, "", host.Insecure)
			client.Signer, client.Sudo = signer, a.options.sudo
			t.client = client
			return t, nil
		}
		credential, err := a.loadCredential(ctx, t, account, req.offline)
		if err != nil {
			return t, clierr.Wrap(clierr.Authentication, err)
		}
		token = credential.AccessToken
	}
	t.client = a.deps.Client(t.Host, token, host.Insecure)
	if environmentCredential && !req.offline {
		actual, err := services.CurrentAccount(ctx, t.client)
		if err != nil {
			return t, err
		}
		// Like GH_TOKEN, the token overrides the stored credentials whichever account is active;
		// only an account that --user or GITEA_CLI_USER names must match. The message must not reveal
		// which account the token belongs to.
		if t.explicitUser() && !services.MatchAccount(t.User, actual) {
			return t, clierr.Wrap(clierr.Authentication, errEnvironmentAccount)
		}
		if !t.explicitUser() {
			t.UserSource = "GITEA_TOKEN authenticated account"
		}
		t.User = actual
	}
	// Set after the environment-token identity check, which must see the authenticated account.
	t.client.Sudo = a.options.sudo
	return t, nil
}

// loadCredential loads the account's stored credential, refreshing an expiring OAuth token unless
// offline, when an expired one fails instead.
func (a *application) loadCredential(ctx context.Context, t target, account settings.Account, offline bool) (auth.Credential, error) {
	if account.AuthMethod != settings.AuthOAuth || offline {
		credential, err := a.deps.Credentials.Load(t.Host, t.User, account.CredentialStore)
		if err == nil && !credential.ExpiresAt.IsZero() && !a.deps.Now().Before(credential.ExpiresAt) {
			err = errors.New("access token expired")
		}
		return credential, err
	}
	manager, err := auth.NewManager(a.deps.Settings, a.deps.Credentials)
	if err != nil {
		return auth.Credential{}, err
	}
	return manager.Refresh(ctx, t.Host, t.User, a.deps.Client(t.Host, "", t.host.Insecure).HTTP, false, a.deps.Now())
}

// repositorySource returns the raw repository the command targets and records its source: an
// argument's repository, --repo, GITEA_CLI_REPO, the saved gt.repository, or a discovered Git remote.
func (a *application) repositorySource(ctx context.Context, t *target, s settings.Snapshot, req targetRequest) (string, error) {
	if req.repo != "" {
		if err := a.agreesWithRepoFlag(req, s.Hosts.Hosts); err != nil {
			return "", err
		}
		if a.options.repo != "" && !a.namesInstance(req.repo) && a.namesInstance(a.options.repo) {
			// The argument agrees with --repo and only lacks its instance.
			t.RepoSource = "--repo"
			return a.options.repo, nil
		}
		t.RepoSource = req.repoSource
		return req.repo, nil
	}
	if a.options.repo != "" {
		t.RepoSource = "--repo"
		return a.options.repo, nil
	}
	if raw := a.deps.Getenv("GITEA_CLI_REPO"); raw != "" {
		t.RepoSource = "GITEA_CLI_REPO"
		return raw, nil
	}
	if !req.requireRepo {
		return "", nil
	}
	// Discovery probes fail outside a checkout; Git's diagnostics would only precede gt's own error.
	probe := func(args ...string) (string, error) { return a.gitProbe().Output(ctx, a.options.cwd, false, args...) }
	if saved, err := probe("config", "--get", "gt.repository"); err == nil && saved != "" {
		t.RepoSource = "git config gt.repository"
		return saved, nil
	}
	remote := cmp.Or(a.options.remote, s.Preferences.Remote)
	hosts, err := a.repositoryHosts(s.Hosts.Hosts)
	if err != nil {
		return "", err
	}
	urls := map[string]string{}
	// Only remotes on an instance gt can act on name a repository, so that a clone whose origin is
	// on another forge still finds its Gitea remote.
	repository := func(name string) string {
		raw, err := probe("remote", "get-url", name)
		if err != nil {
			return ""
		}
		urls[name] = raw
		instance, slug, err := settings.RepositoryURL(raw, hosts)
		if err != nil || !a.canActOn(instance, hosts) {
			return ""
		}
		return instance + "/" + strings.ToLower(slug)
	}
	remote, err = git.DiscoverRemote(ctx, a.gitProbe(), a.options.cwd, remote, repository)
	if errors.Is(err, git.ErrAmbiguousRemote) {
		return "", clierr.Wrap(clierr.Usage, err)
	}
	if err != nil {
		return "", clierr.Wrap(clierr.Usage, errRepositoryRequired)
	}
	raw, known := urls[remote]
	if !known {
		if raw, err = probe("remote", "get-url", remote); err != nil {
			return "", fmt.Errorf("cannot read the URL of Git remote %q: %w", remote, err)
		}
	}
	t.RepoSource = "remote " + remote
	return raw, nil
}

// canActOn reports whether gt can use a discovered remote on instance: it is the --host instance,
// else the GITEA_TOKEN instance while that token is set (connect then ignores stored accounts), else
// an instance with an account.
func (a *application) canActOn(instance string, hosts map[string]settings.Host) bool {
	selected := a.options.host
	if selected == "" && a.deps.Getenv("GITEA_TOKEN") != "" {
		selected = a.tokenInstance()
	}
	if selected != "" {
		resolved, err := settings.ResolveHost(selected, hosts)
		return err == nil && resolved == instance
	}
	return len(hosts[instance].Users) > 0
}

// repositoryHosts returns the instances a repository URL may name: with --host, only that one when
// it is configured.
func (a *application) repositoryHosts(configured map[string]settings.Host) (map[string]settings.Host, error) {
	if a.options.host == "" {
		return configured, nil
	}
	explicit, err := settings.ResolveHost(a.options.host, configured)
	if err != nil {
		return nil, clierr.Wrap(clierr.Usage, err)
	}
	if host, exists := configured[explicit]; exists {
		return map[string]settings.Host{explicit: host}, nil
	}
	return configured, nil
}

// agreesWithRepoFlag rejects an argument naming another repository than --repo.
func (a *application) agreesWithRepoFlag(req targetRequest, hosts map[string]settings.Host) error {
	if a.options.repo == "" {
		return nil
	}
	selected, selectedErr := settings.ParseRepository(req.repo, hosts)
	explicit, explicitErr := settings.ParseRepository(a.options.repo, hosts)
	if explicitErr != nil {
		return clierr.Wrap(clierr.Usage, explicitErr) // The argument's repository replaces --repo, so nothing else parses it.
	}
	if selectedErr != nil {
		return nil // Resolution reports the invalid argument.
	}
	sameHost := selected.Host == "" || explicit.Host == "" || selected.Host == explicit.Host
	sameRepo := strings.EqualFold(selected.Name, explicit.Name) && (explicit.Owner == "" || strings.EqualFold(selected.Owner, explicit.Owner))
	if sameHost && sameRepo {
		return nil
	}
	return clierr.Wrap(clierr.Usage, fmt.Errorf("%s points to %s but --repo is %s", req.repoSource, selected.Slug(), a.options.repo))
}

// selectRepository parses raw into the target's repository and, for URLs and HOST/OWNER/REPO,
// its instance. With --host, URLs match only that instance.
func (a *application) selectRepository(ctx context.Context, t *target, s settings.Snapshot, raw string) error {
	configured, err := a.repositoryHosts(s.Hosts.Hosts)
	if err != nil {
		return err
	}
	ref, err := settings.ParseRepository(raw, configured)
	if err != nil {
		return clierr.Wrap(clierr.Usage, err)
	}
	if ref.Owner == "" && ref.Host == "" && a.isGitRemote(ctx, ref.Name) {
		return clierr.Wrap(clierr.Usage, fmt.Errorf("invalid repository %q: use OWNER/REPO, HOST/OWNER/REPO or a URL; did you mean --remote %s?", raw, ref.Name))
	}
	t.Repo = ref.Slug()
	if ref.Host != "" {
		t.Host, t.HostSource = ref.Host, t.RepoSource
	}
	return nil
}

// isGitRemote reports whether name is a remote of the working directory's Git checkout.
func (a *application) isGitRemote(ctx context.Context, name string) bool {
	remotes, err := a.gitProbe().Output(ctx, a.options.cwd, false, "remote")
	return err == nil && slices.Contains(strings.Fields(remotes), name)
}

// selectHost picks the instance: the repository's, --host, GITEA_CLI_HOST or GITEA_INSTANCE_URL,
// default_host, or the only configured instance.
func (a *application) selectHost(t *target, s settings.Snapshot) error {
	if a.options.host != "" {
		explicit, err := settings.ResolveHost(a.options.host, s.Hosts.Hosts)
		if err != nil {
			return clierr.Wrap(clierr.Usage, err)
		}
		if t.Host != "" && t.Host != explicit {
			return clierr.Wrap(clierr.Usage, fmt.Errorf("--host %s conflicts with %s from %s", explicit, t.Host, t.HostSource))
		}
		t.Host, t.HostSource = explicit, "--host"
	}
	return a.defaultHost(t, s)
}

// defaultHost picks the instance of a target that neither the repository nor --host named.
func (a *application) defaultHost(t *target, s settings.Snapshot) error {
	if value, key := a.envHost(); t.Host == "" && value != "" {
		host, err := settings.ResolveHost(value, s.Hosts.Hosts)
		if err != nil {
			return clierr.Wrap(clierr.Usage, fmt.Errorf("invalid %s: %w", key, err))
		}
		t.Host, t.HostSource = host, key
	}
	if t.Host == "" && s.Preferences.DefaultHost != "" {
		t.Host, t.HostSource = s.Preferences.DefaultHost, "config.yml default_host"
	}
	if t.Host == "" && len(s.Hosts.Hosts) == 1 {
		for host := range s.Hosts.Hosts {
			t.Host, t.HostSource = host, "sole configured instance"
		}
	}
	if t.Host == "" {
		if len(s.Hosts.Hosts) == 0 {
			return clierr.Wrap(clierr.Usage, errors.New("no instance selected: log in with gt auth login, or pass --host"))
		}
		return clierr.Wrap(clierr.Usage, fmt.Errorf("instance required: select --host or set default_host (instances: %s)", strings.Join(slices.Sorted(maps.Keys(s.Hosts.Hosts)), ", ")))
	}
	return nil
}

// selectAccount picks the account: --user or GITEA_CLI_USER, the instance's active account, its only
// account, or an interactive choice.
func (a *application) selectAccount(ctx context.Context, t *target, s settings.Snapshot, authenticate bool) error {
	users := t.host.Users
	t.User, t.UserSource = a.selectedUser()
	if matched, ok := findAccountKey(users, t.User); ok {
		t.User = matched
	}
	if t.User == "" {
		t.User, t.UserSource = t.host.User, "active instance account"
	}
	if t.User != "" || len(users) == 0 {
		return nil
	}
	if len(users) == 1 {
		for user := range users {
			t.User, t.UserSource = user, "sole configured account"
		}
		return nil
	}
	if !authenticate || a.deps.Getenv("GITEA_TOKEN") != "" || !a.policy(s).CanPrompt(a.deps.Streams) || a.deps.Prompter == nil {
		return nil
	}
	selected, err := a.deps.Prompter.Ask(ctx, "account ("+accountNames(users)+")", nil)
	if err != nil {
		return err
	}
	matched, ok := findAccountKey(users, strings.TrimSpace(selected))
	if !ok {
		return clierr.Wrap(clierr.Usage, t.accountError(strings.TrimSpace(selected)))
	}
	t.User, t.UserSource = matched, "interactive account selection"
	return nil
}

// explicitUser reports whether the user named the account with --user or GITEA_CLI_USER.
func (t target) explicitUser() bool {
	return t.User != "" && (t.UserSource == "--user" || t.UserSource == "GITEA_CLI_USER")
}

// accountError explains why user has no stored credential on the target's instance.
func (t target) accountError(user string) error {
	users := t.host.Users
	switch {
	case user != "" && len(users) > 0:
		return fmt.Errorf("account %q is not logged in on %s (accounts: %s)", user, t.Host, accountNames(users))
	case user != "":
		return fmt.Errorf("account %q is not logged in on %s; run gt auth login --host %s", user, t.Host, t.Host)
	case len(users) > 0:
		return fmt.Errorf("no active account on %s (accounts: %s); pass --user or run gt auth switch", t.Host, accountNames(users))
	}
	text := "no account for " + t.Host
	if t.HostSource != "--host" {
		text += " (instance from " + describeSource(t.HostSource) + ")"
	}
	// --host cannot override the instance that the repository names.
	if t.RepoSource != "" && t.HostSource == t.RepoSource {
		return errors.New(text + "; run gt auth login --host " + t.Host + ", or use -R to target another repository")
	}
	return errors.New(text + "; run gt auth login --host " + t.Host + ", or use -R or --host to target another instance")
}

// describeSource words a target source for messages, such as git remote "origin".
func describeSource(source string) string {
	if remote, ok := strings.CutPrefix(source, "remote "); ok {
		return "git remote " + strconv.Quote(remote)
	}
	return source
}

func accountNames(users map[string]settings.Account) string {
	return strings.Join(slices.Sorted(maps.Keys(users)), ", ")
}

// selectedUser returns the account named by --user or GITEA_CLI_USER, and which one named it.
func (a *application) selectedUser() (string, string) {
	if a.options.user != "" {
		return a.options.user, "--user"
	}
	return a.deps.Getenv("GITEA_CLI_USER"), "GITEA_CLI_USER"
}

// findAccountKey returns the configured account name that matches selected, ignoring case.
func findAccountKey(users map[string]settings.Account, selected string) (string, bool) {
	if selected == "" {
		return "", false
	}
	if _, ok := users[selected]; ok {
		return selected, true
	}
	for name := range users {
		if strings.EqualFold(name, selected) {
			return name, true
		}
	}
	return "", false
}
