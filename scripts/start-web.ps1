# MolaSearch web launcher (detached from any terminal).
# Usage: powershell -ExecutionPolicy Bypass -File scripts\start-web.ps1
#
# Paths are resolved from this script's location, so the repo can live anywhere.
# Ports: web 3430, gateway 3432. magpie's own gateway uses 3425; MolaSearch
# does not take it. A caller-set MAGPIE_ADDR wins.
# The web sign-in key is kept in data\webkey.txt so the link survives restarts.
$root = Split-Path -Parent (Split-Path -Parent $MyInvocation.MyCommand.Path)
$exe = Join-Path $root "molasearch.exe"
$data = Join-Path $root "data"
if (-not (Test-Path -LiteralPath $data)) { New-Item -ItemType Directory -Path $data | Out-Null }

$addr = $env:MAGPIE_ADDR
if (-not $addr) { $addr = "127.0.0.1:3432" }
$env:MAGPIE_ADDR = $addr

# Keep MolaSearch out of the user's own agent configs (~/.claude, ~/.codex,
# ~/.dsh, ~/.grok ...). It is a separate app: its agent page only sees this
# sandbox home, and nothing here can rewrite the local Claude's settings.
$agentHome = $env:MOLASEARCH_AGENT_HOME
if (-not $agentHome) { $agentHome = Join-Path $root "agent-home" }
if (-not (Test-Path -LiteralPath $agentHome)) { New-Item -ItemType Directory -Path $agentHome | Out-Null }
$env:USERPROFILE = $agentHome
$env:HOME = $agentHome

$key = $env:MAGPIE_WEB_KEY
if (-not $key) {
  $keyFile = Join-Path $data "webkey.txt"
  if (Test-Path -LiteralPath $keyFile) {
    $key = (Get-Content -LiteralPath $keyFile -Raw).Trim()
  } else {
    $key = -join (1..32 | ForEach-Object { '{0:x}' -f (Get-Random -Maximum 16) })
    Set-Content -LiteralPath $keyFile -Value $key -NoNewline
  }
}
$env:MAGPIE_WEB_KEY = $key

# Keys come from .env beside the exe (gitignored) or from the environment.
# An optional external env file may supply POSTGRES_PASSWORD for the
# Postgres/pgvector backend; point AIHOT_ENV at it. Without it, MOLA_PG_DSN
# must already be set (or the backend reports "unavailable" and the local
# SQLite mirror keeps answering).
$envFile = Join-Path $root ".env"
if (Test-Path -LiteralPath $envFile) {
  Get-Content -LiteralPath $envFile -Encoding UTF8 | ForEach-Object {
    if ($_ -match '^\s*([A-Za-z_][A-Za-z0-9_]*)\s*=\s*(.*)\s*$') {
      Set-Item -Path "env:$($Matches[1])" -Value $Matches[2]
    }
  }
}
$aihotEnv = $env:AIHOT_ENV
if ($aihotEnv -and -not $env:MOLA_PG_DSN -and (Test-Path -LiteralPath $aihotEnv)) {
  $pw = $null
  Get-Content -LiteralPath $aihotEnv -Encoding UTF8 | ForEach-Object {
    if ($_ -match '^\s*POSTGRES_PASSWORD\s*=\s*(.*)$') { $pw = $Matches[1] }
  }
  if ($pw) { $env:MOLA_PG_DSN = "postgres://aihot:$pw@127.0.0.1:15432/molasearch?sslmode=disable" }
}

Start-Process -FilePath $exe -ArgumentList "web" `
  -WorkingDirectory $root -WindowStyle Hidden `
  -RedirectStandardOutput (Join-Path $data "web.log") `
  -RedirectStandardError (Join-Path $data "web.err.log")
Write-Host "molasearch web: http://127.0.0.1:3430/?k=$env:MAGPIE_WEB_KEY (gateway $env:MAGPIE_ADDR)"
