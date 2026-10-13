//go:build windows

package launcher

import (
	"os"
	"os/exec"
	"strings"
)

// configureCmd 在 cmd.Start() 之前注入 caller-controlled 配置（env 等）。
// 提取为 package var 以便 test override：在 hook 里读 cmd.Env 等字段并 panic
// 中断 cmd.Start，避免真实启动 Claude Code 进程。
var configureCmd = func(cmd *exec.Cmd, opts Options) {
	if opts.Env != nil {
		cmd.Env = opts.Env
	}
}

// Launch 在 Windows 上启动 Claude Code 后立即退出，claude 独立运行。
//
// Windows 没有 exec 系统调用（syscall.Exec 直接返回 EWINDOWS）。与 Unix
// `syscall.Exec` 替换进程 image（ccl 进程消失、claude 接管）用户感知对齐的
// 做法是：启动子进程、继承 stdio 后立即 `os.Exit(0)` 终止 ccl。claude 退出
// code 由 cmd.exe → node.exe → claude.exe 进程链传递给 ccl 调用者的父进程
//（PowerShell），ccl 自身不参与。
func Launch(opts Options) error {
	claudePath, args, err := resolveClaude(opts)
	if err != nil {
		return err
	}

	var cmd *exec.Cmd
	// npm 全局安装的 claude 是 claude.cmd/claude.bat（批处理封装），
	// Windows 的 CreateProcess 不能直接执行批处理，必须经 cmd.exe /c 启动。
	if ext := strings.ToLower(claudePath); strings.HasSuffix(ext, ".cmd") || strings.HasSuffix(ext, ".bat") {
		cmd = exec.Command("cmd", append([]string{"/c", claudePath}, args...)...)
	} else {
		cmd = exec.Command(claudePath, args...)
	}
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	configureCmd(cmd, opts)

	// 启动子进程（不 wait）；启动失败（如找不到 claude 二进制）返回 error 让
	// caller 走正常错误处理路径。启动成功则 ccl 立即退 0，claude 独立运行。
	if err := cmd.Start(); err != nil {
		return err
	}
	// 模拟 Unix syscall.Exec 的成功语义：ccl 进程立即消失，claude 接管 stdio。
	// os.Exit 是终止语句，但 Go 编译器不做控制流分析，仍要求函数有 return；
	// 因此保留末尾 return nil 以满足编译器，对读者不是死代码，而是必需的语法占位。
	os.Exit(0)
	return nil
}
