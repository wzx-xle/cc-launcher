//go:build !windows

package main

import (
	"os/exec"
	"syscall"
)

// configureSetpgid 在 Unix 上把子进程设到新 PGID。
//
// ccl fork ccl serve 后做 syscall.Exec(claude)；exec 后 ccl 的 PGID
// 变成 claude 的 PGID。daemon 在新 PGID → ccl stop 不会误杀 claude。
func configureSetpgid(cmd *exec.Cmd) {
	if cmd.SysProcAttr == nil {
		cmd.SysProcAttr = &syscall.SysProcAttr{}
	}
	cmd.SysProcAttr.Setpgid = true
}