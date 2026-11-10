package main

import (
	"fmt"
	"io"
	"net"
	"net/http"
	neturl "net/url"
	"os"
	"os/exec"
	"path/filepath"
	"time"
)

// daemonAddr 是 daemon 的监听地址（loopback）。
//
// **固定 4318**：OTel OTLP/HTTP 的标准端口。用户要改只能改
// OTEL_EXPORTER_OTLP_ENDPOINT（导出目标），daemon 自己监听的端口不变。
const daemonAddr = "127.0.0.1:4318"

// daemonBaseURL 返回 daemon 的 HTTP 基地址。
func daemonBaseURL() string { return "http://" + daemonAddr }

// probeDaemonAddr 探 :4318 是否可达（200ms timeout）。
//
// 返回 nil = 端口在用（daemon 可能活着）；返回 error = 端口空。
func probeDaemonAddr() error {
	conn, err := net.DialTimeout("tcp", daemonAddr, 200*time.Millisecond)
	if err != nil {
		return err
	}
	_ = conn.Close()
	return nil
}

// waitForDaemonReady 阻塞等 :4318 可达，最多 3s。
//
// 每 100ms 探一次；端口可达立即返回。
func waitForDaemonReady() error {
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if probeDaemonAddr() == nil {
			return nil
		}
		time.Sleep(100 * time.Millisecond)
	}
	return fmt.Errorf("daemon 在 3s 内未就绪")
}

// daemonExePath 返回要 fork 出来的 daemon 可执行文件路径。
//
// **做成变量是为了测试能覆盖它**：在 go test 里直接 fork os.Executable() 会 fork
// **测试二进制**——子进程拿到 "serve" 参数后会当成 -test.run 模式去跑测试 →
// 递归。测试覆盖成假 binary 就能安全地验证"compact 后确实拉起了新 daemon"。
var daemonExePath = os.Executable

// waitDaemonReadyFn 等 fork 出来的 daemon 就绪；做成变量供测试覆盖（真实实现
// 会探 :4318 端口，测试里换成"立即就绪"）。
var waitDaemonReadyFn = waitForDaemonReady

// forkDaemon fork 当前 binary 为 ccl serve 子进程。
//
// 决策（见 design.md §3）：
//   - SysProcAttr.Setpgid = true → daemon 在新进程组（独立于 ccl / Claude Code）
//   - stdin/stdout/stderr = nil → disown
//   - Wait = nil（不等）；ccl 自己探端口就绪
func forkDaemon() error {
	exe, err := daemonExePath()
	if err != nil {
		return fmt.Errorf("找自己可执行文件失败: %w", err)
	}

	cmd := exec.Command(exe, "serve")
	cmd.Stdin = nil
	configureSetpgid(cmd) // 跨平台：unix + windows 都设 best-effort

	// **stdout/stderr 必须落到文件**：自动拉起的 daemon 没有终端，日志若丢给
	// nil 就**无处可查**——而 serve 恰恰是出问题时最需要日志的地方（额度接口
	// 429、厂商改了字段、刷新失败……）。用户问"serve 有日志吗"时的答案是
	// "自动拉起的没有" → 这不是可接受的答案。
	//
	// 日志文件：~/.cc-launcher/serve.log（0600，可能含本机路径信息）。
	// 父进程退出时 Close 自己那份句柄 → 子进程那份仍然有效（Unix fork 与
	// Windows CreateProcess 都复制句柄）。
	if logFile, err := openServeLog(); err == nil {
		cmd.Stdout = logFile
		cmd.Stderr = logFile
		defer logFile.Close()
	} else {
		cmd.Stdout = nil
		cmd.Stderr = nil
	}

	if err := cmd.Start(); err != nil {
		return fmt.Errorf("fork ccl serve 失败: %w", err)
	}
	// 不 Wait——disown
	go func() { _ = cmd.Wait() }()
	return nil
}

// ensureDaemonRunning 探活 + 按需 fork；超时/失败降级（不返回 error）。
//
// 决策（见 design.md §10）：fork 失败/超时 → ccl 仍 exec Claude Code，OTel 静默失败。
//
// 返回的 started=true 表示本次 fork 了新 daemon；started=false 表示本来就在或 fork 失败。
func ensureDaemonRunning() (started bool, err error) {
	if probeDaemonAddr() == nil {
		return false, nil // 端口在用 → 已有 daemon
	}

	// 用户若配了远程 endpoint，ccl 不应拉本地 daemon；本地 OTel 没意义。
	// 但 daemon 仍可作为转发 hub 起到作用？——决策 14：daemon 在
	// CC_ORIG_OTEL_EXPORTER_OTLP_ENDPOINT 非空非 localhost 时启用 forwarder。
	// 所以即使有远程，也拉本地 daemon + forward；保留 stats 功能。
	if err := forkDaemon(); err != nil {
		fmt.Fprintf(os.Stderr, "OTel daemon 拉起失败: %v；usage stats 本次将不记录\n", err)
		return false, err
	}
	if err := waitForDaemonReady(); err != nil {
		fmt.Fprintf(os.Stderr, "OTel daemon 在 3s 内未就绪: %v\n", err)
		return true, err
	}
	return true, nil
}

// declareProviderToDaemon 告诉 daemon"本次会话用这个 provider"。
//
// **为什么需要**：额度数据的唯一消费者是状态栏，而状态栏显示的是**当前会话
// 那个 provider** 的额度。daemon 判断"谁在用"的唯一信号是 Claude Code 推来的
// OTel metrics —— 但那要等 Claude Code 起来并推完第一条（实测 daemon 重启后
// 整整 60 秒盲窗，60 秒 = Claude Code 的 OTel 推送周期）。
//
// **ccl 是唯一确知这次要用哪个 provider 的角色**（它在 exec Claude Code 之前
// 就选定了）→ 声明一次，额度当场可用。
//
// **fire-and-forget，绝不阻塞启动**：daemon 没起来 / 连不上 / 返回错误 → 一律
// **静默忽略**（Claude Code 的 OTel 是兜底路径，功能不会因此坏掉）。
//
// baseURL 是 daemon 的地址（测试用 httptest server 注入）。
func declareProviderToDaemon(baseURL, provider string) {
	if provider == "" {
		return
	}
	url := fmt.Sprintf("%s/ccl/activate?provider=%s", baseURL, neturl.QueryEscape(provider))
	client := &http.Client{Timeout: 500 * time.Millisecond}
	resp, err := client.Post(url, "", nil)
	if err != nil {
		return // 静默：daemon 没起来无所谓
	}
	defer resp.Body.Close()
	// 读完 body 才能复用连接（这里直接丢弃）。
	_, _ = io.Copy(io.Discard, resp.Body)
}

// cclLauncherBaseDir 返回 ~/.cc-launcher 路径（cmd 公共）。
func cclLauncherBaseDir() string {
	home, err := os.UserHomeDir()
	if err != nil {
		// 极端情况：HOME 不可用。回退到 cwd（仅 debug）。
		return ".cc-launcher"
	}
	return filepath.Join(home, ".cc-launcher")
}

// serveLogFileName 是 serve 日志的文件名（放在 ~/.cc-launcher/ 下）。
const serveLogFileName = "serve.log"

// serveLogMaxSize 是日志文件的大小上限（超过就截断重来）。
//
// 日志是"出事时看"的，不是归档品 → 保持小而新。2MB 足够看清最近几次刷新
// 的上下文（每轮每个 provider 一两行）。
const serveLogMaxSize = 2 << 20

// openServeLog 打开 serve 的日志文件（追加模式）。
//
// 文件不存在 → 建；超过上限 → **截断**（保留最新的内容，从头写）。
// 打开失败 → 返回 error（调用方退回"日志丢弃"的旧行为，不阻断 daemon 启动）。
func openServeLog() (*os.File, error) {
	dir := cclLauncherBaseDir()
	if dir == "" || dir == ".cc-launcher" {
		return nil, fmt.Errorf("无法定位 ~/.cc-launcher 目录")
	}
	if err := os.MkdirAll(dir, 0755); err != nil {
		return nil, fmt.Errorf("创建 %s 失败: %w", dir, err)
	}
	path := filepath.Join(dir, serveLogFileName)

	// 超限 → 截断（O_TRUNC）。
	flags := os.O_CREATE | os.O_WRONLY | os.O_APPEND
	if fi, err := os.Stat(path); err == nil && fi.Size() > serveLogMaxSize {
		flags = os.O_CREATE | os.O_WRONLY | os.O_TRUNC
	}
	return os.OpenFile(path, flags, 0600)
}
