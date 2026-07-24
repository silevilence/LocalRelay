param(
    [ValidateSet("user", "machine", "both")]
    [string]$InstallScope = "user",

    [string]$Version = ""
)

$ErrorActionPreference = "Stop"

$Root = Resolve-Path (Join-Path $PSScriptRoot "..\..")
$WailsJson = Join-Path $Root "wails.json"
$InstallerTools = Join-Path $Root "build\windows\installer\wails_tools.nsh"
$OriginalWailsJson = Get-Content -LiteralPath $WailsJson -Raw
$OriginalInstallerTools = ""
if (Test-Path $InstallerTools) {
    $OriginalInstallerTools = Get-Content -LiteralPath $InstallerTools -Raw
}
$Utf8NoBom = New-Object System.Text.UTF8Encoding $false

function Write-Utf8NoBom([string]$Path, [string]$Value) {
    [System.IO.File]::WriteAllText($Path, $Value, $Utf8NoBom)
}

$NsisDir = "C:\Program Files (x86)\NSIS"
if (Test-Path (Join-Path $NsisDir "makensis.exe")) {
    $env:Path = "$NsisDir;$env:Path"
}

$env:GOCACHE = Join-Path $Root ".gocache"
Push-Location $Root
try {
    $Config = $OriginalWailsJson | ConvertFrom-Json
    if ($Version -ne "") {
        if ($Version -notmatch '^\d+\.\d+\.\d+$') {
            throw "Version must use numeric SemVer format, for example: 0.2.0"
        }
        $Config.info.productVersion = $Version
        Write-Utf8NoBom $WailsJson ($Config | ConvertTo-Json -Depth 20)
    }

    $ProductVersion = $Config.info.productVersion
    $Scopes = if ($InstallScope -eq "both") { @("user", "machine") } else { @($InstallScope) }

    foreach ($Scope in $Scopes) {
        $Installer = Join-Path $Root "build\bin\LocalRelay-amd64-installer.exe"
        if (Test-Path $Installer) {
            Remove-Item -LiteralPath $Installer
        }

        wails build -platform windows/amd64 -nsis -installscope $Scope -webview2 error
        if (-not (Test-Path $Installer)) {
            throw "Installer was not created: $Installer"
        }

        $NamedInstaller = Join-Path $Root "build\bin\LocalRelay-$ProductVersion-$Scope-amd64-installer.exe"
        Copy-Item -LiteralPath $Installer -Destination $NamedInstaller -Force
        Get-Item $NamedInstaller
    }
}
finally {
    Write-Utf8NoBom $WailsJson $OriginalWailsJson
    if ($OriginalInstallerTools -ne "") {
        Write-Utf8NoBom $InstallerTools $OriginalInstallerTools
    }
    Pop-Location
}
