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
	stageDone
)

type model struct {
	stage  stage
	cursor int

	providers []provider.Info
	modes     []modeItem

	defaultProvider string
	defaultMode     string

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
		stage:           stageProvider,
		cursor:          defaultIdx,
		providers:       providers,
		modes:           modeItems,
		defaultProvider: defaultProvider,
		defaultMode:     defaultMode,
		selectedMode:    defaultMode,
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
					return m, nil
				}
				m.selectedProvider = info.Name
				m.stage = stageMode
				m.cursor = 0
				for i, mode := range m.modes {
					if mode.mode == m.defaultMode {
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

	sb.WriteString(titleStyle.Render("Claude Code Launcher"))
	sb.WriteString("\n\n")

	if m.stage == stageProvider {
		m.renderProviderView(&sb)
	} else {
		m.renderModeView(&sb)
	}

	return sb.String()
}

func (m model) renderProviderView(sb *strings.Builder) {
	sb.WriteString("选择提供商:\n\n")
	for i, p := range m.providers {
		marker := "[ ]"
		if i == m.cursor {
			marker = "[*]"
		}

		line := fmt.Sprintf("  %s %s", marker, p.Name)
		if p.Error != "" {
			line += dimStyle.Render(fmt.Sprintf(" — %s", p.Error))
		}
		if p.Name == m.defaultProvider && p.Error == "" {
			line += "  " + tagStyle.Render("默认")
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

		line := fmt.Sprintf("  %s %s", marker, mode.mode)
		if mode.desc != "" {
			line += "  " + dimStyle.Render("— "+mode.desc)
		}
		if mode.mode == m.defaultMode {
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
