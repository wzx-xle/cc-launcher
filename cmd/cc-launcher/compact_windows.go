//go:build windows

package main

import (
	"syscall"
	"unsafe"
)

// GetDiskFreeSpaceExW 不在标准库 syscall 里（只在 golang.org/x/sys/windows），
// 而那会给本仓库加一个新依赖 —— 这里用 LazyDLL 直接调系统 API。
var (
	modkernel32             = syscall.NewLazyDLL("kernel32.dll")
	procGetDiskFreeSpaceExW = modkernel32.NewProc("GetDiskFreeSpaceExW")
)

// availableDiskSpace 返回 path 所在卷的可用字节数。
//
// 用于 `ccl serve --compact` 压缩前的空间检查：VACUUM 需要约 2× db 的临时空间
// （db + wal + 备份），空间不够就别让它中途失败留下半成品。
func availableDiskSpace(path string) (uint64, error) {
	p, err := syscall.UTF16PtrFromString(path)
	if err != nil {
		return 0, err
	}
	var freeToCaller, total, totalFree uint64
	r, _, callErr := procGetDiskFreeSpaceExW.Call(
		uintptr(unsafe.Pointer(p)),
		uintptr(unsafe.Pointer(&freeToCaller)),
		uintptr(unsafe.Pointer(&total)),
		uintptr(unsafe.Pointer(&totalFree)),
	)
	if r == 0 {
		return 0, callErr
	}
	return freeToCaller, nil
}
