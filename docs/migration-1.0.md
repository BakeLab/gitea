# Migrating to tea 1.0

tea 1.0 rebuilds the CLI around explicit, singular `noun verb` commands (similar
to GitHub's `gh`), a split configuration, and per-instance accounts. There are no
compatibility aliases: old command names, plural groups and most short flags are
gone, and scripts need updating. This guide lists what changed and what to use
instead.

Quick checklist:

1. Make sure your instances run Gitea 28 or newer
   ([Server requirements](#server-requirements)).
2. Move the old configuration away, or point tea 1.0 at a new directory
   ([Configuration](#old-configuration-file)).
3. Log in again ([Logging in](#logging-in-again)).
4. Replace the old `!tea login helper` Git credential entries
   ([Git credential helper](#git-credential-helper)).
5. Regenerate shell completions ([Shell completion](#shell-completion)).
6. Update scripts: [global flags](#global-flags), [commands](#renamed-commands),
   [arguments](#changed-positional-arguments) and [output](#output-formats).

Run `tea <command> --help` for the full flag list of any command.

## Server requirements

Requires Gitea 28 or newer. tea no longer probes `/api/v1/version` or
`/swagger.v1.json` to adapt to older servers: on an older instance, commands
that need a newer endpoint fail with the server's HTTP error instead of a
"not supported" message.

## Configuration and login

### Old configuration file

Older releases kept logins and preferences in one file, `config.yml` in the `tea`
directory under the platform configuration directory (`~/.config/tea/config.yml`
on Linux; very old releases used `~/.tea/tea.yml`). tea 1.0 stores preferences in
`config.yml` and instances and accounts in `hosts.yml`, in the same directory by
default; `tea config path` prints both paths. When it finds an old-format file it
stops with:

```text
legacy tea configuration: archive the old config.yml or select a separate TEA_CONFIG_DIR, then run tea auth login; existing files have not been changed
```

Nothing is imported automatically. Either archive the old file:

```sh
mv ~/.config/tea/config.yml ~/.config/tea/config.yml.old   # adjust the path on macOS and Windows
```

or keep it and give tea 1.0 its own directory with `TEA_CONFIG_DIR`. Do not run
an old tea against a directory written by tea 1.0: it reads the new `config.yml`
as an empty login list and overwrites it on its next save.

`tea config list` shows effective preferences. Old settings map as follows:

| Old `config.yml` | tea 1.0 |
|---|---|
| `preferences.editor: true` | `tea config set editor "<command>"` (a command, not a switch) |
| `preferences.flag_defaults.remote` | `tea config set remote <name>` |
| login `default: true` | `tea config set default_host <url>` |
| login `insecure`, `ssh_host` | `tea config set --host <url> insecure true`, `... ssh_host <host[:port]>` |
| login `ssh_key`, `ssh_agent`, `ssh_certificate_principal`, `ssh_key_agent_pub` | `tea config set --host <url> --user <name> ssh_key …` (also `ssh_agent`, `ssh_certificate_principal`, `ssh_key_fingerprint`) |

### Logging in again

Accounts are identified by instance URL and username instead of a login name.
`tea auth login --host <url>` replaces `tea login add`:

| Old | tea 1.0 |
|---|---|
| `tea login add --url URL --token TOKEN` | `tea auth login --host URL --with-token < token-file` (the token is read from stdin) |
| `tea login add --oauth` | `tea auth login --host URL --oauth` |
| `tea login add --ssh-key PATH` / `-s` | `tea auth login --host URL --ssh-key PATH` |
| `--ssh-agent-key` / `-a`, `--ssh-agent-principal` / `-c` | same long flags, no shorthands |
| `--insecure` / `-i` | `--insecure` |
| `--user`, `--password`, `--otp`, `--scopes` (create a token with basic auth) | removed: create a token in the web UI (Settings → Applications) or use `--oauth` |
| `--name`, `--no-version-check` | removed |
| `--redirect-url` | removed: OAuth always listens on a random loopback port |
| `--git-credentials` / `--helper` / `-j` | `tea auth setup-git --host URL` |

Token logins of the old release kept the token in plain text under
`logins[].token` in the old `config.yml`, so you can pipe it straight into
`tea auth login --with-token` instead of creating a new one.

Credentials go to the operating-system keyring (service `tea-cli-v2`). Pass
`--credential-store file` to store them in private files under the configuration
directory instead. The old release's OAuth tokens (keyring service `tea-cli`, and
`credentials.json` in the old configuration directory) are not read; delete them once you
have logged in again.

Managing accounts:

| Old | tea 1.0 |
|---|---|
| `tea login list`, `tea login status`, `tea whoami` | `tea auth status` (`--verify` checks each credential) |
| `tea login default NAME` | `tea config set default_host URL`, and `tea auth switch USER --host URL` for the active account |
| `tea login edit` | `tea config set --host URL <key> <value>` |
| `tea login delete NAME`, `tea logout NAME` | `tea auth logout --host URL [--user USER]` |
| `tea login oauth-refresh` | `tea auth refresh` |

### Environment variables

| Variable | tea 1.0 |
|---|---|
| `GITEA_TOKEN`, `GITEA_INSTANCE_URL` | supported; `TEA_HOST` is an alternative to `GITEA_INSTANCE_URL` |
| `GITEA_INSTANCE_INSECURE`, `GITEA_INSTANCE_SSH_HOST` | supported |
| `GH_TOKEN` | no longer read: use `GITEA_TOKEN` |
| `GITEA_SERVER_URL`, `GITEA_SERVER_TOKEN`, `GITEA_SERVER_USER`, `GITEA_SERVER_PASSWORD`, `GITEA_SERVER_OTP`, `GITEA_SCOPES` | removed with `tea login add` |

New variables: `TEA_CONFIG_DIR`, `TEA_HOST`, `TEA_USER`, `TEA_REPO`, `TEA_EDITOR`,
`TEA_PAGER` and `TEA_COLOR`; see `tea config --help`. Environment credentials are
never written to disk.

### Git credential helper

`tea login helper setup` and `tea login add --git-credentials` wrote two entries
to `~/.gitconfig` for each instance: an empty `credential.<url>.helper` and
`!tea login helper`. tea 1.0 has no `login` command, so Git now gets an error
from that helper and falls back to prompting. Remove both entries, then register
the new helper:

```sh
git config --global --get-all credential.https://gitea.example.org.helper
git config --global --unset-all credential.https://gitea.example.org.helper '^!tea login helper$'
git config --global --unset-all credential.https://gitea.example.org.helper '^$'
tea auth setup-git --host https://gitea.example.org
```

`tea repo clone`, `tea pr checkout`, `tea pr clean`, `tea repo sync` and
`tea pr create --agit` authenticate Git without any helper; `tea auth setup-git`
is only needed for plain `git` commands. Add `--local` to configure only the
current repository.

## Global flags

| Old | tea 1.0 |
|---|---|
| `--repo` / `-r` | `--repo` / `-R` |
| `--remote` / `-R` | `--remote` (no shorthand) |
| `--login` / `-l` | `--host URL`, plus `--user NAME` when the instance has several accounts |
| `--output` / `-o`, `--fields` / `-f` | `--json [fields]` and `--jq <expression>` |
| `--debug` / `--vvv` | `--debug` |
| `--confirm` / `-y`, `--force` / `-f` on destructive commands | `--yes` |

`-R` now means `--repo`: `tea issue list -R origin` fails with
`invalid repository URL` instead of selecting the `origin` remote. Use
`--remote origin`.

New global flags: `--no-input` disables prompts, editors and browsers, `--yes`
accepts confirmations, `--cwd` selects the directory used for repository
discovery, and `--sudo USER` acts as another user (administrators only).

## Renamed commands

All command groups are singular, and the built-in aliases (`ls`, `rm`, `c`, `e`,
`i`, `ms`, `co`, `lgtm`, …) are gone. `tea alias set NAME EXPANSION` defines your
own top-level shortcuts, for example `tea alias set co 'pr checkout'`; aliases
for subcommands such as `issue ls` are not possible.

Top-level groups:

| Old | tea 1.0 |
|---|---|
| `issues`, `issue`, `i` | `issue` |
| `pulls`, `pull`, `pr` | `pr` |
| `labels` | `label` |
| `milestones`, `ms` | `milestone` |
| `releases`, `r` | `release` |
| `times`, `t` | `time` |
| `organizations`, `org` | `org` |
| `repos` | `repo` |
| `branches`, `b` | `branch` |
| `actions secrets` | `secret` |
| `actions variables` | `variable` |
| `actions runs` | `run` |
| `actions workflows` | `workflow` |
| `webhooks`, `hooks` | `webhook` |
| `comments`, `c` | `issue comment` / `pr comment` |
| `notifications`, `n` | `notification` |
| `ssh-keys` | `ssh-key` |
| `admin users`, `admin u` | `admin user` |
| `logins`, `login`, `logout`, `whoami` | `auth` (see [Logging in](#logging-in-again)) |
| `clone`, `C` | `repo clone` |
| `open`, `o` | removed (see [Removed features](#removed-features)) |

Groups no longer run a default action: `tea issues` listed issues and
`tea issues 5` showed issue 5; use `tea issue list` and `tea issue view 5`.
The same applies to `pulls`, `notifications` and the other groups.

Subcommands:

| Old | tea 1.0 |
|---|---|
| `issues reopen`, `pulls reopen` (alias `open`) | `issue reopen`, `pr reopen` |
| `pulls approve <n> [comment]` | `pr review <n> --event APPROVED [--body TEXT]` |
| `pulls reject <n> <comment>` | `pr review <n> --event REQUEST_CHANGES --body TEXT` |
| `pulls review <n>` | `pr review <n>` |
| `pulls review-comments` | `pr review comments` |
| `pulls reply`, `resolve`, `unresolve` | `pr review reply`, `pr review resolve`, `pr review unresolve` |
| `labels update --id ID` | `label edit ID` |
| `labels delete --id ID` | `label delete ID` |
| `labels list --save` | `label export` |
| `milestones issues <name>` | `milestone issue list <milestone-id>`, or `milestone issue list --by-name <name>` |
| `milestones issues add\|remove` | `milestone issue add\|remove <milestone-id> <number>...` (`--by-name` selects by title) |
| `releases assets create` | `release asset upload` |
| `times add` | `time create` |
| `times list [user\|#issue]` | `time list [--username USER\|--issue N]` |
| `repos search` | `search repo` |
| `repos create-from-template` | `repo create --template OWNER/REPO` |
| `actions secrets create` | `secret set` |
| `actions variables list --name NAME` | `variable view NAME` |
| `actions runs delete` (alias `cancel`) | `run delete`; `run cancel` cancels without deleting |
| `actions workflows dispatch` | `workflow run` |
| `webhooks update` | `webhook edit` |
| `comments add <n> [body]` | `issue comment create <n> --body TEXT` (also `pr comment create`) |
| `notifications read\|unread [all\|id]` | `notification mark-read\|mark-unread [id]...`; use `--all` for every filtered notification |
| `ssh-keys add` | `ssh-key create` |
| `login helper setup` | `auth setup-git` |
| `completion pwsh` | `completion powershell` |

Renamed flags that appear on many commands:

| Old | tea 1.0 |
|---|---|
| `--description` / `-d`, `--description-file` (issues, pulls, comments) | `--body`, `--body-file` |
| `--note` / `-n`, `--note-file` / `-f` (releases) | `--body`, `--body-file` |
| `--content` / `-c` (wiki) | `--body`, `--body-file` |
| `--labels` / `-L`, `--assignees` / `-a`, `--milestones` / `-m` | `--label`, `--assignee`, `--milestone` |
| `--referenced-version` / `-v` | `--ref` |
| `--keyword` / `-k` | `--search` |
| `--from` / `-F` (issue list) | `--since` |
| `--states` / `-s`, `--types` / `-t` (notifications) | `--status`, `--type` |
| `--page` / `-p`, `--limit` / `--lm` | `--page`, `--limit` |
| `--style` / `-s` (pr merge) | `--method` / `-s` |
| `--owner` / `-O`, `--init`, `--labels`, `--trustmodel`, `--branch`, `--template` (repo create) | `--org`, `--auto-init`, `--label-template`, `--trust-model`, `--default-branch`, `--as-template` |
| `--inactive` (webhook) | `--active=false` |
| `--out` (man) | `--output-file` / `-o` |

Most other short flags were dropped; the long names are unchanged unless listed
here. Two flags kept their name but changed meaning:

- `workflow run`: `-f` was `--follow` and now means `--field KEY=VALUE`;
  `--follow` has no shorthand. `-r` (`--ref`) and `-i` (`--input`) are unchanged.
- `api`: `--field` (`-f`) sent a string and `--Field` (`-F`) a typed value. Now
  `--raw-field` / `-f` sends a string and `--field` / `-F` a typed value, so the
  shorthands behave as before. `--data` / `-d` became `--input FILE` (or `-` for
  stdin), `--include` lost `-i`, and `--output` / `-o` became `--output-file`.

## Changed positional arguments

| Command | Old | tea 1.0 |
|---|---|---|
| `milestone close\|reopen\|delete\|edit\|view` | milestone name | milestone ID; add `--by-name` to select by exact title |
| `milestone issue list\|add\|remove` | milestone name | milestone ID; add `--by-name` to select by exact title |
| `release asset delete <tag>` | attachment name | asset ID; add `--by-name` to select by exact name |
| `label edit\|delete` | `--id ID` | `ID` |
| `repo delete` | `--name NAME --owner OWNER` | optional `OWNER/REPO` argument (or `--repo`) |
| `issue comment create` | optional body argument | `--body`, `--body-file` or `--editor` |

A milestone whose title is a number is now read as an ID unless you pass
`--by-name`. Destructive commands still ask for confirmation unless `--yes` is
given.

## Output formats

`--output` / `-o` (`table`, `simple`, `csv`, `tsv`, `yaml`, `json`) and `--fields`
are gone. Human output is plain text; scripts should use JSON:

```sh
# old: tea issues list --fields index,title -o csv
tea issue list --json number,title --jq '.[] | "\(.number),\(.title)"'

# old: tea notifications --mine -o simple | xargs notify-send
tea notification list --mine --json title --jq '.[].title' | xargs -n1 notify-send
```

`--json` alone prints every field; `--json a,b` selects fields and rejects unknown
names; `--jq` filters the result. Commands that produce no resource, such as
deletes, reject `--json` with a usage error instead of printing nothing.

Lists print aligned columns without a header row and with fewer columns than
before: the ID or number first, then the title or name, then the state and a few
details (`issue list` and `pr list` show number, title, state and author). Do not
parse them; the columns may change. On a color terminal the first column links to
the resource. Tabs and line breaks in list cells become spaces, and control
characters such as escape sequences are dropped from all human output, bodies
included. Commands that change several resources at once, such as
`issue close 1 2`, `issue edit`, `milestone close`, `release edit` or
`notification mark-read`, print one table of the changed items in the matching
list's columns. When a list continues past the requested page, tea prints a
`--page` hint on stderr.

Issue, pull request and other bodies are printed as raw Markdown instead of being
rendered. Long output on a terminal goes through a pager (`TEA_PAGER`, the
`pager` preference or `PAGER`). Each command starts the pager at most once.

## Removed features

| Feature | Replacement |
|---|---|
| `tea open [number\|issues\|pulls\|releases\|commits\|branches\|wiki\|labels\|milestones\|activity\|settings\|<ref>]` | `tea issue view N --web`, `tea pr view N --web`, `tea repo view --web`; for other pages open `$(tea repo view --json url --jq .url)/releases` and similar |
| Basic-auth token creation (`login add --user/--password/--otp/--scopes`) | create a token in the web UI, or `tea auth login --oauth` |
| `login edit` | `tea config set --host URL <key> <value>` |
| Output formats and `--fields` | `--json` and `--jq` |
| Markdown rendering of bodies | raw Markdown, optionally paged |
| `contrib/autocomplete.*` scripts and the `--generate-shell-completion` protocol | `tea completion <shell>` |
| `GH_TOKEN`, `GITEA_SERVER_*`, `GITEA_SCOPES` | `GITEA_TOKEN`, `tea auth login` |
| `--vvv` | `--debug` |

### Shell completion

Completion scripts saved from an older `tea completion` (or the removed
`contrib/autocomplete.*` files) call tea with `--generate-shell-completion`, which
tea 1.0 rejects, so completion silently stops working. Regenerate them:

```sh
source <(tea completion bash)          # ~/.bashrc
source <(tea completion zsh)           # ~/.zshrc
tea completion fish > ~/.config/fish/completions/tea.fish
tea completion powershell | Out-String | Invoke-Expression   # PowerShell profile
```

Packagers can ship the pre-generated scripts and man pages from the release docs
archive, or run `make docs`.

## Behavior changes

- `pr checkout N` creates or reuses the local branch `pr/N`; it no longer
  switches to another local branch that already points at the PR head. Use
  `--branch NAME` to check out into a branch you choose (it must point at the PR
  head; `--update` fast-forwards it) or `--detach` for a detached checkout.
  `-b` now takes the branch name.
- `webhook edit` must be told what to do with the delivery authorization header
  on every call, because Gitea replaces it: pass `--authorization-header`,
  `--authorization-header-file` or `--clear-authorization`. The signing secret
  cannot be edited; recreate the hook to rotate it.
- `notification mark-read`, `mark-unread`, `pin` and `unpin` act on the given IDs;
  pass `--all` to apply them to every notification matching the filters.
- `--no-input` disables prompts, editors and browsers. With `--json`, tea never
  prompts unless `--interactive` is given; required values must come from flags.
- Destructive commands confirm on a terminal and need `--yes` elsewhere.
- `--limit` accepts 1 to 100 and `--page` a positive number; other values are
  usage errors instead of being passed to the server.
- In `--json` output, timestamps the server leaves unset print as `""`, or as
  `null` for optional fields such as deadlines, instead of
  `0001-01-01T00:00:00Z`.
- More input is checked before anything is sent: release titles, issue titles
  and comment bodies cannot be empty, a review needs a body or inline comments
  (unless it approves), team creation needs a permission, migrations from
  GitHub, Gitea, GitLab and Gogs need an access token, and `workflow run` needs
  a ref (`--ref` or a checked-out branch).
- `pr merge` merges the head it read and refuses when the branch moved in the
  meantime; run it again to merge the new head.
- Preferences follow the git/gh order: an explicit `tea config set` value wins
  over `VISUAL`/`EDITOR`, `PAGER` and `BROWSER`, which win over the defaults;
  `TEA_EDITOR`, `TEA_PAGER` and `TEA_COLOR` override both.
