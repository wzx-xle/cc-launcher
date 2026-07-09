package tui

import (
	"fmt"

	"cc-launcher/internal/provider"

	"github.com/charmbracelet/bubbles/list"
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

// ─── bubbletea list item 实现 ───

type providerListItem struct {
	info     provider.Info
	isActive bool
}

func (i providerListItem) Title() string {
	if i.info.Error != "" {
		return fmt.Sprintf("⚠️  %s — 解析失败", i.info.Name)
	}
	if i.isActive {
		return fmt.Sprintf("%s  ← 上次使用", i.info.Name)
	}
	return i.info.Name
}
func (i providerListItem) Description() string { return "" }
func (i providerListItem) FilterValue() string  { return i.info.Name }

type modeListItem struct {
	item modeItem
}

func (m modeListItem) Title() string       { return m.item.mode }
func (m modeListItem) Description() string { return m.item.desc }
func (m modeListItem) FilterValue() string { return m.item.mode }

// ─── 主模型 ───

type stage int

const (
	stageProvider stage = iota
	stageMode
	stageDone
)

type model struct {
	stage       stage
	providerList list.Model
	modeList     list.Model

	selectedProvider string
	selectedMode     string
	quitting         bool

	width  int
	height int
}

func newModel(providers []provider.Info, defaultProvider, defaultMode string) model {
	// ── 提供商列表 ──
	providerItems := make([]list.Item, len(providers))
	defaultProviderIdx := 0
	for i, p := range providers {
		providerItems[i] = providerListItem{
			info:     p,
			isActive: p.Name == defaultProvider,
		}
		if p.Name == defaultProvider {
			defaultProviderIdx = i
		}
	}

	providerDelegate := list.NewDefaultDelegate()
	providerDelegate.Styles.SelectedTitle = lipgloss.NewStyle().
		Foreground(lipgloss.Color("6")).
		BorderLeft(true).
		PaddingLeft(1)
	providerDelegate.Styles.SelectedDesc = lipgloss.NewStyle().
		Foreground(lipgloss.Color("6"))

	providerList := list.New(providerItems, providerDelegate, 0, 0)
	providerList.Title = "选择提供商"
	providerList.SetShowStatusBar(false)
	providerList.SetFilteringEnabled(false)
	providerList.SetShowHelp(false)
	providerList.Select(defaultProviderIdx)

	// ── 模式列表 ──
	modeListItems := make([]list.Item, len(modeItems))
	defaultModeIdx := 0
	for i, m := range modeItems {
		modeListItems[i] = modeListItem{item: m}
		if m.mode == defaultMode {
			defaultModeIdx = i
		}
	}

	modeDelegate := list.NewDefaultDelegate()
	modeDelegate.Styles.SelectedTitle = lipgloss.NewStyle().
		Foreground(lipgloss.Color("6")).
		BorderLeft(true).
		PaddingLeft(1)
	modeDelegate.Styles.SelectedDesc = lipgloss.NewStyle().
		Foreground(lipgloss.Color("6"))

	modeList := list.New(modeListItems, modeDelegate, 0, 0)
	modeList.Title = "选择权限模式"
	modeList.SetShowStatusBar(false)
	modeList.SetFilteringEnabled(false)
	modeList.SetShowHelp(false)
	modeList.Select(defaultModeIdx)

	return model{
		stage:        stageProvider,
		providerList: providerList,
		modeList:     modeList,
		selectedMode: defaultMode,
	}
}

func (m model) Init() tea.Cmd {
	return tea.SetWindowTitle("Claude Code Launcher")
}

func (m model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
		h, v := headerStyle.GetFrameSize()
		f, _ := footerStyle.GetFrameSize()
		availableHeight := m.height - h - v - f - 4
		m.providerList.SetSize(m.width-4, availableHeight/2)
		m.modeList.SetSize(m.width-4, availableHeight/2)
		return m, nil

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

		case "enter":
			if m.stage == stageProvider {
				if item, ok := m.providerList.SelectedItem().(providerListItem); ok {
					if item.info.Error != "" {
						return m, nil // 跳过解析失败的
					}
					m.selectedProvider = item.info.Name
					m.stage = stageMode
					return m, nil
				}
			} else {
				if item, ok := m.modeList.SelectedItem().(modeListItem); ok {
					m.selectedMode = item.item.mode
					m.stage = stageDone
					return m, tea.Quit
				}
			}
		}
	}

	var cmd tea.Cmd
	if m.stage == stageProvider {
		m.providerList, cmd = m.providerList.Update(msg)
	} else {
		m.modeList, cmd = m.modeList.Update(msg)
	}
	return m, cmd
}

func (m model) View() string {
	if m.quitting || m.stage == stageDone {
		return ""
	}

	borderStyle := lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		Padding(0, 1)

	if m.stage == stageProvider {
		content := headerStyle.Render("Claude Code Launcher") + "\n\n"
		content += m.providerList.View() + "\n\n"
		content += footerStyle.Render("Enter 确认  Esc 退出  ↑↓ 移动")
		return borderStyle.Render(content)
	}

	// stageMode
	content := headerStyle.Render("Claude Code Launcher") + "\n\n"
	content += lipgloss.NewStyle().Foreground(lipgloss.Color("8")).Render(
		fmt.Sprintf("提供商: %s\n\n", m.selectedProvider),
	)
	content += m.modeList.View() + "\n\n"
	content += footerStyle.Render("Enter 确认  Esc 返回  ↑↓ 移动")
	return borderStyle.Render(content)
}

// ─── 样式 ───

var (
	headerStyle = lipgloss.NewStyle().Bold(true).PaddingLeft(1)
	footerStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("8")).PaddingLeft(1)
)

// ─── 公开入口 ───

// Run 启动 TUI，依次让用户选择提供商和权限模式。
// defaultProvider 和 defaultMode 用于设置列表默认光标位置。
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
