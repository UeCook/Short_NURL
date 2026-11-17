#requires -Version 7.4
[CmdletBinding()]
param(
    [ValidateSet('windows-386', 'windows-amd64', 'windows-arm64', 'linux-386', 'linux-amd64', 'linux-armv7', 'linux-arm64')]
    [string[]]$Target = @('windows-386', 'windows-amd64', 'windows-arm64', 'linux-386', 'linux-amd64', 'linux-armv7', 'linux-arm64'),
    [ValidatePattern('^[0-9A-Za-z][0-9A-Za-z._-]*$')]
    [string]$Version = '3.1.1_GitHub',
    [string]$OutputDir = '',
    [string]$NfpmPath = 'nfpm',
    [string]$Maintainer = ''
)

$ErrorActionPreference = 'Stop'
$projectRoot = [System.IO.Path]::GetFullPath($PSScriptRoot)
$sourcePackageName = 'Short_NURL_v' + ($Version -replace '_GitHub$', '')
if ([string]::IsNullOrWhiteSpace($OutputDir)) {
    $OutputDir = Join-Path (Split-Path -Parent $projectRoot) ($sourcePackageName + '-release')
}
$outputRoot = $ExecutionContext.SessionState.Path.GetUnresolvedProviderPathFromPSPath($OutputDir)
$projectPrefix = $projectRoot.TrimEnd([char[]]'\/') + [System.IO.Path]::DirectorySeparatorChar
if ($outputRoot.Equals($projectRoot, [System.StringComparison]::OrdinalIgnoreCase) -or
    $outputRoot.StartsWith($projectPrefix, [System.StringComparison]::OrdinalIgnoreCase)) {
    throw 'OutputDir must be outside the source directory.'
}

Get-Command go -ErrorAction Stop | Out-Null
Add-Type -AssemblyName System.IO.Compression.FileSystem
$targets = @($Target | Select-Object -Unique)
$hasLinux = @($targets | Where-Object { $_.StartsWith('linux-') }).Count -gt 0
$nfpmCommand = ''
if ($hasLinux) {
    if ([string]::IsNullOrWhiteSpace($Maintainer)) {
        throw 'Linux packages require -Maintainer with a public maintainer name and email.'
    }
    $nfpmCommand = (Get-Command $NfpmPath -ErrorAction Stop).Source
    . (Join-Path $projectRoot 'deploy/release/package-linux.ps1')
}
$sourcePackagePath = Join-Path $outputRoot $sourcePackageName
$sourceZipPath = Join-Path $outputRoot "Short_NURL_v${Version}_source.zip"
foreach ($path in @($sourcePackagePath, $sourceZipPath)) {
    if (Test-Path -LiteralPath $path) {
        throw "Output already exists: $path. Choose a new OutputDir to avoid overwriting a release."
    }
}
foreach ($platform in $targets) {
    $packageName = "Short_NURL_v${Version}_${platform}"
    $extensions = if ($platform.StartsWith('windows-')) { @('.zip') } else { @('.tar.gz', '.deb', '.rpm') }
    $platformPaths = @((Join-Path $outputRoot $packageName)) + @($extensions | ForEach-Object { Join-Path $outputRoot ($packageName + $_) })
    foreach ($path in $platformPaths) {
        if (Test-Path -LiteralPath $path) {
            throw "Output already exists: $path. Choose a new OutputDir to avoid overwriting a release."
        }
    }
}

$envNames = @('GOOS', 'GOARCH', 'CGO_ENABLED', 'GO386', 'GOAMD64', 'GOARM', 'GOARM64')
$previousEnv = @{}
foreach ($name in $envNames) {
    $previousEnv[$name] = [System.Environment]::GetEnvironmentVariable($name, 'Process')
}
$archives = @()
Push-Location -LiteralPath $projectRoot
try {
    # This project has no C dependencies; pure Go enables all seven targets
    # without installing a separate C cross-compiler for every architecture.
    $env:CGO_ENABLED = '0'
    $env:GO386 = 'sse2'
    $env:GOAMD64 = 'v1'
    $env:GOARM = '7'
    $env:GOARM64 = 'v8.0'

    foreach ($platform in $targets) {
        $parts = $platform.Split('-')
        $env:GOOS = $parts[0]
        $env:GOARCH = if ($parts[1] -eq 'armv7') { 'arm' } else { $parts[1] }
        $packageName = "Short_NURL_v${Version}_${platform}"
        $packagePath = Join-Path $outputRoot $packageName
        $binPath = Join-Path $packagePath 'bin'
        New-Item -ItemType Directory -Path $binPath -Force | Out-Null

        Write-Host "Building $platform ..."
        & go build -trimpath -buildvcs=false -ldflags "-s -w -X main.version=$Version" -o ($binPath + [System.IO.Path]::DirectorySeparatorChar) ./cmd/nurl-server ./cmd/nurl ./cmd/nurl-key
        if ($LASTEXITCODE -ne 0) {
            throw "Build failed: $platform"
        }

        # Include license notices, runtime assets, and the empty data-directory placeholder.
        $runtimePaths = @('LICENSE.txt', 'NOTICE.txt', 'README.md', 'BUILD.md', 'index.html', 'frontend', 'config', 'backend/lua', 'backend/data/.gitkeep', 'deploy/nginx', 'deploy/linux')
        foreach ($relativePath in $runtimePaths) {
            $source = Join-Path $projectRoot $relativePath
            $destination = Join-Path $packagePath $relativePath
            New-Item -ItemType Directory -Path (Split-Path -Parent $destination) -Force | Out-Null
            Copy-Item -LiteralPath $source -Destination $destination -Recurse
        }

        if ($platform.StartsWith('windows-')) {
            $zipPath = Join-Path $outputRoot ($packageName + '.zip')
            [System.IO.Compression.ZipFile]::CreateFromDirectory(
                $packagePath, $zipPath, [System.IO.Compression.CompressionLevel]::Optimal, $true
            )
            $archives += $zipPath
            Write-Host "Created $zipPath"
        } else {
            $archives += New-LinuxReleasePackages -Source $packagePath -Platform $platform -Version ($Version -replace '_GitHub$', '') -Maintainer $Maintainer -NfpmCommand $nfpmCommand
        }
    }

    # Package source from an explicit allowlist; never include live data, credentials, or build caches.
    $sourcePaths = @('.gitignore', 'BUILD.md', 'LICENSE.txt', 'NOTICE.txt', 'README.md', 'build.ps1', 'go.mod', 'index.html', 'backend/lua', 'backend/data/.gitkeep', 'cmd', 'config', 'deploy', 'frontend', 'internal', 'tests')
    foreach ($relativePath in $sourcePaths) {
        $source = Join-Path $projectRoot $relativePath
        $destination = Join-Path $sourcePackagePath $relativePath
        New-Item -ItemType Directory -Path (Split-Path -Parent $destination) -Force | Out-Null
        Copy-Item -LiteralPath $source -Destination $destination -Recurse
    }
    [System.IO.Compression.ZipFile]::CreateFromDirectory(
        $sourcePackagePath, $sourceZipPath, [System.IO.Compression.CompressionLevel]::Optimal, $true
    )
    $archives += $sourceZipPath
    Write-Host "Created $sourceZipPath"

    $hashLines = foreach ($archive in ($archives | Sort-Object)) {
        $hash = (Get-FileHash -LiteralPath $archive -Algorithm SHA256).Hash.ToLowerInvariant()
        $hash + '  ' + [System.IO.Path]::GetFileName($archive)
    }
    $checksumName = if ($targets.Count -eq 7) { 'SHA256SUMS.txt' } else { 'SHA256SUMS-' + ($targets -join '_') + '.txt' }
    $hashLines | Set-Content -LiteralPath (Join-Path $outputRoot $checksumName) -Encoding ASCII
    Write-Host "Release output: $outputRoot"
}
finally {
    foreach ($name in $envNames) {
        [System.Environment]::SetEnvironmentVariable($name, $previousEnv[$name], 'Process')
    }
    Pop-Location
}
