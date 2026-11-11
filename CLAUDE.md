# Working on Theo (rules for Claude and other agents)

## Never hand over unvalidated work

Before marking a pull request ready, run the real flow and attach the evidence:

1. Run the checks that apply (see docs/testing.md): `go test ./...`, and for anything that touches
   `web/` also `npm run test:e2e` and `npm run test:e2e:real` from `web` (needs Docker; on a cloud
   session start the daemon with `dockerd &`, and symlink the preinstalled Chromium if Playwright
   wants a newer build).
2. Everything must pass. Fix failures; do not hand the PR over red. A flaky test is rerun, and the
   rerun is mentioned in the PR.
3. Under "How it was tested" in the PR, list the commands and results, and point to the screenshots
   of the flow that changed (CI uploads them as the `e2e-evidence` artifact).
4. For a UI change, look at the screenshots yourself before delivering.
5. Say plainly what could not be checked here: native desktop notifications, window focus, the
   desktop app (`theo-desktop`), Windows and macOS. Those need a real desktop machine.

Run the checks in parallel: once a change to `web/` or the server compiles, start the `e2e-verifier`
agent (`.claude/agents/e2e-verifier.md`) in the background and keep developing; read its report before
handing the PR over. It runs the real e2e and describes the screenshots of the flow that changed.

If a change adds a feature, add or extend a real e2e test in `web/e2e/real/` for it.
Language, sign-off and release rules are in CONTRIBUTING.md.
