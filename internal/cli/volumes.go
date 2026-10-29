package cli

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"strings"
	"text/tabwriter"

	"github.com/ElMaxter99/Theo/internal/engine"
)

func runVolumes(e *engine.Engine, args []string, stdout, stderr io.Writer) error {
	if len(args) > 0 {
		switch args[0] {
		case "backup", "restore", "clone":
			return runVolumeData(e, args[0], args[1:], stdout, stderr)
		}
	}
	purge := len(args) > 0 && args[0] == "purge"
	if purge {
		args = args[1:]
	}
	name := "volumes"
	if purge {
		name = "volumes purge"
	}
	fs := flag.NewFlagSet(name, flag.ContinueOnError)
	var sel instanceFlags
	sel.register(fs)
	var asJSON bool
	var confirm, out string
	var backup bool
	if purge {
		fs.StringVar(&confirm, "confirm", "", "the instance's Compose project name, to confirm the deletion")
		fs.BoolVar(&backup, "backup", false, "back the volumes up first, and delete them only if that succeeded")
		fs.StringVar(&out, "out", "", "with --backup, the directory for the backups")
	} else {
		fs.BoolVar(&asJSON, "json", false, "print JSON")
	}
	pos, err := parse(fs, args)
	if err != nil {
		return err
	}
	if len(pos) != 1 {
		return usageErrorf("%s needs exactly one project name", name)
	}
	ctx := context.Background()
	inst, err := e.InstanceFor(ctx, pos[0], sel.worktree, sel.options())
	if err != nil {
		return err
	}

	if purge {
		var removed []engine.VolumeInfo
		var report *engine.BackupReport
		if backup {
			removed, report, err = e.PurgeVolumesWithBackup(ctx, inst, confirm, out)
		} else {
			removed, err = e.PurgeVolumes(ctx, inst, confirm)
		}
		if report != nil {
			printBackups(stdout, stderr, report)
		}
		var pe *engine.PurgeError
		if errors.As(err, &pe) {
			msg := fmt.Sprintf("%s\n\n%s", err, volumeLines(pe.Volumes))
			if strings.Contains(err.Error(), "for good") {
				msg += fmt.Sprintf("\n\nTo delete them:\n  theo volumes purge %s -w %s%s --confirm %s", inst.Project, worktreeArg(inst), flagIf(backup, " --backup")+flagIf(out != "", " --out "+out), inst.Compose)
			}
			return errors.New(msg)
		}
		if err != nil {
			return err
		}
		if len(removed) == 0 {
			fmt.Fprintf(stdout, "%s has no volumes to delete.\n", inst.Compose)
			return nil
		}
		fmt.Fprintf(stdout, "Deleted %d volume(s) of %s:\n%s\n", len(removed), inst.Compose, volumeLines(removed))
		return nil
	}

	list, err := e.Volumes(ctx, inst)
	if err != nil {
		return err
	}
	if asJSON {
		return writeJSON(stdout, list)
	}
	describe(stdout, inst)
	if len(list) == 0 {
		fmt.Fprintln(stdout, "No volumes.")
		return nil
	}
	tw := tabwriter.NewWriter(stdout, 0, 4, 2, ' ', 0)
	fmt.Fprintln(tw, "VOLUME\tSTATUS\tCONTAINERS\tDOCKER NAME")
	kept := 0
	for _, v := range list {
		users := "-"
		if len(v.Users) > 0 {
			users = strings.Join(v.Users, ",")
		}
		if v.Status == engine.VolumeKept {
			kept++
		}
		fmt.Fprintf(tw, "%s\t%s\t%s\t%s\n", v.Volume, v.Status, users, v.Name)
	}
	if err := tw.Flush(); err != nil {
		return err
	}
	if kept > 0 {
		fmt.Fprintf(stdout, "\n%d volume(s) kept with their data. Delete them on purpose with `theo volumes purge %s -w %s`.\n", kept, inst.Project, worktreeArg(inst))
	}
	return nil
}

func volumeLines(list []engine.VolumeInfo) string {
	lines := make([]string, len(list))
	for i, v := range list {
		lines[i] = fmt.Sprintf("  %s (%s)", v.Name, v.Status)
	}
	return strings.Join(lines, "\n")
}
