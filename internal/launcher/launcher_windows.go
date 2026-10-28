//go:build windows

package launcher

import (
	"errors"
	"os"
	"os/exec"
	"strings"
)

// configureCmd 在 cmd.Run() 之前注入 caller-controlled 配置（env 等）。
// 提取为 package var 以便 test override：在 hook 里读 cmd.Env 等字段并 panic
// 中断 cmd.Run，避免真实启动 Claude Code 进程。
var configureCmd = func(cmd *exec.Cmd, opts Options) {
	if opts.Env != nil {
		cmd.Env = opts.Env
	}
}

// Launch 在 Windows 上启动 Claude Code。
//
// Windows 没有 exec 系统调用（syscall.Exec 直接返回 EWINDOWS），
// 因此改为启动子进程、继承当前终端的 stdio，等待其结束后以相同退出码退出，
// 从而对用户表现得与 Unix 上的“替换进程”一致。
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

	err = cmd.Run()
	// Cleanup 必须在 os.Exit 之前跑——否则 --settings 的临时文件会残留。
	if opts.Cleanup != nil {
		opts.Cleanup()
	}
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) {
		os.Exit(exitErr.ExitCode())
	}
	if err != nil {
		return err
	}
	// 模拟 Unix syscall.Exec 的成功语义：替换进程、不返回。
	// os.Exit 是终止语句，但 Go 编译器不做控制流分析，仍要求函数有 return；
	// 因此保留末尾 return nil 以满足编译器，对读者不是死代码，而是必需的语法占位。
	os.Exit(0)
	return nil
}
