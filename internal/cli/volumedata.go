package cli

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"

	"github.com/ElMaxter99/Theo/internal/engine"
)

func init() {
	registerHelp(40, `
  volumes backup <project> -w <w> [--volume <v>]... [--out <dir>] [--json]
                                                    Write one .tar.gz and a manifest per volume,
                                                    read-only; works while it runs (data may be
                                                    inconsistent). Helper image: THEO_HELPER_IMAGE
  volumes restore <project> -w <w> --from <backup.tar.gz> [--volume <v>] [--replace] --confirm <compose-project>
                                                    Restore a backup into the instance's volume;
                                                    refused while it has containers or data
  volumes clone <project> --from <w> -w <w> [--volume <v>]... [--replace] --confirm <compose-project>
                                                    Copy the data of one instance's volumes to another
`)
}

// runVolumeData runs `volumes backup|restore|clone`.
func runVolumeData(e *engine.Engine, cmd string, args []string, stdout, stderr io.Writer) error {
	name := "volumes " + cmd
	fs := flag.NewFlagSet(name, flag.ContinueOnError)
	var sel instanceFlags
	sel.register(fs)
	var volumes stringList
	var asJSON, replace bool
	var confirm, out, from string
	fs.Var(&volumes, "volume", "volume to work on, by its name in the Compose file (repeatable; default: all)")
	fs.BoolVar(&asJSON, "json", false, "print JSON")
	switch cmd {
	case "backup":
		fs.StringVar(&out, "out", "", "directory for the backups (default: <Theo data dir>/backups/<project>)")
	case "restore":
		fs.StringVar(&from, "from", "", "the .tar.gz written by `theo volumes backup`")
		fs.BoolVar(&replace, "replace", false, "delete the data the volume has now")
		fs.StringVar(&confirm, "confirm", "", "the instance's Compose project name")
	case "clone":
		fs.StringVar(&from, "from", "", "the worktree whose data is copied (branch, directory name or path)")
		fs.BoolVar(&replace, "replace", false, "delete the data the target volumes have now")
		fs.StringVar(&confirm, "confirm", "", "the target instance's Compose project name")
	}
	pos, err := parse(fs, args)
	if err != nil {
		return err
	}
	if len(pos) != 1 {
		return usageErrorf("%s needs exactly one project name", name)
	}
	if cmd != "backup" && from == "" {
		return usageErrorf("%s needs --from", name)
	}
	if cmd == "restore" && len(volumes) > 1 {
		return usageErrorf("%s restores one volume: pass --volume at most once", name)
	}
	ctx := context.Background()
	inst, err := e.Instance(ctx, pos[0], sel.worktree, sel.envFiles)
	if err != nil {
		return err
	}

	switch cmd {
	case "backup":
		report, err := e.BackupVolumes(ctx, inst, volumes, out)
		if asJSON && err == nil {
			return writeJSON(stdout, report)
		}
		if report != nil {
			printBackups(stdout, stderr, report)
		}
		return err

	case "restore":
		vol := ""
		if len(volumes) == 1 {
			vol = volumes[0]
		}
		res, err := e.RestoreVolume(ctx, inst, engine.RestoreOptions{From: from, Volume: vol, Confirm: confirm, Replace: replace})
		if err != nil {
			return confirmHint(err, fmt.Sprintf("theo volumes restore %s -w %s --from %s%s%s", inst.Project, worktreeArg(inst), from, flagIf(vol != "", " --volume "+vol), flagIf(replace, " --replace")), inst.Compose)
		}
		if asJSON {
			return writeJSON(stdout, res)
		}
		fmt.Fprintf(stdout, "Restored %s into volume %s of %s", from, res.Docker, inst.Compose)
		switch {
		case res.Created:
			fmt.Fprint(stdout, " (the volume was created with the Compose labels, so `theo up` adopts it)")
		case res.Replaced:
			fmt.Fprint(stdout, " (the previous data was replaced)")
		}
		fmt.Fprintln(stdout, ".")
		if !res.Verified {
			fmt.Fprintln(stderr, "warning: there was no manifest with a sha256 next to the backup, so its integrity was not checked")
		}
		return nil

	default: // clone
		src, err := e.Instance(ctx, pos[0], from, sel.envFiles)
		if err != nil {
			return err
		}
		report, err := e.CloneVolumes(ctx, src, inst, engine.CloneOptions{Volumes: volumes, Confirm: confirm, Replace: replace})
		if err != nil {
			var vols string
			for _, v := range volumes {
				vols += " --volume " + v
			}
			return confirmHint(err, fmt.Sprintf("theo volumes clone %s --from %s -w %s%s%s", inst.Project, worktreeArg(src), worktreeArg(inst), vols, flagIf(replace, " --replace")), inst.Compose)
		}
		if asJSON {
			return writeJSON(stdout, report)
		}
		for _, w := range report.Warnings {
			fmt.Fprintf(stderr, "warning: %s\n", w)
		}
		fmt.Fprintf(stdout, "Copied %d volume(s) from %s to %s:\n", len(report.Volumes), src.Compose, inst.Compose)
		for _, v := range report.Volumes {
			fmt.Fprintf(stdout, "  %s -> %s\n", v.From, v.To)
		}
		return nil
	}
}

func flagIf(cond bool, s string) string {
	if cond {
		return s
	}
	return ""
}

// confirmHint adds the exact command to run to an error that only asks for
// the confirmation.
func confirmHint(err error, command, compose string) error {
	var ce *engine.ConfirmError
	if errors.As(err, &ce) {
		return fmt.Errorf("%s\n\nTo go on:\n  %s --confirm %s", err, command, compose)
	}
	return err
}

func printBackups(stdout, stderr io.Writer, report *engine.BackupReport) {
	for _, w := range report.Warnings {
		fmt.Fprintf(stderr, "warning: %s\n", w)
	}
	for _, b := range report.Backups {
		fmt.Fprintf(stdout, "Backed up %s (%s): %s\n  manifest: %s\n", b.Volume, humanSize(b.Size), b.File, b.ManifestFile)
	}
}

func humanSize(n int64) string {
	const unit = 1024
	if n < unit {
		return fmt.Sprintf("%d B", n)
	}
	div, exp := int64(unit), 0
	for m := n / unit; m >= unit; m /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %ciB", float64(n)/float64(div), "KMGT"[exp])
}
