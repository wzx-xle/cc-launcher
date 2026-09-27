//go:build windows

package main

import "os/exec"

// configureSetpgid 在 Windows 上是 no-op（Windows 没有 PGID 概念）。
func configureSetpgid(cmd *exec.Cmd) {
	_ = cmd
}