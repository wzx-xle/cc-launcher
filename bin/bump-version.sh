#!/usr/bin/env bash
# bump-version.sh — 用 git tag 推进 semver 版本号（v0.x.y 形式）
#
# 用法:
#   bump-version.sh <major|minor|patch>          # 交互确认后推 origin
#   bump-version.sh --list                       # 列已有 tag（按版本号倒序）
#   bump-version.sh <type> --yes                 # 跳过交互确认（脚本调用）
#
# 设计意图:
#   - 版本号唯一源是 git tag——不维护 internal/version.go 等二次源
#   - bump 由 archive 完成后人工触发；归档 ≠ bump（archive 是 OpenSpec 节奏，bump 是 release 节奏）
#   - pre-release 标签 (v0.1.0-rc1) 当前不支持——v0.x 阶段默认 stable
set -euo pipefail

# 当前最新 tag（按版本号语义排序）；无 tag 时 fallback 到 v0.0.0
current_tag() {
    git tag -l 'v*.*.*' --sort=-v:refname | head -1 || true
}

# 列已有 tag（debug 用）
list_tags() {
    git tag -l 'v*.*.*' --sort=-v:refname
}

# 解析当前 tag 为 major / minor / patch，输出空格分隔
parse_current() {
    local current="${1:-v0.0.0}"
    # 兜底: 无 tag 时 current 为空，用 v0.0.0
    if [ -z "$current" ]; then
        echo "0 0 0"
        return
    fi
    local stripped="${current#v}"
    # 取前 3 段 (major.minor.patch)，忽略 pre-release / build metadata
    local mm="${stripped%%-*}"   # 去掉 -rc1 等
    local mp="${mm%%+*}"          # 去掉 +build
    echo "${mp//./ }"
}

if [ "${1:-}" = "--list" ]; then
    list_tags
    exit 0
fi

bump="${1:?需要参数: major|minor|patch [--yes]}"
skip_confirm=false
[ "${2:-}" = "--yes" ] && skip_confirm=true

current=$(current_tag)
if [ -z "$current" ]; then
    echo "当前无 tag——假设起点 v0.0.0"
    current="v0.0.0"
fi

# 解析当前版本
read -r major minor patch < <(parse_current "$current")

case "$bump" in
    major) major=$((major+1)); minor=0; patch=0 ;;
    minor) minor=$((minor+1)); patch=0 ;;
    patch) patch=$((patch+1)) ;;
    *)
        echo "未知 bump 类型: $bump（只支持 major|minor|patch）" >&2
        exit 1
        ;;
esac

new_tag="v${major}.${minor}.${patch}"

echo "$current → $new_tag"

if [ "$skip_confirm" = false ]; then
    read -rp "确认打 tag $new_tag? [y/N] " reply
    if [[ ! "$reply" =~ ^[Yy]$ ]]; then
        echo "取消"
        exit 1
    fi
fi

git tag "$new_tag"
git push origin "$new_tag"
echo "✓ 已推 $new_tag 到 origin"