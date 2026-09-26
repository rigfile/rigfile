# Rigfile installer for Windows (PowerShell 5.1+ or 7).
#
#   irm https://github.com/digitaldreamer3462/rigfile/releases/latest/download/install.ps1 | iex
#
# Downloads a release, verifies the minisign signature of SHA256SUMS with the public key below, checks the archive's
# SHA-256 against that signed list, and only then installs rigfile.exe. Nothing is run before verification.
# The public key is written into this file when the release is built; a copy without a key refuses to install.
# Requires minisign (scoop install minisign, or winget install jedisct1.minisign).
#
# Environment: RIGFILE_VERSION (default: latest), RIGFILE_INSTALL_DIR (default: %LOCALAPPDATA%\Programs\rigfile),
#              RIGFILE_BASE_URL (default: the GitHub releases).
#              RIGFILE_INSECURE_SKIP_SIGNATURE=1 skips ONLY the signature check (the SHA-256 check still runs).
$ErrorActionPreference = 'Stop'
$Repo   = '__RIGFILE_REPO__'
$PubKey = '__RIGFILE_PUBKEY__'
$Base   = if ($env:RIGFILE_BASE_URL) { $env:RIGFILE_BASE_URL } else { "https://github.com/$Repo/releases/download" }

function Fail($msg) { Write-Error "rigfile install: $msg"; exit 1 }

if ([string]::IsNullOrEmpty($PubKey) -or $PubKey.StartsWith('__RIGFILE_')) {
    if ($env:RIGFILE_INSECURE_SKIP_SIGNATURE -ne '1') { Fail 'this copy of the installer has no signing key built in; use the install.ps1 attached to a release' }
}
[Net.ServicePointManager]::SecurityProtocol = [Net.ServicePointManager]::SecurityProtocol -bor [Net.SecurityProtocolType]::Tls12

$arch = switch ($env:PROCESSOR_ARCHITECTURE) { 'AMD64' { 'amd64' } 'ARM64' { 'arm64' } default { Fail "unsupported CPU $($env:PROCESSOR_ARCHITECTURE)" } }
$tmp = Join-Path ([IO.Path]::GetTempPath()) ("rigfile-install-" + [Guid]::NewGuid().ToString('N'))
New-Item -ItemType Directory -Path $tmp | Out-Null
try {
    $version = $env:RIGFILE_VERSION
    if (-not $version) {
        $rel = Invoke-RestMethod -Uri "https://api.github.com/repos/$Repo/releases/latest" -UseBasicParsing
        $version = $rel.tag_name
    }
    $version = $version.TrimStart('v')
    if ($version -notmatch '^[0-9A-Za-z._-]+$') { Fail "invalid version '$version'" }
    $tag = "v$version"
    $archive = "rigfile_${version}_windows_$arch.zip"
    Write-Host "Installing rigfile $version for windows/$arch"

    Invoke-WebRequest -Uri "$Base/$tag/SHA256SUMS" -OutFile "$tmp\SHA256SUMS" -UseBasicParsing
    if ($env:RIGFILE_INSECURE_SKIP_SIGNATURE -eq '1') {
        Write-Warning 'skipping the signature check; only the SHA-256 is verified'
    } else {
        if (-not (Get-Command minisign -ErrorAction SilentlyContinue)) { Fail 'minisign is required to verify the download (scoop install minisign | winget install jedisct1.minisign). Or set RIGFILE_INSECURE_SKIP_SIGNATURE=1 to skip only the signature check.' }
        try { Invoke-WebRequest -Uri "$Base/$tag/SHA256SUMS.minisig" -OutFile "$tmp\SHA256SUMS.minisig" -UseBasicParsing } catch { Fail 'the release has no signature (SHA256SUMS.minisig); refusing' }
        $out = & minisign -V -P $PubKey -m "$tmp\SHA256SUMS" -x "$tmp\SHA256SUMS.minisig" 2>&1 | Out-String
        if ($LASTEXITCODE -ne 0) { Fail "signature check FAILED; refusing to install: $out" }
        if ($out -notmatch [Regex]::Escape("rigfile $tag")) { Fail "the signed checksums are not for $tag; refusing" }
        Write-Host 'signature verified'
    }

    $want = $null
    foreach ($line in Get-Content "$tmp\SHA256SUMS") {
        $f = $line -split '\s+'
        if ($f.Count -ge 2 -and $f[1].TrimStart('*') -eq $archive) { $want = $f[0].ToLower() }
    }
    if (-not $want) { Fail "$tag has no build for windows/$arch" }
    Invoke-WebRequest -Uri "$Base/$tag/$archive" -OutFile "$tmp\$archive" -UseBasicParsing
    $got = (Get-FileHash -Algorithm SHA256 "$tmp\$archive").Hash.ToLower()
    if ($got -ne $want) { Fail "checksum mismatch for $archive; refusing" }
    Write-Host 'checksum verified'

    Expand-Archive -Path "$tmp\$archive" -DestinationPath "$tmp\x"
    $bin = Get-ChildItem -Path "$tmp\x" -Recurse -Filter rigfile.exe | Select-Object -First 1
    if (-not $bin) { Fail "$archive does not contain rigfile.exe" }
    $dest = if ($env:RIGFILE_INSTALL_DIR) { $env:RIGFILE_INSTALL_DIR } else { Join-Path $env:LOCALAPPDATA 'Programs\rigfile' }
    New-Item -ItemType Directory -Force -Path $dest | Out-Null
    Copy-Item -Force $bin.FullName (Join-Path $dest 'rigfile.exe')
    Write-Host "installed $dest\rigfile.exe"
    $userPath = [Environment]::GetEnvironmentVariable('Path', 'User')
    if (($userPath -split ';') -notcontains $dest) {
        [Environment]::SetEnvironmentVariable('Path', ($userPath.TrimEnd(';') + ';' + $dest), 'User')
        Write-Host "added $dest to your user PATH (open a new terminal to use it)"
    }
    & (Join-Path $dest 'rigfile.exe') version
} finally {
    Remove-Item -Recurse -Force $tmp -ErrorAction SilentlyContinue
}
