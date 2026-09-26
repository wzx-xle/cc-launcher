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

build() {
    echo "==> 编译 $BINARY ..."
    go build -ldflags="-s -w" -o "$BINARY" ./cmd/cc-launcher/
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
    GOOS="$goos" GOARCH="$goarch" go build -ldflags="-s -w" -o "$out" ./cmd/cc-launcher/
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
