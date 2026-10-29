# Issues, pull requests, comments and reviews

Issues and pull requests share one number sequence per repository. Commands take a number
(`17`, `#17`) or a URL; pull request commands also take a head branch name and default to the
current branch's pull request.

## Contents

- [Reading an issue](#reading-an-issue)
- [Opening and closing issues](#opening-and-closing-issues)
- [Working on an issue](#working-on-an-issue)
- [Opening the pull request](#opening-the-pull-request)
- [Updating the pull request](#updating-the-pull-request)
- [Reading a pull request](#reading-a-pull-request)
- [Comments](#comments)
- [Reviews](#reviews)
- [Merging and cleaning up](#merging-and-cleaning-up)
- [Repositories](#repositories)
- [Releases and wiki](#releases-and-wiki)

## Reading an issue

```sh
gt issue view 17 --no-input --json number,title,body,state,labels,assignees
gt issue comment list 17 -L 100 --no-input --json id,author,body,created_at
gt issue view 17 --comments --no-input          # text: body, then the conversation
gt issue timeline 17 --no-input --json type,author,body,created_at
gt issue list --label bug --state open -L 50 --no-input --json number,title,labels
gt issue list --assignee @me --no-input --json number,title,updated_at
```

- The `comments` JSON field is the number of comments, not their text; read the text with
  `gt issue comment list`.
- `blocked_by` and `blocks` cost extra requests; request them only when needed.
- `gt issue list --kind all` includes pull requests; their `kind` reads `pull`.

## Opening and closing issues

```sh
gt issue create --no-input --title "Crash on empty config" --label bug --json number,url --body-file - <<'EOF'
Steps to reproduce, expected and actual behaviour.
EOF
gt issue create --template bug --title "Crash on empty config" --no-input --json number,url
gt issue close 17 --comment "Fixed in #18." --no-input
gt issue reopen 17 --comment "Still happens on main." --no-input
```

- Labels, a milestone, assignees and a due date need write access to the repository's issues.
  Without it Gitea creates the issue without them, and gt names what it left out and fails.
- `--template` starts from a repository issue template; without a terminal it requires
  `--title`.
- Closing and reopening ask no confirmation and are visible to everyone: only when the user
  asks.

## Working on an issue

```sh
gt issue edit 17 --add-assignee @me --no-input
git switch -c fix/issue-17
# change, test, commit with git
```

Assign yourself only when the user asks; it is visible to everyone.

## Opening the pull request

```sh
gt pr create --no-input --yes --base main --title "fix: crash on empty config" --body-file - <<'EOF'
Explain what changed and why.

Closes #17
EOF
gt pr create --fill --draft --no-input --yes      # title and body from the commits, as a draft
gt pr view --no-input --json number,url,state,draft
```

- Any flag of `gt pr create` skips its questions. The title defaults to the commit message of
  a single-commit branch, else the branch name, so pass `--title`.
- A branch not on the remote yet is pushed first, which needs `--yes` without a terminal.
  Ask the user before that push.
- Without push access, the branch goes to the user's fork when one is a remote. Otherwise ask
  the user to run `gt repo fork --add-remote` first; gt does not fork without a terminal.
- `Closes #17` (or `fixes`, `resolves`) in the body closes the issue when the pull request is
  merged.
- Follow the repository's own contribution rules (title style, sign-off, templates) when they
  exist; read `CONTRIBUTING.md` before writing the title.
- Useful flags: `--reviewer`, `--label`, `--milestone`, `--assignee @me`, `--head OWNER:BRANCH`
  for a fork branch, `--agit` for AGit flow without a fork.

## Updating the pull request

```sh
gt pr edit 18 --title "fix: crash on empty config" --no-input
gt pr edit 18 --body-file - --no-input <<'EOF'
New description.
EOF
gt pr edit 18 --add-reviewer carol --add-label bug --no-input
gt pr ready 18 --no-input                       # draft to ready; --undo reverses it
gt pr update-branch 18 --no-input               # merge the base branch in on Gitea; --rebase rewrites the head
```

Pushing new commits with `git push` updates the pull request; then re-check CI
([monitoring.md](monitoring.md)).

## Reading a pull request

```sh
gt pr view 18 --no-input --json title,body,state,base,head,mergeable,review_decision,reviews
gt pr comment list 18 -L 100 --no-input --json id,author,body,created_at
gt pr view 18 --comments --no-input             # text: comments and reviews
gt pr diff 18 --name-only --no-input
gt pr diff 18 --no-input                        # unified diff, uncolored when piped
gt pr checkout 18 --no-input                    # switches branches; ask first
gt pr status --no-input --json current_branch,review_requested
```

Use `gt pr diff` or `gt pr view` to read; `gt pr checkout` changes the working tree.

## Comments

```sh
gt issue comment 17 --no-input --body-file - <<'EOF'
Comment text in Markdown.
EOF
gt pr comment 18 --no-input --body "Rebased on main."
gt pr comment 18 --edit-last --no-input --body "Rebased on main; CI is green."
gt pr comment list 18 --no-input --json id,author,body,created_at
gt issue comment edit 2001 --no-input --body "Corrected text."
```

- `--edit-last` and `--delete-last` act on your own latest comment.
- `--attachment FILE` (repeatable) uploads screenshots or logs with the comment.
- Comments on a pull request's conversation tab use `gt pr comment`; comments on lines of the
  diff are review comments, below.

## Reviews

```sh
gt pr review 18 --approve --no-input --body "Looks good."
gt pr review 18 --request-changes --no-input --body-file - <<'EOF'
Please handle the empty case.
EOF
gt pr review 18 --comment --comments-file inline.json --no-input
```

`inline.json` holds one JSON array:

```json
[
  {"path": "internal/config/load.go", "body": "This returns nil on an empty file.", "new_position": 42},
  {"path": "README.md", "body": "This line was removed but is still referenced.", "old_position": 10}
]
```

- `new_position` is a line number in the new file, `old_position` in the old one; exactly
  one per comment, on a line that is part of the diff. gt checks this before sending.
- `--comments-file` needs a real file, not `-`.
- `--request-changes` requires a body; `--comment` requires a body or inline comments.
- To collect comments first: `gt pr review 18 --pending --comments-file inline.json --no-input`,
  then `gt pr review submit 18 --approve --no-input`. A later review publishes a pending one,
  which needs `--yes` without a terminal.

Following up on reviews:

```sh
gt pr review list 18 --no-input --json id,author,state,body
gt pr review comments 18 --no-input --json id,path,line,body,author,resolver
gt pr review reply 18 1234 --no-input --body "Fixed in the latest commit."
gt pr review resolve 1234 --no-input
```

## Merging and cleaning up

```sh
gt pr merge 18 --squash --delete-branch --no-input --yes
gt pr merge 18 --auto --no-input --yes          # merge once required checks succeed
gt pr clean 18 --no-input --yes                 # delete the branches of a merged pull request
gt pr backport 18 --to release/v1.2 --no-input --yes --json pull
```

Merging is irreversible and visible: only on the user's explicit request. Without a method gt
uses the repository's default merge style. A backport pushes a branch and opens a pull request,
so it also needs the user's request; on a conflict it changes nothing and fails.

## Repositories

```sh
gt repo view --no-input --json full_name,default_branch,private,actions_enabled
gt repo list OWNER -L 100 --no-input --json full_name,description,archived
gt repo fork --add-remote --no-input
gt repo edit --description "A short description" --add-topic cli --no-input
gt repo edit --enable-wiki=false --no-input
```

- `gt repo edit` changes only the flags passed; on/off settings take `=false` to turn off.
- Changing settings, renaming, transferring, archiving and deleting affect everyone; run them
  only on the user's request. `gt repo archive`, `rename`, `transfer` and `delete` need
  `--yes` without a terminal.

## Releases and wiki

```sh
gt release list -L 5 --no-input --json tag,title,draft,published_at
gt release create v1.2.0 dist/app.tar.gz --notes-file CHANGELOG.md --draft --no-input
gt wiki list --no-input --json title
gt wiki view Home --no-input --json body
gt wiki create "Release process" --body-file docs/release.md --no-input
gt wiki edit Home --message "Document the release steps" --no-input --body-file - <<'EOF'
New page content.
EOF
gt wiki edit "Old title" --title "New title" --no-input   # renames, keeping the content
gt wiki revisions Home --no-input --json sha,author,message,created_at
gt wiki delete "Old Notes" --no-input --yes
```

`gt release create` creates the tag at `--target` (the default branch) when it does not exist;
pass `--verify-tag` to require an existing tag.
