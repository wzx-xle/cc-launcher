package tui

import (
	"fmt"
	"strings"

	"cc-launcher/internal/modes"
	"cc-launcher/internal/provider"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

// ErrCancelled 表示用户取消操作。
var ErrCancelled = fmt.Errorf("已取消")

// Result TUI 选择结果。
type Result struct {
	Provider        string
	Mode            string
	SessionKind     string // "" = 新会话, "continue" = 继续上次, "resume" = 会话列表
	RememberBinding bool   // Y → true (调 sm.SetProviderForDir); n → false; skip stage 时 false
}

// ─── 选项定义 ───

type sessionItem struct {
	label string
	desc  string
	key   string // "", "continue", "resume"
}

var sessionItems = []sessionItem{
	{"新会话", "开始全新对话", ""},
	{"继续上次", "恢复最近一次会话", "continue"},
	{"会话列表", "从历史会话列表中选择", "resume"},
}

// ─── 样式 ───

var (
	titleStyle  = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("5"))
	labelStyle  = lipgloss.NewStyle().Foreground(lipgloss.Color("8"))
	cursorStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("6"))
	dimStyle    = lipgloss.NewStyle().Foreground(lipgloss.Color("8"))
	tagStyle    = lipgloss.NewStyle().Foreground(lipgloss.Color("3"))
	footerStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("8"))
)

type stage int

const (
	stageProvider stage = iota
	stageMode
	stageSession
	stageRemember // provider-setenv-cwd-binding §4.2：可选，条件进入
	stageDone
)

type model struct {
	stage  stage
	cursor int

	providers []provider.Info
	modes     []modes.Mode
	sessions  []sessionItem

	preSelectedProvider string
	defaultMode         string

	// provider-setenv-cwd-binding §4.1 + §4.2：cwd binding 相关
	existingBinding string // dir_bindings[wd] 当前值（"" = 无 binding）
	currentWd       string // 显示用 cwd 路径

	selectedProvider string
	selectedMode     string
	selectedSession  string // "", "continue", "resume"
	rememberBound    bool   // stageRemember 的 Y/n 结果

	quitting bool
}

func newModel(providers []provider.Info, preSelectedProvider, defaultMode, existingBinding, currentWd string) model {
	defaultIdx := 0
	for i, p := range providers {
		if p.Name == preSelectedProvider {
			defaultIdx = i
			break
		}
	}

	return model{
		stage:               stageProvider,
		cursor:              defaultIdx,
		providers:           providers,
		modes:               modes.All(),
		sessions:            sessionItems,
		preSelectedProvider: preSelectedProvider,
		defaultMode:         defaultMode,
		existingBinding:     existingBinding,
		currentWd:           currentWd,
	}
}

func (m model) Init() tea.Cmd {
	return nil
}

func (m model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.KeyMsg:
		switch msg.String() {
		case "ctrl+c":
			m.quitting = true
			return m, tea.Quit

		case "esc":
			switch m.stage {
			case stageProvider:
				m.quitting = true
				return m, tea.Quit
			case stageMode:
				m.stage = stageProvider
			case stageSession:
				m.stage = stageMode
				m.cursor = 0
				for i, mode := range m.modes {
					if mode.Name == m.defaultMode {
						m.cursor = i
						break
					}
				}
			case stageRemember:
				// spec/provider-cwd-binding §"TUI cancelled before selection"
				// ——Esc 在 stageRemember 取消整个 flow
				m.quitting = true
				return m, tea.Quit
			}
			return m, nil

		case "up", "k":
			if m.stage == stageRemember {
				// stageRemember 不支持上下导航——按字符 y/n 响应
				return m, nil
			}
			m.cursor--
			if m.cursor < 0 {
				m.cursor = m.currentLen() - 1
			}

		case "down", "j":
			if m.stage == stageRemember {
				return m, nil
			}
			m.cursor++
			if m.cursor >= m.currentLen() {
				m.cursor = 0
			}

		case "enter":
			switch m.stage {
			case stageProvider:
				info := m.providers[m.cursor]
				if info.Error != "" {
					return m, nil
				}
				m.selectedProvider = info.Name
				m.stage = stageMode
				m.cursor = 0
				for i, mode := range m.modes {
					if mode.Name == m.defaultMode {
						m.cursor = i
						break
					}
				}

			case stageMode:
				m.selectedMode = m.modes[m.cursor].Name
				m.stage = stageSession
				m.cursor = 0 // 始终默认新会话

			case stageSession:
				m.selectedSession = m.sessions[m.cursor].key
				// provider-setenv-cwd-binding §4.2：选完 session 后判断是否
				// 进入 stageRemember（仅当 result.Provider != existingBinding）
				if m.selectedProvider != m.existingBinding {
					m.stage = stageRemember
				} else {
					m.stage = stageDone
				}
				return m, tea.Quit

			case stageRemember:
				// Enter 默认 Y（spec §"User accepts prompt" Scenario
				// "Y (or hits Enter)"）
				m.rememberBound = true
				m.stage = stageDone
				return m, tea.Quit
			}

		case "y", "Y":
			if m.stage == stageRemember {
				m.rememberBound = true
				m.stage = stageDone
				return m, tea.Quit
			}

		case "n", "N":
			if m.stage == stageRemember {
				m.rememberBound = false
				m.stage = stageDone
				return m, tea.Quit
			}
		}
	}
	return m, nil
}

func (m *model) currentLen() int {
	switch m.stage {
	case stageProvider:
		return len(m.providers)
	case stageMode:
		return len(m.modes)
	case stageSession:
		return len(m.sessions)
	default:
		return 0
	}
}

func (m model) View() string {
	if m.quitting || m.stage == stageDone {
		return ""
	}

	var sb strings.Builder
	sb.WriteString(titleStyle.Render("Claude Code Launcher"))
	sb.WriteString("\n\n")

	switch m.stage {
	case stageProvider:
		m.renderProviderView(&sb)
	case stageMode:
		m.renderModeView(&sb)
	case stageSession:
		m.renderSessionView(&sb)
	case stageRemember:
		m.renderRememberView(&sb)
	}

	return sb.String()
}

func (m model) renderProviderView(sb *strings.Builder) {
	sb.WriteString("选择提供商:\n\n")
	for i, p := range m.providers {
		// marker 三态：cursor 行 [*]（高亮）、pre-selected 行 [*]（dim）、
		// 普通行 [ ]。pre-selected 与 cursor 重合时由 cursorStyle 整行覆盖。
		marker := "[ ]"
		isPreSelected := p.Name == m.preSelectedProvider && p.Error == ""
		if isPreSelected {
			marker = dimStyle.Render("[*]")
		}
		if i == m.cursor {
			marker = "[*]"
		}
		line := fmt.Sprintf("  %s %s", marker, p.Name)
		if p.Error != "" {
			line += dimStyle.Render(fmt.Sprintf(" — %s", p.Error))
		} else if p.Note != "" {
			// success 路径追加 note；errored 不追加（避免双 dim 段）
			line += dimStyle.Render("  " + p.Note)
		}
		if i == m.cursor {
			line = cursorStyle.Render(line)
		}
		sb.WriteString(line)
		sb.WriteString("\n")
	}
	sb.WriteString("\n")
	sb.WriteString(footerStyle.Render("Enter 确认  Esc 退出  ↑↓ 移动"))
}

func (m model) renderModeView(sb *strings.Builder) {
	sb.WriteString(labelStyle.Render(fmt.Sprintf("提供商: %s", m.selectedProvider)))
	sb.WriteString("\n\n")
	sb.WriteString("选择权限模式:\n\n")
	for i, mode := range m.modes {
		marker := "[ ]"
		if i == m.cursor {
			marker = "[*]"
		}
		line := fmt.Sprintf("  %s %s", marker, mode.Name)
		if mode.Description != "" {
			line += "  " + dimStyle.Render("— "+mode.Description)
		}
		if mode.Name == m.defaultMode {
			line += "  " + tagStyle.Render("默认")
		}
		if i == m.cursor {
			line = cursorStyle.Render(line)
		}
		sb.WriteString(line)
		sb.WriteString("\n")
	}
	sb.WriteString("\n")
	sb.WriteString(footerStyle.Render("Enter 确认  Esc 返回  ↑↓ 移动"))
}

func (m model) renderSessionView(sb *strings.Builder) {
	sb.WriteString(labelStyle.Render(fmt.Sprintf("提供商: %s", m.selectedProvider)))
	sb.WriteString("  ")
	sb.WriteString(labelStyle.Render(fmt.Sprintf("模式: %s", m.selectedMode)))
	sb.WriteString("\n\n")
	sb.WriteString("选择会话:\n\n")
	for i, s := range m.sessions {
		marker := "[ ]"
		if i == m.cursor {
			marker = "[*]"
		}
		line := fmt.Sprintf("  %s %s", marker, s.label)
		if s.desc != "" {
			line += "  " + dimStyle.Render("— "+s.desc)
		}
		if i == m.cursor {
			line = cursorStyle.Render(line)
		}
		sb.WriteString(line)
		sb.WriteString("\n")
	}
	sb.WriteString("\n")
	sb.WriteString(footerStyle.Render("Enter 确认  Esc 返回  ↑↓ 移动"))
}

// renderRememberView provider-setenv-cwd-binding §4.2：末尾"是否记住 cwd binding"
// 提示。spec §"TUI offers to remember cwd binding" + §"TUI cancelled before
// selection" 决定文案。
func (m model) renderRememberView(sb *strings.Builder) {
	sb.WriteString(labelStyle.Render(fmt.Sprintf("提供商: %s", m.selectedProvider)))
	sb.WriteString("  ")
	sb.WriteString(labelStyle.Render(fmt.Sprintf("模式: %s", m.selectedMode)))
	sb.WriteString("\n\n")
	if m.existingBinding != "" {
		sb.WriteString(fmt.Sprintf("已选 %s（override 默认 %s）\n", m.selectedProvider, m.existingBinding))
	} else {
		sb.WriteString(fmt.Sprintf("已选 %s（首次选择，无默认 binding）\n", m.selectedProvider))
	}
	sb.WriteString(fmt.Sprintf("记住当前目录 (%s) 默认用 %s 吗？[Y/n]\n", m.currentWd, m.selectedProvider))
	sb.WriteString("\n")
	sb.WriteString(footerStyle.Render("Y / Enter 记住，n 跳过，Esc 取消整个流程"))
}

// ─── 公开入口 ───

// Run 启动 TUI，依次让用户选择提供商、权限模式、会话，再可选询问"是否记住
// 当前目录的 provider 默认"。
//
// preSelectedProvider：TUI 启动时预选 provider（main.go 已按 dir_bindings[wd]
//
//	> active_provider 顺序解析好；TUI 不用再算）。
//
// existingBinding：dir_bindings[wd] 的当前值（"" = 无 binding）；用于决定末尾
//
//	是否进入 stageRemember 提示（spec/provider-cwd-binding §"TUI offers to
//	remember cwd binding"）。
//
// currentWd：用于显示的 cwd 路径（提示文字 "记住当前目录 (<wd>)"）。
func Run(providers []provider.Info, preSelectedProvider, defaultMode, existingBinding, currentWd string) (*Result, error) {
	if len(providers) == 0 {
		return nil, fmt.Errorf("暂无提供商配置，请在 ~/.cc-launcher/providers/ 下添加 JSON 文件")
	}
	if defaultMode == "" {
		defaultMode = "manual"
	}

	m := newModel(providers, preSelectedProvider, defaultMode, existingBinding, currentWd)
	finalModel, err := tea.NewProgram(m, tea.WithAltScreen()).Run()
	if err != nil {
		return nil, err
	}

	result := finalModel.(model)
	if result.quitting || result.selectedProvider == "" {
		return nil, ErrCancelled
	}

	return &Result{
		Provider:        result.selectedProvider,
		Mode:            result.selectedMode,
		SessionKind:     result.selectedSession,
		RememberBinding: result.rememberBound,
	}, nil
}
