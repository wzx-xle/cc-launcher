package release

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// changelogFixture 是一份精简版 CHANGELOG，覆盖真实文件里会遇到的几种形态：
//   - [Unreleased] 段
//   - `## [0.14.0] - 2026-10-20 — name`（**不带 v**，本仓约定）
//   - `## [v0.13.3] - ...`（带 v 的写法也要认）
//   - **正文里出现的字面量** "## [v0.14.0]"（不在行首 → 不能被误当标题）
//   - H3 子段
const changelogFixture = `# Changelog

## [Unreleased]

### Fixes
- 某个还没发布的修复

## [0.14.0] - 2026-10-20 — stats-session-start-time-fix

### Fixes
- **某 bug 的说明文字**：提取正则是 ## [v0.14.0] + version → 只能匹配带 v 的写法,
  而本仓 CHANGELOG 标题格式是 ## [0.14.0] → 永远匹配不上

### OpenSpec
- archive stats-session-start-time-fix (2026-10-20)

## [0.13.3] - 2026-10-18 — release-notes-changelog-injection

### Features
- 上一版的内容

## [v0.13.2] - 2026-10-18 — 带 v 的标题写法
`

func TestExtractChangelogSection(t *testing.T) {
	cases := []struct {
		name      string
		version   string
		wantFirst string
		wantLast  string
		wantErr   bool
	}{
		{
			name:      "不带 v 的版本号",
			version:   "0.14.0",
			wantFirst: "## [0.14.0] - 2026-10-20 — stats-session-start-time-fix",
			wantLast:  "- archive stats-session-start-time-fix (2026-10-20)",
		},
		{
			name:      "带 v 的版本号等价",
			version:   "v0.14.0",
			wantFirst: "## [0.14.0] - 2026-10-20 — stats-session-start-time-fix",
			wantLast:  "- archive stats-session-start-time-fix (2026-10-20)",
		},
		{
			name:      "标题带 v 的写法也认",
			version:   "0.13.2",
			wantFirst: "## [v0.13.2] - 2026-10-18 — 带 v 的标题写法",
			wantLast:  "## [v0.13.2] - 2026-10-18 — 带 v 的标题写法",
		},
		{
			name:      "段落到下一个 H2 之前为止(不吞下一段)",
			version:   "0.13.3",
			wantFirst: "## [0.13.3] - 2026-10-18 — release-notes-changelog-injection",
			wantLast:  "- 上一版的内容",
		},
		{
			name:    "找不到的版本必须报错(不能静默 skip)",
			version: "9.9.9",
			wantErr: true,
		},
		{
			name:    "空版本号报错",
			version: "  ",
			wantErr: true,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := ExtractChangelogSection(changelogFixture, tc.version)
			if tc.wantErr {
				if err == nil {
					t.Fatalf("期望报错,实际返回了:\n%s", got)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			lines := strings.Split(got, "\n")
			if lines[0] != tc.wantFirst {
				t.Errorf("首行: want %q, got %q", tc.wantFirst, lines[0])
			}
			if last := lines[len(lines)-1]; last != tc.wantLast {
				t.Errorf("末行: want %q, got %q", tc.wantLast, last)
			}
		})
	}
}

// TestExtractChangelogSection_IgnoresInlineLiteral 覆盖 2026-10-21 踩到的第二个
// bug：正则在正文里命中了字面量 "## [v0.14.0]"（本仓 CHANGELOG 自己描述这个
// bug 时就写了它），抽出一段散文当标题段 → 写进 release body。
//
// 修复手段是**行首锚点**：只有行首的 "## [...]" 才算 H2 标题。
func TestExtractChangelogSection_IgnoresInlineLiteral(t *testing.T) {
	got, err := ExtractChangelogSection(changelogFixture, "0.14.0")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.HasPrefix(got, "## [0.14.0] - 2026-10-20") {
		t.Fatalf("必须从真正的 H2 标题开始,got:\n%s", got)
	}
	// 反例：从正文里的字面量开始抽（抽出的是半段散文，段尾被下一个 H2 截断）
	if strings.HasPrefix(got, "## [v0.14.0] + version") {
		t.Errorf("提取结果从正文里的字面量开始了(说明行首锚点失效):\n%s", got)
	}
	// 注：正文里那段字面量**出现在段内**是正常的——它本来就是这一段的内容；
	// 要防的是把它当成"标题起点"。
}

// TestExtractChangelogSection_RepoChangelog 自检：拿仓库真实的 CHANGELOG.md，
// 抽出其中最新的一个版本段 —— 保证"CHANGELOG 的写法"与"提取器的约定"不会
// 再次漂移（这正是 2026-10-21 三个 bug 里的第一个）。
func TestExtractChangelogSection_RepoChangelog(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join("..", "..", "CHANGELOG.md"))
	if err != nil {
		t.Fatalf("读仓库 CHANGELOG.md 失败: %v", err)
	}
	body := string(raw)

	// 取最新的版本号（第一个 `## [X.Y.Z]` 形式的 H2，跳过 [Unreleased]）
	re := regexp.MustCompile(`(?m)^## \[v?(\d+\.\d+\.\d+)\]`)
	loc := re.FindStringSubmatchIndex(body)
	if loc == nil {
		t.Fatal("仓库 CHANGELOG.md 里找不到版本号标题")
	}
	version := body[loc[2]:loc[3]]

	section, err := ExtractChangelogSection(body, version)
	if err != nil {
		t.Fatalf("提取仓库 CHANGELOG 的 %s 段失败: %v", version, err)
	}
	if !strings.HasPrefix(section, "## [") {
		t.Errorf("提取段应以 H2 标题开头,got:\n%s", section)
	}
	if !strings.Contains(section, version) {
		t.Errorf("提取段里应含版本号 %s", version)
	}
	// 段落里至少要有一个 H3 子段(本仓 CHANGELOG 约定:每个版本段按
	// Fixes / Features / OpenSpec 等分类)
	if !strings.Contains(section, "\n### ") {
		t.Errorf("提取段里应含 H3 子段,got:\n%s", section)
	}
}

func TestExtractSHA256Marker(t *testing.T) {
	const hex = "244ce9e2382c336afab3e1cd57c9c8d0d3957746b97e8b513b6a7bb53f043036"

	cases := []struct {
		name    string
		body    string
		want    string
		wantErr bool
	}{
		{
			name: "新格式 marker(带文件名)",
			body: "## [0.14.0]\n\n<!-- sha256:cc-launcher-windows-amd64.zip:" + hex + " -->",
			want: hex,
		},
		{
			name: "旧格式 marker(auto-hash)",
			body: "\n\n<!-- sha256:auto-hash:" + hex + " -->",
			want: hex,
		},
		{
			name: "body 里有 CHANGELOG 段 + marker",
			body: "## [0.14.0]\n\n### Fixes\n- 提到 sha256:auto-hash:HEX 这种占位\n\n<!-- sha256:cc-launcher-windows-amd64.zip:" + hex + " -->",
			want: hex,
		},
		{
			name:    "没有 marker",
			body:    "## [0.14.0]\n\n### Fixes\n- 没有 marker 的 body",
			wantErr: true,
		},
		{
			name:    "占位符不是 64 位 hex 不算 marker",
			body:    "<!-- sha256:cc-launcher-windows-amd64.zip:HEX -->",
			wantErr: true,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := ExtractSHA256Marker(tc.body)
			if tc.wantErr {
				if err == nil {
					t.Fatalf("期望报错,实际返回 %q", got)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got != tc.want {
				t.Errorf("hash: want %q, got %q", tc.want, got)
			}
		})
	}
}
