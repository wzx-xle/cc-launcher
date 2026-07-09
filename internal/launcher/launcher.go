package launcher

import (
	"fmt"
	"os"
	"os/exec"
	"syscall"
)

// Options 启动 Claude Code 的参数。
type Options struct {
	PermissionMode string
	Continue       bool   // --continue
	ResumeID       string // --resume <id>
}

// Launch 用 exec 替换当前进程启动 Claude Code。
func Launch(opts Options) error {
	claudePath, err := exec.LookPath("claude")
	if err != nil {
		return fmt.Errorf("未找到 claude 命令，请确认已安装 Claude Code")
	}

	args := []string{"claude"}
	if opts.PermissionMode != "" {
		args = append(args, "--permission-mode", opts.PermissionMode)
	}
	if opts.Continue {
		args = append(args, "--continue")
	}
	if opts.ResumeID != "" {
		args = append(args, "--resume", opts.ResumeID)
	}

	argv := append([]string{claudePath}, args[1:]...)
	return syscall.Exec(claudePath, argv, os.Environ())
}
