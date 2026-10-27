package main

import (
	"strings"
	"testing"
)

// web-backfill-to-serve §D1 / §D4：--backfill 从 `ccl web` 挪到 `ccl serve`。
//
// 下面两条都是**纯 flag 解析层**的断言：都在任何 db / 文件访问之前就返回，
// 所以不需要 sandbox HOME。

// TestServeBackfill_MutexWithStop 断言 `ccl serve --backfill --stop` 报互斥错误。
func TestServeBackfill_MutexWithStop(t *testing.T) {
	var err error
	_ = captureStderr(t, func() {
		err = runServe([]string{"--backfill", "--stop"})
	})
	if err == nil {
		t.Fatal("runServe --backfill --stop 应报互斥错误")
	}
	if !strings.Contains(err.Error(), "互斥") {
		t.Errorf("错误信息应说明互斥, got %q", err.Error())
	}
}

// TestServeBackfill_MutexWithStatus 断言 `ccl serve --backfill --status` 也互斥。
func TestServeBackfill_MutexWithStatus(t *testing.T) {
	var err error
	_ = captureStderr(t, func() {
		err = runServe([]string{"--backfill", "--status"})
	})
	if err == nil {
		t.Fatal("runServe --backfill --status 应报互斥错误")
	}
}

// TestWebBackfill_Removed 断言 `ccl web --backfill` 不再被识别（回填已挪到 serve）。
func TestWebBackfill_Removed(t *testing.T) {
	var err error
	_ = captureStderr(t, func() {
		err = runWeb([]string{"--backfill"})
	})
	if err == nil {
		t.Fatal("ccl web --backfill 应报 unknown flag")
	}
	if !strings.Contains(err.Error(), "not defined") {
		t.Errorf("错误信息应提到 flag 未定义, got %q", err.Error())
	}
}
