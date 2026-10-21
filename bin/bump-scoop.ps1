# bump-scoop.ps1 — 算出 scoop manifest 需要的 Windows zip SHA256，并输出 manifest JSON
#
# 用法: pwsh bin/bump-scoop.ps1 <version>
#
# 为什么需要 PowerShell 版：
#   scoop 用户就是 Windows 用户。bin/bump-version.ps1 已有 PowerShell 版（打 tag），
#   但发布流程的另一半（更新 scoop manifest）只有 bash 版（build.sh bump-scoop），
#   而它依赖两个 Windows 上默认没有的东西：
#     - `zip` 命令（Git Bash 默认不带）
#     - `python3`（Windows 自带的 python3.exe 是 Microsoft Store 的 App
#       Execution Alias，会弹商店，不是真 Python）
#   本脚本只用 PowerShell 内置能力（Invoke-RestMethod）+ git，**不需要** Git Bash /
#   python3 / gh。
#
# hash 从哪来：
#   GitHub release body 里的 marker
#     <!-- sha256:cc-launcher-windows-amd64.zip:HEX -->
#   由 release workflow 的 "append windows zip SHA256 to release body" 步骤写入。
#   **不是**本地 zip 的 hash —— Go build 的产物含 GOMODCACHE state，fresh CI 与
#   本地 cache 永远不 byte-level 一致（spec build-scoop Requirement 明确规定
#   "本地 zip SHA256 MUST NOT be written to bucket/cc-launcher.json"）。
#   所以本脚本**不打本地 zip**（那是 CI 的职责），只取 release 的真值。
#
# 认证：
#   GitHub REST API 未认证限流 60 req/hr，共享出口 IP 很容易被打满 →
#   设 GITHUB_TOKEN（或 GH_TOKEN）环境变量 → 额度 5000/hr。
#   未认证时若遇到 rate limit，脚本会明确报出来。
#
# 跨 PowerShell 兼容: PS 5.1 (Windows 默认) / PS 7+
#
# 与 bin/bump-scoop.sh（build.sh bump-scoop）语义对齐——两边输出的 manifest JSON
# 完全一致（都用 here-string 拼，避免 ConvertTo-Json 的转义/顺序差异）。

param(
    [Parameter(Position=0)]
    [string]$Version = "",

    [int]$Retries = 3
)

$ErrorActionPreference = "Stop"

$Repo = "wzx-xle/cc-launcher"
$AssetName = "cc-launcher-windows-amd64.zip"

function Show-Usage {
    Write-Host "用法: pwsh bin/bump-scoop.ps1 <version>"
    Write-Host ""
    Write-Host "示例: pwsh bin/bump-scoop.ps1 v0.14.0"
    Write-Host ""
    Write-Host "前置条件:"
    Write-Host "  1. v* tag 已推到 $Repo"
    Write-Host "  2. release workflow 跑完（release body 里已有 sha256 marker）"
    Write-Host ""
    Write-Host "可选: 设 GITHUB_TOKEN 环境变量 → API 额度 60/hr → 5000/hr"
}

function Get-Release {
    param([string]$Tag)

    $uri = "https://api.github.com/repos/$Repo/releases/tags/$Tag"
    $headers = @{
        "Accept"     = "application/vnd.github+json"
        "User-Agent" = "cc-launcher-bump-scoop.ps1"
    }
    $token = $env:GITHUB_TOKEN
    if (-not $token) { $token = $env:GH_TOKEN }
    if ($token) {
        $headers["Authorization"] = "Bearer $token"
    }

    $resp = Invoke-RestMethod -Uri $uri -Headers $headers -Method Get

    # API 错误（rate limit / 404）会返回 { message: ... } 而没有 body
    $names = $resp.PSObject.Properties.Name
    if (($names -contains "message") -and -not ($names -contains "body")) {
        throw ("API error: " + $resp.message)
    }
    return $resp
}

function Get-Sha256Marker {
    param([string]$Body)
    if ($Body -and ($Body -match 'sha256:[^:\s]+:([a-f0-9]{64})')) {
        return $Matches[1]
    }
    return $null
}

function Get-VersionFromTag {
    param([string]$Tag)
    if ($Tag -notmatch '^v?(\d+\.\d+\.\d+)') {
        throw ("版本号格式不对: $Tag（期望 vX.Y.Z）")
    }
    return $Matches[1]
}

if (-not $Version) {
    Show-Usage
    exit 2
}

if ($Version -notmatch '^v') {
    [Console]::Error.WriteLine("ERROR: 版本号要带 v 前缀（例: v0.14.0），got: $Version")
    exit 2
}

$release = $null
$hash = $null
$lastError = ""

for ($attempt = 1; $attempt -le $Retries; $attempt++) {
    try {
        $release = Get-Release -Tag $Version
        $lastError = ""
    } catch {
        $lastError = $_.Exception.Message
        [Console]::Error.WriteLine("fetch attempt ${attempt}: $lastError; retry in 5s...")
        Start-Sleep -Seconds 5
        continue
    }

    $hash = Get-Sha256Marker -Body $release.body
    if ($hash) {
        break
    }

    [Console]::Error.WriteLine("fetch attempt ${attempt}: release body 里还没有 sha256 marker（release workflow 的 'append windows zip SHA256' 步骤可能还没跑完）; retry in 5s...")
    Start-Sleep -Seconds 5
}

if (-not $hash) {
    [Console]::Error.WriteLine("")
    [Console]::Error.WriteLine("ERROR: ${Retries} 次重试后仍未拿到 release body 的 sha256 marker")
    if ($lastError) {
        # API 层就失败了（404 / rate limit / 网络）——先报真实原因，别只说"没有 marker"
        [Console]::Error.WriteLine("  0. 最近的 API 错误: $lastError")
    }
    [Console]::Error.WriteLine("可能原因:")
    [Console]::Error.WriteLine("  1. release workflow 的 'append windows zip SHA256 to release body' 步骤还没跑")
    [Console]::Error.WriteLine("  2. GitHub API rate limit exceeded（未认证 60/hr）—— 设 GITHUB_TOKEN 环境变量")
    [Console]::Error.WriteLine("  3. 手动 fallback（要下 18 MB zip 取真值）:")
    [Console]::Error.WriteLine("     curl -sSL https://github.com/$Repo/releases/download/$Version/$AssetName -o x.zip")
    [Console]::Error.WriteLine("     sha256sum x.zip")
    exit 1
}

# 顺带核对 release 上真的挂了 windows zip（marker 存在说明 append 步骤跑过，
# 而 release workflow 里上传步骤排在 append 之前 → 资产理应在；但仍提醒一句）
$assetNames = @()
foreach ($a in $release.assets) { $assetNames += $a.name }
if ($assetNames -notcontains $AssetName) {
    [Console]::Error.WriteLine("WARNING: release 上没有 $AssetName 资产（当前: $($assetNames -join ', ')）")
    [Console]::Error.WriteLine("         marker 存在但资产缺失——检查 release workflow 的 windows-amd64 job")
}

$versionNoV = Get-VersionFromTag -Tag $Version
$base = "https://github.com/$Repo/releases/download/$Version"

# 用 here-string 拼 JSON（不用 ConvertTo-Json：PS 5.1 的转义与键序与 bash 版
# 不一致，会让两平台产出的 manifest 长得不一样）
$manifest = @"
{
    "version": "$versionNoV",
    "description": "Terminal launcher for Claude Code with provider switching and permission mode selection",
    "homepage": "https://github.com/$Repo",
    "license": "MIT",
    "architecture": {
        "64bit": {
            "url": "$base/$AssetName",
            "hash": "$hash",
            "bin": ["ccl.cmd"]
        }
    }
}
"@

# stdout 只留 manifest JSON（可管道给 scoop bucket 文件）；进度信息走 stderr
[Console]::Error.WriteLine("==> release zip (manifest): sha256=$hash")
Write-Output $manifest
