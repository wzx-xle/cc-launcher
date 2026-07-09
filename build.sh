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
    clean           清理编译产物

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

test_all() {
    echo "==> 运行测试..."
    go test ./... -v
}

clean() {
    echo "==> 清理..."
    rm -f "$BINARY"
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
    clean)   clean ;;
    -h|--help|help) usage ;;
    *)       echo "未知选项: $ACTION"; usage ;;
esac
