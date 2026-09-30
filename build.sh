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
    clean           清理编译产物与 dist/

EOF
    exit 0
}

# version_ldflags 输出注入 main.Version / main.Commit / main.BuildTime 的 -ldflags 串。
# 未在 git 仓库跑（或无 tag）时 fallback 到 dev / unknown——`go build` 直接编译同源。
#
# Version 字段语义:
#   - clean + 在 tag 上 → "vX.Y.Z"（release 形态）
#   - clean + ahead of tag → "vX.Y.Z"（user 看到 release 形态；ahead 信息由 Commit 字段表达）
#   - dirty → "vX.Y.Z-dirty"（用户立刻看出来这是未提交修改的 build）
#
# 跨 OS 兼容:
#   - date 不加引号（macOS BSD date 把 "%Y" 当字面；GNU date 也接受无引号）
#   - fallback 用 `git log -1 --format=%cI`（ISO 8601 commit date）—— git 跨 OS 一致
version_ldflags() {
    local version commit build_time
    # 用 --abbrev=0 只取 tag 名（不显示 ahead count + hash）——ahead 信息由 Commit 字段表达
    version=$(git describe --tags --abbrev=0 2>/dev/null || echo "dev")
    commit=$(git rev-parse --short HEAD 2>/dev/null || echo "unknown")

    # dirty 检测：working tree 不干净 → Version 加 "-dirty" 后缀
    # 注: 用 git status --porcelain 检测 modified / untracked（比 git describe --dirty 更准——
    # 因为后者只能检测 tracked 文件修改)
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
    clean)   clean ;;
    -h|--help|help) usage ;;
    *)       echo "未知选项: $ACTION"; usage ;;
esac
