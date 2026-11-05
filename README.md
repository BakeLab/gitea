# <img alt='gt logo' src='assets/gt-icon.svg' height="40"/> gitea-cli

[![License: MIT](https://img.shields.io/badge/License-MIT-blue.svg)](https://opensource.org/licenses/MIT)
[![Release](https://raster.shields.io/badge/dynamic/json.svg?label=release&url=https://gitea.com/api/v1/repos/gitea/cli/releases&query=$[0].tag_name)](https://gitea.com/gitea/cli/releases)
[![Join the chat at https://img.shields.io/discord/322538954119184384.svg](https://img.shields.io/discord/322538954119184384.svg)](https://discord.gg/Gitea)
[![Go Report Card](https://goreportcard.com/badge/gitea.dev/cli)](https://goreportcard.com/report/gitea.dev/cli) [![GoDoc](https://pkg.go.dev/badge/gitea.dev/cli?status.svg)](https://godoc.org/gitea.dev/cli)
![gitea-cli Release Status](https://gitea.com/gitea/cli/actions/workflows/release-nightly.yml/badge.svg)

## The official CLI for Gitea

gitea-cli manages repositories, issues, pull requests, releases, and other Gitea resources
with explicit commands suitable for local development and CI. The binary is called `gt`.

```sh
gt auth login --host https://gitea.example.org --with-token < token-file
gt issue list --repo owner/repository
gt pr view 123 --repo owner/repository --json
gt alias set bugs 'issue list --label bug'
gt help
```

Scripts and agents get stable machine-readable output: `--json` and `--jq` select fields that gt only
ever adds to, and `gt schema` lists them with their types. `--template` formats the same JSON with a Go
template, as in `gh`.

With `GITEA_CLI_ERROR_FORMAT=json`, a failure is one line of JSON on stderr.

`--dry-run` previews the write requests of commands such as `gt pr merge` without sending them.

gt requires Gitea 28 or newer. It keeps its preferences in `config.yml` and its
instances and accounts in `hosts.yml`.
Run `gt --help` or `gt <command> --help` for supported commands and flags; the
full reference, man pages, and shell completions ship in each release's docs archive.
Credentials use the operating-system keyring by default; explicitly
select `--credential-store file` when private file storage is needed. For CI,
supply `GITEA_TOKEN` and its instance through `GITEA_CLI_HOST` or `GITEA_INSTANCE_URL`;
environment credentials are never persisted.

## Installation

There are different ways to get `gt`:

1. Run the install script. It downloads the binary for your system from dl.gitea.com, verifies its
   checksum and installs `gt`; `GITEA_CLI_VERSION` and `GITEA_CLI_INSTALL_DIR` choose another
   version or directory, and the version `nightly` installs the latest build of the main branch.
   Later, `gt upgrade` upgrades it the same way.

   On Linux, macOS, FreeBSD or OpenBSD:

   ```sh
   curl -fsSL https://gitea.com/gitea/cli/raw/branch/main/scripts/install.sh | sh
   # the latest nightly build
   curl -fsSL https://gitea.com/gitea/cli/raw/branch/main/scripts/install.sh | sh -s -- --nightly
   ```

   On Windows, in PowerShell, which also adds `gt` to your PATH:

   ```powershell
   irm https://gitea.com/gitea/cli/raw/branch/main/scripts/install.ps1 | iex
   # the latest nightly build
   $env:GITEA_CLI_VERSION = 'nightly'; irm https://gitea.com/gitea/cli/raw/branch/main/scripts/install.ps1 | iex
   ```

2. Use the prebuilt binaries from [dl.gitea.com](https://dl.gitea.com/gitea-cli/)

3. Docker: [gitea-cli at docker hub](https://hub.docker.com/r/gitea/gitea-cli)

4. Homebrew on macOS or Linux:

   ```sh
   brew install gitea-cli
   ```

5. Linux packages: download the `.deb`, `.rpm`, `.apk` or Arch `.pkg.tar.zst` for your system from the
   [release](https://gitea.com/gitea/cli/releases) and install it with your package manager, for example
   `sudo apt install ./gitea-cli_<version>_amd64.deb`. They include the man pages and shell completions.

6. Install from source: [see *Compilation*](#compilation)

### Log in to Gitea from gt

Gitea can use many different authentication schemes, and not every authentication method will work with every Gitea deployment. When you are a Gitea instance administrator you can tweak your settings to fit your use case. For the method that is most likely to work with any Gitea deployment use the following steps:

1. Open your Gitea instance in a web browser

2. Log in to Gitea in your web browser. Any MFA, IDP, or whatever else should be available this way.

3. In your "user settings", generate an application token with at least **user read** permissions. If you want to do anything useful with the token add additional permissions/scopes.

4. Save the token in a private file and run `gt auth login --host https://your-instance --with-token < token-file`. Credentials use the operating-system keyring by default.

You should now be logged in to your gitea instance from gt.

Alternatively, `gt auth login --host https://your-instance --oauth` authorizes gt in your browser. OAuth may not be available on all Gitea deployments, and needs a browser on the machine running gt.

### Coming from tea

gt replaces tea. [Migrating from tea](docs/migrating-from-tea.md) maps tea's commands and flags
to gt's.

### Shell completion

If you installed from source or the package does not provide the completions with it you can add them yourself with `gt completion <shell>` command. Add the line for your shell to its startup file:

```shell
# ~/.bashrc (on macOS, ~/.bash_profile)
eval "$(gt completion bash)"

# ~/.zshrc, after compinit
autoload -U compinit && compinit
source <(gt completion zsh)

# fish
gt completion fish > ~/.config/fish/completions/gt.fish

# PowerShell profile
gt completion powershell | Out-String | Invoke-Expression
```

The bash script needs the bash-completion package. `gt completion <shell> --help` shows the details for each shell.

### Man Page

The hidden command `gt man` can be used to generate the `gt` man page.

```shell
# for bash or zsh
man <(gt man)

# for fish
man (gt man | psub)

# write man page to a file
gt man --output-file ./gt.man
```

### Agent skill

An [agent skill](https://agentskills.io) teaches coding agents to drive `gt`: checking CI and
reading the logs of failed jobs, turning an issue into a pull request, commenting and reviewing.
It replaces [gitea-tea-skill](https://gitea.com/gitea/gitea-tea-skill). Install it with
[skills](https://github.com/vercel-labs/skills):

```shell
# for the current project
npx skills add https://gitea.com/gitea/cli.git --skill gt

# for all your projects
npx skills add https://gitea.com/gitea/cli.git --skill gt -g

# after upgrading gt
npx skills update gt
```

## Compilation

Make sure you have a current Go version installed (1.27 or newer).

- To compile the source yourself with the recommended flags & tags:
  ```sh
  git clone https://gitea.com/gitea/cli.git # or: gt repo clone https://gitea.com/gitea/cli ;)
  cd cli
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
  go install gitea.dev/cli/cmd/gt@${version}
  ```

## Contributing

Fork -> Patch -> Push -> Pull Request

- `make test` run unit tests
- `make vet`  run checks (check the order of imports; preventing failure on CI pipeline beforehand)
- `make build` builds the production CLI
- `make docs` regenerates references, man pages, and shell completions in `docs/generated` (not committed; built on release)
- ... (for other development tasks, check the `Makefile`)

**Please** read the [CONTRIBUTING](CONTRIBUTING.md) documentation, it will tell you about internal structures and concepts.

## License

This project is under the MIT License. See the [LICENSE](LICENSE) file for the
full license text.
