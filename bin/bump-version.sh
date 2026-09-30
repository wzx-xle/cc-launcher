#!/bin/sh
# bump-version.sh — 推进 semver 版本号（v0.x.y）
#
# 子命令:
#   current                     显示当前版本（最近 tag，无 tag 时 v0.0.0）
#   list                        列已有 tag（按版本号倒序）
#   bump <major|minor|patch>    bump 并推 origin（交互确认 / --yes 跳过）
#   check                       验证 working tree 干净（无未提交修改）
#   help                        用法
#
# 跨 OS 兼容: POSIX sh 写法——macOS / Linux / BSD / Git Bash on Windows 都跑。
# 不依赖 bash 特性（不用 [[ =~ ]] / read -p / <()）。
#
# 设计:
#   - 版本号唯一源是 git tag——不维护内部 version 文件
#   - bump 由 archive 完成后人工触发；归档 ≠ bump
#   - bump 类型推断不放在脚本里（slash command .claude/commands/bump-version.md 让 Claude
#     自己读 commits 推断）+ 脚本仅负责打 tag + 推
#   - pre-release (v0.1.0-rc1) 当前不支持——v0.x 阶段默认 stable
set -eu

PROG=$(basename "$0")
usage() {
    cat <<EOF
用法: $PROG <子命令> [参数]

子命令:
    current                          显示当前版本
    list                             列已有 tag
    bump <major|minor|patch> [--yes] bump 并推 origin
    check                            验证 working tree 干净
    help                             显示本帮助

示例:
    $PROG current
    $PROG list
    $PROG bump minor
    $PROG bump patch --yes
EOF
}

# 列出所有 vX.Y.Z tag（按版本号倒序）
list_tags() {
    git tag -l 'v*.*.*' --sort=-v:refname
}

# 当前最新 tag（无 tag 时返回空）
current_tag() {
    list_tags | head -1
}

# 解析当前 tag 为 major minor patch（空格分隔输出，便于 read 切分）
# 入参: vX.Y.Z 或空（fallback v0.0.0）
parse_current() {
    current="${1:-v0.0.0}"
    if [ -z "$current" ]; then
        current="v0.0.0"
    fi
    # 去掉 v 前缀 + pre-release (-rc1) + build metadata (+meta)
    stripped="${current#v}"
    stripped="${stripped%%-*}"   # 去掉 -rc1
    stripped="${stripped%%+*}"   # 去掉 +meta
    # 用空格分隔三段（POSIX read 按 IFS 切分）；`tr` 跨 OS 兼容
    echo "$stripped" | tr '.' ' '
}

# 验证 working tree 干净——bump 之前不应该有未提交修改（避免 tag 标错状态）
check_clean() {
    if ! git diff --quiet HEAD 2>/dev/null; then
        echo "✗ working tree 有未提交修改，请先 commit 或 stash" >&2
        return 1
    fi
    if [ -n "$(git status --porcelain 2>/dev/null)" ]; then
        echo "✗ working tree 有未跟踪文件，请先清理或 commit" >&2
        return 1
    fi
    echo "✓ working tree 干净"
}

# bump <type> --<yes>
do_bump() {
    bump_type="$1"
    skip_confirm="${2:-false}"

    current=$(current_tag)
    if [ -z "$current" ]; then
        echo "当前无 tag——假设起点 v0.0.0"
        current="v0.0.0"
    fi

    # 解析当前 major.minor.patch 到临时文件（POSIX 无数组）
    parse_current "$current" > /tmp/.bump-ver-$$
    read -r major minor patch < /tmp/.bump-ver-$$
    rm -f /tmp/.bump-ver-$$

    case "$bump_type" in
        major) major=$((major + 1)); minor=0; patch=0 ;;
        minor) minor=$((minor + 1)); patch=0 ;;
        patch) patch=$((patch + 1)) ;;
        *)
            echo "未知 bump 类型: $bump_type（只支持 major|minor|patch）" >&2
            return 1
            ;;
    esac

    new_tag="v${major}.${minor}.${patch}"
    echo "$current → $new_tag"

    if [ "$skip_confirm" != "true" ]; then
        printf "%s" "确认打 tag $new_tag? [y/N] "
        read -r reply
        case "$reply" in
            y|Y|yes|YES) ;;
            *) echo "取消"; return 1 ;;
        esac
    fi

    if ! check_clean >/dev/null 2>&1; then
        echo "✗ working tree 不干净——拒绝 bump（用 git status 看）" >&2
        return 1
    fi

    git tag "$new_tag"
    git push origin "$new_tag"
    echo "✓ 已推 $new_tag 到 origin"
}

# 主入口
subcmd="${1:-help}"
shift || true

case "$subcmd" in
    current) current_tag ;;
    bump)
        type="${1:?需要 major|minor|patch}"
        shift || true
        skip="false"
        if [ "${1:-}" = "--yes" ]; then
            skip="true"
        fi
        do_bump "$type" "$skip"
        ;;
    check) check_clean ;;
    list) list_tags ;;
    help|-h|--help) usage ;;
    *)
        echo "未知子命令: $subcmd" >&2
        usage >&2
        exit 1
        ;;
esac