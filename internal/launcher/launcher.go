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
	return claudePath, args, nil
}
