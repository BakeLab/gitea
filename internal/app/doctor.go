// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package app

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"gitea.dev/cli/internal/clierr"
	"gitea.dev/cli/internal/git"
	"gitea.dev/cli/internal/services"
	"gitea.dev/cli/internal/settings"
	"gitea.dev/cli/internal/terminal"
	"github.com/spf13/cobra"
)

// Outcomes of a doctor check: only failed checks make gt doctor fail.
const (
	checkPassed  = "ok"
	checkWarning = "warning"
	checkFailed  = "failed"
	checkSkipped = "skipped"
)

// doctorCheck is one diagnosis of gt doctor, with the command that fixes a problem.
type doctorCheck struct {
	Name   string `json:"name"`
	Status string `json:"status"`
	Detail string `json:"detail"`
	Fix    string `json:"fix,omitempty"`
}

// doctorReport is the instance, account and repository that commands select here, and the checks
// of each.
type doctorReport struct {
	Host       string        `json:"host"`
	User       string        `json:"user"`
	Repo       string        `json:"repo"`
	HostSource string        `json:"host_source"`
	UserSource string        `json:"user_source"`
	RepoSource string        `json:"repo_source"`
	Checks     []doctorCheck `json:"checks"`
}

// doctorTimeout bounds each request of a check.
const doctorTimeout = 10 * time.Second

func (a *application) doctorCommand() *cobra.Command {
	return &cobra.Command{
		Use: "doctor", Short: "Check gt's setup for this directory", Args: argsExact(0),
		Long: `Check the setup that commands in this directory use: the settings files, the instance
and whether it answers, the account and its credential, the repository, and Git. Each
problem comes with the command that fixes it. gt doctor fails when a check fails.`,
		Example: "$ gt doctor\n# Check another repository and instance\n$ gt doctor -R gitea.com/gitea/cli\n$ gt doctor --jq '.checks[] | select(.status == \"failed\")'",
		RunE: func(cmd *cobra.Command, _ []string) error {
			report := a.diagnose(cmd.Context())
			failed := 0
			for _, check := range report.Checks {
				if check.Status == checkFailed {
					failed++
				}
			}
			var err error
			if a.options.json != "" {
				err = a.writeJSON(cmd, report)
			} else {
				err = a.writeHuman(cmd, doctorTable(report.Checks, a.display()))
			}
			if err == nil && failed > 0 {
				err = fmt.Errorf("%d of %d checks failed", failed, len(report.Checks))
			}
			return err
		},
	}
}

// diagnose resolves the target like other commands, but reports each problem instead of stopping
// at the first.
func (a *application) diagnose(ctx context.Context) doctorReport {
	var report doctorReport
	add := func(check doctorCheck) { report.Checks = append(report.Checks, check) }
	snapshot, err := a.deps.Settings.Load()
	if err != nil {
		add(doctorCheck{"Config", checkFailed, err.Error(), "repair or move the file; gt config path prints both files"})
		add(a.gitCheck(ctx, ""))
		return report
	}
	add(doctorCheck{"Config", checkPassed, "config.yml and hosts.yml in " + a.deps.Settings.Dir, ""})
	t, err := a.resolveTarget(ctx, true, false)
	repoErr := err
	if err != nil && clierr.ExitCode(err) == clierr.Usage && a.options.repo == "" && a.deps.Getenv("GITEA_CLI_REPO") == "" {
		t, err = a.resolveTarget(ctx, false, false)
	}
	report.Host, report.User, report.Repo = t.Host, t.User, t.Repo
	report.HostSource, report.UserSource, report.RepoSource = t.HostSource, t.UserSource, t.RepoSource
	if t.Host == "" {
		add(doctorCheck{"Instance", checkFailed, Explain(ctx, a.deps, err).Error(), "gt auth login --host URL, or pass --host"})
		add(a.gitCheck(ctx, ""))
		return report
	}
	instance := a.instanceCheck(ctx, t)
	add(instance)
	authenticated, accountErr := a.resolveTarget(ctx, t.Repo != "", true)
	switch {
	case instance.Status == checkFailed:
		accountErr = errors.New("instance does not answer")
		detail := "not checked while the instance does not answer"
		if t.User != "" {
			detail = t.User + ": " + detail
		}
		add(doctorCheck{"Account", checkSkipped, detail, ""})
	case accountErr == nil:
		report.User, report.UserSource = authenticated.User, authenticated.UserSource
		fallthrough
	default:
		add(a.accountCheck(ctx, t, snapshot, authenticated, accountErr))
	}
	switch {
	case t.Repo == "":
		detail := "none: not in a clone of a repository on " + t.Host
		if repoErr != nil && !strings.HasPrefix(repoErr.Error(), errRepositoryRequired.Error()) {
			detail = repoErr.Error()
		}
		add(doctorCheck{"Repository", checkSkipped, detail, ""})
	case accountErr != nil:
		add(doctorCheck{"Repository", checkSkipped, t.Repo + " (" + describeSource(t.RepoSource) + "); not checked without a working account", ""})
	default:
		add(a.repositoryCheck(ctx, authenticated))
	}
	add(a.gitCheck(ctx, t.Host))
	if check, ok := sshCheck(t); ok {
		add(check)
	}
	return report
}

// instanceCheck asks the instance for its version.
func (a *application) instanceCheck(ctx context.Context, t target) doctorCheck {
	ctx, cancel := context.WithTimeout(ctx, doctorTimeout)
	defer cancel()
	start := time.Now()
	version, err := services.ServerVersion(ctx, a.deps.Client(t.Host, "", t.host.Insecure))
	where := t.Host + " (" + describeSource(t.HostSource) + ")"
	if err != nil {
		return doctorCheck{"Instance", checkFailed, where + ": " + Explain(ctx, a.deps, err).Error(), "check --host, the instance URL and your network"}
	}
	return doctorCheck{"Instance", checkPassed, fmt.Sprintf("%s · Gitea %s · %dms", where, version, max(time.Since(start).Milliseconds(), 1)), ""}
}

// accountCheck reports whether the account authenticates, and the scopes of its token.
func (a *application) accountCheck(ctx context.Context, t target, snapshot settings.Snapshot, authenticated target, err error) doctorCheck {
	login := "gt auth login --host " + t.Host
	if a.deps.Getenv("GITEA_TOKEN") != "" && err != nil {
		instance, resolveErr := settings.ResolveHost(a.tokenInstance(), snapshot.Hosts.Hosts)
		switch {
		case a.tokenInstance() == "" || resolveErr != nil:
			return doctorCheck{"Account", checkFailed, "GITEA_TOKEN is set, but neither GITEA_INSTANCE_URL nor GITEA_CLI_HOST names its instance", "export GITEA_INSTANCE_URL=" + t.Host}
		case instance != t.Host:
			return doctorCheck{"Account", checkFailed, "GITEA_TOKEN belongs to " + instance + ", but the instance here is " + t.Host + " (" + describeSource(t.HostSource) + ")", "pass --host " + instance + " or -R a repository there, or unset GITEA_TOKEN"}
		}
	}
	if err != nil {
		detail := Explain(ctx, a.deps, err).Error()
		if strings.Contains(detail, login) {
			login = ""
		}
		return doctorCheck{"Account", checkFailed, detail, login}
	}
	ctx, cancel := context.WithTimeout(ctx, doctorTimeout)
	defer cancel()
	user, err := services.CurrentAccount(ctx, authenticated.client)
	if err != nil {
		return doctorCheck{"Account", checkFailed, authenticated.User + ": " + Explain(ctx, a.deps, err).Error(), login}
	}
	detail := user + " (" + describeSource(authenticated.UserSource) + ") · credential valid"
	if scopes, err := services.TokenScopes(ctx, authenticated.client); err == nil && len(scopes) > 0 {
		detail += " · scopes: " + strings.Join(scopes, ", ")
	}
	return doctorCheck{"Account", checkPassed, detail, ""}
}

// repositoryCheck reports whether the account can read the repository.
func (a *application) repositoryCheck(ctx context.Context, t target) doctorCheck {
	ctx, cancel := context.WithTimeout(ctx, doctorTimeout)
	defer cancel()
	where := t.Repo + " (" + describeSource(t.RepoSource) + ")"
	repo, err := services.Repositories{Client: t.client}.View(ctx, t.Repo)
	if err != nil {
		return doctorCheck{"Repository", checkFailed, where + ": " + Explain(ctx, a.deps, err).Error(), "check the remote URL, or pass -R OWNER/REPO"}
	}
	return doctorCheck{"Repository", checkPassed, where + " · default branch " + repo.DefaultBranch, ""}
}

// gitCheck reports the installed Git and whether it asks gt for the credentials of instance.
func (a *application) gitCheck(ctx context.Context, instance string) doctorCheck {
	version, err := a.gitProbe().Output(ctx, a.options.cwd, false, "--version")
	switch {
	case err != nil:
		return doctorCheck{"Git", checkFailed, "Git is not installed or cannot run", "install Git"}
	case instance == "":
		return doctorCheck{"Git", checkPassed, version, ""}
	}
	if scope := git.HelperScope(ctx, a.gitProbe(), a.options.cwd, instance); scope != "" {
		return doctorCheck{"Git", checkPassed, version + " · gt is the credential helper (" + scope + ")", ""}
	}
	return doctorCheck{"Git", checkWarning, version + " · credential helper not configured for " + instance, "gt auth setup-git, if pushes over HTTPS should use this account"}
}

// sshCheck reports where SSH remotes connect when the instance uses SSH.
func sshCheck(t target) (doctorCheck, bool) {
	switch {
	case t.host.SSHHost != "":
		return doctorCheck{"SSH", checkPassed, "remotes connect to " + t.host.SSHHost + " (ssh_host)", ""}, true
	case t.gitProtocol == "ssh":
		return doctorCheck{"SSH", checkWarning, "remotes connect to the web host on port 22", "gt config set --host " + t.Host + " ssh_host HOST:PORT, if SSH listens elsewhere"}, true
	}
	return doctorCheck{}, false
}

// doctorTable lists the checks: on a terminal with an icon per outcome and the fix under a problem,
// otherwise as tab-separated status, name, detail and fix.
func doctorTable(checks []doctorCheck, d terminal.Display) string {
	icons := map[string]terminal.Cell{
		checkPassed:  {Text: "✓", Color: "green"},
		checkWarning: {Text: "!", Color: "yellow"},
		checkFailed:  {Text: "X", Color: "red"},
		checkSkipped: {Text: "-", Muted: true},
	}
	rows := make([][]terminal.Cell, 0, len(checks))
	for _, check := range checks {
		rows = append(rows, []terminal.Cell{icons[check.Status], {Text: check.Status}, {Text: check.Name, Bold: true}, {Text: check.Detail}, {Text: check.Fix}})
		if d.TTY && check.Fix != "" && check.Status != checkPassed {
			rows = append(rows, []terminal.Cell{{}, {}, {}, {Text: "→ " + check.Fix, Muted: true}})
		}
	}
	return terminal.Table([]terminal.Column{{TTYOnly: true}, {PipedOnly: true}, {}, {}, {PipedOnly: true}}, rows, d)
}
