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
    commit=$(git rev-parse --short HEAD 2>/dev/null || echo "unknown")

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

local() {
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

# ccl_cmd_shim 写 ccl.cmd shim：把 `ccl` 转发到同目录的 cc-launcher.exe。
# 与 .github/workflows/release.yml 的 "zip (windows only)" 步骤保持一致。
ccl_cmd_shim() {
    cat <<'EOF'
@echo off
"%~dp0cc-launcher.exe" %*
EOF
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
    ccl_cmd_shim > "$stage/ccl.cmd"
    [ -f README.md ] && cp README.md "$stage/"
    [ -f LICENSE ] && cp LICENSE "$stage/"
    # 固定所有源文件 mtime 到 epoch 0（对齐 release workflow）
    touch -d @0 "$stage"/*
    # UTC 解读 mtime + 剥 NTFS extra field（对齐 release workflow）
    (cd "$stage" && TZ=UTC zip -q -X "$OLDPWD/$zip" cc-launcher.exe ccl.cmd README.md LICENSE)

    echo "$zip"
}

# bump-scoop VERSION（如 v0.11.0）：本机交叉编译 + 打包 Windows zip（与
# release workflow byte-level 一致）+ 算 SHA256 + 输出 manifest JSON。
#
# **不下载 GitHub release assets**——本机 hash 与 release hash byte-level 一致
# 的前提是两边都用同样约定（touch -d @0 + TZ=UTC zip -X），已在本会话实证。
# 标准流程见 [[scoop-bucket-zip-hash-stability]]（v2 修正版）。
#
# 用法：
#   $0 bump-scoop v0.11.0
#   # 把 stdout 复制到 wzx-xle/scoop-bucket 仓的 bucket/cc-launcher.json
bump_scoop() {
    local version="${1:-}"
    if [ -z "$version" ]; then
        echo "用法: $0 bump-scoop <version>" >&2
        echo "示例: $0 bump-scoop v0.11.0" >&2
        exit 1
    fi

    # 跨编译 Windows amd64（v0.11.2 起去 arm64，与 release.yml matrix 对齐）
    windows

    local zip_amd64
    zip_amd64=$(pack_windows_zip_for_scoop amd64)

    local hash_amd64
    hash_amd64=$(sha256_cmd "$zip_amd64" | awk '{print $1}')

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
            "hash": "${hash_amd64}",
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
    local)   local ;;
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
