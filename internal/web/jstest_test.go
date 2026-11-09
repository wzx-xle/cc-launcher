package web

import (
	"os/exec"
	"path/filepath"
	"testing"
)

// TestFrontendJS_UnitTests 跑前端资源的 node 单测（internal/web/jstest/）。
//
// 为什么要有这个包装：
//
//	前端有些契约**Go 侧测不到**——
//	- **高亮的 HTML 转义**是 provider 编辑器的 XSS 防线（凭证是明文 token，
//	  可能含 < & "，高亮层用 innerHTML 渲染）。转义逻辑被改坏时，Go 只能断言
//	  "文件被 embed 了"，碰不到 JS 逻辑
//	- **额度渲染口径**（与终端状态栏逐条对齐）是**看不见**的契约：网页显示
//	  "6%"、状态栏显示 "6% 6/100 %"，界面上看不出问题，两个数却在打架
//
// 为什么用 node 而不是用 Go 重写一份：
//
//	**两份实现必然漂移**——要测的正是浏览器里跑的那份代码。这两个模块都无 DOM
//	依赖，用 vm 沙箱加载即可，不需要 jsdom / 真浏览器。
//
// node 不在 PATH（精简容器 / 某些开发机）→ 跳过：前端单测不该让 `go test ./...`
// 在没装 node 的机器上红掉。
//
// CI（.github/workflows/ci.yml 的 ubuntu-latest）自带 node，所以 PR 上这条
// 一定会真跑。
func TestFrontendJS_UnitTests(t *testing.T) {
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("node 不在 PATH，跳过前端 JS 单测（高亮转义 / 额度渲染口径）")
	}

	// 测试进程的 cwd = 包目录（internal/web），脚本路径相对它写。
	scripts, err := filepath.Glob(filepath.Join("jstest", "*.test.mjs"))
	if err != nil {
		t.Fatal(err)
	}
	if len(scripts) == 0 {
		t.Fatal("jstest/ 下没找到 .test.mjs —— 前端单测没被接上")
	}
	cmd := exec.Command(node, append([]string{"--test"}, scripts...)...)
	out, err := cmd.CombinedOutput()
	t.Logf("node --test %v:\n%s", scripts, out)
	if err != nil {
		t.Fatalf("前端 JS 单测失败: %v", err)
	}
}
