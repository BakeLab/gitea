// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

// Package extensions stores, verifies and runs the executables that `gt extension` installs. It
// performs no network requests.
package extensions

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"slices"
	"strings"
	"time"

	"gitea.dev/cli/internal/filelock"

	"github.com/kballard/go-shellquote"
	"gopkg.in/yaml.v3"
)

// Prefixes of extension executables; gt- wins over gh- for the same name.
const (
	PrefixGT = "gt"
	PrefixGH = "gh"
)

// Kinds of installed extensions.
const (
	// KindBinary is a precompiled release asset.
	KindBinary = "binary"
	// KindScript is an executable at the root of a cloned repository.
	KindScript = "script"
	// KindLocal is an executable in a local directory, run in place while it is developed.
	KindLocal = "local"
)

// Sources an extension is installed from.
const (
	SourceGitea  = "gitea"
	SourceGitHub = "github"
)

// LockTimeout bounds the wait for another gt installing or removing an extension.
const LockTimeout = 30 * time.Second

var (
	namePattern  = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,38}$`)
	aliasPattern = regexp.MustCompile(`^[a-z][a-z0-9-]*$`)
)

// ErrNotInstalled reports a name that no installed extension has.
var ErrNotInstalled = errors.New("extension is not installed")

// ValidName reports whether name can name an extension: gt-NAME must be a safe file name and a
// command word.
func ValidName(name string) bool {
	return namePattern.MatchString(name) && !strings.HasSuffix(name, "-")
}

// ParseRepositoryName splits a repository name such as gt-hello into its prefix and the
// extension name hello.
func ParseRepositoryName(repo string) (prefix, name string, err error) {
	for _, prefix := range []string{PrefixGT, PrefixGH} {
		if name, ok := strings.CutPrefix(strings.ToLower(repo), prefix+"-"); ok {
			if !ValidName(name) {
				return "", "", fmt.Errorf("invalid extension name %q: use lowercase letters, digits and hyphens", name)
			}
			return prefix, name, nil
		}
	}
	return "", "", fmt.Errorf("extension repository %q must be named gt-NAME or gh-NAME", repo)
}

// Metadata is what an extension declares about itself in gt-extension.yml. gt does not verify it.
type Metadata struct {
	Description string   `yaml:"description,omitempty" json:"description"`
	MinVersion  string   `yaml:"min_gt_version,omitempty" json:"min_gt_version"`
	Platforms   []string `yaml:"platforms,omitempty" json:"platforms"`
	Credentials bool     `yaml:"credentials,omitempty" json:"credentials"`
	// Completion is "cobra" for an executable that answers Cobra's __complete protocol.
	Completion string `yaml:"completion,omitempty" json:"completion"`
	// Aliases maps a top-level command word to the arguments it passes to the extension, so
	// user-group: "user-group" makes gt user-group run gt NAME user-group.
	Aliases map[string]string `yaml:"aliases,omitempty" json:"aliases"`
}

// ParseMetadata reads gt-extension.yml, rejecting unknown keys so a misspelled declaration is noticed.
func ParseMetadata(data []byte) (Metadata, error) {
	var metadata Metadata
	decoder := yaml.NewDecoder(bytes.NewReader(data))
	decoder.KnownFields(true)
	// An empty file declares nothing.
	if err := decoder.Decode(&metadata); err != nil && !errors.Is(err, io.EOF) {
		return Metadata{}, fmt.Errorf("parse gt-extension.yml: %w", err)
	}
	if metadata.Completion != "" && metadata.Completion != "cobra" {
		return Metadata{}, errors.New("parse gt-extension.yml: completion must be cobra")
	}
	return metadata, nil
}

// Alias returns the extension arguments the alias name stands for, or why gt ignores it. An
// unusable alias is ignored, not an error, so a mistake in one never prevents installing the extension.
func (m Metadata) Alias(name string) ([]string, error) {
	if !aliasPattern.MatchString(name) {
		return nil, errors.New("its name must use lowercase letters, digits and hyphens")
	}
	words, err := shellquote.Split(m.Aliases[name])
	if err != nil {
		return nil, fmt.Errorf("its arguments cannot be parsed: %w", err)
	}
	if len(words) == 0 {
		return nil, errors.New("it needs arguments for the extension, such as " + name)
	}
	return words, nil
}

// Supports reports whether the declared platforms include goos/goarch; none declared means any.
func (m Metadata) Supports(goos, goarch string) bool {
	return len(m.Platforms) == 0 || slices.Contains(m.Platforms, goos+"/"+goarch) || slices.Contains(m.Platforms, goos)
}

// Manifest records where an installed extension came from and what it was granted.
type Manifest struct {
	Name   string `yaml:"name" json:"name"`
	Prefix string `yaml:"prefix" json:"prefix"`
	Kind   string `yaml:"kind" json:"kind"`
	Source string `yaml:"source,omitempty" json:"source"`
	// Host is the instance URL, or https://github.com.
	Host   string `yaml:"host,omitempty" json:"host"`
	Owner  string `yaml:"owner,omitempty" json:"owner"`
	Repo   string `yaml:"repo,omitempty" json:"repo"`
	RepoID int64  `yaml:"repo_id,omitempty" json:"repo_id"`
	Tag    string `yaml:"tag,omitempty" json:"tag"`
	Commit string `yaml:"commit,omitempty" json:"commit"`
	// Asset is the release asset a binary extension was downloaded from.
	Asset  string `yaml:"asset,omitempty" json:"asset"`
	SHA256 string `yaml:"sha256,omitempty" json:"sha256"`
	// DigestSource names where the digest checked at install came from: server, checksums.txt or
	// none, when only the digest recorded at install protects the executable.
	DigestSource string `yaml:"digest_source,omitempty" json:"digest_source"`
	Pinned       bool   `yaml:"pinned,omitempty" json:"pinned"`
	Credentials  bool   `yaml:"credentials,omitempty" json:"credentials"`
	CompatGH     bool   `yaml:"compat_gh,omitempty" json:"compat_gh"`
	// Path is the executable of a local extension.
	Path     string   `yaml:"path,omitempty" json:"path"`
	Metadata Metadata `yaml:"metadata,omitempty" json:"metadata"`
}

// Repository is the extension's repository as OWNER/REPO.
func (m Manifest) Repository() string {
	if m.Owner == "" {
		return ""
	}
	return m.Owner + "/" + m.Repo
}

// Executable is the file name of the extension's executable, such as gt-hello or gt-hello.exe.
func (m Manifest) Executable() string {
	name := m.Prefix + "-" + m.Name
	if runtime.GOOS == "windows" && m.Kind == KindBinary {
		name += ".exe"
	}
	return name
}

// Extension is an installed extension.
type Extension struct {
	Manifest
	// Dir holds the manifest and the executable, or for a script extension the clone.
	Dir string
}

// Path is the executable to run.
func (e Extension) Path() string {
	switch e.Kind {
	case KindLocal:
		return e.Manifest.Path
	case KindScript:
		return filepath.Join(e.Dir, "src", e.Executable())
	}
	return filepath.Join(e.Dir, e.Executable())
}

// Store keeps installed extensions in Dir, one directory per extension name.
type Store struct{ Dir string }

const manifestFile = "manifest.yml"

// Lookup returns the installed extension name, or ErrNotInstalled.
func (s Store) Lookup(name string) (Extension, error) {
	if !ValidName(name) {
		return Extension{}, ErrNotInstalled
	}
	dir := filepath.Join(s.Dir, name)
	data, err := os.ReadFile(filepath.Join(dir, manifestFile))
	if errors.Is(err, fs.ErrNotExist) {
		return Extension{}, ErrNotInstalled
	}
	if err != nil {
		return Extension{}, err
	}
	// gt writes the manifest itself; a field a newer gt added must not break an older one.
	var manifest Manifest
	if err := yaml.Unmarshal(data, &manifest); err != nil {
		return Extension{}, fmt.Errorf("parse %s: %w; reinstall extension %s with gt extension install --force, or remove it", filepath.Join(dir, manifestFile), err, name)
	}
	if manifest.Name != name || (manifest.Prefix != PrefixGT && manifest.Prefix != PrefixGH) {
		return Extension{}, fmt.Errorf("%s does not describe extension %s", filepath.Join(dir, manifestFile), name)
	}
	return Extension{Manifest: manifest, Dir: dir}, nil
}

// List returns the installed extensions sorted by name. A broken manifest is reported, not skipped.
func (s Store) List() ([]Extension, error) {
	entries, err := os.ReadDir(s.Dir)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var installed []Extension
	var errs []error
	for _, entry := range entries {
		if !entry.IsDir() || !ValidName(entry.Name()) {
			continue
		}
		extension, err := s.Lookup(entry.Name())
		switch {
		case errors.Is(err, ErrNotInstalled):
		case err != nil:
			errs = append(errs, err)
		default:
			installed = append(installed, extension)
		}
	}
	return installed, errors.Join(errs...)
}

// Names returns the names of the installed extensions that are readable.
func (s Store) Names() map[string]bool {
	installed, _ := s.List()
	names := map[string]bool{}
	for _, extension := range installed {
		names[extension.Name] = true
	}
	return names
}

// locked runs fn while holding the store's lock, so concurrent installs cannot interleave.
func (s Store) locked(fn func() error) error {
	if err := os.MkdirAll(s.Dir, 0o700); err != nil {
		return err
	}
	return filelock.New(filepath.Join(s.Dir, ".lock"), LockTimeout).WithLock(fn)
}

// Stage returns a fresh directory to download an extension into. Publish moves it into place;
// the caller removes it when it is not published.
func (s Store) Stage() (string, error) {
	if err := os.MkdirAll(s.Dir, 0o700); err != nil {
		return "", err
	}
	return os.MkdirTemp(s.Dir, ".staging-")
}

// Publish installs the staged directory stage with manifest, replacing an installed extension
// of the same name only when replace is set.
func (s Store) Publish(stage string, manifest Manifest, replace bool) error {
	if !ValidName(manifest.Name) {
		return fmt.Errorf("invalid extension name %q", manifest.Name)
	}
	data, err := yaml.Marshal(manifest)
	if err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(stage, manifestFile), data, 0o600); err != nil {
		return err
	}
	return s.locked(func() error {
		dir := filepath.Join(s.Dir, manifest.Name)
		if _, err := os.Lstat(dir); err != nil {
			return os.Rename(stage, dir)
		}
		if !replace {
			return fmt.Errorf("extension %s is already installed", manifest.Name)
		}
		old := stage + ".old"
		if err := os.Rename(dir, old); err != nil {
			return err
		}
		if err := os.Rename(stage, dir); err != nil {
			return errors.Join(err, os.Rename(old, dir))
		}
		// The new version is in place; Windows keeps a running old executable from being removed.
		_ = os.RemoveAll(old)
		return nil
	})
}

// Remove deletes the installed extension name, also when its manifest is broken.
func (s Store) Remove(name string) error {
	return s.locked(func() error {
		if _, err := s.Lookup(name); errors.Is(err, ErrNotInstalled) {
			return err
		}
		trash, err := os.MkdirTemp(s.Dir, ".removed-")
		if err != nil {
			return err
		}
		// Moving the directory aside first takes the extension away at once for a concurrent run.
		if err := os.Rename(filepath.Join(s.Dir, name), filepath.Join(trash, name)); err != nil {
			return errors.Join(err, os.Remove(trash))
		}
		return os.RemoveAll(trash)
	})
}
