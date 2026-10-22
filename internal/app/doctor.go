// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package app

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"slices"
	"strings"
	"time"

	"gitea.dev/cli/internal/clierr"
	"gitea.dev/cli/internal/git"
	"gitea.dev/cli/internal/services"
	"gitea.dev/cli/internal/settings"
	"gitea.dev/cli/internal/terminal"
	"gitea.dev/cli/internal/transport"
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
	return jsonOutput(&cobra.Command{
		Use: "doctor", Short: "Check gt's setup for this directory", Args: argsExact(0),
		// doctor loads the settings itself, to report a broken file instead of failing on it.
		Annotations: map[string]string{"gt.offline": "true"},
		Long: `Check the setup that commands in this directory use: the settings files, the instance,
its configured headers and whether it answers, the account and its credential, the
repository, and Git. Each problem comes with the command that fixes it. gt doctor fails
when a check fails.`,
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
	}, jsonFields[doctorReport]())
}

// diagnose resolves the target like other commands, but reports each problem instead of stopping
// at the first.
func (a *application) diagnose(ctx context.Context) doctorReport {
	var report doctorReport
	add := func(check doctorCheck) { report.Checks = append(report.Checks, check) }
	snapshot, err := a.settings()
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
	if t.host.ClientCert != "" {
		add(clientCertificateCheck(t.host))
	}
	if len(t.host.Headers) > 0 {
		add(a.headersCheck(t))
	}
	resolveCtx, cancel := context.WithTimeout(ctx, doctorTimeout)
	authenticated, accountErr := a.resolveTarget(resolveCtx, t.Repo != "", true)
	cancel()
	client := a.instanceClient(t.Host, "", t.host)
	if accountErr == nil {
		client = authenticated.client
	}
	instance := a.instanceCheck(ctx, t, client)
	add(instance)
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
	sshSource := "ssh_host"
	if stored, _ := a.storedHost(t.Host); stored.SSHHost != t.host.SSHHost {
		sshSource = "GITEA_INSTANCE_SSH_HOST"
	}
	if check, ok := sshCheck(t, repoErr, sshSource); ok {
		add(check)
	}
	return report
}

// instanceCheck asks the instance for its version with client, the account's when it has one:
// instances with [service] REQUIRE_SIGNIN_VIEW refuse every anonymous API request.
func (a *application) instanceCheck(ctx context.Context, t target, client *transport.Client) doctorCheck {
	ctx, cancel := context.WithTimeout(ctx, doctorTimeout)
	defer cancel()
	start := time.Now()
	version, err := services.ServerVersion(ctx, client)
	where := t.Host + " (" + describeSource(t.HostSource) + ")"
	latency := max(time.Since(start).Milliseconds(), 1)
	if services.IsUnauthorized(err) || services.IsForbidden(err) {
		// The instance answers; the account check reports why the credential is refused.
		return doctorCheck{"Instance", checkPassed, fmt.Sprintf("%s · answers signed-in requests only · %dms", where, latency), ""}
	}
	if err != nil {
		return doctorCheck{"Instance", checkFailed, where + ": " + Explain(ctx, a.deps, err).Error(), "check --host, the instance URL and your network"}
	}
	detail := fmt.Sprintf("%s · Gitea %s · %dms", where, version, latency)
	// Listings of more items than one page holds take several requests.
	if size, err := services.MaxPageSize(ctx, client); err == nil && size > 0 {
		detail += fmt.Sprintf(" · %d items per page at most", size)
	}
	return doctorCheck{"Instance", checkPassed, detail, ""}
}

// accountCheck reports whether the account authenticates, and the scopes of its token.
func (a *application) accountCheck(ctx context.Context, t target, snapshot settings.Snapshot, authenticated target, err error) doctorCheck {
	login := "gt auth login --host " + t.Host
	if a.deps.Getenv("GITEA_TOKEN") != "" && err != nil {
		instance, resolveErr := settings.ResolveHost(a.tokenInstance(), snapshot.Hosts.Hosts)
		switch {
		case a.tokenInstance() == "" || resolveErr != nil:
			return doctorCheck{"Account", checkFailed, "GITEA_TOKEN is set, but neither GITEA_CLI_HOST nor GITEA_INSTANCE_URL names its instance", "export GITEA_INSTANCE_URL=" + t.Host}
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

// sshCheck reports where SSH remotes connect when the instance uses SSH, or the clone's remote does.
// sshSource names the setting of the instance's SSH host.
func sshCheck(t target, repoErr error, sshSource string) (doctorCheck, bool) {
	var remote *settings.SSHRemoteError
	switch {
	case t.Repo == "" && errors.As(repoErr, &remote) && remote.Endpoint != "":
		fix := remote.Fix()
		if remote.Instance == "" && !remote.Environment {
			fix = "gt config set ssh_host " + remote.Endpoint + " --host " + t.Host + ", if it is the SSH host of " + t.Host
		}
		return doctorCheck{"SSH", checkWarning, "the Git remote connects to " + remote.Endpoint + ", which is no instance's SSH host", fix}, true
	case t.host.SSHHost != "":
		return doctorCheck{"SSH", checkPassed, "remotes connect to " + t.host.SSHHost + " (" + sshSource + ")", ""}, true
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

// headersCheck checks that the instance's configured headers are sent: one whose value names an
// environment variable that is unset or empty is left out of every request.
func (a *application) headersCheck(t target) doctorCheck {
	unsent := a.unsentHeaders(t.host)
	if len(unsent) == 0 {
		return doctorCheck{"Headers", checkPassed, strings.Join(slices.Sorted(maps.Keys(t.host.Headers)), ", "), ""}
	}
	details := make([]string, len(unsent))
	removals := make([]string, len(unsent))
	for i, name := range unsent {
		details[i] = a.unsentHeader(t.host, name)
		removals[i] = fmt.Sprintf("gt config set headers %q --host %s", name+":", t.Host)
	}
	if len(unsent) == 1 {
		return doctorCheck{"Headers", checkWarning, details[0], "set the variable, or remove the header: " + removals[0]}
	}
	return doctorCheck{"Headers", checkWarning, strings.Join(details, "; "), "set the variables, or remove the headers: " + strings.Join(removals, "; ")}
}

// unsentHeaders returns the names of the configured headers of host that are not sent because
// their value names an environment variable that is unset or empty.
func (a *application) unsentHeaders(host settings.Host) []string {
	var unsent []string
	for _, name := range slices.Sorted(maps.Keys(host.Headers)) {
		if variable, ok := headerVariable(host.Headers[name]); ok && a.deps.Getenv(variable) == "" {
			unsent = append(unsent, name)
		}
	}
	return unsent
}

// unsentHeader says why the configured header name of host is not sent, such as
// "X-Proxy-Secret is not sent: $PROXY_SECRET is not set" or "... is empty".
func (a *application) unsentHeader(host settings.Host, name string) string {
	variable, _ := headerVariable(host.Headers[name])
	if _, set := a.deps.LookupEnv(variable); set {
		return name + " is not sent: " + host.Headers[name] + " is empty"
	}
	return name + " is not sent: " + host.Headers[name] + " is not set"
}

// warnUnsentHeaders warns about the headers being configured whose value names an environment
// variable that is unset or empty, so that they would not be sent.
func (a *application) warnUnsentHeaders(cmd *cobra.Command, headers map[string]string) {
	host := settings.Host{Headers: headers}
	for _, name := range a.unsentHeaders(host) {
		a.warn(cmd, "%s", a.unsentHeader(host, name))
	}
}

// clientCertificateCheck checks that the instance's TLS client certificate and key can be loaded.
func clientCertificateCheck(host settings.Host) doctorCheck {
	if _, err := transport.LoadClientCertificate(host.ClientCert, host.ClientKey); err != nil {
		return doctorCheck{"Client certificate", checkFailed, err.Error(), "use an unencrypted PEM certificate and key: gt config set client_cert FILE --host URL"}
	}
	return doctorCheck{"Client certificate", checkPassed, host.ClientCert, ""}
}
