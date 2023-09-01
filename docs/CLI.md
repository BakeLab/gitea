# NAME

tea - command line tool to interact with Gitea

# SYNOPSIS

tea

```
[--help|-h]
[--version|-v]
```

# DESCRIPTION

tea is a productivity helper for Gitea. It can be used to manage most entities on
one or multiple Gitea instances & provides local helpers like 'tea pr checkout'.

tea tries to make use of context provided by the repository in $PWD if available.
tea works best in a upstream/fork workflow, when the local main branch tracks the
upstream repo. tea assumes that local git state is published on the remote before
doing operations with tea.    Configuration is persisted in $XDG_CONFIG_HOME/tea.


**Usage**:

```
tea [GLOBAL OPTIONS] command [COMMAND OPTIONS] [ARGUMENTS...]
```

# GLOBAL OPTIONS

**--help, -h**: show help

**--version, -v**: print the version


# COMMANDS

## logins, login

Log in to a Gitea server

### list, ls

List Gitea logins

**--output, -o**="": Output format. (simple, table, csv, tsv, yaml, json)

### add

Add a Gitea login

**--insecure, -i**: Disable TLS verification

**--name, -n**="": Login name

**--no-version-check, --nv**: Do not check version of Gitea instance

**--password, --pwd**="": Password for basic auth (will create token)

**--ssh-agent-key, -a**="": Use SSH public key or SSH fingerprint to login (needs a running ssh-agent with ssh key loaded)

**--ssh-agent-principal, -c**="": Use SSH certificate with specified principal to login (needs a running ssh-agent with certificate loaded)

**--ssh-key, -s**="": Path to a SSH key/certificate to use, overrides auto-discovery

**--token, -t**="": Access token. Can be obtained from Settings > Applications

**--url, -u**="": Server URL (default: https://gitea.com)

**--user**="": User for basic auth (will create token)

### edit, e

Edit Gitea logins

**--output, -o**="": Output format. (simple, table, csv, tsv, yaml, json)

### delete, rm

Remove a Gitea login

### default

Get or Set Default Login

**--output, -o**="": Output format. (simple, table, csv, tsv, yaml, json)

## logout

Log out from a Gitea server

## shellcompletion, autocomplete

Install shell completion for tea

**--install**: Persist in shell config instead of printing commands

## whoami

Show current logged in user

## issues, issue, i

List, create and update issues

**--assignee, -a**="": 

**--author, -A**="": 

**--comments**: Whether to display comments (will prompt if not provided & run interactively)

**--fields, -f**="": Comma-separated list of fields to print. Available values:
			index,state,kind,author,author-id,url,title,body,created,updated,deadline,assignees,milestone,labels,comments,owner,repo
		 (default: index,title,state,author,milestone,labels,owner,repo)

**--from, -F**="": Filter by activity after this date

**--keyword, -k**="": Filter by search string

**--kind, -K**="": Whether to return `issues`, `pulls`, or `all` (you can use this to apply advanced search filters to PRs)

**--labels, -L**="": Comma-separated list of labels to match issues against.
			
		

**--limit, --lm**="": specify limit of items per page

**--login, -l**="": Use a different Gitea Login. Optional

**--mentions, -M**="": 

**--milestones, -m**="": Comma-separated list of milestones to match issues against.
			
		

**--output, -o**="": Output format. (simple, table, csv, tsv, yaml, json)

**--owner, --org**="": 

**--page, -p**="": specify page, default is 1

**--remote, -R**="": Discover Gitea login from remote. Optional

**--repo, -r**="": Override local repository path or gitea repository slug to interact with. Optional

**--state**="": Filter by state (all|open|closed)

**--until, -u**="": Filter by activity before this date

### list, ls

List issues of the repository

**--assignee, -a**="": 

**--author, -A**="": 

**--fields, -f**="": Comma-separated list of fields to print. Available values:
			index,state,kind,author,author-id,url,title,body,created,updated,deadline,assignees,milestone,labels,comments,owner,repo
		 (default: index,title,state,author,milestone,labels,owner,repo)

**--from, -F**="": Filter by activity after this date

**--keyword, -k**="": Filter by search string

**--kind, -K**="": Whether to return `issues`, `pulls`, or `all` (you can use this to apply advanced search filters to PRs)

**--labels, -L**="": Comma-separated list of labels to match issues against.
			
		

**--limit, --lm**="": specify limit of items per page

**--login, -l**="": Use a different Gitea Login. Optional

**--mentions, -M**="": 

**--milestones, -m**="": Comma-separated list of milestones to match issues against.
			
		

**--output, -o**="": Output format. (simple, table, csv, tsv, yaml, json)

**--owner, --org**="": 

**--page, -p**="": specify page, default is 1

**--remote, -R**="": Discover Gitea login from remote. Optional

**--repo, -r**="": Override local repository path or gitea repository slug to interact with. Optional

**--state**="": Filter by state (all|open|closed)

**--until, -u**="": Filter by activity before this date

### create, c

Create an issue on repository

**--assignees, -a**="": Comma-separated list of usernames to assign

**--deadline, -D**="": Deadline timestamp to assign

**--description, -d**="": 

**--labels, -L**="": Comma-separated list of labels to assign

**--login, -l**="": Use a different Gitea Login. Optional

**--milestone, -m**="": Milestone to assign

**--referenced-version, -v**="": commit-hash or tag name to assign

**--remote, -R**="": Discover Gitea login from remote. Optional

**--repo, -r**="": Override local repository path or gitea repository slug to interact with. Optional

**--title, -t**="": 

### edit, e

Edit one or more issues

**--add-assignees, -a**="": Comma-separated list of usernames to assign

**--add-labels, -L**="": Comma-separated list of labels to assign. Takes precedence over --remove-labels

**--deadline, -D**="": Deadline timestamp to assign

**--description, -d**="": 

**--login, -l**="": Use a different Gitea Login. Optional

**--milestone, -m**="": Milestone to assign

**--referenced-version, -v**="": commit-hash or tag name to assign

**--remote, -R**="": Discover Gitea login from remote. Optional

**--remove-labels**="": Comma-separated list of labels to remove

**--repo, -r**="": Override local repository path or gitea repository slug to interact with. Optional

**--title, -t**="": 

### reopen, open

Change state of one or more issues to 'open'

**--login, -l**="": Use a different Gitea Login. Optional

**--output, -o**="": Output format. (simple, table, csv, tsv, yaml, json)

**--remote, -R**="": Discover Gitea login from remote. Optional

**--repo, -r**="": Override local repository path or gitea repository slug to interact with. Optional

### close

Change state of one ore more issues to 'closed'

**--login, -l**="": Use a different Gitea Login. Optional

**--output, -o**="": Output format. (simple, table, csv, tsv, yaml, json)

**--remote, -R**="": Discover Gitea login from remote. Optional

**--repo, -r**="": Override local repository path or gitea repository slug to interact with. Optional

## pulls, pull, pr

Manage and checkout pull requests

**--comments**: Whether to display comments (will prompt if not provided & run interactively)

**--fields, -f**="": Comma-separated list of fields to print. Available values:
			index,state,author,author-id,url,title,body,mergeable,base,base-commit,head,diff,patch,created,updated,deadline,assignees,milestone,labels,comments
		 (default: index,title,state,author,milestone,updated,labels)

**--limit, --lm**="": specify limit of items per page

**--login, -l**="": Use a different Gitea Login. Optional

**--output, -o**="": Output format. (simple, table, csv, tsv, yaml, json)

**--page, -p**="": specify page, default is 1

**--remote, -R**="": Discover Gitea login from remote. Optional

**--repo, -r**="": Override local repository path or gitea repository slug to interact with. Optional

**--state**="": Filter by state (all|open|closed)

### list, ls

List pull requests of the repository

**--fields, -f**="": Comma-separated list of fields to print. Available values:
			index,state,author,author-id,url,title,body,mergeable,base,base-commit,head,diff,patch,created,updated,deadline,assignees,milestone,labels,comments
		 (default: index,title,state,author,milestone,updated,labels)

**--limit, --lm**="": specify limit of items per page

**--login, -l**="": Use a different Gitea Login. Optional

**--output, -o**="": Output format. (simple, table, csv, tsv, yaml, json)

**--page, -p**="": specify page, default is 1

**--remote, -R**="": Discover Gitea login from remote. Optional

**--repo, -r**="": Override local repository path or gitea repository slug to interact with. Optional

**--state**="": Filter by state (all|open|closed)

### checkout, co

Locally check out the given PR

**--branch, -b**: Create a local branch if it doesn't exist yet

**--login, -l**="": Use a different Gitea Login. Optional

**--output, -o**="": Output format. (simple, table, csv, tsv, yaml, json)

**--remote, -R**="": Discover Gitea login from remote. Optional

**--repo, -r**="": Override local repository path or gitea repository slug to interact with. Optional

### clean

Deletes local & remote feature-branches for a closed pull request

**--ignore-sha**: Find the local branch by name instead of commit hash (less precise)

**--login, -l**="": Use a different Gitea Login. Optional

**--output, -o**="": Output format. (simple, table, csv, tsv, yaml, json)

**--remote, -R**="": Discover Gitea login from remote. Optional

**--repo, -r**="": Override local repository path or gitea repository slug to interact with. Optional

### create, c

Create a pull-request

**--allow-maintainer-edits, --edits**: Enable maintainers to push to the base branch of created pull

**--assignees, -a**="": Comma-separated list of usernames to assign

**--base, -b**="": Branch name of the PR target (default is repos default branch)

**--deadline, -D**="": Deadline timestamp to assign

**--description, -d**="": 

**--head**="": Branch name of the PR source (default is current one). To specify a different head repo, use <user>:<branch>

**--labels, -L**="": Comma-separated list of labels to assign

**--login, -l**="": Use a different Gitea Login. Optional

**--milestone, -m**="": Milestone to assign

**--referenced-version, -v**="": commit-hash or tag name to assign

**--remote, -R**="": Discover Gitea login from remote. Optional

**--repo, -r**="": Override local repository path or gitea repository slug to interact with. Optional

**--title, -t**="": 

### close

Change state of one or more pull requests to 'closed'

**--login, -l**="": Use a different Gitea Login. Optional

**--output, -o**="": Output format. (simple, table, csv, tsv, yaml, json)

**--remote, -R**="": Discover Gitea login from remote. Optional

**--repo, -r**="": Override local repository path or gitea repository slug to interact with. Optional

### reopen, open

Change state of one or more pull requests to 'open'

**--login, -l**="": Use a different Gitea Login. Optional

**--output, -o**="": Output format. (simple, table, csv, tsv, yaml, json)

**--remote, -R**="": Discover Gitea login from remote. Optional

**--repo, -r**="": Override local repository path or gitea repository slug to interact with. Optional

### review

Interactively review a pull request

**--login, -l**="": Use a different Gitea Login. Optional

**--output, -o**="": Output format. (simple, table, csv, tsv, yaml, json)

**--remote, -R**="": Discover Gitea login from remote. Optional

**--repo, -r**="": Override local repository path or gitea repository slug to interact with. Optional

### approve, lgtm, a

Approve a pull request

**--login, -l**="": Use a different Gitea Login. Optional

**--output, -o**="": Output format. (simple, table, csv, tsv, yaml, json)

**--remote, -R**="": Discover Gitea login from remote. Optional

**--repo, -r**="": Override local repository path or gitea repository slug to interact with. Optional

### reject

Request changes to a pull request

**--login, -l**="": Use a different Gitea Login. Optional

**--output, -o**="": Output format. (simple, table, csv, tsv, yaml, json)

**--remote, -R**="": Discover Gitea login from remote. Optional

**--repo, -r**="": Override local repository path or gitea repository slug to interact with. Optional

### merge, m

Merge a pull request

**--login, -l**="": Use a different Gitea Login. Optional

**--message, -m**="": Merge commit message

**--output, -o**="": Output format. (simple, table, csv, tsv, yaml, json)

**--remote, -R**="": Discover Gitea login from remote. Optional

**--repo, -r**="": Override local repository path or gitea repository slug to interact with. Optional

**--style, -s**="": Kind of merge to perform: merge, rebase, squash, rebase-merge (default: merge)

**--title, -t**="": Merge commit title

## labels, label

Manage issue labels

**--limit, --lm**="": specify limit of items per page

**--login, -l**="": Use a different Gitea Login. Optional

**--output, -o**="": Output format. (simple, table, csv, tsv, yaml, json)

**--page, -p**="": specify page, default is 1

**--remote, -R**="": Discover Gitea login from remote. Optional

**--repo, -r**="": Override local repository path or gitea repository slug to interact with. Optional

**--save, -s**: Save all the labels as a file

### list, ls

List labels

**--limit, --lm**="": specify limit of items per page

**--login, -l**="": Use a different Gitea Login. Optional

**--output, -o**="": Output format. (simple, table, csv, tsv, yaml, json)

**--page, -p**="": specify page, default is 1

**--remote, -R**="": Discover Gitea login from remote. Optional

**--repo, -r**="": Override local repository path or gitea repository slug to interact with. Optional

**--save, -s**: Save all the labels as a file

### create, c

Create a label

**--color**="": label color value

**--description**="": label description

**--file**="": indicate a label file

**--login, -l**="": Use a different Gitea Login. Optional

**--name**="": label name

**--output, -o**="": Output format. (simple, table, csv, tsv, yaml, json)

**--remote, -R**="": Discover Gitea login from remote. Optional

**--repo, -r**="": Override local repository path or gitea repository slug to interact with. Optional

### update

Update a label

**--color**="": label color value

**--description**="": label description

**--id**="": label id (default: 0)

**--login, -l**="": Use a different Gitea Login. Optional

**--name**="": label name

**--output, -o**="": Output format. (simple, table, csv, tsv, yaml, json)

**--remote, -R**="": Discover Gitea login from remote. Optional

**--repo, -r**="": Override local repository path or gitea repository slug to interact with. Optional

### delete, rm

Delete a label

**--id**="": label id (default: 0)

**--login, -l**="": Use a different Gitea Login. Optional

**--output, -o**="": Output format. (simple, table, csv, tsv, yaml, json)

**--remote, -R**="": Discover Gitea login from remote. Optional

**--repo, -r**="": Override local repository path or gitea repository slug to interact with. Optional

## milestones, milestone, ms

List and create milestones

**--fields, -f**="": Comma-separated list of fields to print. Available values:
			title,state,items_open,items_closed,items,duedate,description,created,updated,closed,id
		 (default: title,items,duedate)

**--limit, --lm**="": specify limit of items per page

**--login, -l**="": Use a different Gitea Login. Optional

**--output, -o**="": Output format. (simple, table, csv, tsv, yaml, json)

**--page, -p**="": specify page, default is 1

**--remote, -R**="": Discover Gitea login from remote. Optional

**--repo, -r**="": Override local repository path or gitea repository slug to interact with. Optional

**--state**="": Filter by milestone state (all|open|closed)

### list, ls

List milestones of the repository

**--fields, -f**="": Comma-separated list of fields to print. Available values:
			title,state,items_open,items_closed,items,duedate,description,created,updated,closed,id
		 (default: title,items,duedate)

**--limit, --lm**="": specify limit of items per page

**--login, -l**="": Use a different Gitea Login. Optional

**--output, -o**="": Output format. (simple, table, csv, tsv, yaml, json)

**--page, -p**="": specify page, default is 1

**--remote, -R**="": Discover Gitea login from remote. Optional

**--repo, -r**="": Override local repository path or gitea repository slug to interact with. Optional

**--state**="": Filter by milestone state (all|open|closed)

### create, c

Create an milestone on repository

**--deadline, --expires, -x**="": set milestone deadline (default is no due date)

**--description, -d**="": milestone description to create

**--login, -l**="": Use a different Gitea Login. Optional

**--output, -o**="": Output format. (simple, table, csv, tsv, yaml, json)

**--remote, -R**="": Discover Gitea login from remote. Optional

**--repo, -r**="": Override local repository path or gitea repository slug to interact with. Optional

**--state**="": set milestone state (default is open)

**--title, -t**="": milestone title to create

### close

Change state of one or more milestones to 'closed'

**--force, -f**: delete milestone

**--login, -l**="": Use a different Gitea Login. Optional

**--output, -o**="": Output format. (simple, table, csv, tsv, yaml, json)

**--remote, -R**="": Discover Gitea login from remote. Optional

**--repo, -r**="": Override local repository path or gitea repository slug to interact with. Optional

### delete, rm

delete a milestone

**--login, -l**="": Use a different Gitea Login. Optional

**--output, -o**="": Output format. (simple, table, csv, tsv, yaml, json)

**--remote, -R**="": Discover Gitea login from remote. Optional

**--repo, -r**="": Override local repository path or gitea repository slug to interact with. Optional

### reopen, open

Change state of one or more milestones to 'open'

**--login, -l**="": Use a different Gitea Login. Optional

**--output, -o**="": Output format. (simple, table, csv, tsv, yaml, json)

**--remote, -R**="": Discover Gitea login from remote. Optional

**--repo, -r**="": Override local repository path or gitea repository slug to interact with. Optional

### issues, i

manage issue/pull of an milestone

**--fields, -f**="": Comma-separated list of fields to print. Available values:
			index,state,kind,author,author-id,url,title,body,created,updated,deadline,assignees,milestone,labels,comments,owner,repo
		 (default: index,kind,title,state,updated,labels)

**--kind**="": Filter by kind (issue|pull)

**--limit, --lm**="": specify limit of items per page

**--login, -l**="": Use a different Gitea Login. Optional

**--output, -o**="": Output format. (simple, table, csv, tsv, yaml, json)

**--page, -p**="": specify page, default is 1

**--remote, -R**="": Discover Gitea login from remote. Optional

**--repo, -r**="": Override local repository path or gitea repository slug to interact with. Optional

**--state**="": Filter by issue state (all|open|closed)

#### add, a

Add an issue/pull to an milestone

**--login, -l**="": Use a different Gitea Login. Optional

**--output, -o**="": Output format. (simple, table, csv, tsv, yaml, json)

**--remote, -R**="": Discover Gitea login from remote. Optional

**--repo, -r**="": Override local repository path or gitea repository slug to interact with. Optional

#### remove, r

Remove an issue/pull to an milestone

**--login, -l**="": Use a different Gitea Login. Optional

**--output, -o**="": Output format. (simple, table, csv, tsv, yaml, json)

**--remote, -R**="": Discover Gitea login from remote. Optional

**--repo, -r**="": Override local repository path or gitea repository slug to interact with. Optional

## releases, release, r

Manage releases

**--login, -l**="": Use a different Gitea Login. Optional

**--output, -o**="": Output format. (simple, table, csv, tsv, yaml, json)

**--remote, -R**="": Discover Gitea login from remote. Optional

**--repo, -r**="": Override local repository path or gitea repository slug to interact with. Optional

### list, ls

List Releases

**--limit, --lm**="": specify limit of items per page

**--login, -l**="": Use a different Gitea Login. Optional

**--output, -o**="": Output format. (simple, table, csv, tsv, yaml, json)

**--page, -p**="": specify page, default is 1

**--remote, -R**="": Discover Gitea login from remote. Optional

**--repo, -r**="": Override local repository path or gitea repository slug to interact with. Optional

### create, c

Create a release

**--asset, -a**="": Path to file attachment. Can be specified multiple times

**--draft, -d**: Is a draft

**--login, -l**="": Use a different Gitea Login. Optional

**--note, -n**="": Release notes

**--output, -o**="": Output format. (simple, table, csv, tsv, yaml, json)

**--prerelease, -p**: Is a pre-release

**--remote, -R**="": Discover Gitea login from remote. Optional

**--repo, -r**="": Override local repository path or gitea repository slug to interact with. Optional

**--tag**="": Tag name. If the tag does not exist yet, it will be created by Gitea

**--target**="": Target branch name or commit hash. Defaults to the default branch of the repo

**--title, -t**="": Release title

### delete, rm

Delete one or more releases

**--confirm, -y**: Confirm deletion (required)

**--delete-tag**: Also delete the git tag for this release

**--login, -l**="": Use a different Gitea Login. Optional

**--output, -o**="": Output format. (simple, table, csv, tsv, yaml, json)

**--remote, -R**="": Discover Gitea login from remote. Optional

**--repo, -r**="": Override local repository path or gitea repository slug to interact with. Optional

### edit, e

Edit one or more releases

**--draft, -d**="": Mark as Draft [True/false]

**--login, -l**="": Use a different Gitea Login. Optional

**--note, -n**="": Change Notes

**--output, -o**="": Output format. (simple, table, csv, tsv, yaml, json)

**--prerelease, -p**="": Mark as Pre-Release [True/false]

**--remote, -R**="": Discover Gitea login from remote. Optional

**--repo, -r**="": Override local repository path or gitea repository slug to interact with. Optional

**--tag**="": Change Tag

**--target**="": Change Target

**--title, -t**="": Change Title

## times, time, t

Operate on tracked times of a repository's issues & pulls

**--fields**="": Comma-separated list of fields to print. Available values:
	id,created,repo,issue,user,duration


**--from, -f**="": Show only times tracked after this date

**--login, -l**="": Use a different Gitea Login. Optional

**--mine, -m**: Show all times tracked by you across all repositories (overrides command arguments)

**--output, -o**="": Output format. (simple, table, csv, tsv, yaml, json)

**--remote, -R**="": Discover Gitea login from remote. Optional

**--repo, -r**="": Override local repository path or gitea repository slug to interact with. Optional

**--total, -t**: Print the total duration at the end

**--until, -u**="": Show only times tracked before this date

### add, a

Track spent time on an issue

>tea times add <issue> <duration>

**--login, -l**="": Use a different Gitea Login. Optional

**--remote, -R**="": Discover Gitea login from remote. Optional

**--repo, -r**="": Override local repository path or gitea repository slug to interact with. Optional

### delete, rm

Delete a single tracked time on an issue

>tea times delete <issue> <time ID>

**--login, -l**="": Use a different Gitea Login. Optional

**--remote, -R**="": Discover Gitea login from remote. Optional

**--repo, -r**="": Override local repository path or gitea repository slug to interact with. Optional

### reset

Reset tracked time on an issue

>tea times reset <issue>

**--login, -l**="": Use a different Gitea Login. Optional

**--remote, -R**="": Discover Gitea login from remote. Optional

**--repo, -r**="": Override local repository path or gitea repository slug to interact with. Optional

### list, ls

List tracked times on issues & pulls

**--fields**="": Comma-separated list of fields to print. Available values:
	id,created,repo,issue,user,duration


**--from, -f**="": Show only times tracked after this date

**--login, -l**="": Use a different Gitea Login. Optional

**--mine, -m**: Show all times tracked by you across all repositories (overrides command arguments)

**--output, -o**="": Output format. (simple, table, csv, tsv, yaml, json)

**--remote, -R**="": Discover Gitea login from remote. Optional

**--repo, -r**="": Override local repository path or gitea repository slug to interact with. Optional

**--total, -t**: Print the total duration at the end

**--until, -u**="": Show only times tracked before this date

## organizations, organization, org

List, create, delete organizations

**--limit, --lm**="": specify limit of items per page

**--login, -l**="": Use a different Gitea Login. Optional

**--output, -o**="": Output format. (simple, table, csv, tsv, yaml, json)

**--page, -p**="": specify page, default is 1

**--remote, -R**="": Discover Gitea login from remote. Optional

**--repo, -r**="": Override local repository path or gitea repository slug to interact with. Optional

### list, ls

List Organizations

**--limit, --lm**="": specify limit of items per page

**--login, -l**="": Use a different Gitea Login. Optional

**--output, -o**="": Output format. (simple, table, csv, tsv, yaml, json)

**--page, -p**="": specify page, default is 1

**--remote, -R**="": Discover Gitea login from remote. Optional

**--repo, -r**="": Override local repository path or gitea repository slug to interact with. Optional

### create, c

Create an organization

**--description, -d**="": 

**--location, -L**="": 

**--login, -l**="": Use a different Gitea Login. Optional

**--name, -n**="": 

**--repo-admins-can-change-team-access**: 

**--visibility, -v**="": 

**--website, -w**="": 

### delete, rm

Delete users Organizations

**--login, -l**="": Use a different Gitea Login. Optional

**--remote, -R**="": Discover Gitea login from remote. Optional

## repos, repo

Show repository details

**--fields, -f**="": Comma-separated list of fields to print. Available values:
			description,forks,id,name,owner,stars,ssh,updated,url,permission,type
		 (default: owner,name,type,ssh)

**--limit, --lm**="": specify limit of items per page

**--login, -l**="": Use a different Gitea Login. Optional

**--output, -o**="": Output format. (simple, table, csv, tsv, yaml, json)

**--page, -p**="": specify page, default is 1

**--starred, -s**: List your starred repos instead

**--type, -T**="": Filter by type: fork, mirror, source

**--watched, -w**: List your watched repos instead

### list, ls

List repositories you have access to

**--fields, -f**="": Comma-separated list of fields to print. Available values:
			description,forks,id,name,owner,stars,ssh,updated,url,permission,type
		 (default: owner,name,type,ssh)

**--limit, --lm**="": specify limit of items per page

**--login, -l**="": Use a different Gitea Login. Optional

**--output, -o**="": Output format. (simple, table, csv, tsv, yaml, json)

**--page, -p**="": specify page, default is 1

**--starred, -s**: List your starred repos instead

**--type, -T**="": Filter by type: fork, mirror, source

**--watched, -w**: List your watched repos instead

### search, s

Find any repo on an Gitea instance

**--archived**="": Filter archived repos (true|false)

**--fields, -f**="": Comma-separated list of fields to print. Available values:
			description,forks,id,name,owner,stars,ssh,updated,url,permission,type
		 (default: owner,name,type,ssh)

**--limit, --lm**="": specify limit of items per page

**--login, -l**="": Use a different Gitea Login. Optional

**--output, -o**="": Output format. (simple, table, csv, tsv, yaml, json)

**--owner, -O**="": Filter by owner

**--page, -p**="": specify page, default is 1

**--private**="": Filter private repos (true|false)

**--topic, -t**: Search for term in repo topics instead of name

**--type, -T**="": Filter by type: fork, mirror, source

### create, c

Create a repository

**--branch**="": use custom default branch (need --init)

**--description, --desc**="": add description to repo

**--gitignores, --git**="": list of gitignore templates (need --init)

**--init**: initialize repo

**--labels**="": name of label set to add

**--license**="": add license (need --init)

**--login, -l**="": Use a different Gitea Login. Optional

**--name, -**="": name of new repo

**--output, -o**="": Output format. (simple, table, csv, tsv, yaml, json)

**--owner, -O**="": name of repo owner

**--private**: make repo private

**--readme**="": use readme template (need --init)

**--template**: make repo a template repo

**--trustmodel**="": select trust model (committer,collaborator,collaborator+committer)

### create-from-template, ct

Create a repository based on an existing template

**--avatar**: copy repo avatar from template

**--content**: copy git content from template

**--description, --desc**="": add custom description to repo

**--githooks**: copy git hooks from template

**--labels**: copy repo labels from template

**--login, -l**="": Use a different Gitea Login. Optional

**--name, -n**="": name of new repo

**--output, -o**="": Output format. (simple, table, csv, tsv, yaml, json)

**--owner, -O**="": name of repo owner

**--private**: make new repo private

**--template, -t**="": source template to copy from

**--topics**: copy topics from template

**--webhooks**: copy webhooks from template

### fork, f

Fork an existing repository

**--login, -l**="": Use a different Gitea Login. Optional

**--owner, -O**="": name of fork's owner, defaults to current user

**--remote, -R**="": Discover Gitea login from remote. Optional

**--repo, -r**="": Override local repository path or gitea repository slug to interact with. Optional

## comment, c

Add a comment to an issue / pr

**--login, -l**="": Use a different Gitea Login. Optional

**--output, -o**="": Output format. (simple, table, csv, tsv, yaml, json)

**--remote, -R**="": Discover Gitea login from remote. Optional

**--repo, -r**="": Override local repository path or gitea repository slug to interact with. Optional

## open, o

Open something of the repository in web browser

**--login, -l**="": Use a different Gitea Login. Optional

**--remote, -R**="": Discover Gitea login from remote. Optional

**--repo, -r**="": Override local repository path or gitea repository slug to interact with. Optional

## notifications, notification, n

Show notifications

**--fields, -f**="": Comma-separated list of fields to print. Available values:
			id,status,updated,index,type,state,title,repository
		 (default: id,status,index,type,state,title)

**--limit, --lm**="": specify limit of items per page

**--login, -l**="": Use a different Gitea Login. Optional

**--mine, -m**: Show notifications across all your repositories instead of the current repository only

**--output, -o**="": Output format. (simple, table, csv, tsv, yaml, json)

**--page, -p**="": specify page, default is 1

**--remote, -R**="": Discover Gitea login from remote. Optional

**--repo, -r**="": Override local repository path or gitea repository slug to interact with. Optional

**--states, -s**="": Comma-separated list of notification states to filter by. Available values:
			pinned,unread,read
		 (default: unread,pinned)

**--types, -t**="": Comma-separated list of subject types to filter by. Available values:
			issue,pull,repository,commit
		

### ls, list

List notifications

**--fields, -f**="": Comma-separated list of fields to print. Available values:
			id,status,updated,index,type,state,title,repository
		 (default: id,status,index,type,state,title)

**--limit, --lm**="": specify limit of items per page

**--login, -l**="": Use a different Gitea Login. Optional

**--mine, -m**: Show notifications across all your repositories instead of the current repository only

**--output, -o**="": Output format. (simple, table, csv, tsv, yaml, json)

**--page, -p**="": specify page, default is 1

**--remote, -R**="": Discover Gitea login from remote. Optional

**--repo, -r**="": Override local repository path or gitea repository slug to interact with. Optional

**--states, -s**="": Comma-separated list of notification states to filter by. Available values:
			pinned,unread,read
		 (default: unread,pinned)

**--types, -t**="": Comma-separated list of subject types to filter by. Available values:
			issue,pull,repository,commit
		

### read, r

Mark all filtered or a specific notification as read

**--limit, --lm**="": specify limit of items per page

**--login, -l**="": Use a different Gitea Login. Optional

**--mine, -m**: Show notifications across all your repositories instead of the current repository only

**--output, -o**="": Output format. (simple, table, csv, tsv, yaml, json)

**--page, -p**="": specify page, default is 1

**--remote, -R**="": Discover Gitea login from remote. Optional

**--repo, -r**="": Override local repository path or gitea repository slug to interact with. Optional

**--states, -s**="": Comma-separated list of notification states to filter by. Available values:
			pinned,unread,read
		 (default: unread,pinned)

### unread, u

Mark all filtered or a specific notification as unread

**--limit, --lm**="": specify limit of items per page

**--login, -l**="": Use a different Gitea Login. Optional

**--mine, -m**: Show notifications across all your repositories instead of the current repository only

**--output, -o**="": Output format. (simple, table, csv, tsv, yaml, json)

**--page, -p**="": specify page, default is 1

**--remote, -R**="": Discover Gitea login from remote. Optional

**--repo, -r**="": Override local repository path or gitea repository slug to interact with. Optional

**--states, -s**="": Comma-separated list of notification states to filter by. Available values:
			pinned,unread,read
		 (default: unread,pinned)

### pin, p

Mark all filtered or a specific notification as pinned

**--limit, --lm**="": specify limit of items per page

**--login, -l**="": Use a different Gitea Login. Optional

**--mine, -m**: Show notifications across all your repositories instead of the current repository only

**--output, -o**="": Output format. (simple, table, csv, tsv, yaml, json)

**--page, -p**="": specify page, default is 1

**--remote, -R**="": Discover Gitea login from remote. Optional

**--repo, -r**="": Override local repository path or gitea repository slug to interact with. Optional

**--states, -s**="": Comma-separated list of notification states to filter by. Available values:
			pinned,unread,read
		 (default: unread,pinned)

### unpin

Unpin all pinned or a specific notification

**--limit, --lm**="": specify limit of items per page

**--login, -l**="": Use a different Gitea Login. Optional

**--mine, -m**: Show notifications across all your repositories instead of the current repository only

**--output, -o**="": Output format. (simple, table, csv, tsv, yaml, json)

**--page, -p**="": specify page, default is 1

**--remote, -R**="": Discover Gitea login from remote. Optional

**--repo, -r**="": Override local repository path or gitea repository slug to interact with. Optional

**--states, -s**="": Comma-separated list of notification states to filter by. Available values:
			pinned,unread,read
		 (default: unread,pinned)

## clone, C

Clone a repository locally

**--depth, -d**="": num commits to fetch, defaults to all (default: 0)

**--login, -l**="": Use a different Gitea Login. Optional

## admin, a

Operations requiring admin access on the Gitea instance

### users, u

Manage registered users

**--fields, -f**="": Comma-separated list of fields to print. Available values:
			id,login,full_name,email,avatar_url,language,is_admin,restricted,prohibit_login,location,website,description,visibility,activated,lastlogin_at,created_at
		 (default: id,login,full_name,email,activated)

**--limit, --lm**="": specify limit of items per page

**--login, -l**="": Use a different Gitea Login. Optional

**--output, -o**="": Output format. (simple, table, csv, tsv, yaml, json)

**--page, -p**="": specify page, default is 1

**--remote, -R**="": Discover Gitea login from remote. Optional

**--repo, -r**="": Override local repository path or gitea repository slug to interact with. Optional

#### list, ls

List Users

**--fields, -f**="": Comma-separated list of fields to print. Available values:
			id,login,full_name,email,avatar_url,language,is_admin,restricted,prohibit_login,location,website,description,visibility,activated,lastlogin_at,created_at
		 (default: id,login,full_name,email,activated)

**--limit, --lm**="": specify limit of items per page

**--login, -l**="": Use a different Gitea Login. Optional

**--output, -o**="": Output format. (simple, table, csv, tsv, yaml, json)

**--page, -p**="": specify page, default is 1

**--remote, -R**="": Discover Gitea login from remote. Optional

**--repo, -r**="": Override local repository path or gitea repository slug to interact with. Optional

## help, h

Shows a list of commands or help for one command
