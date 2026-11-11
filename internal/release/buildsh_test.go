package release

import (
	"os"
	"strings"
	"testing"
)

// buildShPath 是仓库根的 build.sh（相对 internal/release/）。
const buildShPath = "../../build.sh"

// shFuncBlock 取出 build.sh 里某个 shell 函数的定义块。
func shFuncBlock(t *testing.T, src, name string) string {
	t.Helper()
	idx := strings.Index(src, name+"() {")
	if idx < 0 {
		t.Fatalf("build.sh 里找不到函数 %s", name)
	}
	rest, _, ok := strings.Cut(src[idx:], "\n}")
	if !ok {
		t.Fatalf("build.sh 里函数 %s 没有闭合", name)
	}
	return rest
}

// TestBuildSh_InstallsCclCommandName 守住 build.sh 装出来的命令名是 **ccl**。
//
// 为什么这条要锁住：构建产物名是 `cc-launcher`（= Go 模块名），但**对外命令名
// 一直是 `ccl`** —— README 全篇、Windows（scoop 装 `ccl.cmd`）、以及所有文档
// 都用 `ccl`。曾经 install / local 直接 `cp "$BINARY"` → 用户装完敲 `ccl` 得到
// "command not found"，只能自己 `ln -s` 兜着。
//
// 守护的是"两个名字并存"：`ccl` 是真文件（PATH 入口），`cc-launcher` 是指向它
// 的软链（老用户习惯长名，别断）。
func TestBuildSh_InstallsCclCommandName(t *testing.T) {
	b, err := os.ReadFile(buildShPath)
	if err != nil {
		t.Fatal(err)
	}
	src := string(b)

	if !strings.Contains(src, `CMD_NAME="ccl"`) {
		t.Error(`build.sh 应定义 CMD_NAME="ccl"（对外命令名）`)
	}

	for _, fn := range []string{"install", "local_install"} {
		block := shFuncBlock(t, src, fn)
		// **看 cp 的目标参数**，不能只看"块里出现过 $CMD_NAME"——软链那行
		// 也含 $CMD_NAME，会把"cp 仍然装长名"这种错误放过。
		cpTarget := ""
		for _, line := range strings.Split(block, "\n") {
			cmd := strings.TrimPrefix(strings.TrimSpace(line), "sudo ")
			if strings.HasPrefix(cmd, "cp ") {
				cpTarget = cmd
				break
			}
		}
		if cpTarget == "" {
			t.Errorf("%s 里找不到 cp 命令", fn)
			continue
		}
		if !strings.Contains(cpTarget, "$CMD_NAME") {
			t.Errorf("%s: cp 的目标应是 $CMD_NAME（对外命令名是 ccl），实际: %s", fn, cpTarget)
		}
		// 保留长名软链
		if !strings.Contains(block, "ln -sf") {
			t.Errorf("%s 应建 cc-launcher → ccl 的软链（老用户习惯长名）", fn)
		}
	}
}

// TestBuildSh_RemoveBeforeCopy 守住"**先 rm 再 cp**"。
//
// cp 在目标是**符号链接**时会跟随链接写入源文件 —— 用户自己
// `ln -s ccl <repo>/cc-launcher`（把 ccl 指向仓库里的二进制）是很常见的做法，
// 不 rm 就会把仓库里的二进制覆盖掉，而链接还在 → 看起来"装成功了"，其实是坏的。
func TestBuildSh_RemoveBeforeCopy(t *testing.T) {
	b, err := os.ReadFile(buildShPath)
	if err != nil {
		t.Fatal(err)
	}
	src := string(b)

	for _, fn := range []string{"install", "local_install"} {
		block := shFuncBlock(t, src, fn)
		rmIdx, cpIdx := -1, -1
		for i, line := range strings.Split(block, "\n") {
			trimmed := strings.TrimSpace(line)
			if trimmed == "" || strings.HasPrefix(trimmed, "#") {
				continue // **跳过注释** —— 注释里提到 "cp" 不是命令
			}
			cmd := strings.TrimPrefix(trimmed, "sudo ") // install 走 sudo
			if rmIdx < 0 && strings.HasPrefix(cmd, "rm ") {
				rmIdx = i
			}
			if cpIdx < 0 && strings.HasPrefix(cmd, "cp ") {
				cpIdx = i
			}
		}
		if rmIdx < 0 {
			t.Errorf("%s 必须先 rm -f 目标（cp 会跟随符号链接写入 → 覆盖源文件）", fn)
			continue
		}
		if cpIdx < 0 {
			t.Errorf("%s 里找不到 cp 命令", fn)
			continue
		}
		if rmIdx > cpIdx {
			t.Errorf("%s: rm 必须在 cp **之前**（否则 cp 跟随符号链接写入源文件）", fn)
		}
	}
}

// TestBuildSh_LongNameStillWorks 守住长名 `cc-launcher` 不会被悄悄删掉。
//
// 构建产物名与对外命令名不同 → 两个名字都得能用（长名做软链）。
func TestBuildSh_LongNameStillWorks(t *testing.T) {
	b, err := os.ReadFile(buildShPath)
	if err != nil {
		t.Fatal(err)
	}
	src := string(b)
	for _, fn := range []string{"install", "local_install"} {
		block := shFuncBlock(t, src, fn)
		if !strings.Contains(block, "$BINARY") {
			t.Errorf("%s 里应出现 $BINARY（长名软链的目标）", fn)
		}
	}
}
