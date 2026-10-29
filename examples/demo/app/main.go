// Command demo is the tiny web app of examples/demo. It has no dependencies:
// the redis counter speaks the few lines of the protocol it needs.
package main

import (
	"bufio"
	"fmt"
	"net"
	"net/http"
	"os"
	"strings"
	"time"
)

// branch is baked at build time: -ldflags "-X main.branch=...".
var branch = "unknown"

func env(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

// incr runs INCR key on redis and returns the new value.
func incr(addr, key string) (string, error) {
	conn, err := net.DialTimeout("tcp", addr, 2*time.Second)
	if err != nil {
		return "", err
	}
	defer conn.Close()
	conn.SetDeadline(time.Now().Add(2 * time.Second))
	fmt.Fprintf(conn, "*2\r\n$4\r\nINCR\r\n$%d\r\n%s\r\n", len(key), key)
	line, err := bufio.NewReader(conn).ReadString('\n')
	if err != nil {
		return "", err
	}
	line = strings.TrimSpace(line)
	if !strings.HasPrefix(line, ":") {
		return "", fmt.Errorf("redis: %s", line)
	}
	return line[1:], nil
}

func main() {
	addr := env("REDIS_ADDR", "cache:6379")
	if len(os.Args) > 1 && os.Args[1] == "worker" {
		worker(addr)
		return
	}

	greeting := env("GREETING", "")
	instance := env("INSTANCE_NAME", "main")
	// Written on every start, so the named volume `data` has something to keep.
	os.WriteFile("/data/started.txt", []byte(time.Now().UTC().Format(time.RFC3339)+"\n"), 0o644)

	http.HandleFunc("/healthz", func(w http.ResponseWriter, _ *http.Request) { fmt.Fprintln(w, "ok") })
	http.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/" {
			http.NotFound(w, r)
			return
		}
		count, err := incr(addr, "demo:visits")
		if err != nil {
			count = "unavailable (" + err.Error() + ")"
		}
		// The mounted config directory is read on every request, so editing
		// config/note.txt in a worktree shows up without a restart.
		note, _ := os.ReadFile("/etc/demo/note.txt")
		fmt.Fprintf(w, "greeting: %s\ninstance: %s\nbranch:   %s\nvisits:   %s\nnote:     %s\n",
			greeting, instance, branch, count, strings.TrimSpace(string(note)))
	})
	fmt.Println("demo listening on :8080 as instance", instance)
	if err := http.ListenAndServe(":8080", nil); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

// worker counts a tick every few seconds in the same redis as the web app.
func worker(addr string) {
	for {
		n, err := incr(addr, "demo:ticks")
		if err != nil {
			fmt.Println("worker: redis not reachable:", err)
		} else {
			fmt.Println("worker tick", n)
		}
		time.Sleep(5 * time.Second)
	}
}
