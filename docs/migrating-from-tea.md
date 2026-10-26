# Migrating from tea

gt replaces [tea](https://gitea.com/gitea/tea). Commands are singular and grouped by resource
(`gt pr`, not `tea pulls`), every `list` also answers to `ls` and every `delete` to `rm`, and
`--json` with `--jq` replaces `--output`. gt reads its own configuration, so log in again with
`gt auth login`.

## Global flags

| tea | gt |
| --- | --- |
| `--login`, `-l` | `--host` and `--user` |
| `--repo`, `-r` | `--repo`, `-R` |
| `--remote`, `-R` | `--remote` |
| `--output`, `-o` | `--json`, `--jq` |

## Commands

| tea | gt |
| --- | --- |
| `tea login add` | `gt auth login` |
| `tea login list`, `tea whoami` | `gt auth status` |
| `tea login default` | `gt auth switch` |
| `tea login delete`, `tea logout` | `gt auth logout` |
| `tea login oauth-refresh` | `gt auth refresh` |
| `tea login helper setup` | `gt auth setup-git` |
| `tea open` | `gt browse` |
| `tea clone` | `gt repo clone` |
| `tea repos create-from-template` | `gt repo create --template` |
| `tea repos search` | `gt search repo` |
| `tea issues` | `gt issue` |
| `tea comment` | `gt issue comment`, `gt pr comment` |
| `tea pulls` | `gt pr` |
| `tea pulls approve` | `gt pr review --approve` |
| `tea pulls reject` | `gt pr review --request-changes` |
| `tea pulls review-comments` | `gt pr review comments` |
| `tea pulls reply`, `resolve`, `unresolve` | `gt pr review reply`, `resolve`, `unresolve` |
| `tea labels list --save` | `gt label export` |
| `tea labels create --file` | `gt label import` |
| `tea labels update` | `gt label edit` |
| `tea milestones issues` | `gt milestone issue` |
| `tea releases assets` | `gt release asset` |
| `tea times add` | `gt time create` |
| `tea notifications read`, `unread` | `gt notification mark-read`, `mark-unread` |
| `tea organizations` | `gt org` |
| `tea ssh-keys add` | `gt ssh-key create` |
| `tea webhooks update` | `gt webhook edit` |
| `tea actions runs` | `gt run` |
| `tea actions workflows dispatch` | `gt workflow run` |
| `tea actions secrets`, `variables` | `gt secret`, `gt variable` |
| `tea admin users` | `gt admin user` |

Other tea commands keep their name in the singular, such as `tea branches` as `gt branch` and
`tea wiki` as `gt wiki`. `gt <command> --help` lists the flags of each command.
