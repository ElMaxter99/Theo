package cli

func init() {
	registerHelp(40, `
Workspace extras (see docs/cli/worktrees.md):
  worktree add <project> <branch> [--clone] [--owner <name>] [--copy] [--copy-file <f>]...
                                               --clone creates an independent clone (git clone
                                               --local, origin set to the project's remote,
                                               submodules initialized) instead of a linked
                                               worktree. --owner labels it. --copy copies the
                                               files listed with project set --copy-on-add from
                                               the main worktree; --copy-file <f> copies one
                                               file. Only untracked/ignored files, never
                                               overwriting; copied secrets are reported
  project set <name> --copy-on-add <f>... | --no-copy-on-add
                                               Root-relative files that --copy copies
  worktrees <project> [--owner <name>] [--json]
                                               Includes KIND (main, worktree, clone) and OWNER
  worktree owner <project> <w> <name> | --clear
                                               Set or clear the owner label of a workspace
`)
}
