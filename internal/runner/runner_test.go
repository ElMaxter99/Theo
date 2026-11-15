package runner

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"testing"
	"time"
)

// The test binary doubles as the application: with HELPER_APP=1 it serves HTTP
// on $PORT. HELPER_DELAY makes it answer late, HELPER_STATUS sets the status,
// HELPER_CHILD=<file> starts a child process and writes its pid there.
func TestMain(m *testing.M) {
	if os.Getenv("HELPER_APP") == "1" {
		if f := os.Getenv("HELPER_CHILD"); f != "" {
			c := exec.Command("sleep", "300")
			if err := c.Start(); err == nil {
				os.WriteFile(f, []byte(strconv.Itoa(c.Process.Pid)), 0o644)
			}
		}
		if d, err := time.ParseDuration(os.Getenv("HELPER_DELAY")); err == nil {
			time.Sleep(d)
		}
		if os.Getenv("HELPER_EXIT") == "1" {
			fmt.Println("boom: cannot bind")
			os.Exit(3)
		}
		status := 200
		if s, err := strconv.Atoi(os.Getenv("HELPER_STATUS")); err == nil {
			status = s
		}
		http.ListenAndServe("127.0.0.1:"+os.Getenv("PORT"), http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(status)
		}))
		return
	}
	os.Exit(m.Run())
}

func helperCmd() string { return `"` + os.Args[0] + `"` }

func helperEnv(extra map[string]string) map[string]string {
	env := map[string]string{"HELPER_APP": "1"}
	for k, v := range extra {
		env[k] = v
	}
	return env
}

func TestStartWaitsUntilReadyThenStops(t *testing.T) {
	p, err := Start(context.Background(), Spec{Dir: t.TempDir(), Command: helperCmd(), Env: helperEnv(map[string]string{"HELPER_DELAY": "700ms"}), ReadyTimeout: 20 * time.Second})
	if err != nil {
		t.Fatal(err)
	}
	resp, err := http.Get(p.URL() + "/")
	if err != nil || resp.StatusCode != 200 {
		t.Fatalf("the application must answer once Start returns: %v %v", resp, err)
	}
	resp.Body.Close()
	p.Stop()
	p.Stop() // twice is harmless
	select {
	case <-p.Done():
	default:
		t.Fatal("Stop must wait for the process")
	}
	if c, err := net.DialTimeout("tcp", fmt.Sprintf("127.0.0.1:%d", p.Port), time.Second); err == nil {
		c.Close()
		t.Error("the port must be free after Stop")
	}
}

func TestPortPlaceholderAndCustomVariable(t *testing.T) {
	// The helper reads PORT; here the port also goes through ${APP_PORT} in the command.
	p, err := Start(context.Background(), Spec{Dir: t.TempDir(), Command: helperCmd() + " # ${APP_PORT}", PortEnv: "PORT", Env: helperEnv(map[string]string{"SEEN": "${PORT}"}), ReadyTimeout: 20 * time.Second})
	if err != nil {
		t.Fatal(err)
	}
	defer p.Stop()
	if p.Port == 0 || !strings.HasPrefix(p.URL(), "http://127.0.0.1:") {
		t.Errorf("port %d url %s", p.Port, p.URL())
	}
}

func TestReadyStatusAndTimeout(t *testing.T) {
	// A 404 is not an accepted status by default: the wait ends in a timeout.
	_, err := Start(context.Background(), Spec{Dir: t.TempDir(), Command: helperCmd(), Env: helperEnv(map[string]string{"HELPER_STATUS": "404"}), ReadyTimeout: 1500 * time.Millisecond})
	if err == nil || !strings.Contains(err.Error(), "did not become ready") {
		t.Fatalf("a 404 must not count as ready: %v", err)
	}
	// ...unless the section accepts it.
	p, err := Start(context.Background(), Spec{Dir: t.TempDir(), Command: helperCmd(), Env: helperEnv(map[string]string{"HELPER_STATUS": "404"}), ReadyStatus: []int{404}, ReadyTimeout: 20 * time.Second})
	if err != nil {
		t.Fatal(err)
	}
	p.Stop()
}

func TestEarlyExitReportsTheOutput(t *testing.T) {
	_, err := Start(context.Background(), Spec{Dir: t.TempDir(), Command: helperCmd(), Env: helperEnv(map[string]string{"HELPER_EXIT": "1"}), ReadyTimeout: 20 * time.Second})
	if err == nil || !strings.Contains(err.Error(), "ended before it was ready") || !strings.Contains(err.Error(), "boom: cannot bind") {
		t.Fatalf("an early exit must say so and show what the command printed: %v", err)
	}
}

func TestCancelledContext(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	go func() { time.Sleep(300 * time.Millisecond); cancel() }()
	_, err := Start(ctx, Spec{Dir: t.TempDir(), Command: helperCmd(), Env: helperEnv(map[string]string{"HELPER_DELAY": "30s"}), ReadyTimeout: 60 * time.Second})
	if err == nil {
		t.Fatal("a cancelled start must fail")
	}
}

func TestNoCommand(t *testing.T) {
	if _, err := Start(context.Background(), Spec{Command: " "}); err == nil {
		t.Error("an empty command must be an error")
	}
}
