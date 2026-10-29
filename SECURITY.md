# Security policy

## Reporting a problem

Please do not open a public issue for a security problem. Use GitHub's private reporting: open the **Security** tab of this repository and choose **Report a vulnerability**. Include the Theo version (`theo version`), your system, and the smallest steps that reproduce it. If you cannot use that form, open a public issue that says only that you have a security report and ask for a private channel, without details.

This is a small project maintained in spare time. Expect an acknowledgement within a few days, and a fix or a clear answer as soon as it can be done. You will be credited in the release notes if you wish.

## What is in scope

Theo runs on your machine with your permissions and drives `git` and `docker`. These count as security problems:

- **Data loss**: any way Theo deletes or overwrites volumes, worktrees, branches, ignored or untracked files, or your own files without the explicit, confirmed command that is meant to do it.
- **Secrets leakage**: values set with `theo env set --secret` appearing in Theo's output, logs, history, `--json` output or errors; generated files readable by other users.
- Running something other than intended: arguments or file contents that make Theo execute unexpected commands, or write outside its data directory or the instance it was asked about.

## What is not

- Anything that needs you to run a malicious Compose file or Git repository: Theo runs what the project says, as Docker does.
- Secrets are stored in plain text in Theo's data files, readable only by your user. This is documented behaviour, not encryption; reports about it being unencrypted are not vulnerabilities, but ways to make it weaker than documented are.
- Problems in Git, Docker or Docker Compose themselves.

## Supported versions

Only the latest release is supported.
