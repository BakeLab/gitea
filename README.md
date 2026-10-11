# <img alt='tea logo' src='https://gitea.com/repo-avatars/550-80a3a8c2ab0e2c2d69f296b7f8582485' height="40"/> *T E A*

[![License: MIT](https://img.shields.io/badge/License-MIT-blue.svg)](https://opensource.org/licenses/MIT)
[![Release](https://raster.shields.io/badge/dynamic/json.svg?label=release&url=https://gitea.com/api/v1/repos/gitea/tea/releases&query=$[0].tag_name)](https://gitea.com/gitea/tea/releases)
[![Join the chat at https://img.shields.io/discord/322538954119184384.svg](https://img.shields.io/discord/322538954119184384.svg)](https://discord.gg/Gitea)
[![Go Report Card](https://goreportcard.com/badge/gitea.dev/tea)](https://goreportcard.com/report/gitea.dev/tea) [![GoDoc](https://pkg.go.dev/badge/gitea.dev/tea?status.svg)](https://godoc.org/gitea.dev/tea)
![Tea Release Status](https://gitea.com/gitea/tea/actions/workflows/release-nightly.yml/badge.svg)

## The official CLI for Gitea

Tea manages repositories, issues, pull requests, releases, and other Gitea resources
with explicit commands suitable for local development and CI.

```sh
tea auth login --host https://gitea.example.org --with-token < token-file
tea issue list --repo owner/repository
tea pr view 123 --repo owner/repository --json
tea alias set bugs 'issue list --label bug'
tea help
```

tea 1.0 is a breaking release: it requires Gitea 28 or newer, commands, flags and
output formats changed, it uses separate `config.yml` and `hosts.yml` files, and it
requires fresh authentication.
Existing combined configuration files are left untouched; archive the old file or
choose an isolated `TEA_CONFIG_DIR` before setup. See
[Migrating to tea 1.0](docs/migration-1.0.md) for the old-to-new mapping.
Run `tea --help` or `tea <command> --help` for supported commands and flags; the
full reference, man pages, and shell completions ship in each release's docs archive.
Credentials use the operating-system keyring by default; explicitly
select `--credential-store file` when private file storage is needed. For CI,
supply `GITEA_TOKEN` and its instance through `TEA_HOST` or `GITEA_INSTANCE_URL`;
environment credentials are never persisted.

## Installation

There are different ways to get `tea`:

1. Install via your system package manager:
    - macOS via `brew` (official):
      ```sh
      brew install tea
      ```
    - arch linux ([tea](https://archlinux.org/packages/extra/x86_64/tea/), thirdparty)
    - alpine linux ([tea](https://pkgs.alpinelinux.org/packages?name=tea&branch=edge), thirdparty)
    - Windows via `MSYS2` ([tea](https://packages.msys2.org/base/mingw-w64-tea), thirdparty)

2. Use the prebuilt binaries from [dl.gitea.com](https://dl.gitea.com/tea/)

3. Install from source: [see *Compilation*](#compilation)

4. Docker: [Tea at docker hub](https://hub.docker.com/r/gitea/tea)

### Log in to Gitea from tea

Gitea can use many different authentication schemes, and not every authentication method will work with every Gitea deployment. When you are a Gitea instance administrator you can tweak your settings to fit your use case. For the method that is most likely to work with any Gitea deployment use the following steps:

1. Open your Gitea instance in a web browser

2. Log in to Gitea in your web browser. Any MFA, IDP, or whatever else should be available this way.

3. In your "user settings", generate an application token with at least **user read** permissions. If you want to do anything useful with the token add additional permissions/scopes.

4. Save the token in a private file and run `tea auth login --host https://your-instance --with-token < token-file`. Credentials use the operating-system keyring by default.

You should now be logged in to your gitea instance from tea.

Alternatively, `tea auth login --host https://your-instance --oauth` authorizes tea in your browser. OAuth may not be available on all Gitea deployments, and needs a browser on the machine running tea.

### Shell completion

If you installed from source or the package does not provide the completions with it you can add them yourself with `tea completion <shell>` command. To generate the completions run one of the following commands depending on your shell.

```shell
# .bashrc
source <(tea completion bash)

# .zshrc
source <(tea completion zsh)

# fish
tea completion fish > ~/.config/fish/completions/tea.fish

# PowerShell profile
tea completion powershell | Out-String | Invoke-Expression
```

### Man Page

The hidden command `tea man` can be used to generate the `tea` man page.

```shell
# for bash or zsh
man <(tea man)

# for fish
man (tea man | psub)

# write man page to a file
tea man --output-file ./tea.man
```

## Compilation

Make sure you have a current Go version installed (1.27 or newer).

- To compile the source yourself with the recommended flags & tags:
  ```sh
  git clone https://gitea.com/gitea/tea.git # or: tea repo clone https://gitea.com/gitea/tea ;)
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
  go install gitea.dev/tea@${version}
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
