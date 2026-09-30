#!/bin/sh
# bump-version.sh — 推进 semver 版本号（v0.x.y）
#
# 子命令:
#   current                     显示当前版本（最近 tag，无 tag 时 v0.0.0）
#   list                        列已有 tag（按版本号倒序）
#   bump <major|minor|patch>    bump 并推 origin（交互确认 / --yes 跳过）
#   auto [--yes]                从 last tag..HEAD 扫 commit subjects，按 conventional commit 规则
#                                自动选 bump 类型（feat!:BREAKING → major; feat: → minor;
#                                其它 → patch），再走 bump 流程
#   check                       验证 working tree 干净（无未提交修改）
#   help                        用法
#
# 跨 OS 兼容: POSIX sh 写法——macOS / Linux / BSD / Git Bash on Windows 都跑。
# 不依赖 bash 特性（不用 [[ =~ ]] / read -p / <()）。
#
# 设计:
#   - 版本号唯一源是 git tag——不维护内部 version 文件
#   - bump 由 archive 完成后人工触发；归档 ≠ bump
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
    auto [--yes]                     自动从 last tag..HEAD 推断 bump 类型
                                     （conventional commit: feat!:BREAKING→major;
                                      feat:→minor; 其它→patch）并 bump
    check                            验证 working tree 干净
    help                             显示本帮助

示例:
    $PROG current
    $PROG list
    $PROG bump minor
    $PROG auto           # 看 commits + 推断 + 交互确认
    $PROG auto --yes     # 跳过确认直接 bump
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

# do_auto: 从 latest tag..HEAD 扫 commit subjects，按 conventional commit 规则推断 bump 类型。
# 规则（major > minor > patch 取最大）:
#   - 任意 type!  (如 feat!: fix!:) → major
#   - 含 "BREAKING CHANGE:" 字串 → major
#   - feat: / feat(scope:) → minor
#   - 其它 → patch（fix / refactor / perf / chore / docs / style / test / build / ci / revert 都算）
#
# 注意: BREAKING CHANGE: 必须在 commit message 标出（subject `feat!:` 或 footer `BREAKING CHANGE:`）
# ——否则不会触发 major bump。这是 conventional commit 约定。
do_auto() {
    skip_confirm="${1:-false}"

    current=$(current_tag)
    if [ -z "$current" ]; then
        echo "当前无 tag——auto 模式假设 v0.0.0"
        current="v0.0.0"
    fi

    # 取 last tag 至今的 commit subjects（无 tag 时 fallback v0.0.0，从第一个 commit 起）
    if [ "$current" = "v0.0.0" ]; then
        range_range=""
    else
        range_range="$current..HEAD"
    fi

    subjects=$(git log $range_range --pretty=format:'%s' 2>/dev/null)

    if [ -z "$subjects" ]; then
        echo "✗ 自 $current 以来无 commit，auto 无法推断" >&2
        return 1
    fi

    # 推断 bump 类型
    bump="patch"
    while IFS= read -r subject; do
        [ -z "$subject" ] && continue
        case "$subject" in
            *\!:*)                bump="major" ;;
            *"BREAKING CHANGE:"*)  bump="major" ;;
            feat:*)                bump="minor" ;;
            feat\(*\):*)           bump="minor" ;;
            # 其它 type 默认 patch，不动 bump
        esac
    done <<EOF
$subjects
EOF

    # 显示 commit 列表 + 推断结果
    echo "═══ 自 $current 至今的 commits ═══"
    echo "$subjects" | sed 's/^/  /'
    echo ""
    echo "═══ 推断 bump 类型: $bump ═══"

    do_bump "$bump" "$skip_confirm"
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
    auto)
        skip="false"
        if [ "${1:-}" = "--yes" ]; then
            skip="true"
        fi
        do_auto "$skip"
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