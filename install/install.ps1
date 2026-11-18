# Installs the SkillMaster CLI on Windows.
#
# **This file starts with a UTF-8 BOM, and it has to.** Windows PowerShell 5.1 decodes a BOM-less
# script with the system ANSI codepage — GBK on a Chinese Windows — so the messages below arrive
# as mojibake and a stray byte can break the parse. PowerShell 7 and the CI parser assume UTF-8
# and will not notice if the BOM is removed; `shell.yml` checks the first three bytes for that
# reason. Do not strip it, and do not "fix" the file by saving it as ANSI.
#
# The artifact names, the checksum file and this script's behaviour are specified in
# skillmaster-cli/RELEASE.md §7; that document is the authority, this file is the implementation.
#
# **Picking the package is deterministic work, so it is done here rather than by a person or a
# model.** An architecture this script does not recognise is a refusal, not a guess: 32-bit Windows
# (`x86`) cannot run an amd64 binary, and Windows on ARM gets a real arm64 package, so there is
# nothing left for a fallback to rescue.
#
# Usage:  irm https://dl.skillmasterai.com/install.ps1 | iex
$ErrorActionPreference = 'Stop'

# Windows PowerShell 5.1 on .NET Framework 4.5/4.6 negotiates SSL3|TLS1.0 by default, and the
# download host speaks TLS 1.2 and 1.3 only — the first request would die as a generic
# "underlying connection was closed". Harmless on PowerShell 7, where this is already on.
try {
    [Net.ServicePointManager]::SecurityProtocol = [Net.ServicePointManager]::SecurityProtocol -bor [Net.SecurityProtocolType]::Tls12
} catch { }

$BaseUrl = if ($env:SKILLMASTER_DL_URL) { $env:SKILLMASTER_DL_URL } else { 'https://dl.skillmasterai.com' }
$InstallDir = if ($env:SKILLMASTER_INSTALL_DIR) { $env:SKILLMASTER_INSTALL_DIR } else { Join-Path $env:LOCALAPPDATA 'SkillMaster\bin' }

function Say([string]$Message) { Write-Host $Message }
function Die([string]$Message) { Write-Error "install: $Message"; exit 1 }

# --- 1. Pick the package -----------------------------------------------------

# **`PROCESSOR_ARCHITEW6432` first.** A 32-bit PowerShell on 64-bit Windows reports
# `PROCESSOR_ARCHITECTURE=x86` and puts the real machine type in the WOW64 variable; reading only
# the first would refuse a machine that has a package it can run.
$machine = if ($env:PROCESSOR_ARCHITEW6432) { $env:PROCESSOR_ARCHITEW6432 } else { $env:PROCESSOR_ARCHITECTURE }

$arch = switch ($machine) {
    'ARM64' { 'arm64' }
    'AMD64' { 'amd64' }
    default {
        Die "认不出架构「$machine」，没有为它构建的包（32 位 Windows 跑不了 64 位程序）。产物清单与手工安装步骤见 $($BaseUrl.TrimEnd('/'))/skillmaster.md"
    }
}

$artifact = "skillmaster_windows_$arch.zip"

# --- 2. Fetch the package and the checksums ---------------------------------

$tmp = Join-Path ([System.IO.Path]::GetTempPath()) ("skillmaster-install-" + [System.Guid]::NewGuid().ToString('N'))
New-Item -ItemType Directory -Path $tmp | Out-Null
$staged = $null

try {
    Say "下载 $BaseUrl/$artifact"
    try {
        Invoke-WebRequest -Uri "$BaseUrl/$artifact" -OutFile (Join-Path $tmp $artifact) -UseBasicParsing
    } catch {
        Die "下载失败：$BaseUrl/$artifact（网络，或这个平台暂时没有产物）"
    }
    # **A caller that already verified the manifest passes it in.** The page verifies a signed
    # `checksums.txt` and then runs this script; without this, the script would download a fresh
    # copy from the same origin and the signed one would protect nothing.
    $sums = $env:SKILLMASTER_CHECKSUMS
    if ($sums) {
        if (-not (Test-Path $sums)) { Die "SKILLMASTER_CHECKSUMS 指向的文件不存在：$sums" }
    } else {
        # Its own message: a missing checksums.txt says nothing about whether this platform has a
        # package, and naming the package sends the reader after the wrong thing.
        $sums = Join-Path $tmp 'checksums.txt'
        try {
            Invoke-WebRequest -Uri "$BaseUrl/checksums.txt" -OutFile $sums -UseBasicParsing
        } catch {
            Die "下载失败：$BaseUrl/checksums.txt（包下载成功了，缺的是校验和文件）"
        }
    }

    # --- 3. Check the checksum -----------------------------------------------

    $line = Select-String -Path $sums -Pattern "([0-9a-f]{64})\s+$([regex]::Escape($artifact))$" |
        Select-Object -First 1
    if (-not $line) { Die "checksums.txt 里没有 $artifact 这一行" }
    $want = $line.Matches[0].Groups[1].Value

    $got = (Get-FileHash -Algorithm SHA256 -Path (Join-Path $tmp $artifact)).Hash.ToLowerInvariant()
    if ($want -ne $got) { Die "校验和不符：$artifact（期望 $want，实际 $got）——已停止，没有安装" }
    Say "校验和正确"

    # --- 4. Unpack and install ------------------------------------------------

    try { Expand-Archive -Path (Join-Path $tmp $artifact) -DestinationPath $tmp -Force -ErrorAction Stop }
    catch { Die "解包失败：$artifact" }
    # Named failures, so the installer reports a cause the install page can describe instead of
    # a raw PowerShell exception.
    try { New-Item -ItemType Directory -Path $InstallDir -Force -ErrorAction Stop | Out-Null }
    catch { Die "建不了安装目录：$InstallDir" }
    # The lines printed below are commands the user or an agent will paste; a relative override
    # would stop meaning the same thing after a `Set-Location`. Resolve with PowerShell's own
    # provider, not `[IO.Path]::GetFullPath`: the latter uses the process directory (which
    # `Set-Location` need not update) and treats `~` as a literal character.
    $InstallDir = $ExecutionContext.SessionState.Path.GetUnresolvedProviderPathFromPSPath($InstallDir)

    # **Write the executable in place first.** A sandbox can allow writing a file and still refuse
    # to rename over an existing one; this installer is often run by an agent (on Windows the
    # sandbox story is less tested, but the shape is the same). No skmr.exe is running while this
    # copy happens — the `--version` probe runs after it.
    # In-place copy is not atomic: killing it midway can leave a truncated skmr.exe with no backup,
    # so the failure message below names the install command as the way to redo it.
    # The staged + Replace/Move form is a fallback for a failed copy (a running exe cannot be
    # replaced this way either — `skmr update` has a helper for that case, this installer does not).
    # Staged inside the install directory, not in %TEMP%: `File.Replace` wraps ReplaceFile, which
    # fails with ERROR_NOT_SAME_DEVICE across volumes.
    # **The archive's entry is `skmr.exe`** (ADR 0113 决定 2), so this is a plain copy.
    $target = Join-Path $InstallDir 'skmr.exe'
    try {
        Copy-Item -Path (Join-Path $tmp 'skmr.exe') -Destination $target -Force -ErrorAction Stop
    } catch {
        $staged = Join-Path $InstallDir ('skmr.exe.new.' + [System.Guid]::NewGuid().ToString('N'))
        try { Copy-Item -Path (Join-Path $tmp 'skmr.exe') -Destination $staged -Force -ErrorAction Stop }
        catch { Die "复制失败：$InstallDir（如果这次执行被沙箱拒绝，请在你自己的终端里重新运行这条命令）" }
        try {
            if (Test-Path $target) {
                [System.IO.File]::Replace($staged, $target, $null)
            } else {
                [System.IO.File]::Move($staged, $target)
            }
        } catch {
            Die "写不进 $target，也改不了名：如果这次执行被沙箱拒绝，请在你自己的终端里重新运行这条命令"
        }
    }

    # --- 5. Say what to do next -----------------------------------------------

    $userPath = [Environment]::GetEnvironmentVariable('Path', 'User')
    if (($userPath -split ';') -notcontains $InstallDir) {
        $newPath = if ([string]::IsNullOrEmpty($userPath)) { $InstallDir } else { "$userPath;$InstallDir" }
        [Environment]::SetEnvironmentVariable('Path', $newPath, 'User')
        $env:Path = "$env:Path;$InstallDir"
        Say ""
        Say "已把 $InstallDir 加进用户 PATH——**新开**一个终端它才会生效。"
    }

    Say ""
    Say "已安装到 $target"
    Say ""

    & $target --version
    if ($LASTEXITCODE -ne 0) { Die "装好了但跑不起来：$target --version" }

    Say ""
    Say '客户端默认最多每 24 小时检查一次更新，下载的更新包按 checksums.txt 校验。'
    Say '不想自动更新：在 PowerShell profile 里设 $env:SKILLMASTER_AUTO_UPDATE=0。'
    Say ''
    Say '可选：如果想让 SkillMaster 在这些 AI 工具里少弹窗，先看会改什么：'
    Say ('  & "' + $target + '" doctor --fix --dry-run')
    Say ('确认后再运行 & "' + $target + '" doctor --fix；是否运行由你决定，这一步不会自动执行。')
    Say '然后确认登录：'
    Say ('  & "' + $target + '" auth status')
    Say '已登录后，新开一个会话，让 Agent 带你走一遍：'
    Say '  把 /skill-name 当入口的 Agent（如 Claude Code）：/skillmaster 带我走一遍 SkillMaster 的新人引导'
    Say ('  其他 Agent：& "' + $target + '" startup')
    Say '云端 / 远程 Agent（每开一个会话就是新环境）不在这一版的安装范围：请换一台本地电脑或本机 Agent。'
} finally {
    # The staged copy lives in the install directory (it has to, for the rename to be atomic), so it
    # is not covered by removing $tmp: a failed replace would otherwise leave it beside the old
    # binary for good. It is only this run's own copy — the name carries a GUID.
    if ($staged) { Remove-Item -Force -ErrorAction SilentlyContinue $staged }
    Remove-Item -Recurse -Force -ErrorAction SilentlyContinue $tmp
}
