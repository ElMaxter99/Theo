#!/usr/bin/env bash
# Smoke test of the Linux desktop app under a virtual display.
#
#   xvfb-run -a scripts/desktop-smoke.sh path/to/theo-desktop
#
# Needs: openbox, wmctrl, curl (and the WebKitGTK runtime the binary links).
# It starts the app with an empty, throw-away Theo data directory and checks,
# with the server's own request log and ui.lock:
#   1. the page was loaded and a session was established (POST /session);
#   2. exactly one window titled "Theo" exists;
#   3. a second app reuses the running server (same ui.lock) and its window
#      closing leaves that server running;
#   4. closing the first window (a real WM close request) stops the server:
#      process gone, ui.lock removed, port closed.
set -euo pipefail

bin="${1:?usage: desktop-smoke.sh path/to/theo-desktop}"
: "${DISPLAY:?run under xvfb-run -a}"

work="$(mktemp -d)"
export THEO_HOME="$work/home" HOME="$work/user" XDG_CONFIG_HOME="$work/user/.config"
export THEO_DESKTOP_REQUEST_LOG="$work/requests.log"
export NO_AT_BRIDGE=1 WEBKIT_DISABLE_COMPOSITING_MODE=1
mkdir -p "$THEO_HOME" "$HOME"
pids=()
cleanup() { for p in "${pids[@]:-}"; do kill "$p" 2>/dev/null || true; done; rm -rf "$work"; }
trap cleanup EXIT

fail() { echo "SMOKE FAIL: $*" >&2; echo "--- request log"; cat "$THEO_DESKTOP_REQUEST_LOG" 2>/dev/null || true; echo "--- app output"; cat "$work"/app*.log 2>/dev/null || true; exit 1; }
ok() { echo "ok: $*"; }
wait_for() { # wait_for <seconds> <description> <command...>
  local n=$(($1 * 10)) what=$2; shift 2
  for _ in $(seq "$n"); do if "$@" >/dev/null 2>&1; then return 0; fi; sleep 0.1; done
  fail "timeout waiting for $what"
}
not_running() { ! kill -0 "$1"; }
logged() { grep -q "$1" "$THEO_DESKTOP_REQUEST_LOG"; }
theo_windows() { wmctrl -l | awk '$NF=="Theo" {print $1}'; }
windows_are() { [ "$(count_windows)" = "$1" ]; }
sessions() { grep -c "POST /api/v1/session" "$THEO_DESKTOP_REQUEST_LOG"; }
sessions_over() { [ "$(sessions)" -gt "$1" ]; }
count_windows() { theo_windows | wc -l | tr -d ' '; }

openbox >"$work/wm.log" 2>&1 &
pids+=($!)
wait_for 10 "window manager" wmctrl -m

"$bin" >"$work/app1.log" 2>&1 &
app1=$!
pids+=("$app1")

wait_for 40 "the session to be established" logged "POST /api/v1/session"
wait_for 20 "the projects list to load" logged "GET /api/v1/projects"
ok "page loaded and session established"

lock="$THEO_HOME/ui.lock"
[ -f "$lock" ] || fail "no ui.lock"
port="$(sed -n 's/.*"port":\([0-9]*\).*/\1/p' "$lock")"
pid="$(sed -n 's/.*"pid":\([0-9]*\).*/\1/p' "$lock")"
[ "$pid" = "$app1" ] || fail "ui.lock pid $pid is not the app ($app1)"
code="$(curl -s -o /dev/null -w '%{http_code}' "http://127.0.0.1:$port/api/v1/projects")"
[ "$code" = 401 ] || fail "the server answers $code without a session, expected 401"
ok "server on 127.0.0.1:$port, pid $app1, closed to requests without a session"

wait_for 20 "the window" windows_are 1
[ "$(count_windows)" = 1 ] || fail "expected one window titled Theo, got $(count_windows)"
win1="$(theo_windows)"
ok "one window titled Theo"

# Second app: reuses the server, opens its own window, must not stop the server.
before="$(sessions)"
"$bin" >"$work/app2.log" 2>&1 &
app2=$!
pids+=("$app2")
wait_for 30 "the second window" windows_are 2
[ "$(sed -n 's/.*"pid":\([0-9]*\).*/\1/p' "$lock")" = "$app1" ] || fail "the second app replaced ui.lock"
wait_for 30 "the second session" sessions_over "$before"
grep -q "already running" "$work/app2.log" || fail "second app did not say it reused the server"
ok "second app reused the running server"
win2="$(theo_windows | grep -v "^$win1$")"
wmctrl -i -c "$win2"
wait_for 15 "the second app to exit" not_running "$app2"
[ -f "$lock" ] || fail "closing the reusing window removed ui.lock"
kill -0 "$app1" || fail "closing the reusing window stopped the first app"
code="$(curl -s -o /dev/null -w '%{http_code}' "http://127.0.0.1:$port/api/v1/projects")"
[ "$code" = 401 ] || fail "server no longer answers after the second window closed ($code)"
ok "closing the reusing window left the server running"

# Close the first window the way the user does: the window manager asks it to close.
wmctrl -i -c "$win1"
wait_for 15 "the app to exit after closing its window" not_running "$app1"
[ ! -f "$lock" ] || fail "ui.lock still there after the window closed"
if curl -s -o /dev/null --max-time 2 "http://127.0.0.1:$port/api/v1/meta"; then fail "server still answers on $port"; fi
ok "closing the window stopped the server (process gone, ui.lock removed, port closed)"
echo "SMOKE PASS"
