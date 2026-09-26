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
