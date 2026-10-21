package release

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"
)

// psScripts 是仓库里的 PowerShell 脚本清单。
//
// 为什么它们需要"字节级"的约定（而不是"能用就行"）：
//
//   - **UTF-8 BOM**：Windows PowerShell 5.1（Windows 默认的 powershell.exe）
//     读**无 BOM** 的 .ps1 时按 Windows-1252 解码 → 脚本里的中文注释变成
//     乱码 → 可能被解析成语法错误（引号/括号被误判）。PS 7 默认按 UTF-8 无 BOM
//     读，所以在 PS 7 上测**测不出来** —— 这就是 2026-10-21 用户在 Windows 上
//     实机撞到、而 CI 一直是绿的根因。
//   - **CRLF 行尾**：.gitattributes 声明 `*.ps1 text eol=crlf`（Windows 标准）。
//
// 代价是：**任何在 Linux 上用普通工具编辑 .ps1 的动作都可能悄悄破坏它**
// （Python 的通用换行转换会把 CRLF 变成 LF；不显式处理的话 BOM 也会丢）。
// 这条测试就是那个守卫。
var psScripts = []string{
	filepath.Join("..", "..", "bin", "bump-scoop.ps1"),
	filepath.Join("..", "..", "bin", "bump-version.ps1"),
}

// TestPowerShellScriptsHaveUTF8BOM 守住 BOM。
func TestPowerShellScriptsHaveUTF8BOM(t *testing.T) {
	bom := []byte{0xEF, 0xBB, 0xBF}
	for _, p := range psScripts {
		raw, err := os.ReadFile(p)
		if err != nil {
			t.Fatalf("读 %s 失败: %v", p, err)
		}
		if !bytes.HasPrefix(raw, bom) {
			t.Errorf("%s 缺 UTF-8 BOM —— Windows PowerShell 5.1 会按 Windows-1252 "+
				"解码，中文注释乱码 → 语法错误（PS 7 上测不出来，只能靠这条测试守）", p)
		}
	}
}

// TestPowerShellScriptsUseCRLF 守住行尾。
func TestPowerShellScriptsUseCRLF(t *testing.T) {
	for _, p := range psScripts {
		raw, err := os.ReadFile(p)
		if err != nil {
			t.Fatalf("读 %s 失败: %v", p, err)
		}
		crlf := bytes.Count(raw, []byte("\r\n"))
		lf := bytes.Count(raw, []byte("\n")) - crlf
		if lf != 0 {
			t.Errorf("%s 有 %d 处裸 LF（.gitattributes 声明 *.ps1 eol=crlf）", p, lf)
		}
		if crlf == 0 {
			t.Errorf("%s 完全没有 CRLF 行尾", p)
		}
	}
}
