# End-to-end smoke battery for Windows PowerShell 5.1 and PowerShell 7: walks
# examples/demo through every Theo feature against a real Docker daemon. It is
# the same sequence, with the same check names, as scripts/smoke.sh.
#
#   .\scripts\smoke.ps1 [-Theo path\to\theo.exe]     (default: theo from PATH)
#
# Needs: git, Docker Desktop (or another daemon) running Linux containers with
# the Compose plugin, curl.exe (built into Windows 10 and later), and free host
# ports 8080 and 8081. The demo's cache service has the fixed container_name
# "cache", so no other container may be called "cache" either.
#
# Isolation: Theo's data goes to a temporary THEO_HOME, the demo is copied to a
# temporary Git repository and its Compose name is changed in that copy to
# $env:THEO_SMOKE_PROJECT (default "theo-smoke"), so a theo-demo of your own is
# never touched. Docker objects of these Compose projects are deleted at the
# end, even after a failure: <p>, <p>-feature-x, <p>-feature-c, <p>-shared.
#
# Environment: THEO_SMOKE_PROJECT, THEO_SMOKE_VERBOSE=1 (print the output of
# every check), THEO_SMOKE_KEEP_IMAGES=1 (keep the images built for the demo).
# Exit code: 0 when every check passed, 1 otherwise, 2 when it could not start.
param([string]$Theo = "theo")

$ErrorActionPreference = "Continue"
$P = if ($env:THEO_SMOKE_PROJECT) { $env:THEO_SMOKE_PROJECT } else { "theo-smoke" }
$Verbose = $env:THEO_SMOKE_VERBOSE -eq "1"
$IsWin = ($PSVersionTable.PSVersion.Major -lt 6) -or $IsWindows
$Curl = if ($IsWin) { "curl.exe" } else { "curl" }
$NullDev = if ($IsWin) { "NUL" } else { "/dev/null" }
$RepoRoot = Split-Path -Parent $PSScriptRoot
$DemoSrc = Join-Path $RepoRoot "examples/demo"
$Stacks = @($P, "$P-feature-x", "$P-feature-c", "$P-shared")

function Die($msg) { Write-Host "smoke: $msg" -ForegroundColor Red; exit 2 }

# The theo binary, as a full path (Start-Process needs it for `theo serve`).
$cmdInfo = Get-Command $Theo -CommandType Application -ErrorAction SilentlyContinue | Select-Object -First 1
if (-not $cmdInfo) { Die "theo not found: pass its path, for example .\scripts\smoke.ps1 -Theo .\dist\theo.exe" }
$TheoPath = $cmdInfo.Source
# `theo` runs the chosen binary. A bare -- must be written '--' by the caller.
function theo { & $TheoPath @args }

# --- Requirements -----------------------------------------------------------
foreach ($c in "git", "docker", $Curl) {
  if (-not (Get-Command $c -ErrorAction SilentlyContinue)) { Die "$c is missing" }
}
if ($P -notmatch '^[a-z0-9][a-z0-9_-]*$') { Die "THEO_SMOKE_PROJECT must be a valid Compose name (lowercase letters, digits, - and _)" }
$osType = docker info --format '{{.OSType}}' 2>$null
if ($LASTEXITCODE -ne 0) { Die "Docker does not answer: start Docker Desktop" }
if ("$osType".Trim() -ne "linux") { Die "Docker runs $osType containers; switch Docker Desktop to Linux containers" }
docker compose version 2>$null | Out-Null
if ($LASTEXITCODE -ne 0) { Die "the Docker Compose plugin is missing" }
if (-not (Test-Path (Join-Path $DemoSrc "compose.yaml"))) { Die "examples/demo not found next to this script ($DemoSrc)" }

function Test-PortBusy([int]$port) {
  $client = New-Object System.Net.Sockets.TcpClient
  try {
    $iar = $client.BeginConnect("127.0.0.1", $port, $null, $null)
    [void]$iar.AsyncWaitHandle.WaitOne(1000)
    return $client.Connected
  } catch { return $false } finally { $client.Close() }
}

# Ids of the containers, volumes or networks of the smoke's Compose projects.
# Filters avoid quotes inside --format, which Windows PowerShell 5.1 mangles.
function Get-Ours([string]$kind) {
  $ids = @()
  foreach ($s in $Stacks) {
    $filter = "label=com.docker.compose.project=$s"
    switch ($kind) {
      "container" { $ids += @(docker ps -aq --filter $filter 2>$null) }
      "volume" { $ids += @(docker volume ls -q --filter $filter 2>$null) }
      "network" { $ids += @(docker network ls -q --filter $filter 2>$null) }
    }
  }
  @($ids | Where-Object { $_ } | Select-Object -Unique)
}
function Remove-Ours {
  # An array passed to a native command becomes one argument per element.
  $c = @(Get-Ours container); if ($c.Count) { docker rm -f $c 2>&1 | Out-Null }
  $n = @(Get-Ours network); if ($n.Count) { docker network rm $n 2>&1 | Out-Null }
  $v = @(Get-Ours volume); if ($v.Count) { docker volume rm -f $v 2>&1 | Out-Null }
}

# Leftovers of an earlier, interrupted run of this script are ours to delete.
Remove-Ours
foreach ($port in 8080, 8081) {
  if (Test-PortBusy $port) { Die "host port $port is in use; the demo publishes 8080 and its second instance 8081. Free them and run again" }
}
if (docker ps -aq --filter "name=^/cache$" 2>$null) {
  Die "a container called `"cache`" exists; the demo's cache service has that fixed container_name. Remove or rename it and run again"
}

# --- Work area --------------------------------------------------------------
$Work = Join-Path ([IO.Path]::GetTempPath()) ("theo-smoke-" + [IO.Path]::GetRandomFileName())
$env:THEO_HOME = Join-Path $Work "home"
$Demo = Join-Path $Work $P
New-Item -ItemType Directory -Force $env:THEO_HOME | Out-Null
$script:pass = 0; $script:fail = 0; $script:Fails = New-Object System.Collections.ArrayList
$script:Serve = $null
$script:Proxy = $null

# T ok|fail "name" { command }: runs the command and compares its exit code.
# Native output goes through ForEach-Object, so stderr lines are kept as text
# (Windows PowerShell 5.1 turns them into error records otherwise).
function T([string]$Want, [string]$Name, [scriptblock]$Cmd) {
  $global:LASTEXITCODE = 0
  $out = @()
  try {
    $out = @(& $Cmd 2>&1 | ForEach-Object { "$_" })
    $code = $LASTEXITCODE
  } catch {
    $out += "$_"; $code = 1
  }
  $ok = if ($Want -eq "ok") { $code -eq 0 } else { $code -ne 0 }
  if ($ok) {
    $script:pass++
    Write-Host "OK   $Name" -ForegroundColor Green
    if ($Verbose) { $out | ForEach-Object { Write-Host "     $_" } }
  } else {
    $script:fail++; [void]$script:Fails.Add($Name)
    $expected = if ($Want -eq "ok") { "0" } else { "a failure" }
    Write-Host "FAIL $Name (exit $code, expected $expected)" -ForegroundColor Red
    $out | Select-Object -Last 8 | ForEach-Object { Write-Host "     $_" }
  }
}
# Check ok "name" { ...; $true/$false }: a check that is not a command. The
# last value the block outputs decides; earlier output is only shown.
function Check([string]$Want, [string]$Name, [scriptblock]$Cond) {
  T $Want $Name {
    $r = @(& $Cond 2>&1)
    if ($r.Count -gt 1) { $r[0..($r.Count - 2)] | ForEach-Object { "$_" } }
    if ($r.Count -gt 0 -and $r[-1] -eq $true) { $global:LASTEXITCODE = 0 } else { $global:LASTEXITCODE = 1 }
  }
}
function Section($title) { Write-Host ""; Write-Host "== $title ==" -ForegroundColor Cyan }
function Page([int]$port) { (& $Curl -s --max-time 5 "http://localhost:$port/" 2>$null) -join "`n" }
function PageHas([int]$port, [string]$text) {
  for ($i = 0; $i -lt 30; $i++) { if ((Page $port).Contains($text)) { return $true }; Start-Sleep 1 }
  return $false
}
function Web([int]$port, [string]$text) { Check ok "localhost:$port shows '$text'" { PageHas $port $text } }
function Visits([int]$port) { if ((Page $port) -match 'visits:\s+(\d+)') { [int]$Matches[1] } else { -1 } }
function WtPath([string]$dir) { Join-Path (Join-Path (Join-Path $env:THEO_HOME "worktrees") $P) $dir } # Theo's own directory
function GitClean([string]$dir) { $s = git -C $dir status --porcelain 2>&1; $s; (-not $s) -and $LASTEXITCODE -eq 0 }

$started = Get-Location
try {
  Write-Host "theo:    $TheoPath ($((theo version 2>&1 | Select-Object -First 1)))"
  Write-Host "project: $P   THEO_HOME: $env:THEO_HOME"

  # The demo, in a Git repository of its own, under the smoke's Compose name.
  Copy-Item -Recurse -Force $DemoSrc $Demo
  $composeFile = Join-Path $Demo "compose.yaml"
  $text = [IO.File]::ReadAllText($composeFile) -replace '(?m)^name: theo-demo(?=\r?$)', "name: $P"
  [IO.File]::WriteAllText($composeFile, $text)
  if (-not ([IO.File]::ReadAllText($composeFile) -match "(?m)^name: $([regex]::Escape($P))\r?$")) { throw "could not set the Compose name in the demo copy" }
  Set-Location $Demo
  git init -q -b main 2>&1 | Out-Null
  git add -A 2>&1 | Out-Null
  git -c user.name=theo-smoke -c user.email=smoke@example.invalid -c core.autocrlf=false commit -q -m demo 2>&1 | Out-Null

  Section "A. Project"
  T ok   "project add" { theo project add . --name $P }
  T ok   "project list" { theo project list }
  T ok   "project show" { theo project show $P }
  T fail "services without .env (asks for GREETING)" { theo services $P }
  Copy-Item .env.example .env
  T ok   "services" { theo services $P }
  T ok   "services --json" { theo services $P --json }
  T ok   "ports" { theo ports $P }
  T ok   "branches" { theo branches $P }
  T ok   "--explain up (changes nothing)" { theo --explain up $P }
  T ok   "ps with nothing running" { theo ps $P }

  Section "B. Main instance"
  T ok   "up --build" { theo up $P --build }
  Web 8080 "greeting"
  T ok   "up --profile worker" { theo up $P --profile worker }
  Start-Sleep 6
  T ok   "logs worker" { theo logs $P --tail 3 worker }
  T ok   "exec in web" { theo exec $P -T web '--' sh -c "echo inside" }
  T ok   "stop web" { theo stop $P web }
  T fail "stopped web does not answer" { & $Curl -sf --max-time 5 "http://localhost:8080/" }
  T ok   "start web" { theo start $P web }
  Web 8080 "greeting"
  T ok   "restart web" { theo restart $P web }
  T ok   "recreate web" { theo recreate $P web }
  T ok   "up --service web --force-recreate" { theo up $P --service web --force-recreate }
  Web 8080 "greeting"

  Section "B2. v0.2: proxy, MCP, engine"
  $PX = 17080; while ((Test-PortBusy $PX) -and $PX -lt 17099) { $PX++ }
  $script:Proxy = Start-Process -FilePath $TheoPath -ArgumentList "proxy", "--port", "$PX" `
    -RedirectStandardOutput (Join-Path $Work "proxy.out") -RedirectStandardError (Join-Path $Work "proxy.err") -NoNewWindow -PassThru
  function ProxyHas([string]$hostName, [string]$text) {
    for ($i = 0; $i -lt 20; $i++) {
      $o = (& $Curl -s --max-time 5 -H "Host: $hostName" "http://localhost:$PX/" 2>$null) -join "`n"
      if ($o -match [regex]::Escape($text)) { return $true }
      Start-Sleep -Milliseconds 500
    }
    $false
  }
  Check ok "proxy: main.$P.localhost reaches web" { ProxyHas "main.$P.localhost" "greeting" }
  Check ok "proxy: web.main.$P.localhost reaches web" { ProxyHas "web.main.$P.localhost" "greeting" }
  Check ok "proxy: unknown host lists the routes (404)" { $c = & $Curl -s -o $NullDev -w "%{http_code}" -H "Host: nope.$P.localhost" "http://localhost:$PX/"; "HTTP $c"; "$c" -eq "404" }
  T ok   "proxy routes --json" { theo proxy routes $P --port $PX --json }
  T ok   "proxy routes --hosts" { theo proxy routes $P --port $PX --hosts }
  function OpenIs([string]$want) { $o = ((theo open $P --print 2>&1 | ForEach-Object { "$_" }) -join "`n").Trim(); $o; $o -eq $want }
  Check ok "open --print gives the local domain while the proxy runs" { OpenIs "http://main.$P.localhost:$PX" }
  Stop-Process -Id $script:Proxy.Id -ErrorAction SilentlyContinue; $script:Proxy = $null
  Start-Sleep -Milliseconds 500
  Check ok "open --print gives localhost without the proxy" { OpenIs "http://localhost:8080" }
  T fail "proxy refuses a non-loopback address" { theo proxy --addr 0.0.0.0 --port $PX }
  Check ok "mcp --read-only: tools/list and status" {
    $msgs = @(
      '{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-06-18","capabilities":{},"clientInfo":{"name":"smoke","version":"1"}}}',
      '{"jsonrpc":"2.0","method":"notifications/initialized"}',
      '{"jsonrpc":"2.0","id":2,"method":"tools/list"}',
      ('{"jsonrpc":"2.0","id":3,"method":"tools/call","params":{"name":"status","arguments":{"project":"' + $P + '"}}}')
    )
    $in = Join-Path $Work "mcp.in"
    [IO.File]::WriteAllText($in, (($msgs -join "`n") + "`n"))
    $o = (Get-Content $in | & $TheoPath mcp --read-only 2>&1 | ForEach-Object { "$_" }) -join "`n"
    if ($o.Length -gt 2000) { $o.Substring(0, 2000) } else { $o }
    ($o -match '"list_projects"') -and -not ($o -match '"name":"up"') -and ($o -match '"id":3') -and ($o -match [regex]::Escape($P))
  }
  Check ok "doctor --json identifies the engine" { $o = (theo doctor --json 2>&1 | ForEach-Object { "$_" }) -join "`n"; $o -match '"engine"' }
  T ok   "mcp install --json" { theo mcp install --json }
  function ChangedWarns { $o = (theo status $P --json 2>&1 | ForEach-Object { "$_" }) -join "`n"; $o -match '"compose-changed"' }
  Check fail "no compose-changed right after up" { ChangedWarns }
  (Get-Content .env) -replace '^GREETING=.*', 'GREETING=Hello again from Theo' | Set-Content .env
  Check ok "status warns compose-changed after editing .env" { ChangedWarns }
  T ok   "up applies the change" { theo up $P }
  Web 8080 "Hello again"
  Check fail "no compose-changed after up" { ChangedWarns }
  T ok   "stop --all" { theo stop --all $P }
  T fail "stopped instance does not answer" { & $Curl -sf --max-time 5 "http://localhost:8080/" }
  T ok   "start after stop --all" { theo start $P }
  Web 8080 "greeting"

  Section "C. Worktrees and clones"
  T ok   "project set --copy-on-add .env" { theo project set $P --copy-on-add .env }
  T ok   "worktree add feature/x --new" { theo worktree add $P feature/x --new }
  T ok   "worktree add feature/c --new --clone --owner agent-a --copy" { theo worktree add $P feature/c --new --clone --owner agent-a --copy }
  T ok   "worktree owner feature/x agent-b" { theo worktree owner $P feature/x agent-b }
  T ok   "worktrees --owner agent-a" { theo worktrees $P --owner agent-a }
  T fail "worktree add of a branch in use (refused)" { theo worktree add $P feature/x }

  Section "D. Second instance: ports, container_name, variables"
  T fail "up feature/x without options (8080 in use)" {
    $env:BRANCH = "feature-x"
    try { theo up $P -w feature/x --build } finally { Remove-Item Env:\BRANCH -ErrorAction SilentlyContinue }
  }
  T fail "up --auto-ports (container_name cache)" { theo up $P -w feature/x --auto-ports }
  T ok   "instance set --neutralize-container-names" { theo instance set $P -w feature/x --neutralize-container-names }
  T ok   "up --auto-ports" { theo up $P -w feature/x --auto-ports }
  T ok   "ports feature/x" { theo ports $P -w feature/x }
  Web 8081 "instance"
  T ok   "env set INSTANCE_NAME" { theo env set $P -w feature/x INSTANCE_NAME=second }
  T ok   "env set secret" { theo env set $P -w feature/x API_TOKEN=not-a-real-secret --secret }
  Check ok "env list masks the secret" {
    $o = (theo env list $P -w feature/x 2>&1 | ForEach-Object { "$_" }) -join "`n"
    $o
    ($o -match "API_TOKEN") -and -not ($o -match "not-a-real-secret")
  }
  T ok   "up applies the variables" { theo up $P -w feature/x --auto-ports }
  Web 8081 "second"
  T ok   "env unset" { theo env unset $P -w feature/x INSTANCE_NAME }
  T ok   "instance show" { theo instance show $P -w feature/x }
  T ok   "instance set --profile worker" { theo instance set $P -w feature/x --profile worker }
  T ok   "instance config" { theo instance config $P -w feature/x }
  T ok   "instance set --no-profiles" { theo instance set $P -w feature/x --no-profiles }
  Check ok "git status clean in feature/x (Theo leaves your files alone)" { GitClean (WtPath "feature-x") }
  T ok   "ps" { theo ps $P }
  T ok   "ps -w feature/x --json" { theo ps $P -w feature/x --json }
  T ok   "status" { theo status $P }
  T ok   "status --json" { theo status --json }
  T ok   "history" { theo history $P --limit 5 }
  T fail "project rename with containers (refused)" { theo project rename $P "$P-other" }

  Section "E. Shared services"
  T ok   "down main" { theo down $P }
  T ok   "down feature/x" { theo down $P -w feature/x }
  T ok   "shared add cache" { theo shared add $P cache }
  T ok   "shared up" { theo shared up $P }
  T ok   "shared use main" { theo shared use $P -w main cache }
  T ok   "shared use feature/x" { theo shared use $P -w feature/x cache }
  T ok   "up main --start-shared" { theo up $P --start-shared }
  T ok   "up feature/x --start-shared" { theo up $P -w feature/x --auto-ports --start-shared }
  [void](PageHas 8080 "visits"); [void](PageHas 8081 "visits")
  $a = Visits 8080; $b = Visits 8081
  Check ok "shared counter across instances ($a then $b)" { $b -gt $a }
  T ok   "shared status" { theo shared status $P }
  T fail "shared down while instances use it (refused)" { theo shared down $P }

  Section "F. Volumes"
  T ok   "down feature/x" { theo down $P -w feature/x }
  T ok   "volumes feature/x (kept after down)" { theo volumes $P -w feature/x }
  T fail "purge without --confirm (refused)" { theo volumes purge $P -w feature/x }
  T ok   "purge confirmed" { theo volumes purge $P -w feature/x --confirm "$P-feature-x" }
  T ok   "backup data" { theo volumes backup $P -w main --volume data --out ./backups }
  $bk = (Get-ChildItem (Join-Path $Demo "backups") -Filter *.tar.gz -ErrorAction SilentlyContinue | Select-Object -First 1).FullName
  T fail "restore with containers (refused)" { theo volumes restore $P -w main --from $bk --replace --confirm $P }
  T ok   "down main" { theo down $P }
  T ok   "restore" { theo volumes restore $P -w main --from $bk --replace --confirm $P }
  T ok   "clone main -> feature/x" { theo volumes clone $P --from main -w feature/x --volume data --replace --confirm "$P-feature-x" }
  T ok   "shared unuse main" { theo shared unuse $P -w main }
  T ok   "shared unuse feature/x" { theo shared unuse $P -w feature/x }
  T ok   "shared down" { theo shared down $P }
  T ok   "shared purge" { theo shared purge $P --confirm "$P-shared" }
  T ok   "shared remove" { theo shared remove $P }

  Section "G. Removing worktrees and reconcile"
  $fx = WtPath "feature-x"; $fc = WtPath "feature-c"
  if (Test-Path $fc) { Set-Content -Path (Join-Path $fc "uncommitted.txt") -Value "change" }
  T fail "worktree remove with changes (refused)" { theo worktree remove $P feature/c }
  T ok   "worktree remove --discard-changes" { theo worktree remove $P feature/c --discard-changes --confirm (Split-Path $fc -Leaf) }
  T ok   "volumes purge feature/x" { theo volumes purge $P -w feature/x --confirm "$P-feature-x" }
  T ok   "worktree prune --merged (dry run)" { theo worktree prune $P --merged }
  Check ok "dry run removed nothing" { Test-Path $fx }
  if (Test-Path $fx) { Remove-Item -Recurse -Force $fx }
  T ok   "reconcile (reports only)" { theo reconcile $P }
  T ok   "reconcile --apply" { theo reconcile $P --apply }

  Section "H. Local interface and extras"
  Check ok "serve opens the interface (HTTP 200)" {
    $sj = Join-Path $Work "serve.json"; $se = Join-Path $Work "serve.err"
    $script:Serve = Start-Process -FilePath $TheoPath -ArgumentList "serve", "--no-open", "--print-launch-url" `
      -RedirectStandardOutput $sj -RedirectStandardError $se -NoNewWindow -PassThru
    $url = $null
    for ($i = 0; $i -lt 20 -and -not $url; $i++) {
      Start-Sleep -Milliseconds 500
      $raw = Get-Content $sj -Raw -ErrorAction SilentlyContinue
      if ($raw -and $raw.Trim()) { try { $url = ($raw | ConvertFrom-Json).url } catch { } }
    }
    "launch url: $url"
    $code = if ($url) { & $Curl -s -o $NullDev -w "%{http_code}" -L -c (Join-Path $Work "cookies.txt") $url } else { "none" }
    "HTTP $code"
    Stop-Process -Id $script:Serve.Id -ErrorAction SilentlyContinue; $script:Serve = $null
    "$code" -eq "200"
  }
  T ok   "completion powershell" { theo completion powershell }
  T ok   "completion bash" { theo completion bash }
  T ok   "doctor --json" { theo doctor --json }
  T ok   "reconcile --json" { theo reconcile --json }

  Section "I. Cleanup"
  T ok   "volumes purge main" { theo volumes purge $P --confirm $P }
  T ok   "project rename" { theo project rename $P "$P-renamed" }
  T ok   "project rename back" { theo project rename "$P-renamed" $P }
  T ok   "worktree add feature/z --new (to delete with the project)" { theo worktree add $P feature/z --new }
  $fz = WtPath "feature-z"
  if (Test-Path $fz) { Set-Content -Path (Join-Path $fz "uncommitted.txt") -Value "change" }
  T fail "project remove --delete-worktrees without --confirm (refused)" { theo project remove $P --delete-worktrees }
  T fail "project remove --delete-worktrees with changes (refused)" { theo project remove $P --delete-worktrees --confirm $P }
  Check ok "worktree still there after the refusals" { Test-Path (Join-Path $fz "uncommitted.txt") }
  T ok   "project remove --delete-worktrees --discard-changes --forget-records" { theo project remove $P --delete-worktrees --confirm $P --discard-changes --forget-records }
  Check ok "feature/z deleted from disk" { -not (Test-Path $fz) }
  Check ok "main worktree kept" { Test-Path $composeFile }
  T ok   "branch feature/z kept" { git -C $Demo rev-parse --verify -q refs/heads/feature/z }
  T fail "project is unregistered" { theo project show $P }
  Check ok "no container or volume of $P remains" {
    $left = @(Get-Ours container) + @(Get-Ours volume)
    $left -join ", "
    $left.Count -eq 0
  }
}
catch {
  $script:fail++; [void]$script:Fails.Add("stopped: $_")
  Write-Host "STOPPED: $_" -ForegroundColor Red
}
finally {
  Set-Location $started
  if ($script:Serve) { Stop-Process -Id $script:Serve.Id -ErrorAction SilentlyContinue }
  if ($script:Proxy) { Stop-Process -Id $script:Proxy.Id -ErrorAction SilentlyContinue }
  Remove-Ours
  if ($env:THEO_SMOKE_KEEP_IMAGES -ne "1") {
    $pattern = "^(" + (($Stacks | ForEach-Object { [regex]::Escape($_) }) -join "|") + ")-[a-z0-9_-]+:"
    $imgs = @(docker images --format '{{.Repository}}:{{.Tag}}' 2>$null | Where-Object { $_ -match $pattern })
    if ($imgs.Count) { docker image rm -f $imgs 2>&1 | Out-Null }
  }
  if (Test-Path $Work) { Remove-Item -Recurse -Force $Work -ErrorAction SilentlyContinue }
  Remove-Item Env:\THEO_HOME -ErrorAction SilentlyContinue
}

Write-Host ""
Write-Host "== Summary: $script:pass passed, $script:fail failed ($($script:pass + $script:fail) checks) ==" -ForegroundColor $(if ($script:fail) { "Red" } else { "Green" })
if ($script:fail -gt 0) {
  $script:Fails | ForEach-Object { Write-Host "  FAIL $_" -ForegroundColor Red }
  exit 1
}
exit 0
