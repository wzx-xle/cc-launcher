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
//	-X main.Version=$VERSION -X main.Commit=$COMMIT -X main.BuildTime=$BUILD_TIME
var (
	Version   = "dev"
	Commit    = "unknown"
	BuildTime = "unknown"
)

// printVersion 实现 `ccl --version` / `-V`。
// 多行详细输出：版本 / commit / build time / Go runtime / 目标平台——debug / issue 报告用。
func printVersion() {
	fmt.Printf("Version:   %s\n", Version)
	fmt.Printf("Commit:    %s\n", Commit)
	fmt.Printf("Built:     %s\n", BuildTime)
	fmt.Printf("Go:        %s\n", runtime.Version())
	fmt.Printf("OS/Arch:   %s/%s\n", runtime.GOOS, runtime.GOARCH)
}