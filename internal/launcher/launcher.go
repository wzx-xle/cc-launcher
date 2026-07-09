package launcher

import (
	"fmt"
	"os"
	"os/exec"
	"syscall"
)

// Launch 用 exec 替换当前进程启动 Claude Code。
func Launch(permissionMode string) error {
	claudePath, err := exec.LookPath("claude")
	if err != nil {
		return fmt.Errorf("未找到 claude 命令，请确认已安装 Claude Code")
	}

	args := []string{"claude"}
	if permissionMode != "" {
		args = append(args, "--permission-mode", permissionMode)
	}

	argv := append([]string{claudePath}, args[1:]...)
	return syscall.Exec(claudePath, argv, os.Environ())
}
