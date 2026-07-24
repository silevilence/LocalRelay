param(
    [ValidateSet("user", "machine", "both")]
    [string]$InstallScope = "user",

    [string]$Version = ""
)

$ErrorActionPreference = "Stop"

& "$PSScriptRoot\build\windows\package.ps1" -InstallScope $InstallScope -Version $Version
