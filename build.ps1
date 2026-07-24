param(
    [ValidateSet("user", "machine", "both")]
    [string]$InstallScope = "user",

    [string]$Version = "",

    [string]$ReleaseRepo = "silevilence/LocalRelay"
)

$ErrorActionPreference = "Stop"

& "$PSScriptRoot\build\windows\package.ps1" -InstallScope $InstallScope -Version $Version -ReleaseRepo $ReleaseRepo
