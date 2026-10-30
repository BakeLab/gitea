// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package app

import (
	"cmp"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"maps"
	"os"
	"path"
	"path/filepath"
	"runtime"
	"slices"
	"strings"

	"gitea.dev/cli/internal/clierr"
	"gitea.dev/cli/internal/extensions"
	"gitea.dev/cli/internal/extensions/github"
	"gitea.dev/cli/internal/services"
	"gitea.dev/cli/internal/settings"
	"gitea.dev/cli/internal/terminal"
	"gitea.dev/cli/internal/version"
	"github.com/spf13/cobra"
)

// extensionRepository is the repository an extension is installed from.
type extensionRepository struct {
	id                                        int64
	owner, name, defaultBranch, url, cloneURL string
}

type extensionRelease struct {
	id     int64
	tag    string
	assets []extensionAsset
}

type extensionAsset struct {
	id   int64
	name string
	size int64
	// digest is the SHA-256 the source recorded with the asset, if it lists one.
	digest string
	url    string
}

// extensionSource reads an extension's repository from a Gitea instance or from GitHub.
type extensionSource interface {
	host() string
	kind() string
	repository(ctx context.Context) (extensionRepository, error)
	// release returns the release tagged tag, or the latest one when tag is empty, and whether
	// it exists.
	release(ctx context.Context, tag string) (extensionRelease, bool, error)
	commit(ctx context.Context, ref string) (string, error)
	file(ctx context.Context, ref, path string) ([]byte, bool, error)
	download(ctx context.Context, release extensionRelease, asset extensionAsset) (io.ReadCloser, error)
	// cloneArgs are the Git arguments that authenticate a clone, or turn off credentials.
	cloneArgs() ([]string, error)
}

type giteaExtensionSource struct {
	a *application
	t target
	// anonymous is set when gt has no account on the instance.
	anonymous bool
}

func (s giteaExtensionSource) host() string { return s.t.Host }
func (s giteaExtensionSource) kind() string { return extensions.SourceGitea }

func (s giteaExtensionSource) repository(ctx context.Context) (extensionRepository, error) {
	repo, err := (services.Repositories{Client: s.t.client}).View(ctx, s.t.Repo)
	if err != nil {
		return extensionRepository{}, err
	}
	owner, name, _ := strings.Cut(repo.FullName, "/")
	return extensionRepository{id: repo.ID, owner: owner, name: name, defaultBranch: repo.DefaultBranch, url: repo.URL, cloneURL: repo.CloneURL}, nil
}

func (s giteaExtensionSource) release(ctx context.Context, tag string) (extensionRelease, bool, error) {
	releases := services.Releases{Client: s.t.client, Repository: s.t.Repo}
	var release services.Release
	var err error
	if tag == "" {
		release, err = releases.Latest(ctx)
	} else {
		release, err = releases.View(ctx, tag)
	}
	if services.IsNotFound(err) {
		return extensionRelease{}, false, nil
	}
	if err != nil {
		return extensionRelease{}, false, err
	}
	result := extensionRelease{id: release.ID, tag: release.Tag}
	for _, asset := range release.Assets {
		result.assets = append(result.assets, extensionAsset{id: asset.ID, name: asset.Name, size: asset.Size, url: asset.URL})
	}
	return result, true, nil
}

func (s giteaExtensionSource) commit(ctx context.Context, ref string) (string, error) {
	return (services.Repositories{Client: s.t.client}).Commit(ctx, s.t.Repo, ref)
}

func (s giteaExtensionSource) file(ctx context.Context, ref, path string) ([]byte, bool, error) {
	return (services.Repositories{Client: s.t.client}).File(ctx, s.t.Repo, ref, path)
}

func (s giteaExtensionSource) download(ctx context.Context, release extensionRelease, asset extensionAsset) (io.ReadCloser, error) {
	return (services.Releases{Client: s.t.client, Repository: s.t.Repo}).DownloadAsset(ctx, release.id, asset.id)
}

func (s giteaExtensionSource) cloneArgs() ([]string, error) {
	if s.anonymous {
		return []string{"-c", "credential.helper="}, nil
	}
	return s.a.gitTransportArguments(s.t, false)
}

type githubExtensionSource struct {
	client      github.Client
	owner, repo string
}

func (s githubExtensionSource) host() string { return github.Host }
func (s githubExtensionSource) kind() string { return extensions.SourceGitHub }

func (s githubExtensionSource) repository(ctx context.Context) (extensionRepository, error) {
	repo, err := s.client.Repository(ctx, s.owner, s.repo)
	if errors.Is(err, github.ErrNotFound) {
		return extensionRepository{}, fmt.Errorf("repository %s/%s not found on GitHub", s.owner, s.repo)
	}
	return extensionRepository{id: repo.ID, owner: repo.Owner.Login, name: repo.Name, defaultBranch: repo.DefaultBranch, url: repo.HTMLURL, cloneURL: repo.CloneURL}, err
}

func (s githubExtensionSource) release(ctx context.Context, tag string) (extensionRelease, bool, error) {
	release, err := s.client.Release(ctx, s.owner, s.repo, tag)
	if errors.Is(err, github.ErrNotFound) {
		return extensionRelease{}, false, nil
	}
	if err != nil {
		return extensionRelease{}, false, err
	}
	result := extensionRelease{tag: release.Tag}
	for _, asset := range release.Assets {
		result.assets = append(result.assets, extensionAsset{name: asset.Name, size: asset.Size, digest: asset.SHA256(), url: asset.URL})
	}
	return result, true, nil
}

func (s githubExtensionSource) commit(ctx context.Context, ref string) (string, error) {
	return s.client.Commit(ctx, s.owner, s.repo, ref)
}

func (s githubExtensionSource) file(ctx context.Context, ref, path string) ([]byte, bool, error) {
	return s.client.File(ctx, s.owner, s.repo, ref, path)
}

func (s githubExtensionSource) download(ctx context.Context, _ extensionRelease, asset extensionAsset) (io.ReadCloser, error) {
	return s.client.Download(ctx, github.Asset{Name: asset.name, URL: asset.url})
}

func (s githubExtensionSource) cloneArgs() ([]string, error) {
	return []string{"-c", "credential.helper="}, nil
}

// githubRepository returns OWNER and REPO of a github.com repository URL, and whether raw is one.
func githubRepository(raw string) (owner, repo string, ok bool, err error) {
	rest, found := strings.CutPrefix(raw, "https://github.com/")
	if !found {
		if rest, found = strings.CutPrefix(raw, "github.com/"); !found {
			return "", "", false, nil
		}
	}
	owner, repo, _ = strings.Cut(strings.TrimSuffix(strings.TrimSuffix(rest, "/"), ".git"), "/")
	if owner == "" || repo == "" || strings.Contains(repo, "/") {
		return "", "", true, clierr.Wrap(clierr.Usage, fmt.Errorf("invalid GitHub repository %q: use https://github.com/OWNER/REPO", raw))
	}
	return owner, repo, true, nil
}

// extensionRepositoryName returns the repository name raw ends in, without contacting a server.
func extensionRepositoryName(raw string) string {
	raw = strings.TrimSuffix(strings.TrimSuffix(raw, "/"), ".git")
	return path.Base(raw)
}

// extensionSourceFor selects the source of the repository raw names: github.com URLs are read
// anonymously from GitHub, everything else from a Gitea instance, with gt's account there if it
// has one.
func (a *application) extensionSourceFor(ctx context.Context, raw string) (extensionSource, error) {
	if owner, repo, ok, err := githubRepository(raw); ok || err != nil {
		if err != nil {
			return nil, err
		}
		return githubExtensionSource{client: a.deps.GitHub(), owner: owner, repo: repo}, nil
	}
	req := targetRequest{requireRepo: true, repo: raw, repoSource: "argument"}
	t, err := a.resolveRequest(ctx, req)
	if err != nil {
		return nil, err
	}
	snapshot, err := a.settings()
	if err != nil {
		return nil, err
	}
	host := snapshot.Hosts.Hosts[t.Host]
	_, account := host.Users[t.User]
	if instance, err := settings.ResolveHost(a.tokenInstance(), snapshot.Hosts.Hosts); account || (a.deps.Getenv("GITEA_TOKEN") != "" && err == nil && instance == t.Host) {
		req.authenticate = true
		t, err := a.resolveRequest(ctx, req)
		return giteaExtensionSource{a: a, t: t}, err
	}
	t.client = a.instanceClient(t.Host, "", host)
	return giteaExtensionSource{a: a, t: t, anonymous: true}, nil
}

// extensionCandidate is a version of an extension resolved for installing.
type extensionCandidate struct {
	manifest extensions.Manifest
	source   extensionSource
	repo     extensionRepository
	release  extensionRelease
	asset    extensionAsset
	// digest is the digest the source publishes for the asset, from digestSource.
	digest string
}

// resolveExtension finds what installing the extension of source at pin, a tag or commit, or
// else at its latest release or default branch, would install, without downloading it.
func (a *application) resolveExtension(ctx context.Context, source extensionSource, pin string) (extensionCandidate, error) {
	repo, err := source.repository(ctx)
	if err != nil {
		return extensionCandidate{}, err
	}
	prefix, name, err := extensions.ParseRepositoryName(repo.name)
	if err != nil {
		return extensionCandidate{}, err
	}
	c := extensionCandidate{source: source, repo: repo, manifest: extensions.Manifest{
		Name: name, Prefix: prefix, Source: source.kind(), Host: source.host(), Owner: repo.owner, Repo: repo.name, RepoID: repo.id, Pinned: pin != "",
	}}
	release, found, err := source.release(ctx, pin)
	if err != nil {
		return c, err
	}
	ref := cmp.Or(pin, repo.defaultBranch)
	if found {
		c.release, ref, c.manifest.Tag = release, release.tag, release.tag
	}
	if c.manifest.Commit, err = source.commit(ctx, ref); err != nil {
		return c, err
	}
	data, exists, err := source.file(ctx, c.manifest.Commit, "gt-extension.yml")
	if err != nil {
		return c, err
	}
	if exists {
		if c.manifest.Metadata, err = extensions.ParseMetadata(data); err != nil {
			return c, err
		}
	}
	if err := checkExtensionMetadata(name, c.manifest.Metadata); err != nil {
		return c, err
	}
	c.manifest.Kind = extensions.KindScript
	if found {
		names := make([]string, len(release.assets))
		for i, asset := range release.assets {
			names[i] = asset.name
		}
		if index := extensions.SelectAsset(names, repo.name, name, runtime.GOOS, runtime.GOARCH); index >= 0 {
			c.manifest.Kind, c.asset, c.manifest.Asset = extensions.KindBinary, release.assets[index], release.assets[index].name
			if err := a.publishedDigest(ctx, &c); err != nil {
				return c, err
			}
		}
	}
	return c, nil
}

// checkExtensionMetadata refuses an extension whose gt-extension.yml rules out this gt or platform.
func checkExtensionMetadata(name string, metadata extensions.Metadata) error {
	if minimum := metadata.MinVersion; extensions.Older(version.Current(), minimum) {
		return fmt.Errorf("extension %s requires gt %s or newer; this is gt %s", name, minimum, version.Current())
	}
	if !metadata.Supports(runtime.GOOS, runtime.GOARCH) {
		return fmt.Errorf("extension %s does not support %s/%s (platforms: %s)", name, runtime.GOOS, runtime.GOARCH, strings.Join(metadata.Platforms, ", "))
	}
	return nil
}

// publishedDigest finds the digest the source publishes for the candidate's asset: the one it
// recorded at upload, else the one checksums.txt lists. Without either, only the digest gt records
// at install protects the executable from then on.
func (a *application) publishedDigest(ctx context.Context, c *extensionCandidate) error {
	if digest := c.asset.digest; digest != "" {
		if !extensions.ValidDigest(strings.ToLower(digest)) {
			return fmt.Errorf("the source published an invalid digest for %s", c.asset.name)
		}
		c.digest, c.manifest.DigestSource = strings.ToLower(digest), "server"
		return nil
	}
	for _, asset := range c.release.assets {
		if !strings.EqualFold(asset.name, extensions.ChecksumsAsset) {
			continue
		}
		if asset.size > extensions.MaxChecksumsSize {
			return fmt.Errorf("%s of %s is larger than %d bytes", extensions.ChecksumsAsset, c.release.tag, extensions.MaxChecksumsSize)
		}
		body, err := c.source.download(ctx, c.release, asset)
		if err != nil {
			return err
		}
		defer body.Close()
		if c.digest, err = extensions.ChecksumFor(body, c.asset.name); err != nil {
			return err
		}
		c.manifest.DigestSource = extensions.ChecksumsAsset
		return nil
	}
	c.manifest.DigestSource = "none"
	return nil
}

// stageExtension downloads or clones the candidate into a staging directory of the store and
// records the digest of its executable. The caller removes the directory unless it publishes it.
func (a *application) stageExtension(ctx context.Context, c *extensionCandidate) (string, error) {
	stage, err := a.extensionStore().Stage()
	if err != nil {
		return "", err
	}
	if c.manifest.Kind == extensions.KindBinary {
		err = a.downloadExtension(ctx, c, filepath.Join(stage, c.manifest.Executable()))
	} else {
		err = a.cloneExtension(ctx, c, filepath.Join(stage, "src"))
	}
	if err != nil {
		return "", errors.Join(err, os.RemoveAll(stage))
	}
	return stage, nil
}

func (a *application) downloadExtension(ctx context.Context, c *extensionCandidate, file string) error {
	if c.asset.size > extensions.MaxAssetSize {
		return fmt.Errorf("%s is larger than %d MiB", c.asset.name, extensions.MaxAssetSize>>20)
	}
	body, err := c.source.download(ctx, c.release, c.asset)
	if err != nil {
		return err
	}
	defer body.Close()
	out, err := os.OpenFile(file, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o755)
	if err != nil {
		return err
	}
	hash := sha256.New()
	written, err := io.Copy(io.MultiWriter(out, hash), io.LimitReader(body, extensions.MaxAssetSize+1))
	if closeErr := out.Close(); err == nil {
		err = closeErr
	}
	if err != nil {
		return fmt.Errorf("download %s: %w", c.asset.name, err)
	}
	if written > extensions.MaxAssetSize {
		return fmt.Errorf("%s is larger than %d MiB", c.asset.name, extensions.MaxAssetSize>>20)
	}
	digest := hex.EncodeToString(hash.Sum(nil))
	if c.digest != "" && digest != c.digest {
		return fmt.Errorf("the SHA-256 of %s is %s, but %s lists %s; it was not installed", c.asset.name, digest, digestOrigin(c.manifest.DigestSource), c.digest)
	}
	c.manifest.SHA256 = digest
	return nil
}

// cloneExtension clones the candidate's repository into dir at its commit, which must hold the
// executable gt-NAME or gh-NAME at its root.
func (a *application) cloneExtension(ctx context.Context, c *extensionCandidate, dir string) error {
	if c.repo.cloneURL == "" {
		return errors.New("the source did not return a clone URL")
	}
	transport, err := c.source.cloneArgs()
	if err != nil {
		return err
	}
	if _, err := a.gitIn(ctx, "", append(transport, "clone", "--quiet", "--no-checkout", "--", c.repo.cloneURL, dir)...); err != nil {
		return err
	}
	if _, err := a.gitIn(ctx, dir, "-c", "advice.detachedHead=false", "checkout", "--quiet", "--detach", c.manifest.Commit); err != nil {
		return err
	}
	script := filepath.Join(dir, c.manifest.Executable())
	info, err := os.Lstat(script) // a symlink could point outside the commit
	if err == nil && info.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("%s holds %s as a symbolic link, which gt does not install; the script must be a regular file in the repository", c.manifest.Repository(), c.manifest.Executable())
	}
	if err != nil || !info.Mode().IsRegular() || (runtime.GOOS != "windows" && info.Mode().Perm()&0o111 == 0) {
		what := "a release asset for " + runtime.GOOS + "-" + runtime.GOARCH
		if c.manifest.Tag == "" {
			what = "a release"
		}
		return fmt.Errorf("%s has neither %s nor an executable %s at its root", c.manifest.Repository(), what, c.manifest.Executable())
	}
	c.manifest.DigestSource = "git commit"
	c.manifest.SHA256, err = extensions.FileSHA256(script)
	return err
}

// digestOrigin words where a digest came from for messages.
func digestOrigin(source string) string {
	switch source {
	case "server":
		return "the server"
	case extensions.ChecksumsAsset:
		return extensions.ChecksumsAsset
	case "git commit":
		return "the Git commit"
	}
	return "nothing published"
}

// describeCandidate shows what is about to be installed, as the confirmation asks about it.
func describeCandidate(c extensionCandidate) string {
	m := c.manifest
	var b strings.Builder
	fmt.Fprintf(&b, "  source       %s\n", terminal.SafeLine(extensionOrigin(m)))
	if m.Tag != "" {
		fmt.Fprintf(&b, "  release      %s (commit %s)\n", terminal.SafeLine(m.Tag), shortCommit(m.Commit))
	} else {
		fmt.Fprintf(&b, "  commit       %s\n", shortCommit(m.Commit))
	}
	if m.Asset != "" {
		fmt.Fprintf(&b, "  asset        %s\n", terminal.SafeLine(m.Asset))
	} else {
		fmt.Fprintf(&b, "  script       %s\n", m.Executable())
	}
	switch m.DigestSource {
	case "none":
		fmt.Fprintf(&b, "  sha256       %s (not published; recorded now and checked before each run)\n", m.SHA256)
	default:
		fmt.Fprintf(&b, "  sha256       %s (verified against %s)\n", m.SHA256, digestOrigin(m.DigestSource))
	}
	if description := m.Metadata.Description; description != "" {
		fmt.Fprintf(&b, "  description  %s\n", terminal.SafeLine(description))
	}
	if len(m.Metadata.Aliases) > 0 {
		names := slices.Sorted(maps.Keys(m.Metadata.Aliases))
		fmt.Fprintf(&b, "  commands     %s\n", terminal.SafeLine("gt "+strings.Join(names, ", gt ")))
	}
	credentials := "not requested"
	if m.Metadata.Credentials {
		credentials = "requested by gt-extension.yml"
	}
	fmt.Fprintf(&b, "  credentials  %s\n", credentials)
	return b.String()
}

// extensionConfirmation asks to install the candidate; Enter accepts only a verified download.
func extensionConfirmation(action, nonTTY string, c extensionCandidate) terminal.Confirmation {
	if c.manifest.DigestSource == "none" {
		return terminal.Confirmation{Prompt: action + ", which gt cannot verify?", NonTTY: nonTTY}
	}
	return terminal.Confirmation{Prompt: action + "?", DefaultYes: true, NonTTY: nonTTY}
}

func shortCommit(commit string) string {
	return commit[:min(len(commit), 12)]
}

type extensionInstallOptions struct {
	pin              string
	allowCredentials bool
	compatGH         bool
	force            bool
}

func (a *application) extensionInstallCommand() *cobra.Command {
	var opts extensionInstallOptions
	cmd := &cobra.Command{
		Use: "install <repository>", Short: "Install an extension", Args: nonBlankArgs(argsExact(1), "repository"),
		Annotations: map[string]string{"gt.confirm": "true", "gt.text": "extension install does not return a resource"},
		Long: `Install an extension from a repository named gt-NAME or gh-NAME, which then runs as gt NAME.

The repository is OWNER/REPO, HOST/OWNER/REPO or a URL on a Gitea instance, or
https://github.com/OWNER/gh-NAME for a GitHub CLI extension, which gt reads without any
credentials. "." installs the gt-NAME executable of the current directory, which runs in place
while you develop it.

gt installs the release asset NAME-OS-ARCH or REPO-OS-ARCH (with .exe on Windows), else one
whose name ends in OS-ARCH, of the latest release or of the release that --pin names. Without
such an asset, it clones the repository at the release, the --pin commit or the default branch,
which must hold an executable gt-NAME at its root.

gt checks a downloaded asset against the SHA-256 GitHub recorded for it, else against the
checksums.txt asset of the release. When the release publishes neither, gt asks with a default
of no, or installs with --yes. It records the SHA-256 and refuses to run the extension once the
executable changes.

Extensions run with your privileges, but without your Gitea token: gt passes it, as GITEA_TOKEN,
only to extensions you allow to use it, by answering yes when gt asks or with
--allow-credentials. See gt help extensions.`,
		Example: `$ gt extension install alice/gt-hello
$ gt extension install https://gitea.com/alice/gt-hello --pin v1.2.0
# A GitHub CLI extension
$ gt extension install https://github.com/owner/gh-hello
# The extension in the current directory
$ gt extension install .`,
		RunE: func(cmd *cobra.Command, args []string) error {
			return a.installExtension(cmd, args[0], opts)
		},
	}
	cmd.Flags().StringVar(&opts.pin, "pin", "", "Install the release `tag` or the commit and never upgrade it")
	cmd.Flags().BoolVar(&opts.allowCredentials, "allow-credentials", false, "Give the extension your Gitea token")
	cmd.Flags().BoolVar(&opts.compatGH, "compat-gh", false, "Let the extension call gh, which gt answers for gh api, gh repo view with gt's JSON fields and gh auth token")
	cmd.Flags().BoolVar(&opts.force, "force", false, "Reinstall an extension that is already installed")
	return cmd
}

func (a *application) installExtension(cmd *cobra.Command, raw string, opts extensionInstallOptions) error {
	ctx := cmd.Context()
	if raw == "." || strings.HasPrefix(raw, "./") || strings.HasPrefix(raw, "../") || filepath.IsAbs(raw) {
		if opts.pin != "" {
			return clierr.Wrap(clierr.Usage, errors.New("--pin cannot be combined with a local extension"))
		}
		return a.installLocalExtension(cmd, raw, opts)
	}
	if _, _, _, err := githubRepository(raw); err != nil {
		return err
	}
	_, name, err := extensions.ParseRepositoryName(extensionRepositoryName(raw))
	if err != nil {
		return clierr.Wrap(clierr.Usage, err)
	}
	if err := a.checkExtensionName(cmd, name, opts.force); err != nil {
		return err
	}
	if !a.options.yes && !a.canPrompt() {
		return clierr.Wrap(clierr.Usage, fmt.Errorf("confirmation required to install extension %s; rerun with --yes", name))
	}
	policy, err := a.extensionPolicy()
	if err != nil {
		return err
	}
	if opts.allowCredentials && policy.noCredentials != "" {
		return fmt.Errorf("extensions cannot be given credentials: %s denies them", policy.noCredentials)
	}
	if policy.disabled != "" {
		return fmt.Errorf("extensions are disabled by %s", policy.disabled)
	}
	source, err := a.extensionSourceFor(ctx, raw)
	if err != nil {
		return err
	}
	c, err := a.resolveExtension(ctx, source, opts.pin)
	if err != nil {
		return err
	}
	if err := policy.allows(c.manifest.Host, c.manifest.Owner); err != nil {
		return err
	}
	if c.manifest.Name != name {
		// The repository was renamed; its current name decides.
		if err := a.checkExtensionName(cmd, c.manifest.Name, opts.force); err != nil {
			return err
		}
	}
	stage, err := a.stageExtension(ctx, &c)
	if err != nil {
		return err
	}
	defer os.RemoveAll(stage)
	if c.manifest.DigestSource == "none" && a.options.yes {
		a.warn(cmd, "Release %s of %s publishes no SHA-256 for %s; installing it because of --yes", c.manifest.Tag, c.manifest.Repository(), c.manifest.Asset)
	}
	a.warnExtensionAliases(cmd, c.manifest)
	if !a.options.yes && a.canPrompt() {
		fmt.Fprintf(cmd.ErrOrStderr(), "Extension %s\n%s", c.manifest.Name, describeCandidate(c))
	}
	if err := a.confirmAction(cmd, extensionConfirmation("Install extension "+c.manifest.Name, "install extension "+c.manifest.Name, c)); err != nil {
		return err
	}
	c.manifest.CompatGH = opts.compatGH
	if c.manifest.Credentials, err = a.grantCredentials(cmd, c.manifest, opts.allowCredentials, policy); err != nil {
		return err
	}
	if err := a.extensionStore().Publish(stage, c.manifest, opts.force); err != nil {
		return err
	}
	a.done(cmd, "Installed extension %s %s; run it with gt %s", c.manifest.Name, cmp.Or(c.manifest.Tag, shortCommit(c.manifest.Commit)), c.manifest.Name)
	return nil
}

// grantCredentials decides whether manifest's extension gets the user's token: with
// --allow-credentials, or when it asks for them in gt-extension.yml and the user agrees.
func (a *application) grantCredentials(cmd *cobra.Command, manifest extensions.Manifest, allow bool, policy extensionPolicy) (bool, error) {
	if allow || !manifest.Metadata.Credentials || policy.noCredentials != "" {
		return allow, nil
	}
	if !a.canPrompt() {
		a.warn(cmd, "Extension %s asks for your Gitea token; allow it with gt extension install --force --allow-credentials %s", manifest.Name, extensionOrigin(manifest))
		return false, nil
	}
	return a.deps.Prompter.ConfirmDefault(cmd.Context(), "Give extension "+manifest.Name+" your Gitea token?", false)
}

// warnExtensionAliases warns about the aliases of manifest that gt will ignore, and about those
// of installed extensions manifest takes over. Installing goes on.
func (a *application) warnExtensionAliases(cmd *cobra.Command, manifest extensions.Manifest) {
	snapshot, _ := a.settings()
	installed, _ := a.extensionStore().List()
	installed = slices.DeleteFunc(installed, func(e extensions.Extension) bool { return e.Name == manifest.Name })
	owners := resolveExtensionAliases(cmd.Root(), installed, snapshot.Preferences.Aliases)
	if owner, ok := owners[manifest.Name]; ok {
		a.warn(cmd, "gt %s now runs extension %s instead of the alias extension %s declares", manifest.Name, manifest.Name, owner.extension.Name)
	}
	for _, name := range slices.Sorted(maps.Keys(manifest.Metadata.Aliases)) {
		_, err := manifest.Metadata.Alias(name)
		_, userDefined := snapshot.Preferences.Aliases[name]
		owner, owned := owners[name]
		var reason string
		switch {
		case err != nil:
			reason = err.Error()
		case isCommand(cmd.Root(), name):
			reason = "gt " + name + " is a built-in command"
		case name == manifest.Name:
			reason = "it is the name of the extension"
		case userDefined:
			reason = "you have an alias of that name"
		case slices.ContainsFunc(installed, func(e extensions.Extension) bool { return e.Name == name }):
			reason = "extension " + name + " is installed"
		case owned && owner.extension.Name < manifest.Name:
			reason = "extension " + owner.extension.Name + " declares it too"
		case owned:
			a.warn(cmd, "gt %s now runs extension %s instead of extension %s", name, manifest.Name, owner.extension.Name)
		}
		if reason != "" {
			a.warn(cmd, "Ignoring the alias gt %s of extension %s: %s", name, manifest.Name, reason)
		}
	}
}

// checkExtensionName refuses a name that a command, an alias or, unless force, an installed
// extension already has.
func (a *application) checkExtensionName(cmd *cobra.Command, name string, force bool) error {
	if isCommand(cmd.Root(), name) {
		return clierr.Wrap(clierr.Usage, fmt.Errorf("extension %s cannot be installed: gt %s is a built-in command", name, name))
	}
	snapshot, err := a.settings()
	if err != nil {
		return err
	}
	if _, ok := snapshot.Preferences.Aliases[name]; ok {
		return clierr.Wrap(clierr.Usage, fmt.Errorf("extension %s cannot be installed: the alias %s has that name; delete it with gt alias delete %s", name, name, name))
	}
	if installed, err := a.extensionStore().Lookup(name); err == nil && !force {
		if installed.Kind == extensions.KindLocal || installed.Pinned {
			return fmt.Errorf("extension %s is already installed from %s; reinstall it with --force, or remove it with gt extension remove %s", name, extensionOrigin(installed.Manifest), name)
		}
		return fmt.Errorf("extension %s is already installed from %s; upgrade it with gt extension upgrade %s, or reinstall it with --force", name, extensionOrigin(installed.Manifest), name)
	}
	return nil
}

// installLocalExtension installs the gt-NAME executable of the directory raw, which runs in place.
func (a *application) installLocalExtension(cmd *cobra.Command, raw string, opts extensionInstallOptions) error {
	if !filepath.IsAbs(raw) {
		raw = filepath.Join(a.options.cwd, raw)
	}
	dir, err := filepath.Abs(raw)
	if err != nil {
		return err
	}
	prefix, name, err := extensions.ParseRepositoryName(filepath.Base(dir))
	if err != nil {
		return clierr.Wrap(clierr.Usage, fmt.Errorf("%w; the directory is named %s", err, filepath.Base(dir)))
	}
	if err := a.checkExtensionName(cmd, name, opts.force); err != nil {
		return err
	}
	policy, err := a.extensionPolicy()
	if err != nil {
		return err
	}
	manifest := extensions.Manifest{Name: name, Prefix: prefix, Kind: extensions.KindLocal, Credentials: opts.allowCredentials, CompatGH: opts.compatGH}
	if err := policy.allowsInstalled(extensions.Extension{Manifest: manifest}); err != nil {
		return err
	}
	if opts.allowCredentials && policy.noCredentials != "" {
		return fmt.Errorf("extensions cannot be given credentials: %s denies them", policy.noCredentials)
	}
	executable := filepath.Join(dir, filepath.Base(dir))
	if runtime.GOOS == "windows" {
		if _, err := os.Stat(executable + ".exe"); err == nil {
			executable += ".exe"
		}
	}
	info, err := os.Stat(executable)
	if err != nil || !info.Mode().IsRegular() || (runtime.GOOS != "windows" && info.Mode().Perm()&0o111 == 0) {
		return fmt.Errorf("%s has no executable %s", dir, filepath.Base(dir))
	}
	manifest.Path = executable
	if data, err := os.ReadFile(filepath.Join(dir, "gt-extension.yml")); err == nil {
		if manifest.Metadata, err = extensions.ParseMetadata(data); err != nil {
			return err
		}
	}
	if err := checkExtensionMetadata(name, manifest.Metadata); err != nil {
		return err
	}
	a.warnExtensionAliases(cmd, manifest)
	if manifest.Credentials, err = a.grantCredentials(cmd, manifest, opts.allowCredentials, policy); err != nil {
		return err
	}
	stage, err := a.extensionStore().Stage()
	if err != nil {
		return err
	}
	defer os.RemoveAll(stage)
	if err := a.extensionStore().Publish(stage, manifest, opts.force); err != nil {
		return err
	}
	a.done(cmd, "Installed local extension %s from %s; run it with gt %s", name, dir, name)
	return nil
}

func (a *application) extensionUpgradeCommand() *cobra.Command {
	var all, dryRun, force bool
	cmd := &cobra.Command{
		Use: "upgrade {<name>... | --all}", Short: "Upgrade installed extensions", ValidArgsFunction: a.completeExtensions,
		Annotations: map[string]string{"gt.confirm": "true", "gt.text": "extension upgrade does not return a resource"},
		Long: `Upgrade extensions to their latest release, or to the latest commit of the default branch for
script extensions. gt shows the old and new version and digest and asks first.

gt refuses an upgrade when the repository is no longer the one the extension was installed
from, such as after a transfer, and when a release tag it installed now holds a different
asset. Extensions installed with --pin and local extensions are not upgraded; reinstall them
with gt extension install --force.`,
		Example: `$ gt extension upgrade hello
# See what would change
$ gt extension upgrade --all --dry-run
$ gt extension upgrade --all --yes`,
		Args: func(cmd *cobra.Command, args []string) error {
			if len(args) == 0 && !all {
				return clierr.Wrap(clierr.Usage, errors.New("extension upgrade requires extension names or --all"))
			}
			if len(args) > 0 && all {
				return clierr.Wrap(clierr.Usage, errors.New("--all cannot be combined with extension names"))
			}
			if all && !dryRun && !a.options.yes && !a.canPrompt() {
				return clierr.Wrap(clierr.Usage, errors.New("confirmation required to upgrade all extensions; rerun with --yes"))
			}
			return nil
		},
		RunE: func(cmd *cobra.Command, args []string) error {
			store := a.extensionStore()
			var targets []extensions.Extension
			var errs []error
			if all {
				installed, err := store.List()
				targets = installed
				errs = append(errs, err)
			}
			for _, name := range args {
				extension, err := store.Lookup(name)
				if errors.Is(err, extensions.ErrNotInstalled) {
					return clierr.Wrap(clierr.Usage, fmt.Errorf("no extension named %q; gt extension list shows them", name))
				}
				if err != nil {
					return err
				}
				targets = append(targets, extension)
			}
			for _, extension := range targets {
				if err := a.upgradeExtension(cmd, extension, dryRun, force); err != nil {
					if !all || errors.Is(err, terminal.ErrCanceled) {
						return err
					}
					errs = append(errs, fmt.Errorf("%s: %w", extension.Name, err))
				}
			}
			return errors.Join(errs...)
		},
	}
	cmd.Flags().BoolVar(&all, "all", false, "Upgrade every installed extension")
	cmd.Flags().BoolVar(&dryRun, "dry-run", false, "Show the upgrades without installing them")
	cmd.Flags().BoolVar(&force, "force", false, "Reinstall the current version too")
	return cmd
}

func (a *application) upgradeExtension(cmd *cobra.Command, installed extensions.Extension, dryRun, force bool) error {
	ctx := cmd.Context()
	m := installed.Manifest
	switch {
	case m.Kind == extensions.KindLocal:
		a.warn(cmd, "Extension %s runs from %s; it has nothing to upgrade", m.Name, filepath.Dir(m.Path))
		return nil
	case m.Pinned:
		a.warn(cmd, "Extension %s is pinned to %s; reinstall it with gt extension install --force %s to change that", m.Name, cmp.Or(m.Tag, shortCommit(m.Commit)), extensionOrigin(m))
		return nil
	}
	policy, err := a.extensionPolicy()
	if err != nil {
		return err
	}
	if err := policy.allowsInstalled(installed); err != nil {
		return err
	}
	raw := m.Host + "/" + m.Repository()
	source, err := a.extensionSourceFor(ctx, raw)
	if err != nil {
		return err
	}
	c, err := a.resolveExtension(ctx, source, "")
	if err != nil {
		return err
	}
	// A transferred or renamed repository keeps its ID, and the old URL redirects to it.
	if c.manifest.RepoID != m.RepoID || c.manifest.Host != m.Host || !strings.EqualFold(c.manifest.Owner, m.Owner) {
		return fmt.Errorf("%s is no longer the repository extension %s was installed from, such as after a transfer; reinstall it with gt extension install --force %s to trust it", raw, m.Name, extensionOrigin(c.manifest))
	}
	if c.manifest.Name != m.Name {
		return fmt.Errorf("the repository of extension %s is now named %s; reinstall it to use the new name", m.Name, c.manifest.Repo)
	}
	if err := policy.allows(c.manifest.Host, c.manifest.Owner); err != nil {
		return err
	}
	if c.manifest.Tag != "" && c.manifest.Tag == m.Tag && c.manifest.Kind != m.Kind {
		return fmt.Errorf("release %s of %s now installs %s instead of %s; gt refuses a release that changed after it was installed", m.Tag, m.Repository(), installedForm(c.manifest), installedForm(m))
	}
	from, to := cmp.Or(m.Tag, shortCommit(m.Commit)), cmp.Or(c.manifest.Tag, shortCommit(c.manifest.Commit))
	current := c.manifest.Tag == m.Tag && c.manifest.Commit == m.Commit && c.manifest.Kind == m.Kind
	if current && c.manifest.Kind == extensions.KindScript && !force {
		a.done(cmd, "Extension %s is up to date (%s)", m.Name, from)
		return nil
	}
	if dryRun && !current {
		_, err := fmt.Fprintf(cmd.OutOrStdout(), "%s\t%s\t%s\n", m.Name, terminal.SafeLine(from), terminal.SafeLine(to))
		return err
	}
	if (!current || force) && !dryRun && !a.options.yes && !a.canPrompt() {
		return clierr.Wrap(clierr.Usage, fmt.Errorf("confirmation required to upgrade extension %s; rerun with --yes", m.Name))
	}
	stage, err := a.stageExtension(ctx, &c)
	if err != nil {
		return err
	}
	defer os.RemoveAll(stage)
	if c.manifest.Tag != "" && c.manifest.Tag == m.Tag && c.manifest.SHA256 != m.SHA256 {
		return fmt.Errorf("release %s of %s now holds a different %s (SHA-256 %s, installed %s); gt refuses a release that changed after it was installed", m.Tag, m.Repository(), c.manifest.Executable(), c.manifest.SHA256, m.SHA256)
	}
	if current && !force {
		a.done(cmd, "Extension %s is up to date (%s)", m.Name, from)
		return nil
	}
	if dryRun {
		return nil
	}
	if c.manifest.DigestSource == "none" && a.options.yes {
		a.warn(cmd, "Release %s of %s publishes no SHA-256 for %s; upgrading to it because of --yes", c.manifest.Tag, c.manifest.Repository(), c.manifest.Asset)
	}
	a.warnExtensionAliases(cmd, c.manifest)
	if !a.options.yes && a.canPrompt() {
		fmt.Fprintf(cmd.ErrOrStderr(), "Extension %s: %s → %s\n  installed    %s\n%s", m.Name, terminal.SafeLine(from), terminal.SafeLine(to), m.SHA256, describeCandidate(c))
	}
	if err := a.confirmAction(cmd, extensionConfirmation(fmt.Sprintf("Upgrade extension %s to %s", m.Name, to), "upgrade extension "+m.Name, c)); err != nil {
		return err
	}
	c.manifest.Credentials, c.manifest.CompatGH = m.Credentials && policy.noCredentials == "", m.CompatGH
	if c.manifest.Metadata.Credentials && !m.Metadata.Credentials && !m.Credentials {
		a.warn(cmd, "Extension %s %s asks for your Gitea token; allow it with gt extension install --force --allow-credentials %s", m.Name, to, raw)
	}
	if err := a.extensionStore().Publish(stage, c.manifest, true); err != nil {
		return err
	}
	a.done(cmd, "Upgraded extension %s from %s to %s", m.Name, from, to)
	return nil
}

// installedForm words what an extension installs: a release asset or a script of its repository.
func installedForm(m extensions.Manifest) string {
	if m.Kind == extensions.KindBinary {
		return "the asset " + m.Asset
	}
	return "the script " + m.Executable()
}
