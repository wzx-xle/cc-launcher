package web

import (
	"os/exec"
	"path/filepath"
	"testing"
)

// TestHighlightJS_UnitTests 跑前端高亮器的 node 单测。
//
// 为什么要有这个包装：
//
//   任务 8.1 要求"补单测（转义、字符串里的花括号不误切）"。这两条只能测 JS：
//   - **转义**是整个 provider 编辑器的 XSS 防线——凭证是明文 token，可能含
//     < & "，高亮层用 innerHTML 渲染（assets/editor.js:47）。转义逻辑被改坏
//     时，Go 侧任何断言都抓不住（Go 只能断言"文件被 embed 了"）
//   - **花括号不误切**是 tokenizer 唯一的边界条件，纯 JS 行为
//
// 为什么用 node 而不是用 Go 重写一份 tokenizer：
//
//   **两份实现必然漂移**——要测的正是浏览器里跑的那份代码。highlight.js 无 DOM
//   依赖，用 vm 沙箱加载即可，不需要 jsdom / 真浏览器。
//
// node 不在 PATH（精简容器 / 某些开发机）→ 跳过：前端单测不该让 `go test ./...`
// 在没装 node 的机器上红掉。
//
// CI（.github/workflows/ci.yml 的 ubuntu-latest）自带 node，所以 PR 上这条
// 一定会真跑。
func TestHighlightJS_UnitTests(t *testing.T) {
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("node 不在 PATH，跳过前端 JS 单测（highlight.js 的转义与 tokenizer）")
	}

	// 测试进程的 cwd = 包目录（internal/web），脚本路径相对它写。
	script := filepath.Join("jstest", "highlight.test.mjs")
	cmd := exec.Command(node, "--test", script)
	out, err := cmd.CombinedOutput()
	t.Logf("node --test %s:\n%s", script, out)
	if err != nil {
		t.Fatalf("前端 JS 单测失败: %v", err)
	}
}
