#!/usr/bin/env bash
# End-to-end smoke battery: walks examples/demo through every Theo feature
# against a real Docker daemon, the way a user would.
#
#   scripts/smoke.sh [path-to-theo]        (default: theo from PATH)
#
# Needs: git, docker with the Compose plugin and a running daemon (Linux
# containers), curl, and free host ports 8080 and 8081 (the demo publishes
# 8080; the second instance gets 8081 from --auto-ports). The demo's cache
# service has the fixed container_name "cache", so no other container may be
# called "cache" either.
#
# Isolation: Theo's data goes to a temporary THEO_HOME, the demo is copied to
# a temporary Git repository, and the demo's Compose name is changed in that
# copy to $THEO_SMOKE_PROJECT (default "theo-smoke"), so a theo-demo of your
# own is never touched. Docker objects of these Compose projects are deleted
# on exit, even after a failure: $P, $P-feature-x, $P-feature-c, $P-shared.
#
# Environment:
#   THEO_SMOKE_PROJECT      project and Compose name (default theo-smoke)
#   THEO_SMOKE_VERBOSE=1    print the output of every check, not only failures
#   THEO_SMOKE_KEEP_IMAGES=1  keep the images built for the demo
#
# Exit code: 0 when every check passed, 1 otherwise (2: could not start).
set -uo pipefail

P="${THEO_SMOKE_PROJECT:-theo-smoke}"
VERBOSE="${THEO_SMOKE_VERBOSE:-0}"
REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
DEMO_SRC="$REPO_ROOT/examples/demo"
STACKS=("$P" "$P-feature-x" "$P-feature-c" "$P-shared")

die() { echo "smoke: $*" >&2; exit 2; }

# The theo binary, as an absolute path (`theo serve` runs in the background).
THEO_BIN="${1:-theo}"
if [[ "$THEO_BIN" == */* ]]; then
  [ -x "$THEO_BIN" ] || die "$THEO_BIN is not an executable"
  THEO_BIN="$(cd "$(dirname "$THEO_BIN")" && pwd)/$(basename "$THEO_BIN")"
else
  THEO_BIN="$(command -v "$THEO_BIN")" || die "theo is not in PATH; pass its path: scripts/smoke.sh ./theo"
fi
theo() { "$THEO_BIN" "$@"; }

# --- Requirements -----------------------------------------------------------
for tool in git docker curl; do
  command -v "$tool" >/dev/null || die "$tool is missing"
done
[[ "$P" =~ ^[a-z0-9][a-z0-9_-]*$ ]] || die "THEO_SMOKE_PROJECT must be a valid Compose name (lowercase letters, digits, - and _)"
os_type="$(docker info --format '{{.OSType}}' 2>/dev/null)" || die "Docker does not answer: start the daemon"
[ "$os_type" = linux ] || die "Docker runs $os_type containers; the demo needs Linux containers"
docker compose version >/dev/null 2>&1 || die "the Docker Compose plugin is missing"
[ -f "$DEMO_SRC/compose.yaml" ] || die "examples/demo not found next to this script ($DEMO_SRC)"

port_busy() { (exec 3<>"/dev/tcp/127.0.0.1/$1") 2>/dev/null; }

# Every container, network and volume of the smoke's Compose projects.
compose_label() { docker "$@" --format '{{.ID}} {{.Label "com.docker.compose.project"}}' 2>/dev/null; }
ours() { # reads "<id> <compose project>" lines, prints the ids that are ours
  local id proj s
  while read -r id proj; do
    for s in "${STACKS[@]}"; do
      if [ "$proj" = "$s" ]; then echo "$id"; break; fi
    done
  done
}
leftovers() { # containers and volumes; networks are deleted on exit either way
  { compose_label ps -a | ours | sed 's/^/container /'
    compose_label volume ls | ours | sed 's/^/volume /'; } || true
}
docker_cleanup() {
  local ids
  ids="$(compose_label ps -a | ours)"; [ -z "$ids" ] || docker rm -f $ids >/dev/null 2>&1
  ids="$(compose_label network ls | ours)"; [ -z "$ids" ] || docker network rm $ids >/dev/null 2>&1
  ids="$(compose_label volume ls | ours)"; [ -z "$ids" ] || docker volume rm -f $ids >/dev/null 2>&1
  return 0
}

# Leftovers of an earlier, interrupted run of this script are ours to delete.
docker_cleanup
for port in 8080 8081; do
  port_busy "$port" && die "host port $port is in use; the demo publishes 8080 and its second instance 8081. Free them and run again"
done
if [ -n "$(docker ps -aq --filter name='^/cache$')" ]; then
  die "a container called \"cache\" exists; the demo's cache service has that fixed container_name. Remove or rename it and run again"
fi

# --- Work area and cleanup --------------------------------------------------
WORK="$(mktemp -d "${TMPDIR:-/tmp}/theo-smoke.XXXXXX")"
export THEO_HOME="$WORK/home"
DEMO="$WORK/$P"
SERVE_PID=""
mkdir -p "$THEO_HOME"

on_exit() {
  local code=$?
  [ -n "$SERVE_PID" ] && kill "$SERVE_PID" 2>/dev/null
  [ -n "${PROXY_PID:-}" ] && kill "$PROXY_PID" 2>/dev/null
  docker_cleanup
  if [ "${THEO_SMOKE_KEEP_IMAGES:-0}" != 1 ]; then
    local imgs
    imgs="$(docker images --format '{{.Repository}}:{{.Tag}}' | grep -E "^($(IFS='|'; echo "${STACKS[*]}"))-[a-z0-9_-]+:" || true)"
    [ -z "$imgs" ] || docker image rm -f $imgs >/dev/null 2>&1
  fi
  rm -rf "$WORK" 2>/dev/null || { chmod -R u+w "$WORK" 2>/dev/null; rm -rf "$WORK"; }
  exit "$code"
}
trap on_exit EXIT
trap 'exit 130' INT TERM

# --- Checks -----------------------------------------------------------------
pass=0; fail=0; FAILS=()
# t ok|fail "name" command...: runs the command and compares its exit code.
t() {
  local want=$1 name=$2 out code ok=0
  shift 2
  out="$("$@" 2>&1)"; code=$?
  if [ "$want" = ok ] && [ "$code" = 0 ]; then ok=1; fi
  if [ "$want" = fail ] && [ "$code" != 0 ]; then ok=1; fi
  if [ "$ok" = 1 ]; then
    pass=$((pass + 1)); echo "OK   $name"
    [ "$VERBOSE" = 1 ] && [ -n "$out" ] && printf '%s\n' "$out" | sed 's/^/     /'
  else
    fail=$((fail + 1)); FAILS+=("$name")
    echo "FAIL $name (exit $code, expected $([ "$want" = ok ] && echo 0 || echo 'a failure'))"
    printf '%s\n' "$out" | tail -8 | sed 's/^/     /'
  fi
  return 0
}
section() { echo; echo "== $* =="; }
page() { curl -s --max-time 5 "localhost:$1/"; }
# web <port> <text>: the page on the port contains the text, waiting up to 30s.
page_has() { local i; for i in $(seq 1 30); do page "$1" | grep -qF "$2" && return 0; sleep 1; done; return 1; }
web() { t ok "localhost:$1 shows '$2'" page_has "$1" "$2"; }
visits() { page "$1" | awk '/^visits:/ {print $2}'; }
wt_path() { echo "$THEO_HOME/worktrees/$P/$1"; } # Theo's own worktree directory
git_clean() { test -z "$(git -C "$1" status --porcelain)"; }

echo "theo:    $THEO_BIN ($(theo version 2>&1 | head -1))"
echo "project: $P   THEO_HOME: $THEO_HOME"

# The demo, in a Git repository of its own, under the smoke's Compose name.
cp -R "$DEMO_SRC" "$DEMO"
sed -i.bak "s/^name: theo-demo\$/name: $P/" "$DEMO/compose.yaml" && rm -f "$DEMO/compose.yaml.bak"
grep -q "^name: $P\$" "$DEMO/compose.yaml" || die "could not set the Compose name in the demo copy"
cd "$DEMO" || die "cannot enter $DEMO"
git init -q -b main
git add -A
git -c user.name=theo-smoke -c user.email=smoke@example.invalid commit -q -m demo

section "A. Project"
t ok   "project add" theo project add . --name "$P"
t ok   "project list" theo project list
t ok   "project show" theo project show "$P"
t fail "services without .env (asks for GREETING)" theo services "$P"
cp .env.example .env
t ok   "services" theo services "$P"
t ok   "services --json" theo services "$P" --json
t ok   "ports" theo ports "$P"
t ok   "branches" theo branches "$P"
t ok   "--explain up (changes nothing)" theo --explain up "$P"
t ok   "ps with nothing running" theo ps "$P"

section "B. Main instance"
t ok   "up --build" theo up "$P" --build
web 8080 greeting
t ok   "up --profile worker" theo up "$P" --profile worker
sleep 6
t ok   "logs worker" theo logs "$P" --tail 3 worker
t ok   "exec in web" theo exec "$P" -T web -- sh -c "echo inside"
t ok   "stop web" theo stop "$P" web
t fail "stopped web does not answer" curl -sf --max-time 5 localhost:8080/
t ok   "start web" theo start "$P" web
web 8080 greeting
t ok   "restart web" theo restart "$P" web
t ok   "recreate web" theo recreate "$P" web
t ok   "up --service web --force-recreate" theo up "$P" --service web --force-recreate
web 8080 greeting

section "B2. v0.2: proxy, MCP, engine"
proxy_port() { local p; for p in 17080 17081 17082 17083; do port_busy "$p" || { echo "$p"; return; }; done; echo 17099; }
PX=$(proxy_port)
"$THEO_BIN" proxy --port "$PX" >"$WORK/proxy.log" 2>&1 &
PROXY_PID=$!
proxy_get() { local i; for i in $(seq 1 20); do curl -s --max-time 5 -H "Host: $1" "localhost:$PX/" | grep -qF "$2" && return 0; sleep 0.5; done; return 1; }
t ok   "proxy: main.$P.localhost reaches web" proxy_get "main.$P.localhost" greeting
t ok   "proxy: web.main.$P.localhost reaches web" proxy_get "web.main.$P.localhost" greeting
t ok   "proxy: unknown host lists the routes (404)" bash -c "test \"\$(curl -s -o /dev/null -w '%{http_code}' -H 'Host: nope.$P.localhost' localhost:$PX/)\" = 404"
t ok   "proxy routes --json" theo proxy routes "$P" --port "$PX" --json
t ok   "proxy routes --hosts" theo proxy routes "$P" --port "$PX" --hosts
open_is() { test "$(theo open "$P" --print)" = "$1"; }
t ok   "open --print gives the local domain while the proxy runs" open_is "http://main.$P.localhost:$PX"
kill "$PROXY_PID" 2>/dev/null; wait "$PROXY_PID" 2>/dev/null
t ok   "open --print gives localhost without the proxy" open_is "http://localhost:8080"
t fail "proxy refuses a non-loopback address" theo proxy --addr 0.0.0.0 --port "$PX"
mcp_list() { printf '%s\n' '{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-06-18","capabilities":{},"clientInfo":{"name":"smoke","version":"1"}}}' '{"jsonrpc":"2.0","method":"notifications/initialized"}' '{"jsonrpc":"2.0","id":2,"method":"tools/list"}' '{"jsonrpc":"2.0","id":3,"method":"tools/call","params":{"name":"status","arguments":{"project":"'"$P"'"}}}' | theo mcp --read-only; }
mcp_ok() { local o; o="$(mcp_list 2>&1)"; echo "$o" | head -c 2000; grep -q '"list_projects"' <<<"$o" && ! grep -q '"name":"up"' <<<"$o" && grep -q '"id":3' <<<"$o" && grep -q "$P" <<<"$o"; }
t ok   "mcp --read-only: tools/list and status" mcp_ok
engine_ok() { theo doctor --json | grep -q '"engine"'; }
t ok   "doctor --json identifies the engine" engine_ok
t ok   "mcp install --json" theo mcp install --json
changed_warns() { theo status "$P" --json | grep -q '"compose-changed"'; }
t fail "no compose-changed right after up" changed_warns
sed -i.bak 's/^GREETING=.*/GREETING=Hello again from Theo/' .env && rm -f .env.bak
t ok   "status warns compose-changed after editing .env" changed_warns
t ok   "up applies the change" theo up "$P"
web 8080 "Hello again"
t fail "no compose-changed after up" changed_warns
t ok   "stop --all" theo stop --all "$P"
t fail "stopped instance does not answer" curl -sf --max-time 5 localhost:8080/
t ok   "start after stop --all" theo start "$P"
web 8080 greeting

section "C. Worktrees and clones"
t ok   "project set --copy-on-add .env" theo project set "$P" --copy-on-add .env
t ok   "worktree add feature/x --new" theo worktree add "$P" feature/x --new
t ok   "worktree add feature/c --new --clone --owner agent-a --copy" theo worktree add "$P" feature/c --new --clone --owner agent-a --copy
t ok   "worktree owner feature/x agent-b" theo worktree owner "$P" feature/x agent-b
t ok   "worktrees --owner agent-a" theo worktrees "$P" --owner agent-a
t fail "worktree add of a branch in use (refused)" theo worktree add "$P" feature/x

section "D. Second instance: ports, container_name, variables"
t fail "up feature/x without options (8080 in use)" env BRANCH=feature-x "$THEO_BIN" up "$P" -w feature/x --build
t fail "up --auto-ports (container_name cache)" theo up "$P" -w feature/x --auto-ports
t ok   "instance set --neutralize-container-names" theo instance set "$P" -w feature/x --neutralize-container-names
t ok   "up --auto-ports" theo up "$P" -w feature/x --auto-ports
t ok   "ports feature/x" theo ports "$P" -w feature/x
web 8081 instance
t ok   "env set INSTANCE_NAME" theo env set "$P" -w feature/x INSTANCE_NAME=second
t ok   "env set secret" theo env set "$P" -w feature/x API_TOKEN=not-a-real-secret --secret
env_masked() { local o; o="$(theo env list "$P" -w feature/x 2>&1)"; echo "$o"; grep -q API_TOKEN <<<"$o" && ! grep -q not-a-real-secret <<<"$o"; }
t ok   "env list masks the secret" env_masked
t ok   "up applies the variables" theo up "$P" -w feature/x --auto-ports
web 8081 second
t ok   "env unset" theo env unset "$P" -w feature/x INSTANCE_NAME
t ok   "instance show" theo instance show "$P" -w feature/x
t ok   "instance set --profile worker" theo instance set "$P" -w feature/x --profile worker
t ok   "instance config" theo instance config "$P" -w feature/x
t ok   "instance set --no-profiles" theo instance set "$P" -w feature/x --no-profiles
t ok   "git status clean in feature/x (Theo leaves your files alone)" git_clean "$(wt_path feature-x)"
t ok   "ps" theo ps "$P"
t ok   "ps -w feature/x --json" theo ps "$P" -w feature/x --json
t ok   "status" theo status "$P"
t ok   "status --json" theo status --json
t ok   "history" theo history "$P" --limit 5
t fail "project rename with containers (refused)" theo project rename "$P" "$P-other"

section "E. Shared services"
t ok   "down main" theo down "$P"
t ok   "down feature/x" theo down "$P" -w feature/x
t ok   "shared add cache" theo shared add "$P" cache
t ok   "shared up" theo shared up "$P"
t ok   "shared use main" theo shared use "$P" -w main cache
t ok   "shared use feature/x" theo shared use "$P" -w feature/x cache
t ok   "up main --start-shared" theo up "$P" --start-shared
t ok   "up feature/x --start-shared" theo up "$P" -w feature/x --auto-ports --start-shared
page_has 8080 visits >/dev/null; page_has 8081 visits >/dev/null
a="$(visits 8080)"; b="$(visits 8081)"
t ok   "shared counter across instances ($a then $b)" test "${b:-0}" -gt "${a:-0}"
t ok   "shared status" theo shared status "$P"
t fail "shared down while instances use it (refused)" theo shared down "$P"

section "F. Volumes"
t ok   "down feature/x" theo down "$P" -w feature/x
t ok   "volumes feature/x (kept after down)" theo volumes "$P" -w feature/x
t fail "purge without --confirm (refused)" theo volumes purge "$P" -w feature/x
t ok   "purge confirmed" theo volumes purge "$P" -w feature/x --confirm "$P-feature-x"
t ok   "backup data" theo volumes backup "$P" -w main --volume data --out ./backups
bk="$(ls ./backups/*.tar.gz 2>/dev/null | head -1)"
t fail "restore with containers (refused)" theo volumes restore "$P" -w main --from "$bk" --replace --confirm "$P"
t ok   "down main" theo down "$P"
t ok   "restore" theo volumes restore "$P" -w main --from "$bk" --replace --confirm "$P"
t ok   "clone main -> feature/x" theo volumes clone "$P" --from main -w feature/x --volume data --replace --confirm "$P-feature-x"
t ok   "shared unuse main" theo shared unuse "$P" -w main
t ok   "shared unuse feature/x" theo shared unuse "$P" -w feature/x
t ok   "shared down" theo shared down "$P"
t ok   "shared purge" theo shared purge "$P" --confirm "$P-shared"
t ok   "shared remove" theo shared remove "$P"

section "G. Removing worktrees and reconcile"
fx="$(wt_path feature-x)"; fc="$(wt_path feature-c)"
echo change > "$fc/uncommitted.txt"
t fail "worktree remove with changes (refused)" theo worktree remove "$P" feature/c
t ok   "worktree remove --discard-changes" theo worktree remove "$P" feature/c --discard-changes --confirm "$(basename "$fc")"
t ok   "volumes purge feature/x" theo volumes purge "$P" -w feature/x --confirm "$P-feature-x"
t ok   "worktree prune --merged (dry run)" theo worktree prune "$P" --merged
t ok   "dry run removed nothing" test -d "$fx"
rm -rf "$fx"
t ok   "reconcile (reports only)" theo reconcile "$P"
t ok   "reconcile --apply" theo reconcile "$P" --apply

section "H. Local interface and extras"
serve_ok() {
  local out="$WORK/serve.json" url code i
  "$THEO_BIN" serve --no-open --print-launch-url >"$out" 2>"$WORK/serve.err" &
  SERVE_PID=$!
  for i in $(seq 1 20); do grep -q '"url"' "$out" 2>/dev/null && break; sleep 0.5; done
  url="$(sed -n 's/.*"url" *: *"\([^"]*\)".*/\1/p' "$out" | head -1)"
  echo "launch url: ${url:-none}"
  code="$(curl -s -o /dev/null -w '%{http_code}' -L -c "$WORK/cookies" "$url")"
  echo "HTTP $code"
  kill "$SERVE_PID" 2>/dev/null; wait "$SERVE_PID" 2>/dev/null; SERVE_PID=""
  [ "$code" = 200 ]
}
t ok   "serve opens the interface (HTTP 200)" serve_ok
t ok   "completion powershell" theo completion powershell
t ok   "completion bash" theo completion bash
t ok   "doctor --json" theo doctor --json
t ok   "reconcile --json" theo reconcile --json

section "I. Cleanup"
t ok   "volumes purge main" theo volumes purge "$P" --confirm "$P"
t ok   "project rename" theo project rename "$P" "$P-renamed"
t ok   "project rename back" theo project rename "$P-renamed" "$P"
t ok   "worktree add feature/z --new (to delete with the project)" theo worktree add "$P" feature/z --new
fz="$(wt_path feature-z)"
echo change > "$fz/uncommitted.txt"
t fail "project remove --delete-worktrees without --confirm (refused)" theo project remove "$P" --delete-worktrees
t fail "project remove --delete-worktrees with changes (refused)" theo project remove "$P" --delete-worktrees --confirm "$P"
t ok   "worktree still there after the refusals" test -f "$fz/uncommitted.txt"
t ok   "project remove --delete-worktrees --discard-changes --forget-records" theo project remove "$P" --delete-worktrees --confirm "$P" --discard-changes --forget-records
t ok   "feature/z deleted from disk" test ! -e "$fz"
t ok   "main worktree kept" test -f "$DEMO/compose.yaml"
t ok   "branch feature/z kept" git -C "$DEMO" rev-parse --verify -q refs/heads/feature/z
t fail "project is unregistered" theo project show "$P"
no_leftovers() { local l; l="$(leftovers)"; [ -n "$l" ] && echo "$l"; [ -z "$l" ]; }
t ok   "no container or volume of $P remains" no_leftovers

echo
echo "== Summary: $pass passed, $fail failed ($((pass + fail)) checks) =="
if [ "$fail" -gt 0 ]; then
  printf '  FAIL %s\n' "${FAILS[@]}"
  exit 1
fi
exit 0
