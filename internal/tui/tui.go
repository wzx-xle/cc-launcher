package tui

import (
	"fmt"
	"strings"

	"cc-launcher/internal/provider"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

// ErrCancelled 表示用户取消操作。
var ErrCancelled = fmt.Errorf("已取消")

// 权限模式及其描述。
type modeItem struct {
	mode string
	desc string
}

var modeItems = []modeItem{
	{"manual", "每次操作询问确认"},
	{"acceptEdits", "自动接受文件编辑"},
	{"auto", "自动执行非破坏性操作"},
	{"bypassPermissions", "跳过所有权限检查"},
	{"dontAsk", "不询问，直接执行"},
	{"plan", "只读，仅分析不执行"},
}

// ─── 样式 ───

var (
	titleStyle   = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("5"))
	labelStyle   = lipgloss.NewStyle().Foreground(lipgloss.Color("8"))
	cursorStyle  = lipgloss.NewStyle().Foreground(lipgloss.Color("6"))
	dimStyle     = lipgloss.NewStyle().Foreground(lipgloss.Color("8"))
	footerStyle  = lipgloss.NewStyle().Foreground(lipgloss.Color("8"))
)

type stage int

const (
	stageProvider stage = iota
	stageMode
	stageDone
)

type model struct {
	stage   stage
	cursor  int

	providers []provider.Info
	modes     []modeItem

	selectedProvider string
	selectedMode     string
	quitting         bool
}

func newModel(providers []provider.Info, defaultProvider, defaultMode string) model {
	defaultIdx := 0
	for i, p := range providers {
		if p.Name == defaultProvider {
			defaultIdx = i
			break
		}
	}

	return model{
		stage:     stageProvider,
		cursor:    defaultIdx,
		providers: providers,
		modes:     modeItems,
		selectedMode: defaultMode,
		// 光标初始在 defaultModeIdx（但当前 stage 是 provider，暂不用 mode cursor）
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
			if m.stage == stageProvider {
				m.quitting = true
				return m, tea.Quit
			}
			m.stage = stageProvider
			return m, nil

		case "up", "k":
			m.cursor--
			if m.cursor < 0 {
				m.cursor = m.currentLen() - 1
			}

		case "down", "j":
			m.cursor++
			if m.cursor >= m.currentLen() {
				m.cursor = 0
			}

		case "enter":
			if m.stage == stageProvider {
				info := m.providers[m.cursor]
				if info.Error != "" {
					return m, nil // 跳过解析失败的
				}
				m.selectedProvider = info.Name
				m.stage = stageMode
				// 恢复光标到 mode 的默认位置
				m.cursor = 0
				for i, mode := range m.modes {
					if mode.mode == m.selectedMode {
						m.cursor = i
						break
					}
				}
			} else {
				m.selectedMode = m.modes[m.cursor].mode
				m.stage = stageDone
				return m, tea.Quit
			}
		}
	}
	return m, nil
}

func (m *model) currentLen() int {
	if m.stage == stageProvider {
		return len(m.providers)
	}
	return len(m.modes)
}

func (m model) View() string {
	if m.quitting || m.stage == stageDone {
		return ""
	}

	var sb strings.Builder

	// 标题
	sb.WriteString(titleStyle.Render("Claude Code Launcher"))
	sb.WriteString("\n\n")

	// 提供商选择阶段
	if m.stage == stageProvider {
		sb.WriteString("选择提供商:\n\n")
		for i, p := range m.providers {
			cursor := " "
			if i == m.cursor {
				cursor = "*"
			}
			marker := "[ ]"
			if cursor == "*" {
				marker = "[*]"
			}

			line := fmt.Sprintf("  %s %s", marker, p.Name)
			if i == m.cursor {
				line = cursorStyle.Render(line)
			}
			if p.Error != "" {
				line += dimStyle.Render(fmt.Sprintf(" — %s", p.Error))
			}
			sb.WriteString(line)
			sb.WriteString("\n")
		}
		sb.WriteString("\n")
		sb.WriteString(footerStyle.Render("Enter 确认  Esc 退出  ↑↓ 移动"))
	} else {
		// 权限模式选择阶段
		sb.WriteString(labelStyle.Render(fmt.Sprintf("提供商: %s", m.selectedProvider)))
		sb.WriteString("\n\n")
		sb.WriteString("选择权限模式:\n\n")

		for i, mode := range m.modes {
			cursor := " "
			if i == m.cursor {
				cursor = "*"
			}
			marker := "[ ]"
			if cursor == "*" {
				marker = "[*]"
			}

			line := fmt.Sprintf("  %s %s", marker, mode.mode)
			if mode.desc != "" {
				line += "  " + dimStyle.Render("— "+mode.desc)
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

	return sb.String()
}

// ─── 公开入口 ───

// Run 启动 TUI，依次让用户选择提供商和权限模式。
func Run(providers []provider.Info, defaultProvider, defaultMode string) (string, string, error) {
	if len(providers) == 0 {
		return "", "", fmt.Errorf("暂无提供商配置，请在 ~/.cc-launcher/providers/ 下添加 JSON 文件")
	}

	if defaultMode == "" {
		defaultMode = "manual"
	}

	m := newModel(providers, defaultProvider, defaultMode)
	finalModel, err := tea.NewProgram(m, tea.WithAltScreen()).Run()
	if err != nil {
		return "", "", err
	}

	result := finalModel.(model)
	if result.quitting || result.selectedProvider == "" {
		return "", "", ErrCancelled
	}

	return result.selectedProvider, result.selectedMode, nil
}
