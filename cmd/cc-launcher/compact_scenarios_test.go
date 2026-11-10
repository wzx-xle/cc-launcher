package main

import (
	"context"
	"database/sql"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
)

// fakeDaemonScript 写一个"假 daemon"脚本：任意参数都能跑，把自己的 pid 写进
// PIDFILE 然后睡。
//
// 为什么需要它：真 daemon 会占 :4318 端口，测试环境里不可用（也会和用户正在
// 跑的 daemon 打架）。假 daemon 只需要能被 fork、能被 SIGTERM 杀掉、能留下
// pidfile —— 足以验证 compact 的"停旧 → 拉新"编排。
func fakeDaemonScript(t *testing.T, dir string) string {
	t.Helper()
	path := filepath.Join(dir, "fake-daemon.sh")
	script := "#!/bin/sh\n" +
		"echo $$ > \"" + filepath.Join(dir, "fake.pid") + "\"\n" +
		"sleep 30\n"
	if err := os.WriteFile(path, []byte(script), 0700); err != nil {
		t.Fatal(err)
	}
	return path
}

// pidAlive 报告 pid 是否还活着。
//
// **zombie 算"已死"**：zombie 进程仍然存在于进程表里，kill(pid, 0) 对它照样
// 返回成功——只看信号投递会把"已经被杀掉的进程"判成"还活着"。
func pidAlive(pid int) bool {
	if pid <= 0 {
		return false
	}
	p, err := os.FindProcess(pid)
	if err != nil {
		return false
	}
	if err := p.Signal(syscall.Signal(0)); err != nil {
		return false
	}
	// /proc/<pid>/stat 第 3 个字段是状态字符，'Z' = zombie
	if b, err := os.ReadFile("/proc/" + strconv.Itoa(pid) + "/stat"); err == nil {
		fields := strings.Fields(string(b))
		if len(fields) > 2 && fields[2] == "Z" {
			return false
		}
	}
	return true
}

// TestServeCompact_RunningDaemonStoppedAndRestarted 断言 daemon 在跑时
// compact 会停掉它、压缩、再拉起一个新的。
//
// 对应 spec scenario："a running daemon is stopped, compacted and started again"。
func TestServeCompact_RunningDaemonStoppedAndRestarted(t *testing.T) {
	dir := t.TempDir()
	stateDir := filepath.Join(dir, ".cc-launcher")
	if err := os.MkdirAll(stateDir, 0700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("HOME", dir)
	t.Setenv("USERPROFILE", dir)

	// 造一个"正在跑"的 daemon：假脚本 + pidfile
	script := fakeDaemonScript(t, dir)
	cmd := exec.Command(script)
	// **必须自己起进程组**：stopDaemon 用 kill(-pid, SIGTERM) 杀**整个进程组**
	// （PGID == pid）——真实 daemon 由 forkDaemon 带 Setpgid=true 起，PGID==pid。
	// 假 daemon 若继承测试进程的进程组，kill(-pid) 就杀不到它。
	configureSetpgid(cmd)
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	// **后台回收**：被 SIGTERM 杀掉后进程会变成 zombie，而 zombie 仍然存在
	// （kill(pid,0) 对 zombie 依然成功）→ 不回收的话，"进程还活着吗"和
	// "进程组还在吗"的探测都会误判。
	go cmd.Wait()
	defer cmd.Process.Kill()
	oldPid := cmd.Process.Pid
	pidPath := filepath.Join(stateDir, "daemon.pid")
	if err := os.WriteFile(pidPath, []byte(strconv.Itoa(oldPid)+"\n"), 0644); err != nil {
		t.Fatal(err)
	}
	// 等假 daemon 写下自己的 pid
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if _, err := os.Stat(filepath.Join(dir, "fake.pid")); err == nil {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if !pidAlive(oldPid) {
		t.Fatalf("假 daemon 没起来（pid=%d）", oldPid)
	}

	// 造 db
	dbPath := filepath.Join(stateDir, "telemetry.db")
	mkCompactTestDB(t, dbPath, 30)

	// 覆盖注入点：fork 出来的是假脚本；就绪探测立即返回
	origExe, origReady := daemonExePath, waitDaemonReadyFn
	daemonExePath = func() (string, error) { return script, nil }
	waitDaemonReadyFn = func() error { return nil }
	defer func() { daemonExePath, waitDaemonReadyFn = origExe, origReady }()

	if err := runCompactInner(); err != nil {
		t.Fatalf("runCompactInner: %v", err)
	}

	// 旧 daemon 必须被停掉
	if pidAlive(oldPid) {
		t.Errorf("旧 daemon (pid=%d) 还活着，compact 应停掉它", oldPid)
	}
	// 新 daemon 必须被拉起（假脚本会写新 pid）
	newPid := waitFakePid(t, filepath.Join(dir, "fake.pid"), oldPid)
	if newPid == oldPid {
		t.Errorf("新 daemon 的 pid 与旧相同（%d），说明没重新拉起", newPid)
	}
	if !pidAlive(newPid) {
		t.Errorf("新 daemon (pid=%d) 没活着", newPid)
	}
}

// waitFakePid 等假 daemon 写下**新的** pid 并返回它。
//
// 为什么必须等"变化"而不是"文件存在"：新 daemon 是 forkDaemon **异步**拉起
// 的，测试读到 fake.pid 时新进程可能还没跑到那行 echo → 文件里还是旧 pid。
func waitFakePid(t *testing.T, path string, oldPid int) int {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		b, err := os.ReadFile(path)
		if err == nil {
			if pid, err := strconv.Atoi(strings.TrimSpace(string(b))); err == nil && pid > 0 && pid != oldPid {
				return pid
			}
		}
		time.Sleep(30 * time.Millisecond)
	}
	t.Fatalf("假 daemon 没写新 pid（%s，旧 pid=%d）", path, oldPid)
	return 0
}

// TestServeCompact_FailedCompactionLeavesDBUsable 断言压缩失败时报错、且原
// 数据仍可读（压缩失败不能损坏数据）。
//
// 对应 spec scenario："a failed compaction leaves the database usable"。
//
// 怎么让压缩失败：把 db 文件设成只读 → VACUUM 需要写 → 失败。而只读文件
// 里的数据仍能读出来 —— 这正是要验证的"失败不毁数据"。
func TestServeCompact_FailedCompactionLeavesDBUsable(t *testing.T) {
	dir := t.TempDir()
	stateDir := filepath.Join(dir, ".cc-launcher")
	if err := os.MkdirAll(stateDir, 0700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("HOME", dir)
	t.Setenv("USERPROFILE", dir)

	dbPath := filepath.Join(stateDir, "telemetry.db")
	mkCompactTestDB(t, dbPath, 10)

	// 记下原始行数
	before := compactRowCount(t, dbPath)

	// 设成只读 → VACUUM 写不进去
	if err := os.Chmod(dbPath, 0444); err != nil {
		t.Fatal(err)
	}
	defer os.Chmod(dbPath, 0644)

	err := runCompactInner()
	if err == nil {
		t.Fatal("只读 db 的压缩应报错")
	}
	if !strings.Contains(err.Error(), "VACUUM") {
		t.Errorf("错误信息应提到 VACUUM, got %q", err.Error())
	}

	// 数据必须还在、还能读
	after := compactRowCount(t, dbPath)
	if after != before {
		t.Errorf("压缩失败后行数变了: %d -> %d", before, after)
	}
}

// mkCompactTestDB 造一个带 n 行 metrics 的 db。
func mkCompactTestDB(t *testing.T, path string, n int) {
	t.Helper()
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	db.Exec(`CREATE TABLE sessions (id TEXT PRIMARY KEY, started_at INTEGER NOT NULL,
		ended_at INTEGER, provider TEXT NOT NULL DEFAULT '', provider_endpoint TEXT NOT NULL DEFAULT '',
		cwd TEXT NOT NULL DEFAULT '', model TEXT NOT NULL DEFAULT '')`)
	db.Exec(`CREATE TABLE metrics (id INTEGER PRIMARY KEY AUTOINCREMENT, session_id TEXT NOT NULL,
		name TEXT NOT NULL, value REAL NOT NULL, labels TEXT NOT NULL DEFAULT '{}', captured_at INTEGER NOT NULL)`)
	for i := 0; i < n; i++ {
		db.Exec(`INSERT INTO metrics (session_id, name, value, labels, captured_at) VALUES (?,?,?,?,?)`,
			"s1", "claude_code.token.usage", float64(i), `{"type":"input"}`, time.Now().UnixMilli())
	}
}

// compactRowCount 读出 db 里的 metrics 行数。
func compactRowCount(t *testing.T, path string) int {
	t.Helper()
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	var n int
	db.QueryRow(`SELECT COUNT(*) FROM metrics`).Scan(&n)
	return n
}

// 确认 context 被用到（编译期检查 import 是否必要）。
var _ = context.Background
