// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package app

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"maps"
	"sort"
	"strconv"
	"strings"

	"gitea.dev/tea/internal/auth"
	"gitea.dev/tea/internal/clierr"
	"gitea.dev/tea/internal/git"
	"gitea.dev/tea/internal/services"
	"gitea.dev/tea/internal/settings"
	"gitea.dev/tea/internal/transport"
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

var errRepositoryRequired = errors.New("repository required: use --repo owner/name")

func (a *application) resolve(ctx context.Context, requireRepo bool) (target, error) {
	return a.resolveTarget(ctx, requireRepo, true)
}

// tokenInstance returns the instance a GITEA_TOKEN belongs to: GITEA_INSTANCE_URL, else TEA_HOST.
func (a *application) tokenInstance() string {
	if instance := a.deps.Getenv("GITEA_INSTANCE_URL"); instance != "" {
		return instance
	}
	return a.deps.Getenv("TEA_HOST")
}

// environmentHosts applies GITEA_INSTANCE_INSECURE and GITEA_INSTANCE_SSH_HOST to the instance
// of a GITEA_TOKEN login, without persisting them.
func (a *application) environmentHosts(hosts map[string]settings.Host) (map[string]settings.Host, error) {
	insecure, sshHost := a.deps.Getenv("GITEA_INSTANCE_INSECURE"), a.deps.Getenv("GITEA_INSTANCE_SSH_HOST")
	if a.deps.Getenv("GITEA_TOKEN") == "" || (insecure == "" && sshHost == "") {
		return hosts, nil
	}
	instance, err := settings.NormalizeHost(a.tokenInstance())
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

func (a *application) resolveTarget(ctx context.Context, requireRepo, authenticate bool) (target, error) {
	s, err := a.deps.Settings.Load()
	if err != nil {
		return target{}, err
	}
	if s.Hosts.Hosts, err = a.environmentHosts(s.Hosts.Hosts); err != nil {
		return target{}, err
	}
	t := target{}
	rawRepo := a.options.repo
	if rawRepo != "" {
		t.RepoSource = "--repo"
	} else if rawRepo = a.deps.Getenv("TEA_REPO"); rawRepo != "" {
		t.RepoSource = "TEA_REPO"
	}
	if rawRepo == "" && requireRepo {
		// Discovery probes fail outside a checkout; Git's diagnostics would only precede tea's own error.
		probe := func(args ...string) (string, error) { return a.gitProbe().Output(ctx, a.options.cwd, false, args...) }
		if saved, saveErr := probe("config", "--get", "tea.repository"); saveErr == nil && saved != "" {
			rawRepo = saved
			t.RepoSource = "git config tea.repository"
		} else {
			remote := a.options.remote
			if remote == "" {
				remote = s.Preferences.Remote
			}
			remote, err = git.DiscoverRemote(ctx, a.gitProbe(), a.options.cwd, remote)
			if errors.Is(err, git.ErrAmbiguousRemote) {
				return t, clierr.Wrap(clierr.Usage, err)
			}
			if err != nil {
				return t, clierr.Wrap(clierr.Usage, errRepositoryRequired)
			}
			rawRepo, err = probe("remote", "get-url", remote)
			if err != nil {
				return t, fmt.Errorf("cannot read the URL of Git remote %q: %w", remote, err)
			}
			t.RepoSource = "remote " + remote
		}
	}
	if rawRepo != "" {
		if settings.ValidSlug(rawRepo) {
			t.Repo = rawRepo
		} else {
			configured := s.Hosts.Hosts
			if a.options.host != "" {
				explicit, normalizeErr := settings.NormalizeHost(a.options.host)
				if normalizeErr != nil {
					return t, clierr.Wrap(clierr.Usage, normalizeErr)
				}
				if host, exists := configured[explicit]; exists {
					configured = map[string]settings.Host{explicit: host}
				}
			}
			t.Host, t.Repo, err = settings.RepositoryURL(rawRepo, configured)
			if err != nil {
				return t, clierr.Wrap(clierr.Usage, err)
			}
			t.HostSource = t.RepoSource
		}
	}
	if a.options.host != "" {
		explicit, err := settings.NormalizeHost(a.options.host)
		if err != nil {
			return t, clierr.Wrap(clierr.Usage, err)
		}
		if t.Host != "" && t.Host != explicit {
			return t, clierr.Wrap(clierr.Usage, errors.New("--host conflicts with repository instance"))
		}
		t.Host, t.HostSource = explicit, "--host"
	}
	if t.Host == "" {
		for _, key := range []string{"TEA_HOST", "GITEA_INSTANCE_URL"} {
			if value := a.deps.Getenv(key); value != "" {
				t.Host, err = settings.NormalizeHost(value)
				if err != nil {
					return t, clierr.Wrap(clierr.Usage, err)
				}
				t.HostSource = key
				break
			}
		}
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
		return t, clierr.Wrap(clierr.Usage, errors.New("instance required: select --host or set default_host"))
	}
	host := s.Hosts.Hosts[t.Host]
	t.host = host
	t.gitProtocol = cmp.Or(host.GitProtocol, s.Preferences.GitProtocol)
	t.User, t.UserSource = a.selectedUser()
	if matched, ok := matchAccount(host.Users, t.User); ok {
		t.User = matched
	}
	if t.User == "" {
		t.User, t.UserSource = host.User, "active instance account"
	}
	if t.User == "" {
		switch len(host.Users) {
		case 1:
			for user := range host.Users {
				t.User, t.UserSource = user, "sole configured account"
			}
		case 0:
		default:
			if authenticate && a.deps.Getenv("GITEA_TOKEN") == "" && a.policy(s).CanPrompt(a.deps.Streams) && a.deps.Prompter != nil {
				names := make([]string, 0, len(host.Users))
				for user := range host.Users {
					names = append(names, user)
				}
				sort.Strings(names)
				selected, err := a.deps.Prompter.Input(ctx, "account ("+strings.Join(names, ", ")+")")
				if err != nil {
					return t, err
				}
				selected = strings.TrimSpace(selected)
				matched, ok := matchAccount(host.Users, selected)
				if !ok {
					return t, clierr.Wrap(clierr.Usage, fmt.Errorf("account %q is not configured on %s", selected, t.Host))
				}
				t.User, t.UserSource = matched, "interactive account selection"
			}
		}
	}
	if !authenticate {
		return t, nil
	}
	token := a.deps.Getenv("GITEA_TOKEN")
	environmentCredential := token != ""
	if token != "" {
		normalized, err := settings.NormalizeHost(a.tokenInstance())
		if err != nil || normalized != t.Host {
			return t, clierr.Wrap(clierr.Authentication, errors.New("GITEA_TOKEN requires GITEA_INSTANCE_URL or TEA_HOST matching the selected instance"))
		}
	} else {
		account, exists := host.Users[t.User]
		if !exists {
			return t, clierr.Wrap(clierr.Authentication, fmt.Errorf("no selected account on %s; run tea auth login or tea auth switch", t.Host))
		}
		if account.Tokenless() {
			signer, err := auth.SignerForAccount(account, a.sshPassphrase(ctx, a.policy(s), account.SSHKey))
			if err != nil {
				return t, clierr.Wrap(clierr.Authentication, err)
			}
			client := a.deps.Client(t.Host, "", host.Insecure)
			client.Signer, client.Sudo = signer, a.options.sudo
			t.client = client
			return t, nil
		}
		credential, err := a.deps.Credentials.Load(t.Host, t.User, account.CredentialStore)
		if err != nil {
			return t, clierr.Wrap(clierr.Authentication, err)
		}
		if account.AuthMethod == settings.AuthOAuth {
			manager, err := auth.NewManager(a.deps.Settings, a.deps.Credentials)
			if err != nil {
				return t, err
			}
			credential, err = manager.Refresh(ctx, t.Host, t.User, a.deps.Client(t.Host, "", host.Insecure).HTTP, false, a.deps.Now())
			if err != nil {
				return t, clierr.Wrap(clierr.Authentication, err)
			}
		}
		token = credential.AccessToken
	}
	t.client = a.deps.Client(t.Host, token, host.Insecure)
	if environmentCredential {
		actual, err := services.CurrentAccount(ctx, t.client)
		if err != nil {
			return t, err
		}
		// The message must not reveal which account the token belongs to.
		if !services.MatchAccount(t.User, actual) {
			return t, clierr.Wrap(clierr.Authentication, errors.New("GITEA_TOKEN does not belong to the selected account; supply its matching --user or remove the environment token to use stored credentials"))
		}
		if t.User == "" {
			t.UserSource = "GITEA_TOKEN authenticated account"
		}
		t.User = actual
	}
	// Set after the environment-token identity check, which must see the authenticated account.
	t.client.Sudo = a.options.sudo
	return t, nil
}

// selectedUser returns the account named by --user or TEA_USER, and which one named it.
func (a *application) selectedUser() (string, string) {
	if a.options.user != "" {
		return a.options.user, "--user"
	}
	return a.deps.Getenv("TEA_USER"), "TEA_USER"
}

func matchAccount(users map[string]settings.Account, selected string) (string, bool) {
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
