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
    scoop-bump      从 GitHub release 算 Windows zip 真实 SHA256 并输出 manifest JSON
    clean           清理编译产物与 dist/

EOF
    exit 0
}

# version_ldflags 输出注入 main.Version / main.Commit / main.BuildTime 的 -ldflags 串。
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
    local version commit build_time
    # git describe --tags 输出: v<X.Y.Z>-<commits_ahead>-g<hash>（无 --dirty——自己加）
    version=$(git describe --tags 2>/dev/null || echo "dev")
    commit=$(git rev-parse --short HEAD 2>/dev/null || echo "unknown")

    # dirty 检测: working tree 不干净 → Version 末尾加 "-dirty"
    # 用 git status --porcelain 检测（包含 untracked）比 git describe --dirty 更准
    if [ -n "$(git status --porcelain 2>/dev/null)" ]; then
        version="${version}-dirty"
    fi

    # date 跨 OS 兼容写法：GNU / BSD 都接受无引号 format；git commit date 作 fallback
    build_time=$(date -u +%Y-%m-%dT%H:%M:%SZ 2>/dev/null \
        || git log -1 --format=%cI 2>/dev/null \
        || echo "unknown")
    echo "-s -w -X main.Version=$version -X main.Commit=$commit -X main.BuildTime=$build_time"
}

build() {
    echo "==> 编译 $BINARY ..."
    # shellcheck disable=SC2046
    go build -ldflags="$(version_ldflags)" -o "$BINARY" ./cmd/cc-launcher/
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
    GOOS="$goos" GOARCH="$goarch" go build -ldflags="$(version_ldflags)" -o "$out" ./cmd/cc-launcher/
    echo "==> 完成: $(pwd)/$out"
}

windows_build() {
    cross_build windows amd64 .exe
    cross_build windows arm64 .exe
}

# sha256_cmd 跨 OS 兼容：Linux 用 sha256sum，macOS 用 shasum -a 256。
sha256_cmd() {
    if command -v sha256sum >/dev/null 2>&1; then
        sha256sum "$@"
    else
        shasum -a 256 "$@"
    fi
}

# scoop-bump VERSION（如 v0.11.0）：从 GitHub release 下载 Windows zip + 算
# SHA256 + 输出 manifest JSON 到 stdout。
#
# **必须用 release assets 的真 hash**（mtime / zip 版本 / 压缩参数差异让
# 本地手打 zip 跟 release zip 字节级永远不一致，scoop install 校验会失败）。
# 标准流程见 [[scoop-bucket-zip-hash-stability]]。
#
# 用法：
#   $0 scoop-bump v0.11.0
#   # 把 stdout 复制到 wzx-xle/scoop-bucket 仓的 bucket/cc-launcher.json
scoop_bump() {
    local version="${1:-}"
    if [ -z "$version" ]; then
        echo "用法: $0 scoop-bump <version>" >&2
        echo "示例: $0 scoop-bump v0.11.0" >&2
        exit 1
    fi

    local repo="wzx-xle/cc-launcher"
    local base="https://github.com/${repo}/releases/download/${version}"
    local stage
    stage=$(mktemp -d)
    trap "rm -rf '$stage'" EXIT

    local zip_amd64="cc-launcher-windows-amd64.zip"
    local zip_arm64="cc-launcher-windows-arm64.zip"

    echo "==> 下载 ${base}/${zip_amd64} ..." >&2
    curl -sSL -o "$stage/$zip_amd64" "$base/$zip_amd64"
    echo "==> 下载 ${base}/${zip_arm64} ..." >&2
    curl -sSL -o "$stage/$zip_arm64" "$base/$zip_arm64"

    local hash_amd64 hash_arm64
    hash_amd64=$(sha256_cmd "$stage/$zip_amd64" | awk '{print $1}')
    hash_arm64=$(sha256_cmd "$stage/$zip_arm64" | awk '{print $1}')

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
        },
        "arm64": {
            "url": "${base}/cc-launcher-windows-arm64.zip",
            "hash": "${hash_arm64}",
            "bin": ["ccl.cmd"]
        }
    }
}
EOF
}

release() {
    windows_build
    cross_build linux amd64
    cross_build linux arm64
    cross_build darwin amd64
    cross_build darwin arm64
}

test_all() {
    echo "==> 运行测试..."
    go test ./... -v
}

# 契约测试：把项目硬编码的权限模式列表与本机 claude --help 的实际取值对表。
# 用 build tag 隔离，避免常规 test 依赖外部 CLI；本机无 claude 时自动 SKIP。
contract_test() {
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
    test)    test_all ;;
    contract) contract_test ;;
    windows) windows_build ;;
    release) release ;;
    scoop-bump) scoop_bump "${1:-}" ;;
    clean)   clean ;;
    -h|--help|help) usage ;;
    *)       echo "未知选项: $ACTION"; usage ;;
esac
