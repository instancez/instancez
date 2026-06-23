# instancez installer for Windows (PowerShell 5.1+).
#
#   irm https://get.instancez.ai/windows | iex
#
# Downloads the inz.exe that matches your CPU, checks it against the release
# checksums, installs it under %LOCALAPPDATA%\instancez\bin, and puts that
# directory on your user PATH. Pin a release with $env:INSTANCEZ_VERSION.
$ErrorActionPreference = 'Stop'
$ProgressPreference = 'SilentlyContinue'

$repo = 'instancez/instancez'
$bin = 'inz.exe'
$installDir = if ($env:INSTANCEZ_INSTALL_DIR) { $env:INSTANCEZ_INSTALL_DIR } else { Join-Path $env:LOCALAPPDATA 'instancez\bin' }
$version = $env:INSTANCEZ_VERSION

# Windows on ARM reports the native arch via PROCESSOR_ARCHITEW6432 when the
# host is a 32-bit process; prefer it when set.
$proc = if ($env:PROCESSOR_ARCHITEW6432) { $env:PROCESSOR_ARCHITEW6432 } else { $env:PROCESSOR_ARCHITECTURE }
switch ($proc) {
  'AMD64' { $arch = 'amd64' }
  'ARM64' { $arch = 'arm64' }
  default { throw "unsupported architecture '$proc'. instancez ships amd64 and arm64." }
}

$asset = "inz_windows_$arch.exe"

# Empty version means latest. A pinned version uses the same stable asset name
# under that tag's release, so only the path changes.
if ($version) {
  $base = "https://github.com/$repo/releases/download/v$($version.TrimStart('v'))"
} else {
  $base = "https://github.com/$repo/releases/latest/download"
}

# PowerShell 5.1 defaults to TLS 1.0; GitHub needs 1.2.
[Net.ServicePointManager]::SecurityProtocol = [Net.SecurityProtocolType]::Tls12

$tmp = Join-Path ([System.IO.Path]::GetTempPath()) ("inz-" + [System.Guid]::NewGuid().ToString('N'))
New-Item -ItemType Directory -Path $tmp -Force | Out-Null
try {
  $exePath = Join-Path $tmp $bin
  Write-Host "Downloading $asset..."
  try {
    Invoke-WebRequest -Uri "$base/$asset" -OutFile $exePath -UseBasicParsing
  } catch {
    throw "could not download $base/$asset (is the release published for your platform?)"
  }

  # Verify against the release checksums; only skip if the file is unavailable.
  $sumsPath = Join-Path $tmp 'checksums.txt'
  try {
    Invoke-WebRequest -Uri "$base/checksums.txt" -OutFile $sumsPath -UseBasicParsing
    $line = Select-String -Path $sumsPath -Pattern ("\s" + [regex]::Escape($asset) + "$") | Select-Object -First 1
    if (-not $line) {
      Write-Warning "$asset not listed in checksums.txt, skipping verification"
    } else {
      $expected = ($line.Line -split '\s+')[0]
      $actual = (Get-FileHash -Path $exePath -Algorithm SHA256).Hash.ToLower()
      if ($actual -ne $expected.ToLower()) {
        throw "checksum mismatch for $asset (expected $expected, got $actual)"
      }
    }
  } catch [System.Net.WebException] {
    Write-Warning "could not fetch checksums.txt, skipping verification"
  }

  New-Item -ItemType Directory -Path $installDir -Force | Out-Null
  Move-Item -Path $exePath -Destination (Join-Path $installDir $bin) -Force
  Write-Host "Installed inz to $(Join-Path $installDir $bin)"
} finally {
  Remove-Item -Path $tmp -Recurse -Force -ErrorAction SilentlyContinue
}

# Put the install dir on the user PATH if it isn't already, and on the current
# session so 'inz' works without reopening the terminal.
$userPath = [Environment]::GetEnvironmentVariable('Path', 'User')
if (($userPath -split ';') -notcontains $installDir) {
  $newPath = if ($userPath) { "$userPath;$installDir" } else { $installDir }
  [Environment]::SetEnvironmentVariable('Path', $newPath, 'User')
  Write-Host "Added $installDir to your user PATH (restart other terminals to pick it up)."
}
if (($env:Path -split ';') -notcontains $installDir) {
  $env:Path = "$env:Path;$installDir"
}

Write-Host ''
& (Join-Path $installDir $bin) version
