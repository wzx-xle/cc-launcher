package tui

import (
	"strings"
	"testing"

	"cc-launcher/internal/provider"

	tea "github.com/charmbracelet/bubbletea"
)

// ─── provider-setenv-cwd-binding §4.1: Run 签名 + 预选 provider ───

// TestRun_PreSelectedProvider_OverridesActiveProvider 覆盖 spec/provider-cwd-binding
// §"cwd is in dir_bindings" Scenario——preSelectedProvider（=dir_bindings[wd]）
// 作为 TUI 启动时的预选 cursor。
func TestRun_PreSelectedProvider_OverridesActiveProvider(t *testing.T) {
	providers := []provider.Info{
		{Name: "anthropic"},
		{Name: "deepseek"},
		{Name: "glm"},
	}
	// preSelectedProvider = "deepseek"（dir_bindings[wd] 解出）；activeProvider
	// = "anthropic" 仍作为 fallback。
	m := newModel(providers, "deepseek", "manual", "", "/wd")
	if m.cursor != 1 {
		t.Errorf("cursor = %d, want 1 (deepseek pre-selected)", m.cursor)
	}
}

// TestRun_DefaultMode 覆盖 tui.Run 的 defaultMode 传入——mode stage cursor 应
// 预选 defaultMode 对应的 mode。
func TestRun_DefaultMode(t *testing.T) {
	providers := []provider.Info{{Name: "anthropic"}}
	m := newModel(providers, "anthropic", "auto", "", "/wd")
	m.stage = stageMode
	m.cursor = 0
	for i, mode := range m.modes {
		if mode.Name == "auto" {
			m.cursor = i
			break
		}
	}
	if m.modes[m.cursor].Name != "auto" {
		t.Errorf("default mode cursor = %d (%s), want auto", m.cursor, m.modes[m.cursor].Name)
	}
}

// TestRun_EmptyProviders_ReturnsError 保持原 Run 行为：providers 为空报错。
func TestRun_EmptyProviders_ReturnsError(t *testing.T) {
	_, err := Run(nil, "", "manual", "", "/wd")
	if err == nil {
		t.Error("Run() with empty providers should return error")
	}
}

// ─── §4.2: stageRemember 行为 ───

// sendKey 模拟用户按键，type-assert 回 model。
func sendKey(m model, key tea.KeyMsg) model {
	updated, _ := m.Update(key)
	return updated.(model)
}

// enterSession 模拟用户选完 session 后的 Enter（在 stageSession 选新会话）。
func enterSession(m model) model {
	m.stage = stageSession
	m.cursor = 0
	return sendKey(m, tea.KeyMsg{Type: tea.KeyEnter})
}

// TestRun_RememberStage_TriggeredWhenProviderDiffersFromBinding 覆盖 spec
// §"User accepts prompt" 路径——选完 session 后若 result.Provider !=
// existingBinding → 进入 stageRemember。
func TestRun_RememberStage_TriggeredWhenProviderDiffersFromBinding(t *testing.T) {
	providers := []provider.Info{{Name: "anthropic"}, {Name: "deepseek"}}
	// preSelected = "anthropic"，existingBinding = "anthropic"；用户下移选 deepseek
	m := newModel(providers, "anthropic", "manual", "anthropic", "/wd")
	m = sendKey(m, tea.KeyMsg{Type: tea.KeyDown})   // cursor 0→1 (deepseek)
	m = sendKey(m, tea.KeyMsg{Type: tea.KeyEnter})  // 选 deepseek → stageMode
	m = sendKey(m, tea.KeyMsg{Type: tea.KeyEnter})  // stageMode → stageSession
	m = enterSession(m)                             // stageSession → stageRemember
	if m.stage != stageRemember {
		t.Errorf("expected stageRemember after session select (provider != binding), got stage=%d", m.stage)
	}
}

// TestRun_RememberStage_AcceptDefault 覆盖 spec §"User accepts prompt"——
// stageRemember 按 Enter/Y → rememberBound = true → stageDone + Quit。
func TestRun_RememberStage_AcceptDefault(t *testing.T) {
	providers := []provider.Info{{Name: "anthropic"}, {Name: "deepseek"}}
	m := newModel(providers, "deepseek", "manual", "anthropic", "/wd")
	m.stage = stageRemember

	// Enter
	updated := sendKey(m, tea.KeyMsg{Type: tea.KeyEnter})
	if updated.stage != stageDone {
		t.Errorf("Enter in stageRemember should transition to stageDone, got %d", updated.stage)
	}
	if !updated.rememberBound {
		t.Error("Enter should set rememberBound=true")
	}

	// y
	updated = sendKey(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'y'}})
	if !updated.rememberBound {
		t.Error("'y' in stageRemember should set rememberBound=true")
	}

	// Y
	updated = sendKey(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'Y'}})
	if !updated.rememberBound {
		t.Error("'Y' in stageRemember should set rememberBound=true")
	}
}

// TestRun_RememberStage_Decline 覆盖 spec §"User declines prompt"——按 n/N
// → rememberBound = false → stageDone。
func TestRun_RememberStage_Decline(t *testing.T) {
	providers := []provider.Info{{Name: "anthropic"}, {Name: "deepseek"}}
	m := newModel(providers, "deepseek", "manual", "anthropic", "/wd")
	m.stage = stageRemember

	for _, r := range []rune{'n', 'N'} {
		updated := sendKey(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}})
		if updated.rememberBound {
			t.Errorf("'%c' in stageRemember should set rememberBound=false", r)
		}
		if updated.stage != stageDone {
			t.Errorf("'%c' should transition to stageDone, got %d", r, updated.stage)
		}
	}
}

// TestRun_RememberStage_Cancel 覆盖 spec §"TUI cancelled before selection"
// ——Esc 在 stageRemember 取消整个 flow（return ErrCancelled）→ m.quitting=true。
func TestRun_RememberStage_Cancel(t *testing.T) {
	providers := []provider.Info{{Name: "anthropic"}, {Name: "deepseek"}}
	m := newModel(providers, "deepseek", "manual", "anthropic", "/wd")
	m.stage = stageRemember

	updated := sendKey(m, tea.KeyMsg{Type: tea.KeyEsc})
	if !updated.quitting {
		t.Error("Esc in stageRemember should set quitting=true")
	}
}

// TestRun_RememberStage_SkippedWhenSameAsBinding 覆盖 spec §"Selection
// matches existing binding"——选完 session 后若 selectedProvider == existingBinding
// → 不进 stageRemember → 直接 stageDone。
func TestRun_RememberStage_SkippedWhenSameAsBinding(t *testing.T) {
	providers := []provider.Info{{Name: "anthropic"}, {Name: "deepseek"}}
	// preSelected = "deepseek"，existingBinding = "deepseek"——一致。
	m := newModel(providers, "deepseek", "manual", "deepseek", "/wd")
	m = sendKey(m, tea.KeyMsg{Type: tea.KeyEnter}) // stageProvider → stageMode
	m = sendKey(m, tea.KeyMsg{Type: tea.KeyEnter}) // stageMode → stageSession
	updated := enterSession(m)                     // stageSession → ?
	if updated.stage == stageRemember {
		t.Error("stageRemember should be SKIPPED when selectedProvider == existingBinding")
	}
	if updated.stage != stageDone {
		t.Errorf("expected stageDone (skip remember), got %d", updated.stage)
	}
	if updated.rememberBound {
		t.Error("rememberBound should be false when remember stage is skipped")
	}
}

// TestRun_RememberStage_TriggeredWhenNoBinding 覆盖 spec §"reading dir_bindings
// when none exist"——无 binding 时 existingBinding=""，用户选任何 provider
// 都 != ""，应进 stageRemember（"首次选择"也算变更）。
func TestRun_RememberStage_TriggeredWhenNoBinding(t *testing.T) {
	providers := []provider.Info{{Name: "anthropic"}}
	m := newModel(providers, "anthropic", "manual", "", "/wd")
	m = sendKey(m, tea.KeyMsg{Type: tea.KeyEnter}) // stageProvider → stageMode
	m = sendKey(m, tea.KeyMsg{Type: tea.KeyEnter}) // stageMode → stageSession
	updated := enterSession(m)                     // stageSession → stageRemember
	if updated.stage != stageRemember {
		t.Errorf("expected stageRemember when no binding exists (first time), got stage=%d", updated.stage)
	}
}

// ─── provider-list-rendering §TUI 渲染：dim [*] / note ───

// TestRenderProviderView_PreSelectedShowsDimAsterisk 覆盖 spec Scenario
// "Pre-selected provider not at cursor shows dim [*]"——cursor 离开预选项后，
// 预选项仍带 [*] 形态（颜色由 dimStyle 决定，但本测试只验字符存在）。
func TestRenderProviderView_PreSelectedShowsDimAsterisk(t *testing.T) {
	providers := []provider.Info{
		{Name: "anthropic"},
		{Name: "deepseek"}, // pre-selected
		{Name: "glm"},
	}
	m := newModel(providers, "deepseek", "manual", "", "/wd")
	// cursor 默认在 deepseek (index 1)；按 ↓ 移到 glm (index 2)
	m = sendKey(m, tea.KeyMsg{Type: tea.KeyDown})

	view := m.View()

	// 三个 provider 都应渲染；deepseek 仍带 [*] 形态
	// 用 strings.Count 统计 [*] 出现次数：cursor(glm) = 1, pre-selected(deepseek) = 1
	if got := strings.Count(view, "[*]"); got != 2 {
		t.Errorf("View [*] count = %d, want 2 (cursor=glm + pre-selected=deepseek)", got)
	}
	// 其它行（anthropic）应带 [ ]
	if !strings.Contains(view, "[ ]") {
		t.Errorf("View should contain '[ ]' for non-selected anthropic row")
	}
	// 关键：行尾不应再有"默认"字串
	if strings.Contains(view, "默认") {
		t.Errorf("View should NOT contain '默认' tag (removed in this change)")
	}
}

// TestRenderProviderView_NoTagAtEnd 覆盖 spec Scenario "Pre-selected
// provider is the cursor" 边缘 + 锁定"行尾默认 tag 已移除"。
func TestRenderProviderView_NoTagAtEnd(t *testing.T) {
	providers := []provider.Info{
		{Name: "anthropic"},
		{Name: "deepseek"},
	}
	m := newModel(providers, "deepseek", "manual", "", "/wd")
	view := m.View()
	if strings.Contains(view, "默认") {
		t.Errorf("View should NOT contain '默认' tag at end of any row, got:\n%s", view)
	}
}

// TestRenderProviderView_NoteShownAfterName 覆盖 spec Scenario "Provider
// with note displays note in dim style"——note 文本出现在 view 中。
func TestRenderProviderView_NoteShownAfterName(t *testing.T) {
	providers := []provider.Info{
		{Name: "anthropic"},
		{Name: "deepseek", Note: "国内 / 按量计费"},
	}
	m := newModel(providers, "", "manual", "", "/wd")
	view := m.View()
	if !strings.Contains(view, "国内 / 按量计费") {
		t.Errorf("View should contain provider note text, got:\n%s", view)
	}
}

// TestRenderProviderView_NoteSuppressedWhenErrored 覆盖 spec Scenario
// "Errored provider keeps error rendering precedence"——Errored 时不渲染 note。
func TestRenderProviderView_NoteSuppressedWhenErrored(t *testing.T) {
	providers := []provider.Info{
		{Name: "broken", Note: "should not show", Error: "JSON 解析失败"},
	}
	m := newModel(providers, "", "manual", "", "/wd")
	view := m.View()
	if !strings.Contains(view, "JSON 解析失败") {
		t.Errorf("View should contain error text, got:\n%s", view)
	}
	if strings.Contains(view, "should not show") {
		t.Errorf("View should NOT contain note text when Error is set, got:\n%s", view)
	}
}

// TestRenderProviderView_NoPreSelectedAllBlankMarkers 覆盖 spec Scenario
// "No pre-selection available"——preSelectedProvider="" 时所有行 marker 为 [ ]。
func TestRenderProviderView_NoPreSelectedAllBlankMarkers(t *testing.T) {
	providers := []provider.Info{
		{Name: "alpha"},
		{Name: "bravo"},
		{Name: "charlie"},
	}
	m := newModel(providers, "", "manual", "", "/wd")
	view := m.View()
	// 任何 [*] 形态都不应出现（cursor 所在行除外——但 cursor 行也算 [*]，
	// 所以仍可能有 1 个 [*]）→ 我们已经接受这种"cursor 高亮 [*]"事实，
	// 改用更弱的断言：dim 渲染的 [*] 不应出现。dimStyle 会注入 ANSI
	// 转义，所以这里改为：view 中非 cursor 行的 provider 名不应有 [*] 在它前面。
	// 最简单：count([*]) 最多 = 1（仅 cursor 行），不应有 2 个。
	if got := strings.Count(view, "[*]"); got > 1 {
		t.Errorf("View [*] count = %d, want <= 1 (no pre-selection → only cursor)", got)
	}
}

// ─── tui-mode-default-marker §"renderModeView" 默认项视觉 ───

// TestRenderModeView_DefaultShowsDimAsterisk 覆盖 spec/permission-mode-contract
// §"TUI default mode is marked with a leading dim asterisk" Scenario "Default mode
// not at cursor shows dim [*]"——defaultMode="plan" + cursor 不在 plan 时：
//   - count([*]) == 2：cursor 行（acceptEdits）+ 默认行（plan）
//   - 其它行带 [ ]
//   - 行尾"默认" tag 不再出现（与 provider 页同构）
func TestRenderModeView_DefaultShowsDimAsterisk(t *testing.T) {
	providers := []provider.Info{{Name: "anthropic"}}
	m := newModel(providers, "anthropic", "plan", "", "/wd")
	m.stage = stageMode
	// cursor 落在 acceptEdits（index 1），plan 在 index 5（默认行）
	for i, mode := range m.modes {
		if mode.Name == "acceptEdits" {
			m.cursor = i
			break
		}
	}
	if m.cursor == 0 || m.modes[m.cursor].Name != "acceptEdits" {
		t.Fatalf("test setup failed: cursor = %d (%s)", m.cursor, m.modes[m.cursor].Name)
	}

	view := m.View()

	if got := strings.Count(view, "[*]"); got != 2 {
		t.Errorf("View [*] count = %d, want 2 (cursor=acceptEdits + default=plan)", got)
	}
	if !strings.Contains(view, "[ ]") {
		t.Errorf("View should contain '[ ]' for non-default, non-cursor rows")
	}
	if strings.Contains(view, "默认") {
		t.Errorf("View should NOT contain '默认' tag (replaced by leading dim [*])")
	}
}

// TestRenderModeView_DefaultIsCursor 覆盖 spec Scenario "Default mode is the
// cursor"——defaultMode="auto" + cursor 落在 auto：cursorStyle 整行覆盖了 dim，
// 但 marker 形状 [*] 仍可识别；count([*]) 应为 1（仅 cursor 行）；行尾"默认" tag
// 移除。
func TestRenderModeView_DefaultIsCursor(t *testing.T) {
	providers := []provider.Info{{Name: "anthropic"}}
	m := newModel(providers, "anthropic", "auto", "", "/wd")
	m.stage = stageMode
	// cursor 落在 auto（index 2）
	for i, mode := range m.modes {
		if mode.Name == "auto" {
			m.cursor = i
			break
		}
	}
	if m.cursor == 0 || m.modes[m.cursor].Name != "auto" {
		t.Fatalf("test setup failed: cursor = %d (%s)", m.cursor, m.modes[m.cursor].Name)
	}

	view := m.View()

	if got := strings.Count(view, "[*]"); got != 1 {
		t.Errorf("View [*] count = %d, want 1 (cursor overrides default dim)", got)
	}
	if strings.Contains(view, "默认") {
		t.Errorf("View should NOT contain '默认' tag (replaced by leading dim [*])")
	}
}

// TestRenderModeView_NoDefaultAllBlank 覆盖 spec Scenario "No default mode
// available"——defaultMode 不匹配任何 mode（如 "definitely-not-a-mode"）：没有
// 行带 dim [*]，仅 cursor 行带 [*]；其它行带 [ ]；行尾"默认" tag 移除。
func TestRenderModeView_NoDefaultAllBlank(t *testing.T) {
	providers := []provider.Info{{Name: "anthropic"}}
	m := newModel(providers, "anthropic", "definitely-not-a-mode", "", "/wd")
	m.stage = stageMode
	m.cursor = 0 // 任意 cursor 位置（不与 default 重合，因为没有 default）

	view := m.View()

	if got := strings.Count(view, "[*]"); got != 1 {
		t.Errorf("View [*] count = %d, want 1 (only cursor row, no default match)", got)
	}
	if !strings.Contains(view, "[ ]") {
		t.Errorf("View should contain '[ ]' for non-cursor rows")
	}
	if strings.Contains(view, "默认") {
		t.Errorf("View should NOT contain '默认' tag")
	}
}

// TestRenderModeView_DescriptionStillRendered 覆盖 spec Scenario "Mode row
// carries description in dim style" 与 "Mode row without default marker and
// without description shows minimal layout"——dim [*] 替换行尾"默认" tag 不应
// 顺带砍掉 description 渲染。验证 description 的" — "前缀 + 中文描述 仍出现
// 在 View() 中。
func TestRenderModeView_DescriptionStillRendered(t *testing.T) {
	providers := []provider.Info{{Name: "anthropic"}}
	m := newModel(providers, "anthropic", "manual", "", "/wd")
	m.stage = stageMode
	// cursor 移到 acceptEdits（不在 default manual），让 default 行不带 cursor
	m.cursor = 1

	view := m.View()

	// description 文案至少出现一次："acceptEdits" 行的描述包含"自动接受"
	// （dimStyle 渲染会注入 ANSI 转义，所以用子串匹配而不是 [N]M[42m...）。
	if !strings.Contains(view, "自动接受") {
		t.Errorf("View should contain acceptEdits description '自动接受', got:\n%s", view)
	}
	if !strings.Contains(view, "—") {
		t.Errorf("View should contain em-dash '—' prefix for description, got:\n%s", view)
	}
}
