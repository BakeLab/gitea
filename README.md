# <img alt='gt logo' src='https://gitea.com/repo-avatars/550-80a3a8c2ab0e2c2d69f296b7f8582485' height="40"/> *T E A*

[![License: MIT](https://img.shields.io/badge/License-MIT-blue.svg)](https://opensource.org/licenses/MIT)
[![Release](https://raster.shields.io/badge/dynamic/json.svg?label=release&url=https://gitea.com/api/v1/repos/gitea/cli/releases&query=$[0].tag_name)](https://gitea.com/gitea/cli/releases)
[![Join the chat at https://img.shields.io/discord/322538954119184384.svg](https://img.shields.io/discord/322538954119184384.svg)](https://discord.gg/Gitea)
[![Go Report Card](https://goreportcard.com/badge/gitea.dev/cli)](https://goreportcard.com/report/gitea.dev/cli) [![GoDoc](https://pkg.go.dev/badge/gitea.dev/cli?status.svg)](https://godoc.org/gitea.dev/cli)
![gitea-cli Release Status](https://gitea.com/gitea/cli/actions/workflows/release-nightly.yml/badge.svg)

## The official CLI for Gitea

gitea-cli manages repositories, issues, pull requests, releases, and other Gitea resources
with explicit commands suitable for local development and CI.

```sh
gt auth login --host https://gitea.example.org --with-token < token-file
gt issue list --repo owner/repository
gt pr view 123 --repo owner/repository --json
gt alias set bugs 'issue list --label bug'
gt help
```

gt 1.0 is a breaking release: it requires Gitea 28 or newer, commands, flags and
output formats changed, it uses separate `config.yml` and `hosts.yml` files, and it
requires fresh authentication.
Existing combined configuration files are left untouched; archive the old file or
choose an isolated `GITEA_CLI_CONFIG_DIR` before setup.
Run `gt --help` or `gt <command> --help` for supported commands and flags; the
full reference, man pages, and shell completions ship in each release's docs archive.
Credentials use the operating-system keyring by default; explicitly
select `--credential-store file` when private file storage is needed. For CI,
supply `GITEA_TOKEN` and its instance through `GITEA_CLI_HOST` or `GITEA_INSTANCE_URL`;
environment credentials are never persisted.

## Installation

There are different ways to get `gt`:

1. Install via your system package manager:
    - macOS via `brew` (official):
      ```sh
      brew install gt
      ```
    - arch linux ([gt](https://archlinux.org/packages/extra/x86_64/gt/), thirdparty)
    - alpine linux ([gt](https://pkgs.alpinelinux.org/packages?name=gt&branch=edge), thirdparty)
    - Windows via `MSYS2` ([gt](https://packages.msys2.org/base/mingw-w64-gt), thirdparty)

2. Use the prebuilt binaries from [dl.gitea.com](https://dl.gitea.com/gitea-cli/)

3. Install from source: [see *Compilation*](#compilation)

4. Docker: [gitea-cli at docker hub](https://hub.docker.com/r/gitea/gitea-cli)

### Log in to Gitea from gt

Gitea can use many different authentication schemes, and not every authentication method will work with every Gitea deployment. When you are a Gitea instance administrator you can tweak your settings to fit your use case. For the method that is most likely to work with any Gitea deployment use the following steps:

1. Open your Gitea instance in a web browser

2. Log in to Gitea in your web browser. Any MFA, IDP, or whatever else should be available this way.

3. In your "user settings", generate an application token with at least **user read** permissions. If you want to do anything useful with the token add additional permissions/scopes.

4. Save the token in a private file and run `gt auth login --host https://your-instance --with-token < token-file`. Credentials use the operating-system keyring by default.

You should now be logged in to your gitea instance from gt.

Alternatively, `gt auth login --host https://your-instance --oauth` authorizes gt in your browser. OAuth may not be available on all Gitea deployments, and needs a browser on the machine running gt.

### Shell completion

If you installed from source or the package does not provide the completions with it you can add them yourself with `gt completion <shell>` command. Completion offers commands, flags, flag values such as `--state` and `--json` fields, and values from your instance such as repositories. Add the line for your shell to its startup file:

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

The bash script needs the bash-completion package. On macOS, install `bash-completion` with Homebrew for the system bash 3.2 (or `bash-completion@2` for a Homebrew bash 4.2+), and use `eval` as above: bash 3.2 cannot `source <(...)`. Without `compinit`, zsh reports `command not found: compdef`. `gt completion <shell> --help` shows these steps and how to install the script system-wide.

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
  go install gitea.dev/cli@${version}
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
