// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package app

import (
	"cmp"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"runtime/debug"
	"strings"
	"time"

	"gitea.dev/cli/internal/clierr"
	"gitea.dev/cli/internal/extensions"
	"gitea.dev/cli/internal/services"
	"gitea.dev/cli/internal/terminal"
	"gitea.dev/cli/internal/transport"
	"gitea.dev/cli/internal/version"

	"github.com/spf13/cobra"
)

// gt's releases are published on gitea.com, and their files on dl.gitea.com, where the install
// scripts download them.
const (
	releaseInstance     = "https://gitea.com"
	releaseRepository   = "gitea/cli"
	releaseDownloadURL  = "https://dl.gitea.com/gitea-cli"
	updateCheckInterval = 24 * time.Hour
	updateCheckTimeout  = 10 * time.Second
)

type installKind int

const (
	// installSource is a development build or one built from a checkout, which gt does not replace.
	installSource installKind = iota
	installScript
	installHomebrew
	installPackage
	installNix
	installGo
)

// installation is the running gt executable and how it was installed.
type installation struct {
	path string
	kind installKind
}

// detectInstallation tells how the gt at path was installed: by its version stamp and,
// for release builds, by the directory a package manager put it in.
func detectInstallation(path, stamped, goos string) installation {
	i := installation{path: path}
	slashed := filepath.ToSlash(path)
	switch {
	case stamped == "" || stamped == "development":
		if version.Current() != "development" {
			i.kind = installGo
		}
	case !version.IsRelease(stamped):
	case strings.Contains(slashed, "/Cellar/"):
		i.kind = installHomebrew
	case strings.HasPrefix(slashed, "/nix/store/"):
		i.kind = installNix
	case goos != "windows" && goos != "darwin" && (strings.HasPrefix(slashed, "/usr/bin/") || strings.HasPrefix(slashed, "/bin/") || strings.HasPrefix(slashed, "/usr/sbin/")):
		i.kind = installPackage
	default:
		i.kind = installScript
	}
	return i
}

func (a *application) installation() (installation, error) {
	executable, err := a.deps.Executable()
	if err != nil {
		return installation{}, fmt.Errorf("cannot find the gt executable: %w", err)
	}
	if resolved, err := filepath.EvalSymlinks(executable); err == nil {
		executable = resolved
	}
	return detectInstallation(executable, version.Version, runtime.GOOS), nil
}

// upgradeHint words how to upgrade this installation.
func (i installation) upgradeHint() string {
	switch i.kind {
	case installScript:
		return "To upgrade, run: gt upgrade"
	case installHomebrew:
		return "To upgrade, run: brew upgrade gitea-cli"
	case installGo:
		return "To upgrade, run: go install gitea.dev/cli/cmd/gt@latest"
	case installNix:
		return "Upgrade it with Nix."
	case installPackage:
		return "Upgrade it with your package manager."
	}
	return ""
}

// notInstalledByScript explains why gt upgrade leaves this installation to its package manager.
func (i installation) notInstalledByScript() error {
	switch i.kind {
	case installHomebrew:
		return errors.New("gt was installed with Homebrew; upgrade it with brew upgrade gitea-cli")
	case installGo:
		return errors.New("gt was installed with go install; upgrade it with go install gitea.dev/cli/cmd/gt@latest")
	case installNix:
		return fmt.Errorf("gt at %s was installed with Nix; upgrade it with Nix", i.path)
	case installPackage:
		return fmt.Errorf("gt at %s was installed by a package; upgrade it with your package manager", i.path)
	case installSource:
		return fmt.Errorf("gt %s was built from source; rebuild it to upgrade", version.Current())
	}
	return nil
}

// updateState is what gt last learned about its releases, so that it asks at most once a day
// and tells about a new release at most once a day.
type updateState struct {
	CheckedAt  time.Time `json:"checked_at"`
	Latest     string    `json:"latest,omitempty"`
	URL        string    `json:"url,omitempty"`
	Published  time.Time `json:"published_at,omitzero"`
	NotifiedAt time.Time `json:"notified_at,omitzero"`
}

func (a *application) updateStatePath() string {
	return filepath.Join(a.deps.DataDir, "update-check.json")
}

// notifiesUpdates returns the installation and whether gt may check for a new release while a
// command runs: for a release build on a terminal, outside CI and extensions, unless
// GITEA_CLI_NO_UPDATE_NOTIFIER is set.
func (a *application) notifiesUpdates(args []string) (installation, bool) {
	getenv := a.deps.Getenv
	if getenv == nil || a.deps.Client == nil || a.deps.DataDir == "" || !a.deps.Streams.OutTTY || !a.deps.Streams.ErrTTY {
		return installation{}, false
	}
	for _, name := range []string{"GITEA_CLI_NO_UPDATE_NOTIFIER", "GITEA_CLI_EXTENSION", "CI", "BUILD_NUMBER", "RUN_ID"} {
		if getenv(name) != "" {
			return installation{}, false
		}
	}
	for _, arg := range args {
		if arg == cobra.ShellCompRequestCmd || arg == cobra.ShellCompNoDescRequestCmd {
			return installation{}, false
		}
	}
	i, err := a.installation()
	return i, err == nil && i.kind != installSource
}

// checkForUpdate asks for the latest release in the background while the command runs, when the
// last answer is a day old, and returns a function that tells about a newer release after a
// command that succeeded. A command that ends first abandons the check, so it never waits for
// it; the next one asks again. client is the undecorated client factory, since the command's
// flags are parsed concurrently.
func (a *application) checkForUpdate(ctx context.Context, args []string, client func(string, string, bool) *transport.Client) func(cmd *cobra.Command, succeeded bool) {
	i, ok := a.notifiesUpdates(args)
	if !ok {
		return func(*cobra.Command, bool) {}
	}
	var state updateState
	if data, err := os.ReadFile(a.updateStatePath()); err == nil {
		_ = json.Unmarshal(data, &state) // a broken file is checked again
	}
	ctx, cancel := context.WithCancel(ctx)
	checked := make(chan updateState, 1)
	if a.deps.Now().Sub(state.CheckedAt) >= updateCheckInterval {
		go func() {
			if latest, err := a.latestRelease(ctx, client); err == nil { // an offline gt must not complain about it
				latest.NotifiedAt = state.NotifiedAt
				checked <- latest
			}
		}()
	}
	return func(cmd *cobra.Command, succeeded bool) {
		cancel()
		changed := false
		select {
		case state = <-checked:
			changed = true
		default:
		}
		if succeeded && a.notifyUpdate(cmd, i, state) {
			state.NotifiedAt, changed = a.deps.Now(), true
		}
		if changed {
			_ = a.saveUpdateState(state)
		}
	}
}

func (a *application) latestRelease(ctx context.Context, client func(string, string, bool) *transport.Client) (updateState, error) {
	release, err := (services.Releases{Client: client(releaseInstance, "", false).WithResponseTimeout(updateCheckTimeout), Repository: releaseRepository}).Latest(ctx)
	if err != nil {
		return updateState{}, err
	}
	state := updateState{CheckedAt: a.deps.Now(), Latest: strings.TrimPrefix(release.Tag, "v"), URL: release.URL}
	if release.PublishedAt != nil {
		state.Published, _ = time.Parse(time.RFC3339, *release.PublishedAt)
	}
	return state, nil
}

func (a *application) saveUpdateState(state updateState) error {
	data, err := json.Marshal(state)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(a.updateStatePath()), 0o700); err != nil {
		return err
	}
	return os.WriteFile(a.updateStatePath(), data, 0o600)
}

// notifyUpdate tells about a newer release than this gt, unless it did within a day, cmd was gt
// upgrade, or Homebrew may not have the release yet. It reports whether it told.
func (a *application) notifyUpdate(cmd *cobra.Command, i installation, state updateState) bool {
	now := a.deps.Now()
	switch {
	case !version.IsRelease(state.Latest) || !extensions.Older(version.Current(), state.Latest):
		return false
	case now.Sub(state.NotifiedAt) < updateCheckInterval:
		return false
	case cmd == nil || cmd.Name() == "upgrade" && !cmd.Parent().HasParent():
		return false
	case i.kind == installHomebrew && now.Sub(state.Published) < updateCheckInterval:
		return false // the formula is bumped some time after the release
	}
	color := a.errColorEnabled()
	out := a.deps.Streams.ErrOut
	fmt.Fprintf(out, "\n%s %s → %s\n", terminal.Colorize("yellow", "A new release of gt is available:", color), version.Current(), state.Latest)
	if hint := i.upgradeHint(); hint != "" {
		fmt.Fprintln(out, hint)
	}
	if state.URL != "" {
		fmt.Fprintln(out, terminal.Colorize("yellow", terminal.SafeLine(state.URL), color))
	}
	return true
}

func (a *application) upgradeCommand() *cobra.Command {
	var want string
	var nightly, dryRun, force bool
	cmd := &cobra.Command{
		Use: "upgrade", Short: "Upgrade gt to its latest release",
		Annotations: map[string]string{"gt.confirm": "true", "gt.offline": "true", "gt.text": "upgrade does not return a resource"},
		Long: `Upgrade gt to its latest release, to the release --version names, or to the latest build of
the main branch with --nightly. gt downloads the build for this system from dl.gitea.com, like
the install script, verifies its SHA-256 checksum and replaces its executable with it;
GITEA_CLI_DOWNLOAD_URL selects another download location.

gt installed with Homebrew, a Linux package, Nix or go install is upgraded the way it was
installed; gt upgrade names the command for that instead.

After a command, gt tells at most once a day when a newer release is out, asking gitea.com
for it. It does not check in CI or when stdout or stderr is not a terminal; set
GITEA_CLI_NO_UPDATE_NOTIFIER to turn the check off.`,
		Example: `$ gt upgrade
# See whether a newer release is out
$ gt upgrade --dry-run
$ gt upgrade --version 1.0.0 --yes
$ gt upgrade --nightly`,
		Args: func(cmd *cobra.Command, args []string) error {
			if err := argsExact(0)(cmd, args); err != nil {
				return err
			}
			if nightly && want != "" {
				return clierr.Wrap(clierr.Usage, errors.New("--nightly cannot be combined with --version"))
			}
			if cmd.Flags().Changed("version") && !version.IsRelease(strings.TrimPrefix(want, "v")) {
				return clierr.Wrap(clierr.Usage, fmt.Errorf("--version requires a release such as 1.0.0, not %q", want))
			}
			if !dryRun && !a.options.yes && !a.canPrompt() {
				return clierr.Wrap(clierr.Usage, errors.New("confirmation required to upgrade gt; rerun with --yes"))
			}
			return nil
		},
		RunE: func(cmd *cobra.Command, _ []string) error {
			return a.upgrade(cmd, strings.TrimPrefix(want, "v"), nightly, dryRun, force)
		},
	}
	cmd.Flags().StringVar(&want, "version", "", "Install this `release` instead of the latest one")
	cmd.Flags().BoolVar(&nightly, "nightly", false, "Install the latest build of the main branch")
	cmd.Flags().BoolVar(&dryRun, "dry-run", false, "Show the upgrade without installing it")
	cmd.Flags().BoolVar(&force, "force", false, "Reinstall the current version too")
	return cmd
}

func (a *application) upgrade(cmd *cobra.Command, want string, nightly, dryRun, force bool) error {
	ctx := cmd.Context()
	i, err := a.installation()
	if err != nil {
		return err
	}
	if err := i.notInstalledByScript(); err != nil {
		return err
	}
	_ = os.Remove(i.path + ".old") // left behind by an upgrade on Windows, which cannot remove a running executable
	current := version.Current()
	label := want
	switch {
	case nightly:
		want, label = "main-nightly", "the latest nightly build"
	case want == "":
		release, err := (services.Releases{Client: a.deps.Client(releaseInstance, "", false).WithResponseTimeout(a.responseTimeout()), Repository: releaseRepository}).Latest(ctx)
		if err != nil {
			return fmt.Errorf("find the latest gt release: %w", err)
		}
		want = strings.TrimPrefix(release.Tag, "v")
		if !version.IsRelease(want) {
			return fmt.Errorf("the latest gt release has the invalid tag %q", terminal.SafeLine(release.Tag))
		}
		label = want
		if !force && !extensions.Older(current, want) {
			a.done(cmd, "gt is up to date (%s)", current)
			return nil
		}
	case want == current && !force:
		a.done(cmd, "gt %s is already installed", current)
		return nil
	}
	platform, err := releasePlatform(runtime.GOOS, runtime.GOARCH)
	if err != nil {
		return err
	}
	file := "gitea-cli-" + want + "-" + platform
	base := strings.TrimRight(cmp.Or(a.deps.Getenv("GITEA_CLI_DOWNLOAD_URL"), releaseDownloadURL), "/")
	client := a.deps.Client(base, "", false).WithResponseTimeout(a.responseTimeout())
	digest, err := publishedChecksum(ctx, client, base+"/"+want+"/"+file+".sha256", file)
	if err != nil {
		return err
	}
	if installed, err := extensions.FileSHA256(i.path); err == nil && installed == digest && !force {
		a.done(cmd, "gt is up to date (%s)", label)
		return nil
	}
	if dryRun {
		_, err := fmt.Fprintf(cmd.OutOrStdout(), "gt\t%s\t%s\n", current, want)
		return err
	}
	if err := a.confirmAction(cmd, terminal.Confirmation{Prompt: fmt.Sprintf("Upgrade gt from %s to %s?", current, label), DefaultYes: true, NonTTY: "upgrade gt"}); err != nil {
		return err
	}
	staged, err := downloadRelease(ctx, client, base+"/"+want+"/"+file, file, digest, i.path)
	if err != nil {
		return err
	}
	defer os.Remove(staged)
	if err := replaceExecutable(staged, i.path, runtime.GOOS); err != nil {
		return fmt.Errorf("replace %s: %w", i.path, err)
	}
	a.done(cmd, "Upgraded gt from %s to %s", current, label)
	return nil
}

// releasePlatform names the build for goos/goarch as the release files do, such as linux-arm-7
// or windows-amd64.exe.
func releasePlatform(goos, goarch string) (string, error) {
	platform := goos + "-" + goarch
	if goarch == "arm" {
		arm := ""
		if info, ok := debug.ReadBuildInfo(); ok {
			for _, setting := range info.Settings {
				if setting.Key == "GOARM" {
					arm, _, _ = strings.Cut(setting.Value, ",")
				}
			}
		}
		if arm == "" {
			return "", errors.New("cannot tell which ARM version gt was built for; download it from " + releaseDownloadURL)
		}
		platform += "-" + arm
	}
	if goos == "windows" {
		platform += ".exe"
	}
	return platform, nil
}

// publishedChecksum reads the digest of file from its .sha256 file, in sha256sum format.
func publishedChecksum(ctx context.Context, client *transport.Client, url, file string) (string, error) {
	response, err := client.Download(ctx, url)
	if err != nil {
		return "", fmt.Errorf("download the checksum of %s: %w", file, err)
	}
	defer response.Body.Close()
	digest, err := extensions.ChecksumFor(response.Body, file)
	if err != nil {
		return "", fmt.Errorf("read the checksum of %s: %w", file, err)
	}
	return digest, nil
}

// downloadRelease downloads file next to executable, so that it can replace it by renaming, and
// returns its path once its SHA-256 matches digest.
func downloadRelease(ctx context.Context, client *transport.Client, url, file, digest, executable string) (string, error) {
	response, err := client.Download(ctx, url)
	if err != nil {
		return "", fmt.Errorf("download %s: %w", file, err)
	}
	defer response.Body.Close()
	out, err := os.CreateTemp(filepath.Dir(executable), ".gt-upgrade-*"+filepath.Ext(executable))
	if err != nil {
		return "", fmt.Errorf("cannot write to %s, which holds gt: %w", filepath.Dir(executable), err)
	}
	hash := sha256.New()
	written, err := io.Copy(io.MultiWriter(out, hash), io.LimitReader(response.Body, extensions.MaxAssetSize+1))
	err = cmp.Or(err, out.Chmod(0o755), out.Close())
	switch got := hex.EncodeToString(hash.Sum(nil)); {
	case err != nil:
		err = fmt.Errorf("download %s: %w", file, err)
	case written > extensions.MaxAssetSize:
		err = fmt.Errorf("%s is larger than %d MiB", file, extensions.MaxAssetSize>>20)
	case got != digest:
		err = fmt.Errorf("the SHA-256 of %s is %s, but its checksum file lists %s; gt was not upgraded", file, got, digest)
	}
	if err != nil {
		_ = out.Close()
		return "", errors.Join(err, os.Remove(out.Name()))
	}
	return out.Name(), nil
}

// replaceExecutable moves staged over executable. Windows cannot replace a running executable,
// but can rename it, so it moves it aside first and puts it back if that fails.
func replaceExecutable(staged, executable, goos string) error {
	if goos != "windows" {
		return os.Rename(staged, executable)
	}
	old := executable + ".old"
	if err := os.Rename(executable, old); err != nil {
		return err
	}
	if err := os.Rename(staged, executable); err != nil {
		return errors.Join(err, os.Rename(old, executable))
	}
	_ = os.Remove(old) // fails while the old executable still runs; the next upgrade replaces it
	return nil
}
