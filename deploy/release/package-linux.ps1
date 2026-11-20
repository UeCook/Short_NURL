#requires -Version 7.4

function Write-LinuxTarGz {
    param([string]$Source, [string]$Destination, [DateTimeOffset]$Timestamp)
    Add-Type -AssemblyName System.Formats.Tar
    $parent = Split-Path -Parent $Source
    $items = @((Get-Item -LiteralPath $Source)) + @(Get-ChildItem -LiteralPath $Source -Recurse -Force | Sort-Object FullName)
    $stream = [System.IO.File]::Open($Destination, [System.IO.FileMode]::CreateNew)
    $gzip = [System.IO.Compression.GZipStream]::new($stream, [System.IO.Compression.CompressionLevel]::Optimal, $true)
    $writer = [System.Formats.Tar.TarWriter]::new($gzip, [System.Formats.Tar.TarEntryFormat]::Pax, $true)
    try {
        foreach ($item in $items) {
            if ($item.Attributes -band [System.IO.FileAttributes]::ReparsePoint) {
                throw "Refusing to archive a link or reparse point: $($item.FullName)"
            }
            $name = [System.IO.Path]::GetRelativePath($parent, $item.FullName).Replace('\', '/')
            $relative = [System.IO.Path]::GetRelativePath($Source, $item.FullName).Replace('\', '/')
            $kind = if ($item.PSIsContainer) { [System.Formats.Tar.TarEntryType]::Directory } else { [System.Formats.Tar.TarEntryType]::RegularFile }
            if ($item.PSIsContainer) { $name = $name.TrimEnd('/') + '/' }
            $entry = [System.Formats.Tar.PaxTarEntry]::new($kind, $name)
            # 493 = 0755; 420 = 0644. Supply Unix executable bits explicitly on Windows.
            $entry.Mode = [System.IO.UnixFileMode]$(if ($item.PSIsContainer -or $relative -in @('bin/nurl-server', 'bin/nurl', 'bin/nurl-key')) { 493 } else { 420 })
            $entry.Uid = 0
            $entry.Gid = 0
            $entry.UserName = 'root'
            $entry.GroupName = 'root'
            $entry.ModificationTime = $Timestamp
            if ($item.PSIsContainer) {
                $writer.WriteEntry($entry)
            } else {
                $data = [System.IO.File]::OpenRead($item.FullName)
                try {
                    $entry.DataStream = $data
                    $writer.WriteEntry($entry)
                } finally {
                    $data.Dispose()
                }
            }
        }
    } finally {
        $writer.Dispose()
        $gzip.Dispose()
        $stream.Dispose()
    }
}

function New-LinuxReleasePackages {
    param(
        [string]$Source,
        [string]$Platform,
        [string]$Version,
        [string]$Maintainer,
        [string]$NfpmCommand
    )
    $architecture = switch ($Platform) {
        'linux-386' { '386' }
        'linux-amd64' { 'amd64' }
        'linux-armv7' { 'arm7' }
        'linux-arm64' { 'arm64' }
        default { throw "Unsupported Linux target: $Platform" }
    }
    $timestamp = if ([string]::IsNullOrWhiteSpace($env:SOURCE_DATE_EPOCH)) {
        [DateTimeOffset]::FromUnixTimeSeconds([DateTimeOffset]::UtcNow.ToUnixTimeSeconds())
    } else {
        [DateTimeOffset]::FromUnixTimeSeconds([long]$env:SOURCE_DATE_EPOCH)
    }
    $tarPath = $Source + '.tar.gz'
    Write-LinuxTarGz -Source $Source -Destination $tarPath -Timestamp $timestamp
    Write-Host "Created $tarPath"
    $contents = @(
        @{
            dst = '/opt/short-nurl'
            type = 'dir'
            file_info = @{ mode = 493; owner = 'root'; group = 'root'; mtime = $timestamp.ToString('o') }
        }
    )
    foreach ($item in (Get-ChildItem -LiteralPath $Source -Recurse -Force | Sort-Object FullName)) {
        if ($item.Attributes -band [System.IO.FileAttributes]::ReparsePoint) {
            throw "Refusing to package a link or reparse point: $($item.FullName)"
        }
        $relative = [System.IO.Path]::GetRelativePath($Source, $item.FullName).Replace('\', '/')
        $entry = @{
            dst = '/opt/short-nurl/' + $relative
            file_info = @{
                mode = if ($item.PSIsContainer -or $relative -in @('bin/nurl-server', 'bin/nurl', 'bin/nurl-key')) { 493 } else { 420 }
                owner = 'root'
                group = 'root'
                mtime = $timestamp.ToString('o')
            }
        }
        if ($item.PSIsContainer) {
            $entry.type = 'dir'
        } else {
            $entry.src = $item.FullName
            if ($relative -eq 'config/config.json') { $entry.type = 'config|noreplace' }
        }
        $contents += $entry
    }
    $configuration = @{
        name = 'short-nurl'
        arch = $architecture
        platform = 'linux'
        version = $Version
        release = '1'
        section = 'web'
        priority = 'optional'
        maintainer = $Maintainer
        vendor = 'Short_NURL'
        homepage = 'https://github.com/UeCook/Short_NURL'
        license = 'Apache-2.0 AND MIT'
        description = 'Self-hosted URL shortener: Go management API and CLI, Lua backend and web UI. Runtime tree under /opt/short-nurl; configure OpenResty separately.'
        contents = $contents
        deb = @{ compression = 'gzip' }
        rpm = @{ compression = 'gzip'; group = 'Applications/Internet'; summary = 'Self-hosted URL shortener' }
    }
    $configurationPath = Join-Path ([System.IO.Path]::GetTempPath()) ('short-nurl-nfpm-' + [guid]::NewGuid().ToString('N') + '.json')
    $result = @($tarPath)
    try {
        $configuration | ConvertTo-Json -Depth 12 | Set-Content -LiteralPath $configurationPath -Encoding utf8NoBOM
        foreach ($format in @('deb', 'rpm')) {
            $destination = $Source + '.' + $format
            & $NfpmCommand package --config $configurationPath --packager $format --target $destination | ForEach-Object { Write-Host $_ }
            if ($LASTEXITCODE -ne 0) { throw "Packaging failed: $Platform/$format" }
            $result += $destination
            Write-Host "Created $destination"
        }
    } finally {
        if (Test-Path -LiteralPath $configurationPath) {
            Remove-Item -LiteralPath $configurationPath -Force
        }
    }
    return $result
}
