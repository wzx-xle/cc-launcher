package main

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"

	"cc-launcher/internal/collector"
	"cc-launcher/internal/sessiondir"
	"cc-launcher/internal/web"
)

// webHelp 是 `ccl web --help` 的输出。
const webHelp = `用法: ccl web [--rotate | --backfill | --open]

打印用量看板的访问 URL（含 token）。看板由 OTel daemon 在 :4318 上提供，
不新增端口、不新增进程。

选项:
    --rotate      轮换访问 token（旧的 URL 与已签发的 session 立即失效）
    --backfill    触发历史 session 的 cwd 回填（不限量）
    --open        用系统默认浏览器打开看板
    -h, --help    显示本帮助并退出

说明：
  - 看板 URL 形如 http://<本机IP>:4318/ui?token=<tok>；首次访问会种一个
    HttpOnly session cookie 并重定向到不带 token 的 /ui。
  - token 存在 ~/.cc-launcher/web-token（权限 0600）。
  - 直接用 claude 启动的会话没有 provider / 项目维度（显示"未知"）；
    通过 ccl 启动的会话才有。`

// registerWebFlags 把 web 子命令的 flag 注册到 fs，返回值供 runWeb 与
// cmd/cc-launcher/completion.go::completeAtWeb 共用。
//
// cli-shell-completion §D2：flag 改动自动同步——加新 flag 时（仅改本函数）
// completion 自动跟随。
func registerWebFlags(fs *flag.FlagSet) (rotate, backfill, open *bool) {
	rotate = fs.Bool("rotate", false, "轮换访问 token（旧的 URL 与 session 立即失效）")
	backfill = fs.Bool("backfill", false, "触发历史 session 的 cwd 回填（不限量）")
	open = fs.Bool("open", false, "用系统默认浏览器打开看板")
	return
}

// runWeb 是 `ccl web` 的入口。
//
// 行为（web-usage-stats-ui §D9 / spec §"ccl web is the entry point to the
// dashboard"）：
//   - 默认：确保 daemon 在跑（不在则 fork），打印带 token 的看板 URL
//   - --rotate：轮换 token，并提示旧 URL / 旧 session 失效
//   - --backfill：显式触发**不限量**的历史 cwd 回填
//   - --open：用系统浏览器打开看板 URL
//
// 关键约束：**打印 URL 与轮换 token 都不要求 daemon 在跑**。
func runWeb(args []string) error {
	fs := flag.NewFlagSet("web", flag.ContinueOnError)
	// 自己处理 help：flag package 默认会打 "Usage of web:" 并返回 ErrHelp，
	// 那样 `ccl web --help` 会以 exit 1 收场。
	fs.SetOutput(io.Discard)
	rotate, backfill, open := registerWebFlags(fs)
	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			fmt.Println(webHelp)
			return nil
		}
		return err
	}

	baseDir := cclLauncherBaseDir()
	tokenPath := filepath.Join(baseDir, web.TokenFileName)
	dbPath := filepath.Join(baseDir, "telemetry.db")

	// --rotate：重新生成 token 并覆盖。旧 token 与由它派生的全部 session
	// cookie 立即失效（cookie 的 HMAC key 就是 token；daemon 每次请求都
	// 重新读 token 文件 → 无需重启 daemon）。
	if *rotate {
		tok, err := web.RotateToken(tokenPath)
		if err != nil {
			return fmt.Errorf("轮换 token 失败: %w", err)
		}
		fmt.Println("✓ token 已轮换")
		fmt.Println("  旧的看板 URL 与此前签发的 session cookie 已全部失效，需要用下面的新 URL 重新访问。")
		printWebURLs(tok)
		return nil
	}

	// --backfill：不限量的历史 cwd 回填。独立进程直接操作 db 文件
	// （WAL 模式下与 daemon 的写入并发安全）。
	if *backfill {
		resolver, err := newCwdResolver()
		if err != nil {
			return fmt.Errorf("定位 Claude Code 会话目录失败: %w", err)
		}
		resolved, failed, err := collector.BackfillCwdInFile(dbPath, resolver, 0)
		if err != nil {
			return fmt.Errorf("cwd 回填失败: %w", err)
		}
		fmt.Printf("cwd 回填完成: resolved=%d failed=%d\n", resolved, failed)
		if failed > 0 {
			fmt.Println("  failed 的行是查不到会话记录的历史会话（Claude Code 已清理），保持「未知项目」。")
		}
		return nil
	}

	// token：不存在则生成（daemon 启动时也会做同样的事，两边读同一个文件）。
	tok, err := web.LoadOrCreateToken(tokenPath)
	if err != nil {
		return fmt.Errorf("准备访问 token 失败: %w", err)
	}

	// 确保 daemon 在跑（看板随 daemon 生死——design §D1 的代价由这一步消化）。
	if started, err := ensureDaemonRunning(); err != nil {
		fmt.Fprintf(os.Stderr, "⚠ OTel daemon 未就绪: %v（看板可能打不开，URL 先给你）\n", err)
	} else if started {
		fmt.Println("✓ OTel daemon 已拉起（监听 :4318）")
	}

	printWebURLs(tok)

	if *open {
		url := fmt.Sprintf("http://127.0.0.1:4318/ui?token=%s", tok)
		if err := openBrowser(url); err != nil {
			return fmt.Errorf("打开浏览器失败: %w", err)
		}
	}
	return nil
}

// printWebURLs 打印看板访问 URL（本机 LAN IP + 127.0.0.1 两个）。
func printWebURLs(tok string) {
	fmt.Println()
	fmt.Println("用量看板：")
	if ip := lanIPv4(); ip != "" {
		fmt.Printf("  http://%s:4318/ui?token=%s\n", ip, tok)
	}
	fmt.Printf("  http://127.0.0.1:4318/ui?token=%s\n", tok)
	fmt.Println()
	fmt.Println("  首次访问会把 token 换成 session cookie 并重定向到不带 token 的地址。")
}

// newCwdResolver 构造 Claude Code 会话目录的 cwd 反查器。
func newCwdResolver() (*sessiondir.Resolver, error) {
	dir, err := sessiondir.DefaultProjectsDir()
	if err != nil {
		return nil, err
	}
	return sessiondir.NewResolver(dir), nil
}

// lanIPv4 返回本机第一个非 loopback 的 IPv4 地址；找不到返回空串。
//
// 只读网卡地址，不发任何网络包（纯本地枚举）。
func lanIPv4() string {
	ifaces, err := net.Interfaces()
	if err != nil {
		return ""
	}
	for _, iface := range ifaces {
		if iface.Flags&net.FlagUp == 0 || iface.Flags&net.FlagLoopback != 0 {
			continue
		}
		addrs, err := iface.Addrs()
		if err != nil {
			continue
		}
		for _, addr := range addrs {
			var ip net.IP
			switch v := addr.(type) {
			case *net.IPNet:
				ip = v.IP
			case *net.IPAddr:
				ip = v.IP
			}
			if ip == nil || ip.IsLoopback() {
				continue
			}
			if v4 := ip.To4(); v4 != nil {
				return v4.String()
			}
		}
	}
	return ""
}
