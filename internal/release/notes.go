// Package release 实现 release 阶段需要的文本处理逻辑。
//
// 为什么这些逻辑放在 Go 包里、而不是留在 .github/workflows/release.yml 的
// inline python / shell 里：
//
//   - release workflow 的逻辑此前**零测试覆盖**。2026-10-21 连续踩了三个 bug：
//     ① 提取正则的版本前缀与 CHANGELOG 标题格式不一致（`## [v0.X.Y]` vs
//     `## [0.X.Y]`）→ 注入静默 skip，三个版本都没生效；
//     ② 正则缺行首锚点 → 会把 CHANGELOG 正文里出现的字面量（形如
//     "## [v0.14.0]" 的说明文字）当成标题，抽出半段散文写进 release body；
//     ③ 未认证调 GitHub API 撞 60/hr 限流。
//   - 其中 ①② 是"写个 20 行单测就能当场抓住"的类型。抽进 Go 包后，
//     `go test ./...` 就能覆盖，PR 期 CI 也能自动跑。
//
// 调用方（release workflow / build.sh）都走同一份实现，避免两处解析逻辑漂移。
package release

import (
	"fmt"
	"regexp"
	"strings"
)

// ExtractChangelogSection 从 CHANGELOG.md 全文里提取某个版本对应的整段
// （H2 标题行 + 其下所有 H3 子段），到下一个 H2 之前为止。
//
// version 允许带 "v" 前缀（"v0.14.0" 与 "0.14.0" 等价）。
//
// 只认**行首**的 H2 标题：CHANGELOG 正文里出现的字面量（"…只能匹配
// `## [v0.14.0]`…"）不在行首，不会被误当成标题。
//
// 找不到对应段 → 返回 error（调用方应 fail，而不是静默跳过——静默跳过正是
// ① 这个 bug 藏了三个版本的原因）。
func ExtractChangelogSection(body, version string) (string, error) {
	v := strings.TrimPrefix(strings.TrimSpace(version), "v")
	if v == "" {
		return "", fmt.Errorf("版本号为空")
	}

	// 行首 H2 标题：## [v0.14.0] 后必须行尾或空白
	heading := regexp.MustCompile(`(?m)^## \[v?` + regexp.QuoteMeta(v) + `\](?:[ \t]|$)`)
	loc := heading.FindStringIndex(body)
	if loc == nil {
		return "", fmt.Errorf("CHANGELOG 里找不到 ## [%s] 段", v)
	}

	// 段落到下一个 H2（行首 "## ["）为止
	rest := body[loc[1]:]
	next := regexp.MustCompile(`(?m)^## \[`).FindStringIndex(rest)
	section := rest
	if next != nil {
		section = rest[:next[0]]
	}

	return strings.TrimRight(body[loc[0]:loc[1]]+section, " \t\r\n"), nil
}

// LatestChangelogVersion 返回 CHANGELOG.md 里最新的版本号（不含 "v" 前缀）。
//
// 用途：release workflow 的 dry-run 模式没有真实 tag（ref_name 是分支名），
// 需要一个"看起来像版本号"的值来验证 CHANGELOG 段提取是否正常——取 CHANGELOG
// 里最新的版本段即可。
//
// 找不到任何 `## [X.Y.Z]` 形式的标题 → 返回 error。
func LatestChangelogVersion(body string) (string, error) {
	re := regexp.MustCompile(`(?m)^## \[v?(\d+\.\d+\.\d+)\]`)
	m := re.FindStringSubmatch(body)
	if m == nil {
		return "", fmt.Errorf("CHANGELOG 里找不到版本号标题")
	}
	return m[1], nil
}

// ExtractSHA256Marker 从 release body 文本里提取 SHA256 marker 的十六进制值。
//
// marker 格式（release-notes-changelog-injection 之后）：
//
//	<!-- sha256:cc-launcher-windows-amd64.zip:244ce9e2... -->
//
// 旧格式（v0.12.0 ~ v0.13.2）也兼容：
//
//	<!-- sha256:auto-hash:e3f2fbc1... -->
//
// 文件名部分用 [^:\s]+ 匹配（任意非冒号非空白串），不绑死具体文件名。
// body 里有多个 marker 时取第一个。找不到 → 返回 error。
func ExtractSHA256Marker(body string) (string, error) {
	re := regexp.MustCompile(`sha256:[^:\s]+:([a-f0-9]{64})`)
	m := re.FindStringSubmatch(body)
	if m == nil {
		return "", fmt.Errorf("release body 里找不到 sha256 marker")
	}
	return m[1], nil
}
