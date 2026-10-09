//go:build !windows

package launcher

import (
	"os"
	"syscall"
)

// execFunc 是进程替换函数的可注入 hook。生产路径 = syscall.Exec；
// test override 为 fakeExec 记录 argv0/argv/envv 后返回 nil，避免真的替换进程。
// 签名必须严格匹配 syscall.Exec。
var execFunc = syscall.Exec

// Launch 用 exec 替换当前进程启动 Claude Code。
func Launch(opts Options) error {
	claudePath, args, err := resolveClaude(opts)
	if err != nil {
		return err
	}

	argv := append([]string{claudePath}, args...)
	env := opts.Env
	if env == nil {
		env = os.Environ()
	}
	return execFunc(claudePath, argv, env)
}
