// Command release-notes 是 release 流程里的文本处理 CLI。
//
// 存在的理由：release workflow 与 build.sh 之前各自内联了一份 CHANGELOG 段
// 提取 / marker 解析逻辑（workflow 里是 inline python，build.sh 里是 grep），
// 两处都没有测试覆盖 —— 2026-10-21 连续踩了三个 bug，其中两个就是解析逻辑
// 写错（版本前缀不匹配、正则缺行首锚点）。
//
// 现在统一走 internal/release 里的实现，逻辑本身有单测（internal/release/
// notes_test.go），CI 里 `go test ./...` 就能覆盖。
//
// 用法:
//
//	release-notes changelog-section <changelog.md> <version>   打印版本对应的整段
//	release-notes sha-marker                                   从 stdin 的 body 里打印 64 位 hex
//
// 失败一律非零退出 + stderr 报错（调用方应 fail，不该静默跳过）。
package main

import (
	"fmt"
	"io"
	"os"

	"cc-launcher/internal/release"
)

func main() {
	if err := run(os.Args[1:], os.Stdin, os.Stdout); err != nil {
		fmt.Fprintf(os.Stderr, "release-notes: %v\n", err)
		os.Exit(1)
	}
}

func run(args []string, stdin io.Reader, stdout io.Writer) error {
	if len(args) == 0 {
		return fmt.Errorf("用法: release-notes changelog-section <changelog.md> <version> | sha-marker")
	}

	switch args[0] {
	case "changelog-section":
		if len(args) != 3 {
			return fmt.Errorf("用法: release-notes changelog-section <changelog.md> <version>")
		}
		raw, err := os.ReadFile(args[1])
		if err != nil {
			return fmt.Errorf("读 %s 失败: %w", args[1], err)
		}
		section, err := release.ExtractChangelogSection(string(raw), args[2])
		if err != nil {
			return err
		}
		_, err = fmt.Fprint(stdout, section)
		return err

	case "changelog-latest":
		if len(args) != 2 {
			return fmt.Errorf("用法: release-notes changelog-latest <changelog.md>")
		}
		raw, err := os.ReadFile(args[1])
		if err != nil {
			return fmt.Errorf("读 %s 失败: %w", args[1], err)
		}
		version, err := release.LatestChangelogVersion(string(raw))
		if err != nil {
			return err
		}
		_, err = fmt.Fprint(stdout, version)
		return err

	case "sha-marker":
		raw, err := io.ReadAll(stdin)
		if err != nil {
			return fmt.Errorf("读 stdin 失败: %w", err)
		}
		hash, err := release.ExtractSHA256Marker(string(raw))
		if err != nil {
			return err
		}
		_, err = fmt.Fprint(stdout, hash)
		return err

	default:
		return fmt.Errorf("未知子命令 %q（可用: changelog-section / sha-marker）", args[0])
	}
}
