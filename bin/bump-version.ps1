# bump-version.ps1 — 推进 semver 版本号（v0.x.y）
#
# 用法: pwsh bin/bump-version.ps1 <子命令> [参数]
#
# 子命令:
#   current                     显示当前版本（最近 tag，无 tag 时 v0.0.0）
#   list                        列已有 tag（按版本号倒序）
#   bump <major|minor|patch>    默认只本地打 tag；加 --push 才推 origin（--yes 跳过交互）
#   check                       验证 working tree 干净
#   help                        显示本帮助
#
# 跨 PowerShell 兼容: PS 5.1 (Windows 默认) / PS 7+ (跨平台 Core)
# 用 git 命令做实质工作（git 跨 OS），脚本只做解析 + 确认。
#
# 与 bin/bump-version.sh 语义对齐——两边 CLI 一致。
# 版本号唯一源是 git tag——bump 后人工跑；归档 ≠ bump。
# bump 类型推断不放在脚本里——slash command .claude/commands/bump-version.md 让 Claude
# 自己读 commits 推断，脚本负责打 tag（默认）+ 可选 push（--push）。
# 保守默认（2026-09-30 改）：脚本不主动 push——push 由人显式 --push 决定。
param(
    [Parameter(Position=0)]
    [string]$Subcommand = "help",

    [Parameter(Position=1)]
    [string]$Type,

    [switch]$Yes,

    [switch]$Push
)

$ErrorActionPreference = "Stop"

# 编码设置：确保中文输出在 PS 5.1/7+ 都正确显示
[Console]::OutputEncoding = [System.Text.Encoding]::UTF8
$OutputEncoding = [System.Text.Encoding]::UTF8

function Show-Usage {
    @"
用法: bump-version.ps1 <子命令> [参数]

子命令:
    current                                  显示当前版本
    list                                     列已有 tag
    bump <major|minor|patch> [-Yes] [-Push]  默认本地打 tag；-Push 同步推 commits + tag
    check                                    验证 working tree 干净
    help                                     显示本帮助

示例:
    pwsh bin/bump-version.ps1 current
    pwsh bin/bump-version.ps1 list
    pwsh bin/bump-version.ps1 bump minor            # 仅本地打 tag
    pwsh bin/bump-version.ps1 bump minor -Push      # 打 tag + atomic push
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
    param([string]$BumpType, [bool]$SkipConfirm, [bool]$DoPush)

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

    # 永远先打本地 tag
    git tag $newTag
    if ($LASTEXITCODE -ne 0) { exit 1 }

    if ($DoPush) {
        # --push opt-in：atomic push（commits → tag，tag 失败 rollback）
        $currentBranch = git branch --show-current 2>$null
        if ($LASTEXITCODE -ne 0 -or [string]::IsNullOrWhiteSpace($currentBranch)) {
            Write-Host "✗ 无法获取当前分支——tag 已打本地但 push 中止" -ForegroundColor Red
            Write-Host "  请手动 'git push origin <branch> $newTag' 完成发布" -ForegroundColor Red
            exit 1
        }

        git push origin $currentBranch
        if ($LASTEXITCODE -ne 0) {
            Write-Host "✗ commits push 失败——tag 已打本地但未推" -ForegroundColor Red
            Write-Host "  请手动 'git push origin $currentBranch $newTag' 完成发布" -ForegroundColor Red
            exit 1
        }

        git push origin $newTag
        if ($LASTEXITCODE -ne 0) {
            # rollback 本地 tag——commits 已在 origin，留给用户手动重试
            git tag -d $newTag 2>$null
            Write-Host "✗ tag push 失败——已 rollback 本地 tag $newTag" -ForegroundColor Red
            Write-Host "  commits 已在 origin，可手动 'git push origin $newTag' 重试" -ForegroundColor Red
            exit 1
        }

        Write-Host "✓ 已打 tag $newTag + 推 commits + tag 到 origin" -ForegroundColor Green
    } else {
        # 默认：本地 bump，不推送（保守）
        $currentBranch = git branch --show-current 2>$null
        if ($LASTEXITCODE -ne 0) { $currentBranch = "<unknown>" }
        Write-Host "✓ 已打本地 tag $newTag（未推送）" -ForegroundColor Green
        Write-Host "  推送:"
        Write-Host "    git push origin $currentBranch $newTag"
        Write-Host "    或（git ≥1.8.3，自动推 commits + 关联 tag）："
        Write-Host "    git push origin $currentBranch --follow-tags"
    }
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
        Invoke-Bump -BumpType $Type -SkipConfirm ([bool]$Yes) -DoPush ([bool]$Push)
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