# Working on Theo (rules for Claude and other agents)

## Never hand over unvalidated work

Before marking a pull request ready, run the real flow and attach the evidence:

1. Run `scripts/evidence.sh` (needs Docker; on a cloud session start the daemon with `dockerd &`
   and, if Playwright wants a newer Chromium than the preinstalled one, symlink the version).
2. It must exit 0. If something fails, fix it; do not hand the PR over red. A flaky test is rerun,
   and the rerun is mentioned in the PR.
3. Paste `evidence/report.md` into the PR description under "How it was tested", and point to the
   screenshots of the flow that changed (CI uploads them as the `e2e-evidence` artifact).
4. For a UI change, look at the screenshots yourself before delivering.
5. Say plainly what could not be checked here: native desktop notifications, window focus, the
   desktop app (`theo-desktop`), Windows and macOS. Those need a real desktop machine.

If a change adds a feature, add or extend a real e2e test in `web/e2e/real/` for it.
Language, sign-off and release rules are in CONTRIBUTING.md.
