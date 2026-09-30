# bump-version.ps1 — 推进 semver 版本号（v0.x.y）
#
# 用法: pwsh bin/bump-version.ps1 <子命令> [参数]
#
# 子命令:
#   current                     显示当前版本（最近 tag，无 tag 时 v0.0.0）
#   list                        列已有 tag（按版本号倒序）
#   bump <major|minor|patch>    bump 并推 origin（--yes 跳过交互）
#   check                       验证 working tree 干净
#   help                        显示本帮助
#
# 跨 PowerShell 兼容: PS 5.1 (Windows 默认) / PS 7+ (跨平台 Core)
# 用 git 命令做实质工作（git 跨 OS），脚本只做解析 + 确认。
#
# 与 bin/bump-version.sh 语义对齐——两边 CLI 一致。
# 版本号唯一源是 git tag——bump 后人工跑；归档 ≠ bump。
param(
    [Parameter(Position=0)]
    [string]$Subcommand = "help",

    [Parameter(Position=1)]
    [string]$Type,

    [switch]$Yes
)

$ErrorActionPreference = "Stop"

function Show-Usage {
    @"
用法: bump-version.ps1 <子命令> [参数]

子命令:
    current                          显示当前版本
    list                             列已有 tag
    bump <major|minor|patch> [--yes] bump 并推 origin
    check                            验证 working tree 干净
    help                             显示本帮助

示例:
    pwsh bin/bump-version.ps1 current
    pwsh bin/bump-version.ps1 list
    pwsh bin/bump-version.ps1 bump minor
    pwsh bin/bump-version.ps1 bump patch -Yes
"@
}

function Get-LatestTag {
    # 列 tag 按版本号倒序取第一——git --sort 跨 OS 一致
    $tags = git tag -l 'v*.*.*' --sort=-v:refname
    if ($LASTEXITCODE -ne 0) { return $null }
    if ($tags -and $tags.Count -gt 0) { return $tags[0] }
    return $null
}

function Get-Tags {
    git tag -l 'v*.*.*' --sort=-v:refname
}

function Test-CleanWorkingTree {
    # git diff --quiet HEAD — 有未提交修改就 exit 非 0
    git diff --quiet HEAD 2>$null
    $diffOk = $LASTEXITCODE -eq 0

    # git status --porcelain — 列 dirty（含 untracked），空输出 = 干净
    $status = git status --porcelain 2>$null
    $statusEmpty = [string]::IsNullOrWhiteSpace($status)

    if (-not $diffOk) {
        Write-Host "✗ working tree 有未提交修改，请先 commit 或 stash" -ForegroundColor Red
        return $false
    }
    if (-not $statusEmpty) {
        Write-Host "✗ working tree 有未跟踪文件，请先清理或 commit" -ForegroundColor Red
        return $false
    }
    return $true
}

function Invoke-Bump {
    param([string]$BumpType, [bool]$SkipConfirm)

    $current = Get-LatestTag
    if (-not $current) {
        Write-Host "当前无 tag——假设起点 v0.0.0"
        $current = "v0.0.0"
    }

    # 解析 vX.Y.Z-prerelease+meta → 数字段
    $stripped = $current -replace '^v', ''
    $stripped = $stripped -replace '-.*$', ''   # 去掉 -rc1
    $stripped = $stripped -replace '\+.*$', ''   # 去掉 +meta
    $parts = $stripped.Split('.')
    $major = [int]$parts[0]
    $minor = [int]$parts[1]
    $patch = [int]$parts[2]

    switch ($BumpType) {
        "major" { $major++; $minor = 0; $patch = 0 }
        "minor" { $minor++; $patch = 0 }
        "patch" { $patch++ }
        default {
            Write-Host "未知 bump 类型: $BumpType（只支持 major|minor|patch）" -ForegroundColor Red
            exit 1
        }
    }

    $newTag = "v${major}.${minor}.${patch}"
    Write-Host "$current → $newTag"

    if (-not $SkipConfirm) {
        $reply = Read-Host "确认打 tag $newTag? [y/N]"
        if ($reply -notin @('y','Y','yes','YES')) {
            Write-Host "取消"
            exit 1
        }
    }

    if (-not (Test-CleanWorkingTree)) {
        Write-Host "✗ working tree 不干净——拒绝 bump（用 git status 看）" -ForegroundColor Red
        exit 1
    }

    git tag $newTag
    if ($LASTEXITCODE -ne 0) { exit 1 }
    git push origin $newTag
    if ($LASTEXITCODE -ne 0) { exit 1 }
    Write-Host "✓ 已推 $newTag 到 origin" -ForegroundColor Green
}

switch ($Subcommand) {
    "current" {
        $tag = Get-LatestTag
        if ($tag) { Write-Host $tag } else { Write-Host "v0.0.0" }
    }
    "list" { Get-Tags }
    "bump" {
        if (-not $Type) {
            Write-Host "需要 major|minor|patch" -ForegroundColor Red
            exit 1
        }
        Invoke-Bump -BumpType $Type -SkipConfirm ([bool]$Yes)
    }
    "check" {
        if (Test-CleanWorkingTree) {
            Write-Host "✓ working tree 干净" -ForegroundColor Green
        } else {
            exit 1
        }
    }
    { @("help","-h","--help") -contains $_ } { Show-Usage }
    default {
        Write-Host "未知子命令: $Subcommand" -ForegroundColor Red
        Show-Usage
        exit 1
    }
}