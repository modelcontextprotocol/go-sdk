### PR Tips

Typically, PRs should consist of a single commit, and so should generally follow
the [rules for Go commit messages](https://go.dev/wiki/CommitMessage), with the following
changes and additions:

- Markdown is allowed.

- For a pervasive change, use "all" in the title instead of a package name.

- The PR description should provide context (why this change?) and describe the changes
  at a high level. Changes that are obvious from the diffs don't need to be mentioned.
