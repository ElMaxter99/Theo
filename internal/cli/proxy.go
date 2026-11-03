package cli

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"log"
	"net"
	"os"
	"os/signal"
	"runtime"
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
  proxy [--port 7080] [--addr 127.0.0.1] [--allow-remote] [--https [--https-port 7443]]
                                               Run a reverse proxy in the foreground that serves every
                                               instance at http://<service>.<instance>.<project>.localhost:7080
                                               and http://<instance>.<project>.localhost:7080 (its web, app,
                                               api or frontend service). <instance> is "main" for the main
                                               worktree, else the worktree directory; the branch works too.
                                               Loopback only unless --allow-remote. Ctrl+C stops it.
                                               --https also serves https://...localhost:7443 with certificates
                                               of Theo's local authority, which can only sign *.localhost
  proxy trust | untrust                        Make this machine trust Theo's local authority (or stop).
                                               The system asks to confirm; on Linux it runs sudo
  proxy routes [project] [--port <n>] [--json | --hosts]
                                               The routes: name, instance, service, target, running.
                                               --hosts prints hosts-file lines for tools that do not
                                               resolve *.localhost by themselves
`)
	portFlag := flagSpec{[]string{"--port"}, "text"}
	registerCompletion(cmdSpec{name: "proxy",
		flags: []flagSpec{portFlag, {[]string{"--addr"}, "text"}, {names: []string{"--allow-remote"}},
			{names: []string{"--https"}}, {[]string{"--https-port"}, "text"}},
		subs: []cmdSpec{{name: "routes", args: []string{"project"},
			flags: []flagSpec{portFlag, jsonFlag, {names: []string{"--hosts"}}}},
			{name: "trust"}, {name: "untrust"}},
	})
}

func runProxy(e *engine.Engine, args []string, stdout, stderr io.Writer) error {
	if len(args) > 0 {
		switch args[0] {
		case "routes":
			return runProxyRoutes(e, args[1:], stdout, stderr)
		case "trust", "untrust":
			return runProxyTrust(args[0] == "untrust", args[1:], stdout, stderr)
		}
	}
	fs := flag.NewFlagSet("proxy", flag.ContinueOnError)
	port := fs.Int("port", proxy.DefaultPort, "port to listen on")
	addr := fs.String("addr", "127.0.0.1", "address to listen on (loopback unless --allow-remote)")
	allowRemote := fs.Bool("allow-remote", false, "allow a non-loopback --addr: other machines can then reach your instances")
	https := fs.Bool("https", false, "also serve HTTPS, with certificates of Theo's local authority")
	httpsPort := fs.Int("https-port", proxy.DefaultHTTPSPort, "port to serve HTTPS on (with --https)")
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
	if *https && (*httpsPort <= 0 || *httpsPort > 65535 || *httpsPort == *port) {
		return usageErrorf("--https-port must be between 1 and 65535 and differ from --port")
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

	dir, dirErr := registry.DefaultDir()
	var ca *proxy.CA
	var tlsLn net.Listener
	tlsListen := ""
	if *https {
		if dirErr != nil {
			return fmt.Errorf("--https needs Theo's data directory, to keep its local authority: %w", dirErr)
		}
		var created bool
		if ca, created, err = proxy.EnsureCA(dir); err != nil {
			return fmt.Errorf("cannot prepare Theo's local authority: %w", err)
		}
		if created {
			fmt.Fprintf(stderr, "theo: created Theo's local authority in %s; it can only sign names under .localhost\n", ca.Dir)
		}
		if !ca.Trust(context.Background()).Trusted() {
			fmt.Fprintln(stderr, "theo: this machine does not trust it yet, so browsers will warn; run `theo proxy trust`")
		}
		tlsListen = net.JoinHostPort(host, strconv.Itoa(*httpsPort))
		if tlsLn, err = net.Listen("tcp", tlsListen); err != nil {
			return fmt.Errorf("cannot listen on %s: %w (choose another port with --https-port)", tlsListen, err)
		}
		defer tlsLn.Close()
	} else {
		*httpsPort = 0
	}

	e.EnableConfigCache(5 * time.Second)
	h := &proxy.Handler{Source: proxy.EngineSource(e, ""), Port: *port, HTTPSPort: *httpsPort, ErrorLog: log.New(stderr, "theo proxy: ", 0)}

	if dirErr == nil {
		if remove, err := proxy.WriteState(dir, proxy.State{Addr: host, Port: *port, HTTPSPort: *httpsPort, PID: os.Getpid(), Started: time.Now().UTC()}); err == nil {
			defer remove()
		}
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	if ip := net.ParseIP(host); ip != nil && !ip.IsLoopback() {
		fmt.Fprintf(stderr, "theo: WARNING: listening on %s: anyone who can reach this machine on port %d can use your instances, with no authentication\n", host, *port)
	}
	if tlsLn != nil {
		fmt.Fprintf(stderr, "theo: proxy on http://%s/ and https://%s/ (Ctrl+C stops it)\n", listen, tlsListen)
	} else {
		fmt.Fprintf(stderr, "theo: proxy on http://%s/ (Ctrl+C stops it)\n", listen)
	}
	tbl, err := h.Table(ctx, false)
	if err != nil {
		fmt.Fprintf(stderr, "theo: could not read the instances yet: %s\n", firstLine(err.Error()))
	}
	if err := printRoutes(stdout, tbl.Routes); err != nil {
		return err
	}
	tlsErr := make(chan error, 1)
	if tlsLn != nil {
		go func() {
			err := proxy.ServeTLS(ctx, tlsLn, h, ca)
			if err != nil && ctx.Err() == nil {
				stop() // one listener failing stops both
			}
			tlsErr <- err
		}()
	} else {
		tlsErr <- nil
	}
	err = proxy.Serve(ctx, ln, h)
	stop()
	if e2 := <-tlsErr; err == nil {
		err = e2
	}
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
	running, httpsPort := false, 0
	if *port == 0 {
		*port = proxy.DefaultPort
		if dir, err := registry.DefaultDir(); err == nil {
			if s, ok := proxy.Running(dir); ok {
				*port, running, httpsPort = s.Port, true, s.HTTPSPort
			}
		}
	} else if dir, err := registry.DefaultDir(); err == nil {
		s, ok := proxy.Running(dir)
		running = ok && s.Port == *port
		if running {
			httpsPort = s.HTTPSPort
		}
	}

	targets, err := proxy.EngineSource(e, project)(context.Background())
	if err != nil {
		return err
	}
	tbl := proxy.BuildTable(targets, *port)
	tbl.SetHTTPS(httpsPort)
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
	https := routes[0].HTTPSURL != ""
	if https {
		fmt.Fprintln(tw, "ROUTE\tHTTPS\tPROJECT\tINSTANCE\tSERVICE\tTARGET\tRUNNING")
	} else {
		fmt.Fprintln(tw, "ROUTE\tPROJECT\tINSTANCE\tSERVICE\tTARGET\tRUNNING")
	}
	for _, r := range routes {
		yes := "no"
		if r.Running {
			yes = "yes"
		}
		if https {
			fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\t%s\t%s\n", r.URL, r.HTTPSURL, r.Project, r.Instance, r.Service, r.Target, yes)
			continue
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

// runProxyTrust is `theo proxy trust` and `theo proxy untrust`.
func runProxyTrust(untrust bool, args []string, stdout, stderr io.Writer) error {
	name := "proxy trust"
	if untrust {
		name = "proxy untrust"
	}
	fs := flag.NewFlagSet(name, flag.ContinueOnError)
	pos, err := parse(fs, args)
	if err != nil {
		return err
	}
	if len(pos) != 0 {
		return usageErrorf("%s takes no arguments", name)
	}
	dir, err := registry.DefaultDir()
	if err != nil {
		return err
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if untrust {
		ca, err := proxy.LoadCA(dir)
		if errors.Is(err, os.ErrNotExist) {
			fmt.Fprintln(stdout, "Theo has no local authority on this machine: nothing to undo.")
			return nil
		}
		if err != nil {
			return err
		}
		if err := ca.RemoveTrust(ctx, os.Stdin, stderr); err != nil {
			return err
		}
		fmt.Fprintf(stdout, "This machine no longer trusts Theo's local authority. %s stays, to trust it again later.\n", ca.CertPath())
		return nil
	}
	ca, created, err := proxy.EnsureCA(dir)
	if err != nil {
		return err
	}
	if created {
		fmt.Fprintf(stdout, "Created Theo's local authority in %s.\n", ca.Dir)
	}
	if ca.Trust(ctx).Trusted() {
		fmt.Fprintln(stdout, "This machine already trusts Theo's local authority.")
		return nil
	}
	fmt.Fprintln(stdout, "Adding Theo's local authority to this machine's trusted roots.")
	fmt.Fprintln(stdout, "It can only sign names under .localhost, and its key does not leave", ca.Dir+".")
	switch runtime.GOOS {
	case "windows", "darwin":
		fmt.Fprintln(stdout, "The system will ask you to confirm.")
	default:
		fmt.Fprintln(stdout, "This needs sudo for the system's store; Chrome's store (~/.pki/nssdb) does not.")
	}
	if err := ca.AddTrust(ctx, os.Stdin, stderr); err != nil {
		return err
	}
	if st := ca.Trust(ctx); !st.Trusted() {
		fmt.Fprintf(stdout, "The system did not confirm it (system store: %v, Chrome's store: %s); see docs/cli/proxy.md#https.\n", st.System, st.NSS)
		return errors.New("Theo's local authority is not trusted yet")
	}
	fmt.Fprintln(stdout, "Done: browsers trust https://...localhost now (restart them if they were open). Undo with `theo proxy untrust`.")
	fmt.Fprintf(stdout, "Firefox keeps its own store on Linux and macOS: import %s there (docs/cli/proxy.md#firefox).\n", ca.CertPath())
	return nil
}
