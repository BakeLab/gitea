# <img alt='tea logo' src='https://gitea.com/repo-avatars/550-80a3a8c2ab0e2c2d69f296b7f8582485' height="40"/> *T E A*

[![License: MIT](https://img.shields.io/badge/License-MIT-blue.svg)](https://opensource.org/licenses/MIT)
[![Release](https://raster.shields.io/badge/dynamic/json.svg?label=release&url=https://gitea.com/api/v1/repos/gitea/tea/releases&query=$[0].tag_name)](https://gitea.com/gitea/tea/releases)
[![Join the chat at https://img.shields.io/discord/322538954119184384.svg](https://img.shields.io/discord/322538954119184384.svg)](https://discord.gg/Gitea)
[![Go Report Card](https://goreportcard.com/badge/code.gitea.io/tea)](https://goreportcard.com/report/code.gitea.io/tea) [![GoDoc](https://pkg.go.dev/badge/code.gitea.io/tea?status.svg)](https://godoc.org/code.gitea.io/tea)

## The official CLI for Gitea

![demo gif](./demo.gif)

```
   tea - command line tool to interact with Gitea
   version 0.8.0-preview

 USAGE
   tea command [subcommand] [command options] [arguments...]

 DESCRIPTION
   tea is a productivity helper for Gitea. It can be used to manage most entities on
   one or multiple Gitea instances & provides local helpers like 'tea pr checkout'.
   
   tea tries to make use of context provided by the repository in $PWD if available.
   tea works best in a upstream/fork workflow, when the local main branch tracks the
   upstream repo. tea assumes that local git state is published on the remote before
   doing operations with tea.    Configuration is persisted in $XDG_CONFIG_HOME/tea.

 COMMANDS
   help, h  Shows a list of commands or help for one command
   ENTITIES:
     issues, issue, i                  List, create and update issues
     pulls, pull, pr                   Manage and checkout pull requests
     labels, label                     Manage issue labels
     milestones, milestone, ms         List and create milestones
     releases, release, r              Manage releases
     times, time, t                    Operate on tracked times of a repository's issues & pulls
     organizations, organization, org  List, create, delete organizations
     repos, repo                       Show repository details
     comment, c                        Add a comment to an issue / pr
   HELPERS:
     open, o                         Open something of the repository in web browser
     notifications, notification, n  Show notifications
     clone, C                        Clone a repository locally
   SETUP:
     logins, login                  Log in to a Gitea server
     logout                         Log out from a Gitea server
     shellcompletion, autocomplete  Install shell completion for tea
     whoami                         Show current logged in user

 OPTIONS
   --help, -h     show help (default: false)
   --version, -v  print the version (default: false)

 EXAMPLES
   tea login add                       # add a login once to get started

   tea pulls                           # list open pulls for the repo in $PWD
   tea pulls --repo $HOME/foo          # list open pulls for the repo in $HOME/foo
   tea pulls --remote upstream         # list open pulls for the repo pointed at by
                                       # your local "upstream" git remote
   # list open pulls for any gitea repo at the given login instance
   tea pulls --repo gitea/tea --login gitea.com

   tea milestone issues 0.7.0          # view open issues for milestone '0.7.0'
   tea issue 189                       # view contents of issue 189
   tea open 189                        # open web ui for issue 189
   tea open milestones                 # open web ui for milestones

   # send gitea desktop notifications every 5 minutes (bash + libnotify)
   while :; do tea notifications --mine -o simple | xargs -i notify-send {}; sleep 300; done

 ABOUT
   Written & maintained by The Gitea Authors.
   If you find a bug or want to contribute, we'll welcome you at https://gitea.com/gitea/tea.
   More info about Gitea itself on https://about.gitea.com.
```

- [Compare features with other git forge CLIs](./FEATURE-COMPARISON.md)
- tea uses [code.gitea.io/sdk](https://code.gitea.io/sdk) and interacts with the Gitea API.

## Installation

There are different ways to get `tea`:

1. Install via your system package manager:
    - macOS via `brew` (official):
      ```sh
      brew install tea
      ```
    - arch linux ([gitea-tea-git](https://aur.archlinux.org/packages/gitea-tea-git), thirdparty)
    - alpine linux ([tea](https://pkgs.alpinelinux.org/packages?name=tea&branch=edge), thirdparty)

2. Use the prebuilt binaries from [dl.gitea.com](https://dl.gitea.com/tea/)

3. Install from source: [see *Compilation*](#compilation)

4. Docker (thirdparty): [tgerczei/tea](https://hub.docker.com/r/tgerczei/tea)

5. asdf (thirdparty): [mvaldes14/asdf-tea](https://github.com/mvaldes14/asdf-tea)

## Compilation

Make sure you have a current go version installed (1.13 or newer).

- To compile the source yourself with the recommended flags & tags:
  ```sh
  git clone https://gitea.com/gitea/tea.git # or: tea clone gitea.com/gitea/tea ;)
  cd tea
  make
  ```
  Note that GNU Make (gmake on OpenBSD) is required.
  If you want to install the compiled program you have to execute the following command:
  ```sh
  make install
  ```
  This installs the binary into the "bin" folder inside of your GOPATH folder (`go env GOPATH`). It is possible that this folder isn't in your PATH Environment Variable. 

- For a quick installation without `git` & `make`, set $version and exec:
  ```sh
  go install code.gitea.io/tea@${version}
  ```

## Contributing

Fork -> Patch -> Push -> Pull Request

- `make test` run testsuite
- `make vet`  run checks (check the order of imports; preventing failure on CI pipeline beforehand)
- ... (for other development tasks, check the `Makefile`)

**Please** read the [CONTRIBUTING](CONTRIBUTING.md) documentation, it will tell you about internal structures and concepts.

## License

This project is under the MIT License. See the [LICENSE](LICENSE) file for the
full license text.
