#!/usr/bin/env bash
set -euo pipefail

BINARY="cc-launcher"
INSTALL_DIR="/usr/local/bin"

usage() {
    cat <<EOF
用法: $0 [选项]

选项:
    build           编译二进制（默认）
    install         编译并安装到 $INSTALL_DIR
    local           编译并安装到 ~/.local/bin
    run             编译并运行
    test            运行所有测试
    contract        运行契约测试（校验权限模式与本机 claude CLI 一致）
    windows         交叉编译 Windows 版本到 dist/
    release         交叉编译全部目标平台到 dist/
    bump-scoop      从 GitHub release 算 Windows zip 真实 SHA256 并输出 manifest JSON
    clean           清理编译产物与 dist/

EOF
    exit 0
}

# version_ldflags 输出注入 main.Version / main.Commit 的 -ldflags 串。
# 未在 git 仓库跑（或无 tag）时 fallback 到 dev / unknown——`go build` 直接编译同源。
#
# Version 字段语义: git describe --tags 原样输出 + 自定义 dirty 标记
#   - clean + ahead of tag → "v0.6.0-6-g2f479e9"（用户能看到 ahead count + hash）
#   - clean + at tag       → "v0.6.0"
#   - dirty                → "v0.6.0-6-g2f479e9-dirty"（含 tracked 修改）
#   - dirty (含 untracked) → "v0.6.0-6-g2f479e9-dirty"（自检，比 --dirty flag 更准）
#
# 跨 OS 兼容:
#   - date 不加引号（macOS BSD date 把 "%Y" 当字面；GNU date 也接受无引号）
#   - fallback 用 `git log -1 --format=%cI`（ISO 8601 commit date）—— git 跨 OS 一致
version_ldflags() {
    local version commit
    # git describe --tags 输出: v<X.Y.Z>-<commits_ahead>-g<hash>（无 --dirty——自己加）
    version=$(git describe --tags 2>/dev/null || echo "dev")
    # commit hash --short=8 (8 字符) 对齐 .github/workflows/release.yml 的
    # `${GITHUB_SHA::8}`。git --short 默认 7 字符会让 main.Commit 字节长度 8
    # vs CI 的 9，差 1 字节 → .rdata 偏移 → zip hash byte-level 不一致。
    commit=$(git rev-parse --short=8 HEAD 2>/dev/null || echo "unknown00")

    # dirty 检测: working tree 不干净 → Version 末尾加 "-dirty"
    # 用 git status --porcelain 检测（包含 untracked）比 git describe --dirty 更准
    if [ -n "$(git status --porcelain 2>/dev/null)" ]; then
        version="${version}-dirty"
    fi

    # 注：原 -X main.BuildTime= 已移除。BuildTime 每次 build 注入当前时间让二进制
    # byte-level 不可重现（同 commit + 同 source 下两次 build 也不同），导致 zip
    # hash 跨环境无法对齐。version 信息仅含 Version + Commit 已足够（git log 可追
    # 溯具体 build 时间）。
    # -trimpath **不是** ldflags 的一部分（Go 1.21+ link 器会拒绝）——它是 go build
    # 的 flag，在 build.go 调用处加。ldflags 字符串里只放 link 器认识的 flag（-s/-w/-X）。
    echo "-s -w -X main.Version=$version -X main.Commit=$commit"
}

build() {
    echo "==> 编译 $BINARY ..."
    # shellcheck disable=SC2046
    go build -trimpath -ldflags="$(version_ldflags)" -o "$BINARY" ./cmd/cc-launcher/
    echo "==> 完成: $(pwd)/$BINARY"
}

install() {
    build
    echo "==> 安装到 $INSTALL_DIR ..."
    sudo cp "$BINARY" "$INSTALL_DIR/"
    echo "==> 已安装: $INSTALL_DIR/$BINARY"
}

local_install() {
    build
    local dir="${HOME}/.local/bin"
    mkdir -p "$dir"
    cp "$BINARY" "$dir/"
    echo "==> 已安装: $dir/$BINARY"
    if ! echo "$PATH" | grep -q "$dir"; then
        echo "==> 提示: $dir 不在 PATH 中，请添加: export PATH=\"$dir:\$PATH\""
    fi
}

run() {
    build
    exec "./$BINARY" "$@"
}

# cross_build GOOS GOARCH EXT
cross_build() {
    local goos="$1" goarch="$2" ext="${3:-}"
    local out="dist/${BINARY}-${goos}-${goarch}${ext}"
    echo "==> 交叉编译 ${goos}/${goarch} ..."
    mkdir -p dist
    # shellcheck disable=SC2046
    GOOS="$goos" GOARCH="$goarch" go build -trimpath -ldflags="$(version_ldflags)" -o "$out" ./cmd/cc-launcher/
    echo "==> 完成: $(pwd)/$out"
}

windows() {
    # 与 .github/workflows/release.yml matrix 对齐——v0.11.2 起只 windows/amd64
    # （windows/arm64 用户少，CI 资源紧；bucket manifest 只跟 amd64 zip）。
    cross_build windows amd64 .exe
}

# sha256_cmd 跨 OS 兼容：Linux 用 sha256sum，macOS 用 shasum -a 256。
sha256_cmd() {
    if command -v sha256sum >/dev/null 2>&1; then
        sha256sum "$@"
    else
        shasum -a 256 "$@"
    fi
}

# fetch_release_zip_hash_gh_api VERSION
#
# 从 GitHub releases API fetch release body + parse <!-- sha256:auto-hash:HEX -->
# marker。不下载 zip（节省 18 MB 流量）。
#
# bump-scoop-hash-trace change 引入：release workflow 在 windows-amd64 job 末尾
# 把 zip SHA256 写进 release body 注释 marker，bump-scoop 改用这个流程。
#
# Args:
#   $1  = version like "v0.12.0"
#
# Returns:
#   stdout: 64-char hex SHA256
#   exit:   0 on success, non-zero on failure (rate limit / no marker / network)
#
# Retry: 3 attempts with 5-second backoff (release workflow 'append hash' step
# races with manual bump-scoop invocation; operator may run bump-scoop within
# seconds of tag push).
fetch_release_zip_hash_gh_api() {
    local version="$1"
    local repo="wzx-xle/cc-launcher"
    local body hash i api_response

    # 认证 token(可选):GitHub REST API **未认证时限流 60 req/hr**。本机出口 IP 是
    # 共享的(公司/运营商 NAT),额度经常被打满 → 未认证调用直接返回
    # "API rate limit exceeded" → bump-scoop 必失败(2026-10-21 实测)。
    # 有 token 时用认证额度(5000/hr)。优先级:
    #   GITHUB_TOKEN / GH_TOKEN 环境变量 → gh CLI 已登录的 token → 匿名。
    local token="${GITHUB_TOKEN:-${GH_TOKEN:-}}"
    if [ -z "$token" ] && command -v gh >/dev/null 2>&1; then
        token=$(gh auth token 2>/dev/null || echo "")
    fi
    local auth_header=""
    if [ -n "$token" ]; then
        auth_header="Authorization: Bearer ${token}"
    fi

    for attempt in 1 2 3; do
        # GitHub API responses with rate-limit error have message key but no body.
        # Python parse catches JSON errors. Use python for robust parsing.
        # User-Agent 显式给(GitHub API 拒绝空 UA);auth_header 为空时展开成空串,
        # token 不含空格 → 词分割安全(不引号展开)。
        api_response=$(curl -sS \
            -H "Accept: application/vnd.github+json" \
            -H "User-Agent: cc-launcher-build.sh" \
            ${auth_header:+-H "$auth_header"} \
            "https://api.github.com/repos/${repo}/releases/tags/${version}" \
            2>/dev/null) || {
            echo "fetch attempt ${attempt}: curl failed (network); retry in 5s..." >&2
            sleep 5
            continue
        }

        body=$(printf '%s' "$api_response" | python3 -c "
import sys, json
try:
    data = json.load(sys.stdin)
    if 'message' in data and 'body' not in data:
        # API error (e.g. rate limit)
        sys.stderr.write('API error: ' + data['message'] + '\\n')
        sys.exit(1)
    print(data.get('body') or '')
except Exception as e:
    sys.stderr.write('JSON parse error: ' + str(e) + '\\n')
    sys.exit(1)
" 2>&1) || {
            echo "fetch attempt ${attempt}: API error; retry in 5s..." >&2
            sleep 5
            continue
        }

        # Extract SHA256 from <!-- sha256:<filename>:HEX --> marker.
        # 解析走 internal/release 的 Go 实现（cmd/release-notes CLI）——与 release
        # workflow 注入 CHANGELOG 段用的是**同一份逻辑**。此前这里内联 grep、
        # workflow 里内联 python,两处各写各的,谁改谁坏(2026-10-21 连续踩坑)。
        # marker 格式从 `sha256:auto-hash:HEX`(v0.12.0~v0.13.2)到
        # `sha256:cc-launcher-windows-amd64.zip:HEX`(v0.13.3+),实现两种都认。
        hash=$(printf '%s' "$body" | go run ./cmd/release-notes sha-marker 2>/dev/null || true)
        if [ -n "$hash" ]; then
            echo "$hash"
            return 0
        fi

        echo "fetch attempt ${attempt}: marker not in release body yet (release workflow 'append hash' step 可能还没跑完); retry in 5s..." >&2
        sleep 5
    done

    echo "" >&2
    echo "ERROR: fetch_release_zip_hash_gh_api 失败——3 次重试后仍未拿到 release body 的 sha256 marker" >&2
    echo "可能原因：" >&2
    echo "  1. release workflow 的 'append windows zip SHA256 to release body' step 还没跑" >&2
    echo "  2. GitHub API rate limit exceeded —— 未认证 60/hr,本机共享出口 IP 很容易打满;" >&2
    echo "     解法:装 gh 并 `gh auth login`(或设 GITHUB_TOKEN 环境变量)→ 额度提到 5000/hr" >&2
    echo "  3. 手动 fallback（需要下载 18 MB zip 拿真值——bumps-scoop-hash-trace 不自动跑 download）：" >&2
    echo "     curl -sSL https://github.com/${repo}/releases/download/${version}/cc-launcher-windows-amd64.zip -o /tmp/x.zip" >&2
    echo "     sha256sum /tmp/x.zip" >&2
    return 1
}

# pack_windows_zip_for_scoop VERSION ARCH：本机打 zip + 算 SHA256。
# 与 .github/workflows/release.yml "zip (windows only)" 步骤 byte-level 对齐：
#   - touch -d @0 固定 4 个源文件 mtime 到 epoch 0
#   - TZ=UTC zip -X 用 UTC 解读 mtime + 剥 NTFS extra field
# 三个变量（mtime / 时区 / extra field）都固定后，hash 跨环境可重现。
pack_windows_zip_for_scoop() {
    local arch="$1"
    local exe="dist/${BINARY}-windows-${arch}.exe"
    local zip="dist/${BINARY}-windows-${arch}-scoop.zip"
    local stage
    stage=$(mktemp -d)
    trap "rm -rf '$stage'" EXIT

    cp "$exe" "$stage/cc-launcher.exe"
    # ccl.cmd 字节来自 cmd/release-notes（单一来源，与 release workflow 的
    # package 步骤共用）→ 行尾 CRLF。曾经的 heredoc 版本是 LF，且两份实现
    # 各写各的（漂移来源）。
    go run ./cmd/release-notes windows-shim > "$stage/ccl.cmd"
    # 就地断言 CRLF（.cmd 是 cmd.exe 读的批处理文件）——与 release workflow 的
    # package 步骤同一道断言，防"绕过 cmd/release-notes 又手写一份 shim"
    if ! grep -q $'\r' "$stage/ccl.cmd"; then
        echo "ERROR: ccl.cmd 不是 CRLF 行尾（cmd.exe 读批处理文件要 CRLF）" >&2
        return 1
    fi
    [ -f README.md ] && cp README.md "$stage/"
    [ -f LICENSE ] && cp LICENSE "$stage/"
    # 固定所有源文件 mtime 到 epoch 0（对齐 release workflow）
    touch -d @0 "$stage"/*
    # UTC 解读 mtime + 剥 NTFS extra field（对齐 release workflow）
    (cd "$stage" && TZ=UTC zip -q -X "$OLDPWD/$zip" cc-launcher.exe ccl.cmd README.md LICENSE)

    echo "$zip"
}

# bump-scoop VERSION（如 v0.12.0）：本机交叉编译 + 打包 Windows zip（仅供人工
# verify）+ GitHub API fetch release zip SHA256 + 输出 manifest JSON。
#
# bump-scoop-hash-trace change 引入：原假设"本机 zip hash byte-level == release
# zip hash"实际不可能——Go build action hash 含 GOMODCACHE state，fresh CI vs
# incremental local cache 永远不一致（58824 字节 diff / 21822 region，pclntab
# function hash 表 + build ID 字符串，物理不可修）。
#
# 新流程：本地 build 出 zip（**仅用于人工 reference；最终 manifest hash 不写本地
# zip hash**），改用 GitHub releases API fetch release body + parse
# `<!-- sha256:auto-hash:HEX -->` marker（release workflow 'append windows
# zip SHA256 to release body' step 在 CI side 写 marker）。
#
# 流量节省：原本每次 bump-scoop 需下载 18 MB release zip 拿真值。改后只 fetch
# GitHub API metadata（几 KB），下载流量可零至最少。
#
# 用法：
#   $0 bump-scoop v0.12.0
#   # 把 stdout 复制到 wzx-xle/scoop-bucket 仓的 bucket/cc-launcher.json
bump_scoop() {
    local version="${1:-}"
    if [ -z "$version" ]; then
        echo "用法: $0 bump-scoop <version>" >&2
        echo "示例: $0 bump-scoop v0.12.0" >&2
        exit 1
    fi

    # 跨编译 Windows amd64（v0.11.2 起去 arm64，与 release.yml matrix 对齐）
    windows

    # 本地 build zip（保留用于人工 verify / 调试；hash 不会写进 manifest）
    local zip_amd64
    zip_amd64=$(pack_windows_zip_for_scoop amd64)
    local local_hash
    local_hash=$(sha256_cmd "$zip_amd64" | awk '{print $1}')
    echo "==> local zip (verify-only): $zip_amd64 sha256=$local_hash" >&2

    # GitHub API fetch release zip 真值 hash（不走 download）
    local release_hash
    if ! release_hash=$(fetch_release_zip_hash_gh_api "$version"); then
        # helper 已打印 fallback instructions; bump-scoop 不自动 download
        return 1
    fi
    echo "==> release zip (manifest): sha256=$release_hash" >&2

    # 清理临时 scoop zip（dist/ 只留原始 exe）
    rm -f "$zip_amd64"

    local repo="wzx-xle/cc-launcher"
    local base="https://github.com/${repo}/releases/download/${version}"
    cat <<EOF
{
    "version": "${version#v}",
    "description": "Terminal launcher for Claude Code with provider switching and permission mode selection",
    "homepage": "https://github.com/${repo}",
    "license": "MIT",
    "architecture": {
        "64bit": {
            "url": "${base}/cc-launcher-windows-amd64.zip",
            "hash": "${release_hash}",
            "bin": ["ccl.cmd"]
        }
    }
}
EOF
}

release() {
    windows
    cross_build linux amd64
    cross_build linux arm64
    cross_build darwin amd64
    cross_build darwin arm64
}

test() {
    echo "==> 运行测试..."
    go test ./... -v
}

# 契约测试：把项目硬编码的权限模式列表与本机 claude --help 的实际取值对表。
# 用 build tag 隔离，避免常规 test 依赖外部 CLI；本机无 claude 时自动 SKIP。
contract() {
    echo "==> 运行契约测试..."
    go test -tags contract ./... -v -run "^TestContract_"
}

clean() {
    echo "==> 清理..."
    rm -f "$BINARY"
    rm -rf dist
    echo "==> 完成"
}

# 默认动作
ACTION="${1:-build}"
shift || true

case "$ACTION" in
    build)   build ;;
    install) install ;;
    local)   local_install ;;
    run)     run "$@" ;;
    test)    test ;;
    contract) contract ;;
    windows) windows ;;
    release) release ;;
    bump-scoop) bump_scoop "${1:-}" ;;
    clean)   clean ;;
    -h|--help|help) usage ;;
    *)       echo "未知选项: $ACTION"; usage ;;
esac
