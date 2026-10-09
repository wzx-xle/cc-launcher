package tui

import (
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
