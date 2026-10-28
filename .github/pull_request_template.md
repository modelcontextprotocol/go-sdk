<!--
Pull requests from outside the maintainer team need to link an open issue that
a maintainer has assigned to you (or one labeled `help wanted`); others are
closed automatically until that's in place. See CONTRIBUTING.md for details.
-->

### PR Guideline

Typically, PRs should consist of a single commit, and so should generally follow
the [rules for Go commit messages](https://go.dev/wiki/CommitMessage).

You **must** follow the form:

```
net/http: handle foo when bar

[longer description here in the body]

Fixes #12345
```
Notably, for the subject (the first line of description):

- the name of the package affected by the change goes before the colon
- the part after the colon uses the verb tense + phrase that completes the blank in, “this change modifies this package to ___________”
- the verb after the colon is lowercase
- there is no trailing period
- it should be kept as short as possible

Additionally:

- Markdown is allowed.
- For a pervasive change, use "all" in the title instead of a package name.
- The PR description should provide context (why this change?) and describe the changes
  at a high level. Changes that are obvious from the diffs don't need to be mentioned.
- Link the issue with `Fixes #N`, or `Updates #N` for a partial fix. You must be
  assigned to that issue (or it must be labeled `help wanted`).
- Disclose any AI assistance in one line, and be ready to explain the change in
  your own words.
