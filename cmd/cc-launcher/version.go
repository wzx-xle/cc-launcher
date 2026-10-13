package main

import (
	"fmt"
	"runtime"
)

// 版本信息由 build.sh 通过 -ldflags "-X main.Version=..." 注入。
// 未注入时 fallback 到 dev / unknown——`go build` 直接编译会落到这里。
//
// ldflags 注入示例（见 build.sh）：
//
//	-X main.Version=$VERSION -X main.Commit=$COMMIT
//
// 注：原 BuildTime 字段已移除——它每次 build 注入当前时间让二进制 byte-level
// 不可重现（同 commit + 同 source + 同 Go toolchain 下两次 build 也不同），
// 导致 zip hash 跨环境无法对齐。`git log` + `Built` 元信息足够追溯版本。
var (
	Version = "dev"
	Commit  = "unknown"
)

// printVersion 实现 `ccl --version` / `-V`。
// 多行详细输出：版本 / commit / Go runtime / 目标平台——debug / issue 报告用。
func printVersion() {
	fmt.Printf("Version:   %s\n", Version)
	fmt.Printf("Commit:    %s\n", Commit)
	fmt.Printf("Go:        %s\n", runtime.Version())
	fmt.Printf("OS/Arch:   %s/%s\n", runtime.GOOS, runtime.GOARCH)
}