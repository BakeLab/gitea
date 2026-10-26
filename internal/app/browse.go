// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package app

import (
	"errors"
	"fmt"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"

	"gitea.dev/cli/internal/clierr"
	"gitea.dev/cli/internal/services"
	"github.com/spf13/cobra"
)

var (
	issueNumber = regexp.MustCompile(`^#?[0-9]+$`)
	commitSHA   = regexp.MustCompile(`^[0-9a-f]{7,64}$`)
	lineSuffix  = regexp.MustCompile(`:([0-9]+)(?:-([0-9]+))?$`)
)

// browsePages are the repository pages that a flag of gt browse opens, by flag.
var browsePages = []struct{ flag, page, usage string }{
	{"actions", "actions", "Open the repository's Actions"},
	{"projects", "projects", "Open the repository's projects"},
	{"releases", "releases", "Open the repository's releases"},
	{"settings", "settings", "Open the repository's settings"},
	{"wiki", "wiki", "Open the repository's wiki"},
}

func (a *application) browseCommand() *cobra.Command {
	var branch, commit string
	var print bool
	pages := make([]bool, len(browsePages))
	command := &cobra.Command{
		Use:   "browse [<number> | <path>[:<line>] | <commit-sha>]",
		Short: "Open the repository in the browser",
		Long: `Open the repository, or one of its issues, pull requests, files, directories or commits, in the
browser.

A number opens that issue or pull request, and a hexadecimal SHA of at least 7 characters that
commit. Anything else is a path, opened on the default branch unless --branch or --commit selects
another; write ./NAME for a path that looks like a number or SHA. In a clone, a path is relative
to the current directory, otherwise to the repository root. Append :LINE or :START-END to a file
path to select lines. A value after --commit and a space is the commit when it is a SHA, or a
revision of the clone, such as a branch, tag or HEAD~1, that is not also a local path.`,
		Example: `$ gt browse
$ gt browse 217
$ gt browse internal/app/app.go:42 --branch develop
$ gt browse 77507cd
$ gt browse --commit
$ gt browse --commit 77507cd docs
$ gt browse --releases -R alice/project
$ gt browse --no-browser docs/README.md`,
		Annotations: textOutput("browse opens a web page"),
		Args: func(cmd *cobra.Command, args []string) error {
			_, args = a.spacedCommit(cmd, commit, args)
			return nonBlankArgs(cobra.MaximumNArgs(1), "number, path or commit SHA")(cmd, args)
		},
		RunE: func(cmd *cobra.Command, args []string) error {
			commit, args = a.spacedCommit(cmd, commit, args)
			arg := ""
			if len(args) > 0 {
				arg = args[0]
			}
			var names []string
			page := ""
			for i, p := range browsePages {
				names = append(names, "--"+p.flag)
				if pages[i] {
					page = p.page
				}
			}
			exclusive := setFlags(append(names, "--branch", "--commit"), slices.Concat(pages, []bool{cmd.Flags().Changed("branch"), cmd.Flags().Changed("commit")})...)
			switch {
			case len(exclusive) > 1:
				return clierr.Wrap(clierr.Usage, fmt.Errorf("%s cannot be combined with %s", exclusive[0], exclusive[1]))
			case len(exclusive) > 0 && issueNumber.MatchString(arg):
				return clierr.Wrap(clierr.Usage, fmt.Errorf("%s cannot be combined with an issue or pull request number", exclusive[0]))
			case len(exclusive) > 0 && commitSHA.MatchString(arg):
				return clierr.Wrap(clierr.Usage, fmt.Errorf("%s cannot be combined with a commit SHA", exclusive[0]))
			case page != "" && arg != "":
				return clierr.Wrap(clierr.Usage, fmt.Errorf("%s cannot be combined with a path", exclusive[0]))
			case cmd.Flags().Changed("commit") && commit == "":
				return clierr.Wrap(clierr.Usage, errors.New("--commit requires a commit; omit its value for the clone's HEAD"))
			}
			if cmd.Flags().Changed("branch") {
				if err := validateBranch(branch); err != nil {
					return err
				}
			}
			filePath, lines := arg, ""
			if match := lineSuffix.FindStringSubmatchIndex(arg); match != nil && match[0] > 0 {
				filePath, lines = arg[:match[0]], "#L"+arg[match[2]:match[3]]
				if match[4] >= 0 {
					lines += "-L" + arg[match[4]:match[5]]
				}
			}
			var number int64
			if issueNumber.MatchString(arg) {
				var err error
				if number, err = strconv.ParseInt(strings.TrimPrefix(arg, "#"), 10, 64); err != nil {
					return clierr.Wrap(clierr.Usage, fmt.Errorf("invalid issue or pull request number %q", arg))
				}
			}
			needsDefaultBranch := filePath != "" && branch == "" && commit == "" && !issueNumber.MatchString(arg) && !commitSHA.MatchString(arg)
			t, err := a.resolveTarget(cmd.Context(), true, needsDefaultBranch || issueNumber.MatchString(arg))
			if err != nil && issueNumber.MatchString(arg) && clierr.ExitCode(err) != clierr.Usage {
				t, err = a.resolveTarget(cmd.Context(), true, false) // Gitea's /issues/N suffices without an account.
			}
			if err != nil {
				return err
			}
			base := repositoryURL(t)
			link := base
			switch {
			case page != "":
				link += "/" + page
			case issueNumber.MatchString(arg):
				client := t.client
				if client == nil {
					client = a.instanceClient(t.Host, "", t.host) // The number of a public repository is found without an account.
				}
				// Gitea sends /issues/N of a pull request to an external issue tracker, and /pulls/N of an
				// issue to /issues/N: /pulls/N is right but where pull requests are turned off.
				kind := "/pulls/"
				if found, err := (services.Issues{Client: client, Repository: t.Repo}).Kind(cmd.Context(), number); err == nil && found == services.KindIssue {
					kind = "/issues/"
				}
				link += kind + strconv.FormatInt(number, 10)
			case commitSHA.MatchString(arg):
				link += "/commit/" + arg
			default:
				switch {
				case commit != "":
					sha := commit
					if !commitSHA.MatchString(sha) {
						if a.cloneRemote(cmd.Context(), t) == "" {
							return clierr.Wrap(clierr.Usage, fmt.Errorf("--commit without a SHA requires a clone of %s", t.Repo))
						}
						if sha, err = a.gitProbe().Output(cmd.Context(), a.options.cwd, false, "rev-parse", "--verify", "--quiet", commit+"^{commit}"); err != nil || sha == "" {
							return clierr.Wrap(clierr.Usage, fmt.Errorf("--commit %q requires a Git clone with that commit", commit))
						}
					}
					link += "/src/commit/" + sha
				case branch != "":
					link = branchURL(services.Repository{URL: base}, branch)
				}
				if filePath = a.repositoryPath(cmd, t, filePath); filePath == "" {
					break
				}
				if branch == "" && commit == "" {
					repository, err := (services.Repositories{Client: t.client}).View(cmd.Context(), t.Repo)
					if err != nil {
						return err
					}
					link = branchURL(services.Repository{URL: base}, repository.DefaultBranch)
				}
				link += "/" + escapePath(filePath)
				if lines != "" {
					link += "?display=source" + lines // Line anchors exist only in the source view of rendered files such as Markdown.
				}
			}
			if print {
				_, err := fmt.Fprintln(cmd.OutOrStdout(), link)
				return err
			}
			return a.openBrowser(cmd, link)
		},
	}
	flags := command.Flags()
	flags.StringVarP(&branch, "branch", "b", "", "Open a path, or the root, of this branch")
	flags.StringVarP(&commit, "commit", "c", "", "Open a path, or the root, at this commit; without a value, at HEAD of the clone")
	flags.Lookup("commit").NoOptDefVal = "HEAD"
	flags.BoolVarP(&print, "no-browser", "n", false, "Print the URL instead of opening it")
	for i, page := range browsePages {
		flags.BoolVarP(&pages[i], page.flag, page.flag[:1], false, page.usage)
	}
	_ = command.RegisterFlagCompletionFunc("branch", a.completeBranches())
	command.ValidArgsFunction = firstArg(completeFiles)
	return command
}

// spacedCommit takes the commit of "--commit SHA" or "--commit REF" from args, where the optional
// value of --commit leaves it. A REF is one of the clone that is not also a path.
func (a *application) spacedCommit(cmd *cobra.Command, commit string, args []string) (string, []string) {
	flag := cmd.Flags().Lookup("commit")
	if !flag.Changed || commit != flag.NoOptDefVal || len(args) == 0 {
		return commit, args
	}
	if commitSHA.MatchString(args[0]) {
		return args[0], args[1:]
	}
	if _, err := os.Lstat(filepath.Join(a.options.cwd, args[0])); !errors.Is(err, os.ErrNotExist) || issueNumber.MatchString(args[0]) {
		return commit, args
	}
	if sha, err := a.gitProbe().Output(cmd.Context(), a.options.cwd, false, "rev-parse", "--verify", "--quiet", args[0]+"^{commit}"); err == nil && sha != "" {
		return args[0], args[1:]
	}
	return commit, args
}

// repositoryPath resolves a path argument of gt browse against the repository root: relative to
// the current directory in a clone of the repository, else to the root. It is empty for the root.
func (a *application) repositoryPath(cmd *cobra.Command, t target, raw string) string {
	if raw == "" {
		return ""
	}
	p := filepath.ToSlash(raw)
	if !path.IsAbs(p) && t.repoFromClone() {
		prefix, _ := a.gitProbe().Output(cmd.Context(), a.options.cwd, false, "rev-parse", "--show-prefix")
		p = path.Join(prefix, p)
	}
	return strings.TrimPrefix(path.Clean("/"+p), "/")
}

// escapePath escapes each segment of a slash-separated path for a URL.
func escapePath(p string) string {
	segments := strings.Split(p, "/")
	for i, segment := range segments {
		segments[i] = url.PathEscape(segment)
	}
	return strings.Join(segments, "/")
}
