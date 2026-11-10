//go:build !windows

package main

import "syscall"

// availableDiskSpace 返回 path 所在文件系统的可用字节数。
//
// 用于 `ccl serve --compact` 压缩前的空间检查：VACUUM 需要约 2× db 的临时空间
// （db + wal + 备份），空间不够就别让它中途失败留下半成品。
func availableDiskSpace(path string) (uint64, error) {
	var st syscall.Statfs_t
	if err := syscall.Statfs(path, &st); err != nil {
		return 0, err
	}
	// Bsize 是 int64，转 uint64 前先确认非负（Bavail/Bsize 在某些平台是 int32）。
	bsize := uint64(st.Bsize)
	if st.Bsize < 0 {
		bsize = 0
	}
	return uint64(st.Bavail) * bsize, nil
}
