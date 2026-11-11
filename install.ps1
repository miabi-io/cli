# Usage: irm https://raw.githubusercontent.com/miabi-io/cli/main/install.ps1 | iex   (env: MIABI_CLI_VERSION, MIABI_INSTALL_DIR)

function Install-MiabiCli {
    $ErrorActionPreference = 'Stop'
    # Windows PowerShell 5.1 redraws the progress bar per chunk, making downloads many times slower.
    $ProgressPreference = 'SilentlyContinue'
    # Windows PowerShell 5.1 may default to TLS 1.0, which GitHub refuses.
    [Net.ServicePointManager]::SecurityProtocol = [Net.ServicePointManager]::SecurityProtocol -bor [Net.SecurityProtocolType]::Tls12

    $arch = Get-MiabiArch
    $version = Get-MiabiVersion
    $dir = $env:MIABI_INSTALL_DIR
    if (-not $dir) { $dir = Join-Path $env:LOCALAPPDATA 'Programs\miabi' }

    $archive = "miabi_${version}_windows_${arch}.zip"
    $base = "https://github.com/miabi-io/cli/releases/download/v$version"
    $tmp = Join-Path ([IO.Path]::GetTempPath()) ("miabi-" + [guid]::NewGuid())
    New-Item -ItemType Directory -Path $tmp | Out-Null
    try {
        Write-Host "Downloading the miabi CLI $version (windows/$arch)"
        $zip = Join-Path $tmp $archive
        Invoke-WebRequest -UseBasicParsing -Uri "$base/$archive" -OutFile $zip
        Invoke-WebRequest -UseBasicParsing -Uri "$base/checksums.txt" -OutFile (Join-Path $tmp 'checksums.txt')
        Assert-MiabiChecksum -File $zip -Name $archive -Sums (Join-Path $tmp 'checksums.txt')

        Expand-Archive -Path $zip -DestinationPath (Join-Path $tmp 'x') -Force
        New-Item -ItemType Directory -Path $dir -Force | Out-Null
        $exe = Join-Path $dir 'miabi.exe'
        try {
            Copy-Item -Path (Join-Path $tmp 'x\miabi.exe') -Destination $exe -Force
        } catch {
            throw "could not write $exe; close any running miabi and try again ($($_.Exception.Message))"
        }
    } finally {
        Remove-Item -Recurse -Force $tmp -ErrorAction SilentlyContinue
    }

    Add-MiabiToUserPath -Dir $dir
    Write-Host ""
    & $exe --version
    Write-Host "Installed to $exe"
    Write-Host "Open a new terminal if 'miabi' is not found, then: miabi login --server https://miabi.example.com"
}

function Get-MiabiArch {
    # A 32-bit PowerShell on 64-bit Windows reports x86 here and the real architecture in PROCESSOR_ARCHITEW6432.
    $a = $env:PROCESSOR_ARCHITEW6432
    if (-not $a) { $a = $env:PROCESSOR_ARCHITECTURE }
    switch ($a) {
        'AMD64' { return 'amd64' }
        'ARM64' { return 'arm64' }
        default { throw "unsupported architecture '$a': the miabi CLI ships for amd64 and arm64" }
    }
}

function Get-MiabiVersion {
    if ($env:MIABI_CLI_VERSION) { return $env:MIABI_CLI_VERSION.TrimStart('v') }
    try {
        $index = Invoke-RestMethod -UseBasicParsing -Uri 'https://versions.miabi.io/v1/index.json'
        $v = $index.components.cli.stable.version
        if ($v) { return $v.TrimStart('v') }
    } catch { }
    $latest = Invoke-RestMethod -UseBasicParsing -Uri 'https://api.github.com/repos/miabi-io/cli/releases/latest'
    return $latest.tag_name.TrimStart('v')
}

function Assert-MiabiChecksum {
    param([string]$File, [string]$Name, [string]$Sums)
    $line = Get-Content $Sums | Where-Object { ($_ -split '\s+')[1] -eq $Name } | Select-Object -First 1
    if (-not $line) { throw "checksums.txt has no entry for $Name" }
    $want = ($line -split '\s+')[0].ToLower()
    $got = (Get-FileHash -Algorithm SHA256 -Path $File).Hash.ToLower()
    if ($got -ne $want) { throw "checksum mismatch for ${Name}: got $got, want $want" }
}

function Add-MiabiToUserPath {
    param([string]$Dir)
    $key = 'HKCU:\Environment'
    # Read unexpanded so entries like %USERPROFILE%\bin stay intact when written back.
    $raw = (Get-Item -Path $key).GetValue('Path', '', 'DoNotExpandEnvironmentNames')
    $entries = @($raw -split ';' | Where-Object { $_ })
    if ($entries | Where-Object { $_.TrimEnd('\') -ieq $Dir.TrimEnd('\') }) { return }
    Set-ItemProperty -Path $key -Name 'Path' -Type ExpandString -Value (($entries + $Dir) -join ';')
    # Setting any user variable broadcasts WM_SETTINGCHANGE, so new terminals pick up the PATH.
    $probe = 'MIABI_PATH_REFRESH'
    [Environment]::SetEnvironmentVariable($probe, '1', 'User')
    [Environment]::SetEnvironmentVariable($probe, $null, 'User')
    $env:Path = "$env:Path;$Dir"
    Write-Host "Added $Dir to your user PATH"
}

Install-MiabiCli
