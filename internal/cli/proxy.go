package cli

import (
	"context"
	"flag"
	"fmt"
	"io"
	"log"
	"net"
	"os"
	"os/signal"
	"strconv"
	"syscall"
	"text/tabwriter"
	"time"

	"github.com/ElMaxter99/Theo/internal/engine"
	"github.com/ElMaxter99/Theo/internal/proxy"
	"github.com/ElMaxter99/Theo/internal/registry"
)

func init() {
	registerCommand("proxy", runProxy)
	registerHelp(46, `
Local domains (see docs/cli/proxy.md):
  proxy [--port 7080] [--addr 127.0.0.1] [--allow-remote]
                                               Run a reverse proxy in the foreground that serves every
                                               instance at http://<service>.<instance>.<project>.localhost:7080
                                               and http://<instance>.<project>.localhost:7080 (its web, app,
                                               api or frontend service). <instance> is "main" for the main
                                               worktree, else the worktree directory; the branch works too.
                                               Loopback only unless --allow-remote. Ctrl+C stops it
  proxy routes [project] [--port <n>] [--json | --hosts]
                                               The routes: name, instance, service, target, running.
                                               --hosts prints hosts-file lines for tools that do not
                                               resolve *.localhost by themselves
`)
	portFlag := flagSpec{[]string{"--port"}, "text"}
	registerCompletion(cmdSpec{name: "proxy",
		flags: []flagSpec{portFlag, {[]string{"--addr"}, "text"}, {names: []string{"--allow-remote"}}},
		subs: []cmdSpec{{name: "routes", args: []string{"project"},
			flags: []flagSpec{portFlag, jsonFlag, {names: []string{"--hosts"}}}}},
	})
}

func runProxy(e *engine.Engine, args []string, stdout, stderr io.Writer) error {
	if len(args) > 0 && args[0] == "routes" {
		return runProxyRoutes(e, args[1:], stdout, stderr)
	}
	fs := flag.NewFlagSet("proxy", flag.ContinueOnError)
	port := fs.Int("port", proxy.DefaultPort, "port to listen on")
	addr := fs.String("addr", "127.0.0.1", "address to listen on (loopback unless --allow-remote)")
	allowRemote := fs.Bool("allow-remote", false, "allow a non-loopback --addr: other machines can then reach your instances")
	pos, err := parse(fs, args)
	if err != nil {
		return err
	}
	if len(pos) != 0 {
		return usageErrorf("proxy takes no arguments (did you mean `theo proxy routes %s`?)", pos[0])
	}
	if *port <= 0 || *port > 65535 {
		return usageErrorf("--port must be between 1 and 65535")
	}
	host, err := proxy.CheckAddr(*addr, *allowRemote)
	if err != nil {
		return err
	}
	listen := net.JoinHostPort(host, strconv.Itoa(*port))
	ln, err := net.Listen("tcp", listen)
	if err != nil {
		return fmt.Errorf("cannot listen on %s: %w (is another `theo proxy` running? choose another port with --port)", listen, err)
	}
	defer ln.Close()

	e.EnableConfigCache(5 * time.Second)
	h := &proxy.Handler{Source: proxy.EngineSource(e, ""), Port: *port, ErrorLog: log.New(stderr, "theo proxy: ", 0)}

	if dir, err := registry.DefaultDir(); err == nil {
		if remove, err := proxy.WriteState(dir, proxy.State{Addr: host, Port: *port, PID: os.Getpid(), Started: time.Now().UTC()}); err == nil {
			defer remove()
		}
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	if ip := net.ParseIP(host); ip != nil && !ip.IsLoopback() {
		fmt.Fprintf(stderr, "theo: WARNING: listening on %s: anyone who can reach this machine on port %d can use your instances, with no authentication\n", host, *port)
	}
	fmt.Fprintf(stderr, "theo: proxy on http://%s/ (Ctrl+C stops it)\n", listen)
	tbl, err := h.Table(ctx, false)
	if err != nil {
		fmt.Fprintf(stderr, "theo: could not read the instances yet: %s\n", firstLine(err.Error()))
	}
	if err := printRoutes(stdout, tbl.Routes); err != nil {
		return err
	}
	err = proxy.Serve(ctx, ln, h)
	if ctx.Err() != nil {
		fmt.Fprintln(stderr, "theo: proxy stopped")
	}
	return err
}

// proxyRoutes is the document of `theo proxy routes --json`.
type proxyRoutes struct {
	Port    int           `json:"port"`
	Running bool          `json:"proxy_running"`
	Routes  []proxy.Route `json:"routes"`
}

func runProxyRoutes(e *engine.Engine, args []string, stdout, stderr io.Writer) error {
	fs := flag.NewFlagSet("proxy routes", flag.ContinueOnError)
	port := fs.Int("port", 0, "port of the proxy (default: the running proxy's, else 7080)")
	asJSON := fs.Bool("json", false, "print JSON")
	hosts := fs.Bool("hosts", false, "print hosts-file lines")
	pos, err := parse(fs, args)
	if err != nil {
		return err
	}
	if len(pos) > 1 {
		return usageErrorf("proxy routes takes at most one project name")
	}
	if *asJSON && *hosts {
		return usageErrorf("--json and --hosts exclude each other")
	}
	project := ""
	if len(pos) == 1 {
		project = pos[0]
	}
	running := false
	if *port == 0 {
		*port = proxy.DefaultPort
		if dir, err := registry.DefaultDir(); err == nil {
			if s, ok := proxy.Running(dir); ok {
				*port, running = s.Port, true
			}
		}
	} else if dir, err := registry.DefaultDir(); err == nil {
		s, ok := proxy.Running(dir)
		running = ok && s.Port == *port
	}

	targets, err := proxy.EngineSource(e, project)(context.Background())
	if err != nil {
		return err
	}
	tbl := proxy.BuildTable(targets, *port)
	switch {
	case *asJSON:
		return writeJSON(stdout, proxyRoutes{Port: *port, Running: running, Routes: tbl.Routes})
	case *hosts:
		fmt.Fprintln(stdout, "# Theo proxy: names for tools that do not resolve *.localhost themselves.")
		fmt.Fprintln(stdout, "# Add to /etc/hosts (macOS, Linux) or C:\\Windows\\System32\\drivers\\etc\\hosts (Windows).")
		for _, h := range tbl.Hosts() {
			fmt.Fprintf(stdout, "127.0.0.1\t%s\n", h)
		}
		return nil
	}
	if !running {
		fmt.Fprintf(stderr, "theo: no proxy is running on port %d; start it with `theo proxy`\n", *port)
	}
	return printRoutes(stdout, tbl.Routes)
}

func printRoutes(w io.Writer, routes []proxy.Route) error {
	if len(routes) == 0 {
		fmt.Fprintln(w, "No routes: no instance publishes a TCP port on the host.")
		return nil
	}
	tw := tabwriter.NewWriter(w, 0, 4, 2, ' ', 0)
	fmt.Fprintln(tw, "ROUTE\tPROJECT\tINSTANCE\tSERVICE\tTARGET\tRUNNING")
	for _, r := range routes {
		yes := "no"
		if r.Running {
			yes = "yes"
		}
		fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\t%s\n", r.URL, r.Project, r.Instance, r.Service, r.Target, yes)
	}
	return tw.Flush()
}

// addProxyURLs fills the proxy_urls of a status report when a proxy runs.
func addProxyURLs(r *engine.StatusReport) {
	dir, err := registry.DefaultDir()
	if err != nil {
		return
	}
	if s, ok := proxy.Running(dir); ok {
		proxy.AddURLs(r, s.Port)
	}
}
