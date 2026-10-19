// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package services

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/url"

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
	return Classify(err, "package", s.key(kind, name, ""), "")
}

// UploadGeneric streams one file into a version of a generic package, creating either as needed.
// TODO(sdk): the SDK has no client for the package registries' upload endpoints.
func (s Packages) UploadGeneric(ctx context.Context, name, version, file string, input io.Reader) error {
	endpoint := fmt.Sprintf("/api/packages/%s/generic/%s/%s/%s", url.PathEscape(s.Owner), url.PathEscape(name), url.PathEscape(version), url.PathEscape(file))
	response, err := s.Client.Do(ctx, http.MethodPut, endpoint, input, http.Header{"Content-Type": {"application/octet-stream"}})
	if err != nil {
		return Classify(err, "package file", s.key("generic", name, version)+"/"+file, "")
	}
	return response.Body.Close()
}

// key names a package in errors, such as "generic/tool@1.0" of alice.
func (s Packages) key(kind, name, version string) string {
	key := s.Owner + "/" + kind + "/" + name
	if version != "" {
		key += "@" + version
	}
	return key
}
