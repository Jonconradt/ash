package main

import (
	"path/filepath"
	"strings"
)

func pwshProfilePath(home string) string {
	return filepath.Join(home, "Documents", "PowerShell", "Microsoft.PowerShell_profile.ps1")
}

func pwshWrapperFileName() string {
	return ".ash_pwsh.ps1"
}

func pwshInstallSourceBlock() string {
	return strings.TrimSpace(`
` + installStartMarker + `
if (Test-Path "$HOME/.ash/.ash_pwsh.ps1") { . "$HOME/.ash/.ash_pwsh.ps1" }
` + installEndMarker)
}

func pwshInstallWrapperContent() string {
	return strings.TrimSpace(`
` + installStartMarker + `
function global:_ash_prompt_processing_enabled {
  $snoozeFile = Join-Path $HOME ".ash/.ash_snooze_until"
  if (-not (Test-Path $snoozeFile -PathType Leaf)) {
    return $true
  }
  $content = (Get-Content -Raw -ErrorAction SilentlyContinue $snoozeFile).Trim()
  $expiresAt = 0L
  if (-not [long]::TryParse($content, [ref]$expiresAt)) {
    return $true
  }
  return $expiresAt -le [DateTimeOffset]::UtcNow.ToUnixTimeSeconds()
}

function global:_ash_should_route {
  param(
    [string]$Cmd,
    [string[]]$Args
  )

  if (-not $Args -or $Args.Count -eq 0) {
    return $false
  }

  foreach ($arg in $Args) {
    if ($arg -like "-*") {
      return $false
    }
  }

  $joined = "$Cmd $($Args -join ' ')"
  if ($joined.TrimEnd().EndsWith("?")) {
    return $true
  }

  $first = $Args[0].ToLowerInvariant().TrimEnd("?", "!", ".", ",", ":", ";")
  switch ($first) {
    "is" { return $true }
    "are" { return $true }
    "am" { return $true }
    "do" { return $true }
    "does" { return $true }
    "did" { return $true }
    "can" { return $true }
    "could" { return $true }
    "should" { return $true }
    "would" { return $true }
    "will" { return $true }
    "why" { return $true }
    "how" { return $true }
    "when" { return $true }
    "where" { return $true }
    "who" { return $true }
  }

  return $false
}

function global:_ash_route_or_delegate {
  param(
    [string]$Cmd,
    [Parameter(ValueFromRemainingArguments = $true)]
    [string[]]$Args
  )

  if (_ash_prompt_processing_enabled -and _ash_should_route -Cmd $Cmd -Args $Args) {
    ash $Cmd @Args
    return
  }

  & $Cmd @Args
}

function global:what { param([Parameter(ValueFromRemainingArguments = $true)][string[]]$Args) _ash_route_or_delegate what @Args }
function global:which { param([Parameter(ValueFromRemainingArguments = $true)][string[]]$Args) _ash_route_or_delegate which @Args }
function global:who { param([Parameter(ValueFromRemainingArguments = $true)][string[]]$Args) _ash_route_or_delegate who @Args }
function global:where { param([Parameter(ValueFromRemainingArguments = $true)][string[]]$Args) _ash_route_or_delegate where @Args }
function global:at { param([Parameter(ValueFromRemainingArguments = $true)][string[]]$Args) _ash_route_or_delegate at @Args }
function global:say { param([Parameter(ValueFromRemainingArguments = $true)][string[]]$Args) _ash_route_or_delegate say @Args }
function global:In { param([Parameter(ValueFromRemainingArguments = $true)][string[]]$Args) _ash_route_or_delegate In @Args }
function global:For { param([Parameter(ValueFromRemainingArguments = $true)][string[]]$Args) _ash_route_or_delegate For @Args }
function global:Time { param([Parameter(ValueFromRemainingArguments = $true)][string[]]$Args) _ash_route_or_delegate Time @Args }
` + installEndMarker)
}
