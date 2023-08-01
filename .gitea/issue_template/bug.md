---
name: "Bug Report"
about: "Use this template when reporting a bug, so you don't forget important information we'd ask for later."
title: "Bug: "
labels: 
- kind/bug
---

### describe your environment
- tea version used (`tea -v`):
    - [ ] I also reproduced the issue [with the latest master build](https://dl.gitea.com/tea/main/)
- Gitea version used:
    - [ ] the issue only occurred after updating gitea recently
- operating system:
- I make use of...
    - [ ] non-standard default branch names (no `main`,`master`, or `trunk`)
    - [ ] .ssh/config or .gitconfig host aliases in my git remotes
    - [ ] ssh_agent or similar
    - [ ] non-standard ports for gitea and/or ssh
    - [ ] something else that's likely to interact badly with tea: ...


Please provide the output of `git remote -v` (if the issue is related to tea not finding resources on Gitea):
```

```

### describe the issue (observed vs expected behaviour)


