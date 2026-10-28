package launcher

import (
	"fmt"
	"os/exec"
)

// Options 启动 Claude Code 的参数。
type Options struct {
	PermissionMode string
	Continue       bool // --continue
	Resume         bool // --resume（无 ID，打开原生会话列表）

	// Env 透传给 Claude Code 子进程的环境变量列表（KEY=VALUE 形式）。
	//
	// 语义（spec/launcher-provider-env §"Caller is responsible for parent env inclusion"）：
	//   - nil：Unix 退化为 os.Environ()，Windows 退化为父进程 env（legacy 行为）
	//   - 非 nil：直接透传给 syscall.Exec / cmd.Env；launcher 包**不解释 / 不修改**
	//
	// 调用方约定：Env 非 nil 时应至少包含父进程 env（main.go runTUI 用
	// envutil.Upsert 在 os.Environ() 基础上替换/追加 provider keys 后传入）。
	// 若 Env 非 nil 且不含父进程 env，子进程会丢失 PATH / HOME / LANG 等。
	Env []string

	// ExtraArgs 是追加给 Claude Code 的**原始命令行参数**（每个元素一个 argv
	// 槽位，不做 shell 引用处理）。
	//
	// 用途（launch-context-settings-injection）：承载 `--settings <json|file>`。
	// launch 上下文走这个通道而不是进程环境——因为 Claude Code 的 settings.json
	// `env` 块优先级**高于**进程环境，会把进程环境里的同名变量整体顶掉。
	//
	// 语义：追加在 launcher 自己生成的参数之后。launcher 包不解释内容。
	ExtraArgs []string

	// Cleanup 在 Claude Code 进程结束后、ccl 退出前执行一次（可为 nil）。
	//
	// 用途：删掉 `--settings` 的临时文件（Windows 上 cmd.exe 会剥掉 JSON 的
	// 双引号 → 必须走文件；见 cmd/cc-launcher/settings_windows.go）。
	//
	// 语义差异：Unix 上 Launch 用 syscall.Exec 替换进程、**不返回** → Cleanup
	// 不会执行（Unix 走内联 JSON、不产生文件，无影响）；Windows 上 Launch 等
	// 子进程结束 → Cleanup 会执行（launcher 在 os.Exit 之前先调它）。
	Cleanup func()
}

// resolveClaude 查找 claude 可执行文件，并根据 opts 构建参数（不含程序名）。
func resolveClaude(opts Options) (claudePath string, args []string, err error) {
	claudePath, err = exec.LookPath("claude")
	if err != nil {
		return "", nil, fmt.Errorf("未找到 claude 命令，请确认已安装 Claude Code")
	}

	if opts.PermissionMode != "" {
		args = append(args, "--permission-mode", opts.PermissionMode)
	}
	if opts.Continue {
		args = append(args, "--continue")
	}
	if opts.Resume {
		args = append(args, "--resume")
	}
	// ExtraArgs 追加在最后（launch-context-settings-injection）。
	args = append(args, opts.ExtraArgs...)
	return claudePath, args, nil
}
