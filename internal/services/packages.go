// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package services

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"slices"
	"strings"

	"gitea.dev/cli/internal/transport"
	gitea "gitea.dev/sdk"
)

// Package is one version of a package in an owner's package registry.
type Package struct {
	ID         int64  `json:"id"`
	Owner      string `json:"owner"`
	Type       string `json:"type"`
	Name       string `json:"name"`
	Version    string `json:"version"`
	Repository string `json:"repository"`
	Creator    string `json:"creator"`
	URL        string `json:"url"`
	CreatedAt  string `json:"created_at"`
}

func packageFrom(p *gitea.Package) Package {
	result := Package{
		ID: p.ID, Owner: login(p.Owner), Type: p.Type, Name: p.Name, Version: p.Version,
		Creator: login(p.Creator), URL: p.HTMLURL, CreatedAt: timestamp(p.CreatedAt),
	}
	if p.Repository != nil {
		result.Repository = p.Repository.FullName
	}
	return result
}

// PackageFile is one file of a package version.
type PackageFile struct {
	ID     int64  `json:"id"`
	Name   string `json:"name"`
	Size   int64  `json:"size"`
	SHA256 string `json:"sha256"`
}

func packageFileFrom(f *gitea.PackageFile) PackageFile {
	return PackageFile{ID: f.ID, Name: f.Name, Size: f.Size, SHA256: f.SHA256}
}

// Packages manages the package registry of a user or organization.
type Packages struct {
	Client *transport.Client
	Owner  string
}

// PackageListOptions selects packages by type and name.
type PackageListOptions struct {
	Type, Query string
	Limit, Page int
}

// List returns a page of the owner's packages, one entry per version.
func (s Packages) List(ctx context.Context, opts PackageListOptions) (Page[Package], error) {
	packages, err := callPage(ctx, s.Client, opts.Limit, opts.Page, func(ctx context.Context, c *gitea.Client, list gitea.ListOptions) ([]*gitea.Package, *gitea.Response, error) {
		return c.Packages.ListPackages(ctx, s.Owner, gitea.ListPackagesOptions{ListOptions: list, Type: opts.Type, Q: opts.Query})
	})
	return convertPage(packages, packageFrom), Classify(err, "owner", s.Owner, "")
}

// Versions returns a page of the versions of one package.
func (s Packages) Versions(ctx context.Context, kind, name string, limit, page int) (Page[Package], error) {
	versions, err := callPage(ctx, s.Client, limit, page, func(ctx context.Context, c *gitea.Client, list gitea.ListOptions) ([]*gitea.Package, *gitea.Response, error) {
		return c.Packages.ListPackageVersions(ctx, s.Owner, kind, name, gitea.ListPackagesOptions{ListOptions: list})
	})
	return convertPage(versions, packageFrom), Classify(err, "package", s.key(kind, name, ""), "")
}

// View returns one version of a package, or its latest version when version is empty.
func (s Packages) View(ctx context.Context, kind, name, version string) (Package, error) {
	p, _, err := call(ctx, s.Client, func(ctx context.Context, c *gitea.Client) (*gitea.Package, *gitea.Response, error) {
		if version == "" {
			return c.Packages.GetLatestPackage(ctx, s.Owner, kind, name)
		}
		return c.Packages.GetPackage(ctx, s.Owner, kind, name, version)
	})
	if err != nil {
		return Package{}, Classify(err, "package", s.key(kind, name, version), "")
	}
	return packageFrom(p), nil
}

// Files lists the files of one package version.
func (s Packages) Files(ctx context.Context, kind, name, version string) ([]PackageFile, error) {
	files, _, err := call(ctx, s.Client, func(ctx context.Context, c *gitea.Client) ([]*gitea.PackageFile, *gitea.Response, error) {
		return c.Packages.ListPackageFiles(ctx, s.Owner, kind, name, version)
	})
	return convertAll(files, packageFileFrom), Classify(err, "package", s.key(kind, name, version), "")
}

// Delete deletes one version of a package, or every version when version is empty.
func (s Packages) Delete(ctx context.Context, kind, name, version string) error {
	err := do(ctx, s.Client, func(ctx context.Context, c *gitea.Client) (*gitea.Response, error) {
		if version == "" {
			return c.Packages.DeleteAllPackageVersions(ctx, s.Owner, kind, name)
		}
		return c.Packages.DeletePackage(ctx, s.Owner, kind, name, version)
	})
	return Classify(err, "package", s.key(kind, name, version), "")
}

// Link links a package to a repository of its owner, or with repository empty unlinks it.
func (s Packages) Link(ctx context.Context, kind, name, repository string) error {
	err := do(ctx, s.Client, func(ctx context.Context, c *gitea.Client) (*gitea.Response, error) {
		if repository == "" {
			return c.Packages.UnlinkPackage(ctx, s.Owner, kind, name)
		}
		return c.Packages.LinkPackage(ctx, s.Owner, kind, name, repository)
	})
	apiErr, ok := errors.AsType[*transport.APIError](err)
	switch {
	case ok && apiErr.Status == http.StatusNotFound && strings.Contains(apiErr.Message, "repository does not exist"):
		return &NotFoundError{Kind: "repository", Key: s.Owner + "/" + repository, Err: err}
	case ok && apiErr.Status == http.StatusBadRequest && repository == "":
		// Gitea answers "invalid argument" for a package without a repository.
		return fmt.Errorf("package %s is not linked to a repository", s.key(kind, name, ""))
	case ok && apiErr.Status == http.StatusBadRequest:
		// It answers the same for a package that is linked already; linking it again to its
		// repository changes nothing.
		if p, viewErr := s.View(ctx, kind, name, ""); viewErr == nil && p.Repository != "" {
			if strings.EqualFold(p.Repository, s.Owner+"/"+repository) {
				return nil
			}
			return fmt.Errorf("package %s is already linked to %s; unlink it first", s.key(kind, name, ""), p.Repository)
		}
	}
	return Classify(err, "package", s.key(kind, name, ""), "")
}

// CheckGenericUpload refuses to upload files to a version of a generic package that holds a file
// of the same name already, ignoring case as Gitea does; a new version holds none.
func (s Packages) CheckGenericUpload(ctx context.Context, name, version string, files []string) error {
	if err := s.checkRegistryAccount(); err != nil {
		return err
	}
	existing, err := s.Files(ctx, "generic", name, version)
	if IsNotFound(err) {
		return nil
	}
	if err != nil {
		return err
	}
	for _, file := range existing {
		if slices.ContainsFunc(files, func(upload string) bool { return strings.EqualFold(upload, file.Name) }) {
			return genericFileExists(name, version, file.Name, nil)
		}
	}
	return nil
}

// UploadGeneric streams one file into a version of a generic package, creating either as needed.
// TODO(sdk): the SDK has no client for the package registries' upload endpoints.
func (s Packages) UploadGeneric(ctx context.Context, name, version, file string, input io.Reader) error {
	if err := s.checkRegistryAccount(); err != nil {
		return err
	}
	endpoint := fmt.Sprintf("/api/packages/%s/generic/%s/%s/%s", url.PathEscape(s.Owner), url.PathEscape(name), url.PathEscape(version), url.PathEscape(file))
	response, err := s.Client.Do(ctx, http.MethodPut, endpoint, input, http.Header{"Content-Type": {"application/octet-stream"}})
	if apiErr, ok := errors.AsType[*transport.APIError](err); ok && apiErr.Status == http.StatusConflict {
		return genericFileExists(name, version, file, err)
	}
	if err != nil {
		return Classify(err, "package file", s.key("generic", name, version)+"/"+file, "")
	}
	return response.Body.Close()
}

// errRegistrySignature refuses a package upload from an SSH key account up front: Gitea's package
// registries do not accept HTTP signatures, and would answer only "authentication rejected".
var errRegistrySignature = errors.New("Gitea's package registry does not accept SSH key accounts; log in with a token to upload packages: gt auth login --with-token")

// checkRegistryAccount refuses an account that the package registries cannot authenticate.
func (s Packages) checkRegistryAccount() error {
	if s.Client.Signer != nil {
		return errRegistrySignature
	}
	return nil
}

// genericFileExists reports a file that a version of a generic package holds already, such as
// "file tool-linux already exists in tool@1.0".
func genericFileExists(name, version, file string, err error) error {
	return &ConflictError{Reason: ReasonExists, Kind: "file", Key: file, Scope: name + "@" + version, Err: err}
}

// key names a package in errors, such as "generic/tool@1.0" of alice.
func (s Packages) key(kind, name, version string) string {
	key := s.Owner + "/" + kind + "/" + name
	if version != "" {
		key += "@" + version
	}
	return key
}
