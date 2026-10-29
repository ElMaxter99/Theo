# Shell completion

```
theo completion bash|zsh|fish|powershell
```

prints a completion script for the shell. It completes:

* commands and subcommands (`theo wo<Tab>`, `theo project <Tab>`),
* flags of each command (`theo up shop --f<Tab>` gives `--force-recreate`),
* **project names** (`theo up <Tab>`),
* **worktrees** after `-w` / `--worktree`, and where a command takes one
  (`theo worktree remove shop <Tab>`),
* **service names** where a command takes them (`theo stop shop <Tab>`,
  `theo exec shop <Tab>`, `theo up shop --service <Tab>`). The services come
  from the Compose configuration of the worktree given with `-w`, or of the
  main worktree.

File names are offered where a command takes a path (`project add`,
`--env-file`).

## Install

**bash** (needs bash 4+; macOS ships 3.2, install a newer one or use zsh):

```bash
# this session
source <(theo completion bash)
# every session
theo completion bash > ~/.local/share/bash-completion/completions/theo
```

**zsh**:

```zsh
# this session
source <(theo completion zsh)
# every session: put it in a directory of $fpath, then restart the shell
theo completion zsh > "${fpath[1]}/_theo"
```

`compinit` must be loaded (`autoload -U compinit && compinit` in `~/.zshrc`).

**fish**:

```fish
theo completion fish > ~/.config/fish/completions/theo.fish
```

**PowerShell** (Windows PowerShell 5.1 or PowerShell 7):

```powershell
# this session
theo completion powershell | Out-String | Invoke-Expression
# every session: add that line to the file shown by  $PROFILE
```

## How it works

The scripts are thin. On each Tab they call the hidden command

```
theo __complete suggest --cur=<word at the cursor> -- <words before it...>
```

which prints one candidate per line, and offer those. A last line `:files`
means "also offer file names". Because the logic is in `theo` itself, the four
shells behave the same and a new command or flag is completed after you
upgrade `theo`, without regenerating the script (only a change in how the
shells call it needs a new script).

The same hidden command answers for the dynamic names directly, which is
useful for other tools:

```
theo __complete projects
theo __complete worktrees <project>
theo __complete services <project> [worktree]
```

They print nothing, and exit 0, when the project is unknown or Docker cannot
read its configuration: a completion that fails must not print errors into
the command line.

## Adding a command

The table of commands and flags is `completionSpecs` in
`internal/cli/completion.go`. A command defined in its own file calls
`registerCompletion(cmdSpec{...})` from its `init()`. A registered command
without a spec still completes by name.

## Limits

* bash splits words at `=` and `:` (`COMP_WORDBREAKS`), so `--tail=5` is
  completed as three words. Write `--tail 5`.
* Completion of services runs `docker compose config`, so it takes a few
  hundred milliseconds on the first Tab.
* The bash script was run against a real bash in the tests. The zsh, fish and
  PowerShell scripts follow the documented mechanisms of their shells but were
  not run in a real shell in this environment.
