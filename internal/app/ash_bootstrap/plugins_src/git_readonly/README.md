# git_readonly

The `git_readonly` native plugin provides a defensive, two-step interface for
inspecting a Git repository.

Discovery:

```text
git_readonly --list
git_readonly --query status
```

Execution:

```text
git_readonly --run status --arg --short
git_readonly --run diff --arg --stat
git_readonly --run log --arg --oneline --arg=-n10
```

The plugin:

- runs only from a directory whose `.git` entry is a real directory;
- does not accept repository/worktree/config/namespace/exec-path overrides;
- rejects all unapproved subcommands and arguments;
- sets `GIT_OPTIONAL_LOCKS=0`, disables prompt and system config, and uses direct
  argv execution without a shell;
- disables external diff and text conversion; and
- caps execution time and stdout output.
