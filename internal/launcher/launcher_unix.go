//go:build !windows

package launcher

import (
	"os"
	"syscall"
)

// Launch 用 exec 替换当前进程启动 Claude Code。
func Launch(opts Options) error {
	claudePath, args, err := resolveClaude(opts)
	if err != nil {
		return err
	}

	argv := append([]string{claudePath}, args...)
	return syscall.Exec(claudePath, argv, os.Environ())
}
