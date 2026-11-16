package cli

import (
	"context"
	"flag"
	"fmt"
	"io"
	"strings"

	"github.com/ElMaxter99/Theo/internal/ai"
	"github.com/ElMaxter99/Theo/internal/engine"
	"github.com/ElMaxter99/Theo/internal/tools"
)

func init() {
	tools.Register(tools.Tool{
		ID:       "ai",
		Title:    "AI providers",
		Summary:  "Which AI command lines (Claude Code, GitHub Copilot) Theo can ask for work, and which model each role uses.",
		Scope:    "ai",
		Commands: []string{"doctor", "models"},
	})
	registerHelp(56, `
  tool ai doctor [--no-ping] [--json]          AI command lines found (claude, copilot), whether each answers, and the
                                               provider and model of each role. Theo uses the session you already have
                                               there and keeps no credentials. The ping is a real, tiny request
  tool ai models <provider> [--json]           Models a provider is known to accept (any other name still works)
`)
}

// providerFor returns the AI provider with this ID. The tests replace it.
var providerFor = ai.Get

type aiRow struct {
	Provider string   `json:"provider"`
	Found    ai.Found `json:"found"`
	Ping     string   `json:"ping,omitempty"`
	Ready    bool     `json:"ready"`
	Models   []string `json:"models,omitempty"`
}

func runAI(e *engine.Engine, args []string, stdout, stderr io.Writer) error {
	if len(args) == 0 {
		return usageErrorf("tool ai works with: doctor, models")
	}
	switch args[0] {
	case "doctor":
		return runAIDoctor(e, args[1:], stdout)
	case "models":
		return runAIModels(args[1:], stdout)
	}
	return usageErrorf("tool ai works with: doctor, models")
}

func runAIDoctor(e *engine.Engine, args []string, stdout io.Writer) error {
	fl := flag.NewFlagSet("tool ai doctor", flag.ContinueOnError)
	asJSON := fl.Bool("json", false, "print JSON")
	noPing := fl.Bool("no-ping", false, "do not send the tiny test request")
	if _, err := parse(fl, args); err != nil {
		return err
	}
	ctx := context.Background()
	var rows []aiRow
	ready := 0
	for _, id := range ai.IDs() {
		p, err := providerFor(id)
		if err != nil {
			return err
		}
		f, err := p.Detect(ctx)
		if err != nil {
			return err
		}
		row := aiRow{Provider: id, Found: f}
		if f.Installed && !*noPing {
			d, perr := ai.Ping(ctx, p, "")
			if perr != nil {
				row.Found.SignedIn = false
				row.Found.Note = perr.Error()
			} else {
				row.Found.SignedIn = true
				row.Ping = fmt.Sprintf("%.1f s", d.Seconds())
			}
		}
		row.Ready = row.Found.Ready()
		if ms, err := p.Models(ctx); err == nil {
			for _, m := range ms {
				row.Models = append(row.Models, m.ID)
			}
		}
		if row.Ready {
			ready++
		}
		rows = append(rows, row)
	}
	var roles ai.Roles
	var rerr error
	if e != nil {
		roles, rerr = ai.RolesFrom(e.Prefs().Get)
	}
	if *asJSON {
		if err := writeJSON(stdout, map[string]any{"providers": rows, "coordinator": roles.Coordinator.String(), "worker": roles.Worker.String()}); err != nil {
			return err
		}
	} else {
		fmt.Fprintf(stdout, "%-9s %-22s %-10s %s\n", "PROVIDER", "FOUND", "SIGNED IN", "MODELS")
		for _, r := range rows {
			found := "-"
			if r.Found.Installed {
				found = r.Found.Version
				if found == "" {
					found = r.Found.Path
				}
			}
			signed := "no"
			if r.Found.SignedIn {
				signed = "yes"
			}
			if !r.Found.Installed {
				signed = "-"
			}
			tail := fmt.Sprintf("%d", len(r.Models))
			if len(r.Models) == 0 {
				tail = "-"
			}
			if r.Ping != "" {
				tail += "     ping " + r.Ping + " ok"
			}
			fmt.Fprintf(stdout, "%-9s %-22s %-10s %s\n", r.Provider, truncate(found, 22), signed, tail)
			if !r.Ready && r.Found.Note != "" {
				fmt.Fprintf(stdout, "          %s\n", r.Found.Note)
			}
		}
		fmt.Fprintln(stdout, "Theo uses the sessions you already have there. It stores no credentials.")
		if rerr != nil {
			fmt.Fprintf(stdout, "theo: %v\n", rerr)
		} else if roles.Coordinator.Provider != "" || roles.Worker.Provider != "" {
			fmt.Fprintf(stdout, "coordinator: %s    worker: %s\n", orDash(roles.Coordinator.String()), orDash(roles.Worker.String()))
		} else {
			fmt.Fprintln(stdout, "roles: not chosen yet (theo config set ai.roles.coordinator claude:<model>, and ai.roles.worker)")
		}
	}
	if ready == 0 {
		return fmt.Errorf("no AI provider is ready. Install and sign in to claude or copilot")
	}
	return nil
}

func runAIModels(args []string, stdout io.Writer) error {
	fl := flag.NewFlagSet("tool ai models", flag.ContinueOnError)
	asJSON := fl.Bool("json", false, "print JSON")
	pos, err := parse(fl, args)
	if err != nil {
		return err
	}
	if len(pos) != 1 {
		return usageErrorf("tool ai models takes one provider (%s)", strings.Join(ai.IDs(), ", "))
	}
	p, err := providerFor(pos[0])
	if err != nil {
		return usageErrorf("%v", err)
	}
	ms, err := p.Models(context.Background())
	if err != nil {
		return err
	}
	var ids []string
	for _, m := range ms {
		ids = append(ids, m.ID)
	}
	if *asJSON {
		return writeJSON(stdout, ids)
	}
	if len(ids) == 0 {
		fmt.Fprintf(stdout, "%s does not list its models. Pass any name it accepts: theo config set ai.model <name>\n", pos[0])
		return nil
	}
	fmt.Fprintln(stdout, "MODEL")
	for _, id := range ids {
		fmt.Fprintln(stdout, id)
	}
	fmt.Fprintln(stdout, "Other names the command line accepts work too (theo config set ai.roles.worker "+pos[0]+":<name>).")
	return nil
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n-1] + "…"
}
