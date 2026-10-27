# Generates a deliberately broken repository and runs workshere against it.
# The fixture is not part of the product; it exists so the README screenshot and
# the demo GIF can be regenerated from real output instead of being mocked up.
#
#   powershell -ExecutionPolicy Bypass -File testdata/make-demo.ps1
#   powershell -ExecutionPolicy Bypass -File testdata/make-demo.ps1 -Keep

param(
    [string]$Dir = (Join-Path $env:USERPROFILE 'workshere-demo'),
    [switch]$Keep
)

$ErrorActionPreference = 'Stop'

if (Test-Path $Dir) {
    if (-not $Keep) { Remove-Item -Recurse -Force $Dir }
}

New-Item -ItemType Directory -Force $Dir | Out-Null
$utf8 = New-Object System.Text.UTF8Encoding($false)

function Write-Text([string]$rel, [string]$text) {
    $full = Join-Path $Dir $rel
    New-Item -ItemType Directory -Force (Split-Path $full) | Out-Null
    [IO.File]::WriteAllText($full, $text, $utf8)
}

Write-Text 'package.json' @"
{
  "name": "webapp",
  "scripts": {
    "build": "vite build",
    "test": "vitest run"
  }
}
"@

Write-Text 'package-lock.json' @"
{
  "name": "webapp",
  "lockfileVersion": 3
}
"@

Write-Text 'src/utils.ts' "export const slug = (s: string) => s.toLowerCase()`n"
Write-Text 'src/api/handlers.ts' "import { slug } from '../Utils'`nexport const name = slug('Hi')`n"

# A path that only exists on one machine.
$fakeRoot = 'C:' + [char]92 + 'Users' + [char]92 + 'alice' + [char]92 + 'webapp'
Write-Text 'src/config.ts' "export const root = `"$fakeRoot`"`n"

Write-Text '.env' "STRIPE_KEY=sk_test_not_a_real_key`n"
Write-Text '.env.example' "STRIPE_KEY=`n"

Write-Text '.github/workflows/ci.yml' @"
on:
  push:
    paths:
      - "services/api"
      - "src/**"
jobs:
  test:
    runs-on: ubuntu-latest
    steps:
      - uses: actions/checkout@v4
      - uses: actions/setup-node@v4
        with:
          node-version: 18
          cache: npm
      - run: npm ci
      - run: npm run lint
      - run: npm test
"@

# CRLF in a shell script: the classic "bad interpreter" failure.
Write-Text 'scripts/deploy.sh' "#!/bin/sh`r`necho deploying`r`n"

Push-Location $Dir
try {
    git init -q -b main
    git config core.autocrlf false
    git config user.name 'demo'
    git config user.email 'demo@example.invalid'
    git add -A
    git commit -qm 'initial'

    # A dependency change the lockfile does not know about.
    Write-Text 'package.json' @"
{
  "name": "webapp",
  "private": true,
  "scripts": {
    "build": "vite build",
    "test": "vitest run"
  }
}
"@

    $exe = Join-Path (Split-Path $PSScriptRoot -Parent) 'workshere.exe'
    if (-not (Test-Path $exe)) {
        $exe = Join-Path (Split-Path $PSScriptRoot -Parent) 'workshere'
    }

    & $exe --redact --no-color
    # The fixture is meant to have findings, so a non-zero exit is the expected
    # result here; the script itself succeeded.
    $global:LASTEXITCODE = 0
}
finally {
    Pop-Location
    if (-not $Keep) { Remove-Item -Recurse -Force $Dir }
}
