package provider

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"

	"cc-launcher/internal/safefile"
	"cc-launcher/internal/settingsschema"
)

// EnvProviderName 是 ccl 注入给消费方的环境变量名，值是本次启动选中的
// provider 的 Name（provider 配置文件名去掉 .json）。
//
// 用途：让**仓库外**的程序知道"这次 Claude Code 是用哪个 provider 起的"。
// 最典型的消费者是 Claude Code 的状态栏脚本（statusLine）——它拿到的 stdin
// JSON 里没有 provider 信息，唯一能拿到 provider 名的通道就是环境变量：
//
//	const provider = process.env.CCL_PROVIDER
//
// 交付方式（两条通道，见 cmd/cc-launcher/main.go 的 buildLaunchOptions）：
//
//  1. --settings 交付对象的 env 块（主通道，优先级最高）
//  2. 进程环境（冗余第二通道）
//
// **这是对外契约**：变量名与值的语义都不能随便改，改名按 breaking change
// 处理（会静默弄坏所有仓库外的消费方）。两条硬约束：
//
//   - 名字**不能带凭证特征**（TOKEN / KEY / SECRET / PASSWORD）：Claude Code
//     的 CLAUDE_CODE_SUBPROCESS_ENV_SCRUB 会把凭证类变量从子进程环境里删掉，
//     带凭证特征的变量名会被误伤（provider-name-in-statusline 实测）。
//   - 值是**原值**，不转义不拼接：消费方渲染到终端前要自己剥 ANSI 转义序列
//     并限长。
//
// 它**不承载** provider 的生效语义——provider 的生效靠 --settings 交付
// provider 自己的 env 块（provider-settings-injection）。这个变量只传名字。
const EnvProviderName = "CCL_PROVIDER"

// EnvQuotaURL 是 ccl 注入给消费方的环境变量名，值是 daemon 上**只读额度端点**
// 的地址（`http://127.0.0.1:4318/ccl/quota`）。
//
// 用途：让**仓库外**的程序（最典型的是 Claude Code 的状态栏脚本）**不启动任何
// 进程**就能读到额度——Windows 上 `ccl quota --json` 的耗时几乎全在进程启动
// （cmd.exe shim → cmd.exe → exe，p50 170ms），而状态栏的调用预算只有 250ms，
// 系统繁忙时必然超时。
//
// 交付方式与 EnvProviderName **完全同构**：--settings 的 env 块（主通道）+ 进程
// 环境（冗余副本）。
//
// **这是对外契约**：变量名与 URL 的形态都不能随便改，改名 / 改形态按 breaking
// change 处理（会静默弄坏所有仓库外的消费方）。名字同样**不能带凭证特征**
// （TOKEN / KEY / SECRET / PASSWORD）——Claude Code 的 subprocess scrub 会把
// 凭证类变量从子进程环境里删掉。
//
// 它**不承载**额度的生效语义：额度数据由 daemon 周期刷新写进本地数据文件，
// 这个变量只告诉消费方"去哪儿读"。
const EnvQuotaURL = "CCL_QUOTA_URL"

// Info 提供商概要信息。
type Info struct {
	Name  string // 文件名去掉 .json
	Error string // JSON 解析失败时的错误信息
	// Note 是 provider 配置文件顶层 `note` 字段，缺省 ""。
	// 渲染层（TUI / CLI）用它显示一行备注。
	Note string
	// Order 是 provider 配置文件顶层 `order` 字段，缺省 0。
	// List() 返回前按 (Order asc, Name asc) 稳定排序。
	Order int
	// ValidationErrors 是配置的 schema 校验错误（settingsschema）。
	//
	// 非空表示**这个 provider 启动不了**：Claude Code 会拒绝整个 settings
	// 文件，provider 的凭证跟着一起消失（provider-settings-load-guard）。
	// 提前算出来，渲染层就能在选择之前把问题摆到用户眼前——不然用户选完
	// provider、走到启动那一步才看到报错。
	ValidationErrors []string
	// PassthroughKeys 是会被透传给 Claude Code 的顶层键数（env 以外的键）。
	// 用来告诉用户"这个 provider 还带了别的设置"。
	PassthroughKeys int
	// HasQuota 表示配置里声明了额度接口（顶层 `quota` 块）。
	//
	// web 控制台用它决定"要不要显示额度卡"——没声明额度接口的 provider
	// 查不到额度，显示一个空卡片只会让人以为额度是 0。
	HasQuota bool
	// HasCredential 表示 env 块里有凭证（ANTHROPIC_AUTH_TOKEN /
	// ANTHROPIC_API_KEY 之一）。
	//
	// 用来在列表里标出"这个 provider 还没填 token"——它是启动失败的头号原因。
	HasCredential bool
}

// configFile 是 provider JSON 文件的内部表示（仅用于 Unmarshal；List() 不导出）。
// 新增的 note / order 是可选字段，缺失时为零值。
type configFile struct {
	Env   map[string]any `json:"env"`
	Note  string         `json:"note,omitempty"`
	Order int            `json:"order,omitempty"`
	Quota map[string]any `json:"quota,omitempty"`
}

// credentialKeys 是"算有凭证"的 env 键。
//
// 与 CreateWithEnv 的要求一致（必须有 ANTHROPIC_AUTH_TOKEN），但多认一个
// ANTHROPIC_API_KEY——Claude Code 两种都认，provider 文件里两种都见过。
var credentialKeys = []string{"ANTHROPIC_AUTH_TOKEN", "ANTHROPIC_API_KEY"}

// ReservedKeys 是 ccl 自己的 provider 元数据键。
//
// 它们**不是** Claude Code 的 settings 键（Claude Code 的顶层键里没有它们），
// 所以透传时必须排除：
//
//   - note：provider 列表里的备注行
//   - order：provider 列表的排序权重
//   - quota：provider 的额度查询配置（用哪个 adapter、查哪个额度端点）
//
// **维护约定**：新增 ccl 元数据键时必须同步加到这里，否则它会被当普通
// settings 键透传给 Claude Code（虽然会被静默忽略，但语义不对，且 `order`
// 这种通用词名有朝一日可能撞上真实键）。
var ReservedKeys = map[string]bool{
	"note":  true,
	"order": true,
	"quota": true,
}

// IsReservedKey 判断一个顶层键是不是 ccl 自己的元数据键。
func IsReservedKey(key string) bool { return ReservedKeys[key] }

// Manager 管理 ~/.cc-launcher/providers/ 目录。
type Manager struct {
	providersDir string
	// backupDir 是备份目录；空 = 写入不备份。
	//
	// 备份是"写坏了能回退"的最后一道保险：web 控制台是 provider 配置的
	// 第二个写入口（第一个是 CLI），两边都写同一个目录。
	backupDir string
}

// NewManager 创建 Manager（写入不备份）。
func NewManager(providersDir string) *Manager {
	return &Manager{providersDir: providersDir}
}

// SetBackupDir 设置备份目录。设了之后，所有写入都会先把旧内容备份进去。
func (m *Manager) SetBackupDir(dir string) {
	m.backupDir = dir
}

// BackupDir 返回备份目录（空 = 不备份）。
func (m *Manager) BackupDir() string {
	return m.backupDir
}

// providerNamePattern 是 provider 配置名的合法形态。
//
// 名字会被拼成文件名（`<name>.json`）放进 providers 目录，所以必须排除
// 路径分隔符与父目录引用。名字同时也是注入给消费方的 `CCL_PROVIDER` 的值，
// 那条契约也要求名字不带凭证特征（TOKEN / KEY / SECRET / PASSWORD）。
var providerNamePattern = regexp.MustCompile(`^[A-Za-z0-9._-]{1,64}$`)

// ValidName 判断 name 能不能作为 provider 的配置文件名。
//
// "." 与 ".." 都能通过正则，必须单独排除。
func ValidName(name string) bool {
	if !providerNamePattern.MatchString(name) {
		return false
	}
	return name != "." && name != ".."
}

// ValidationError 表示 provider 配置没通过 schema 校验。
//
// 单独一个类型，是为了让调用方把它和"磁盘写不进去"区分开：校验失败是
// **用户配置的问题**（HTTP 400 + 错误列表），其他错误是环境问题（HTTP 500）。
type ValidationError struct {
	Errors []string
}

func (e *ValidationError) Error() string {
	return "配置校验失败:\n  - " + strings.Join(e.Errors, "\n  - ")
}

// ProvidersDir 返回 providers 目录路径（供 CLI 层显示用）。
func (m *Manager) ProvidersDir() string {
	return m.providersDir
}

// List 列出所有提供商。JSON 解析失败的在 Info.Error 中标记，不阻塞其他文件。
// 返回前按 (Order asc, Name asc) 稳定排序。
func (m *Manager) List() ([]Info, error) {
	if err := os.MkdirAll(m.providersDir, 0755); err != nil {
		return nil, err
	}

	entries, err := os.ReadDir(m.providersDir)
	if err != nil {
		return nil, err
	}

	var result []Info
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		name := entry.Name()
		if !strings.HasSuffix(name, ".json") {
			continue
		}
		info := Info{Name: strings.TrimSuffix(name, ".json")}

		data, err := os.ReadFile(filepath.Join(m.providersDir, name))
		if err != nil {
			info.Error = err.Error()
		} else {
			var cfg configFile
			if err := json.Unmarshal(data, &cfg); err != nil {
				info.Error = fmt.Sprintf("JSON 解析失败: %s", err.Error())
			} else {
				info.Note = cfg.Note
				info.Order = cfg.Order
				info.HasQuota = len(cfg.Quota) > 0
				for _, key := range credentialKeys {
					if _, ok := cfg.Env[key]; ok {
						info.HasCredential = true
						break
					}
				}
			}
			// 顶层键：算透传键数 + 跑一遍 schema 校验。
			// 校验失败不阻断 List()——列表要把所有 provider 都列出来，
			// 问题在渲染层标出来。
			var raw map[string]any
			if err := json.Unmarshal(data, &raw); err == nil {
				for k := range raw {
					if k == "env" || IsReservedKey(k) {
						continue
					}
					info.PassthroughKeys++
				}
				if res := settingsschema.Validate(raw); !res.OK() {
					info.ValidationErrors = res.Errors
				}
			}
		}
		result = append(result, info)
	}

	slices.SortStableFunc(result, func(a, b Info) int {
		if a.Order != b.Order {
			return a.Order - b.Order
		}
		return strings.Compare(a.Name, b.Name)
	})
	return result, nil
}

// Get 读取并解析指定提供商的完整配置。
func (m *Manager) Get(name string) (map[string]any, error) {
	path, err := m.pathOf(name)
	if err != nil {
		return nil, err
	}
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, fmt.Errorf("提供商 %q 不存在", name)
		}
		return nil, err
	}
	var cfg map[string]any
	if err := json.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("提供商 %q 配置解析失败: %w", name, err)
	}
	return cfg, nil
}

// pathOf 解析 provider 的配置文件路径，并挡住名字里的路径穿越。
func (m *Manager) pathOf(name string) (string, error) {
	if !ValidName(name) {
		return "", fmt.Errorf("提供商名 %q 不合法", name)
	}
	return filepath.Join(m.providersDir, name+".json"), nil
}

// SaveRaw 用给定原文覆盖 provider 的配置文件。
//
// 顺序是硬要求：**先校验、再备份、最后原子写**。
//
//   - 校验在前：写坏配置的后果是 Claude Code 丢弃**整个** settings 文件
//     （provider 的凭证跟着一起消失）——宁可什么都不写
//   - 备份在中：写盘失败或内容写错时能回退
//   - 原子写在最后：不让用户看到半个文件
//
// 校验不过返回 *ValidationError（调用方据此回 400）。
func (m *Manager) SaveRaw(name string, content []byte) error {
	path, err := m.pathOf(name)
	if err != nil {
		return err
	}

	var raw map[string]any
	if err := json.Unmarshal(content, &raw); err != nil {
		return fmt.Errorf("配置不是合法 JSON: %w", err)
	}
	if res := settingsschema.Validate(raw); !res.OK() {
		return &ValidationError{Errors: res.Errors}
	}

	if err := os.MkdirAll(m.providersDir, 0755); err != nil {
		return err
	}
	return safefile.Write(path, content, m.backupDir, safefile.DefaultKeepBackups)
}

// Create 创建新的提供商配置文件，接受任意 env 字段集合。
// 旧签名 Create(name, baseURL, apiKey) 已删除，由 CreateWithEnv 取代。
func (m *Manager) CreateWithEnv(name string, env map[string]any) error {
	if name == "" {
		return fmt.Errorf("提供商名称不能为空")
	}
	if len(env) == 0 {
		return fmt.Errorf("env 不能为空")
	}
	if _, ok := env["ANTHROPIC_AUTH_TOKEN"]; !ok {
		return fmt.Errorf("env 必须包含 ANTHROPIC_AUTH_TOKEN")
	}
	if m.Exists(name) {
		return fmt.Errorf("提供商 %q 已存在", name)
	}

	cfg := map[string]any{"env": env}

	if err := os.MkdirAll(m.providersDir, 0755); err != nil {
		return err
	}

	data, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return err
	}
	return safefile.Write(filepath.Join(m.providersDir, name+".json"), data, m.backupDir, safefile.DefaultKeepBackups)
}

// Delete 删除指定提供商配置文件。
func (m *Manager) Delete(name string) error {
	path, err := m.pathOf(name)
	if err != nil {
		return err
	}
	if _, err := os.Stat(path); os.IsNotExist(err) {
		return fmt.Errorf("提供商 %q 不存在", name)
	}
	return os.Remove(path)
}

// Exists 检查提供商配置文件是否存在。
func (m *Manager) Exists(name string) bool {
	path, err := m.pathOf(name)
	if err != nil {
		return false
	}
	_, err = os.Stat(path)
	return err == nil
}
