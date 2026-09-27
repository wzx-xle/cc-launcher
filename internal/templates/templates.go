// Package templates 定义 Claude Code 提供商接入的 JSON 配置模板。
// 是 cli 与 --help 输出共享的单一事实源；漂移由两层契约测试阻止：
//   - 本包 templates_test.go 的常规单测（锁 Name / Description / Body 解析 / 关键 env key）
//   - 关键 BASE_URL 必须以 /anthropic 或 /apps/anthropic 结尾（Anthropic 兼容路径前缀）
//
// 新增 / 修改 / 删除模板时：只改本文件的 All 切片，然后跑
// `go test ./...` 校验两侧同步。每个非 anthropic 模板上方必须有
// `// verified YYYY-MM-DD: <URL>` 注释，记录 base_url 和默认 model 的来源。
package templates

// Hint 一个交互步骤的解释。
type Hint struct {
	Step   string // 步骤名，如 "name" / "base_url"
	Prompt string // 打印给用户的提示语（含模板片段）
}

// TemplateField 交互流要问的一个 env 字段。
type TemplateField struct {
	Key         string // env 中的 key，如 "ANTHROPIC_BASE_URL"
	Prompt      string // 用户看到的提示
	Default     string // 模板默认值（用户回车跳过用）
	Required    bool   // 是否必填
	Secret      bool   // 是否不回显（如 ANTHROPIC_AUTH_TOKEN）
	Placeholder string // 用于 URL 的占位符（如 aliyun-pay 的 {WorkspaceId}）
}

// Template 一个 provider 类型。
type Template struct {
	Name        string
	Description string
	Body        string         // 完整 JSON 模板（不含 // 注释）
	Hints       []Hint         // 每步的提示解释
	Fields      []TemplateField // 每步要问什么
}

// All 是全部已实现的模板，按其注册顺序遍历。
var All = []Template{
	anthropicTpl,
	deepseekTpl,
	minimaxTpl,
	glmTpl,
	aliyunCodingTpl,
	aliyunPayTpl,
	aliyunTokenTpl,
}

// ByName 按名称查找；找不到时返回零值与 false。
func ByName(name string) (Template, bool) {
	for _, t := range All {
		if t.Name == name {
			return t, true
		}
	}
	return Template{}, false
}

// verified 2026-09-27: https://api-docs.deepseek.com/zh-cn/quick_start/agent_integrations/claude_code
// 国内默认模型为 deepseek-flash[1m]；claude-opus 自动映射到 deepseek-v4-pro（按 V4 Pro 计费）。
var deepseekTpl = Template{
	Name:        "deepseek",
	Description: "DeepSeek Anthropic 兼容端点（api.deepseek.com/anthropic）",
	Body: `{
  "env": {
    "ANTHROPIC_BASE_URL": "https://api.deepseek.com/anthropic",
    "ANTHROPIC_AUTH_TOKEN": "sk-xxx",
    "ANTHROPIC_MODEL": "deepseek-flash[1m]",
    "ANTHROPIC_DEFAULT_OPUS_MODEL": "deepseek-flash[1m]",
    "ANTHROPIC_DEFAULT_SONNET_MODEL": "deepseek-flash[1m]",
    "ANTHROPIC_DEFAULT_HAIKU_MODEL": "deepseek-flash",
    "CLAUDE_CODE_SUBAGENT_MODEL": "deepseek-flash"
  }
}`,
	Hints: []Hint{
		{Step: "name", Prompt: "提供商名称（用于文件名，如 'deepseek-prod'）"},
		{Step: "base_url", Prompt: "ANTHROPIC_BASE_URL（直接回车用模板默认 https://api.deepseek.com/anthropic）"},
		{Step: "auth_token", Prompt: "ANTHROPIC_AUTH_TOKEN（不回显）"},
		{Step: "model", Prompt: "ANTHROPIC_MODEL（直接回车用 deepseek-flash[1m]）"},
		{Step: "opus", Prompt: "ANTHROPIC_DEFAULT_OPUS_MODEL（直接回车用 deepseek-flash[1m]）"},
		{Step: "sonnet", Prompt: "ANTHROPIC_DEFAULT_SONNET_MODEL（直接回车用 deepseek-flash[1m]）"},
		{Step: "haiku", Prompt: "ANTHROPIC_DEFAULT_HAIKU_MODEL（直接回车用 deepseek-flash）"},
		{Step: "subagent", Prompt: "CLAUDE_CODE_SUBAGENT_MODEL（直接回车用 deepseek-flash）"},
	},
	Fields: []TemplateField{
		{Key: "ANTHROPIC_BASE_URL", Prompt: "ANTHROPIC_BASE_URL", Default: "https://api.deepseek.com/anthropic", Required: true},
		{Key: "ANTHROPIC_AUTH_TOKEN", Prompt: "ANTHROPIC_AUTH_TOKEN", Required: true, Secret: true},
		{Key: "ANTHROPIC_MODEL", Prompt: "ANTHROPIC_MODEL", Default: "deepseek-flash[1m]", Required: true},
		{Key: "ANTHROPIC_DEFAULT_OPUS_MODEL", Prompt: "ANTHROPIC_DEFAULT_OPUS_MODEL", Default: "deepseek-flash[1m]", Required: true},
		{Key: "ANTHROPIC_DEFAULT_SONNET_MODEL", Prompt: "ANTHROPIC_DEFAULT_SONNET_MODEL", Default: "deepseek-flash[1m]", Required: true},
		{Key: "ANTHROPIC_DEFAULT_HAIKU_MODEL", Prompt: "ANTHROPIC_DEFAULT_HAIKU_MODEL", Default: "deepseek-flash", Required: true},
		{Key: "CLAUDE_CODE_SUBAGENT_MODEL", Prompt: "CLAUDE_CODE_SUBAGENT_MODEL", Default: "deepseek-flash", Required: true},
	},
}

// verified 2026-09-27: https://platform.minimax.cn/docs
// BASE_URL 有国内/国际两种（用户在交互流里二选一）。
// 这里 Body 默认填国内 URL；交互流根据用户选择覆盖。
var minimaxTpl = Template{
	Name:        "minimax",
	Description: "MiniMax Anthropic 兼容端点（api.minimax.cn/anthropic 国内 / api.minimax.io/anthropic 国际）",
	Body: `{
  "env": {
    "ANTHROPIC_BASE_URL": "https://api.minimax.cn/anthropic",
    "ANTHROPIC_AUTH_TOKEN": "<MINIMAX_API_KEY>",
    "CLAUDE_CODE_AUTO_COMPACT_WINDOW": "1000000",
    "ANTHROPIC_MODEL": "MiniMax-M3[1m]",
    "ANTHROPIC_DEFAULT_SONNET_MODEL": "MiniMax-M3[1m]",
    "ANTHROPIC_DEFAULT_OPUS_MODEL": "MiniMax-M3[1m]",
    "ANTHROPIC_DEFAULT_HAIKU_MODEL": "MiniMax-M3[1m]"
  }
}`,
	Hints: []Hint{
		{Step: "name", Prompt: "提供商名称（用于文件名，如 'minimax-cn'）"},
		{Step: "region", Prompt: "地域 [cn/io]（国内 api.minimax.cn / 国际 api.minimax.io；默认 cn）"},
		{Step: "base_url", Prompt: "ANTHROPIC_BASE_URL（直接回车用所选地域默认）"},
		{Step: "auth_token", Prompt: "ANTHROPIC_AUTH_TOKEN（不回显）"},
		{Step: "auto_compact", Prompt: "CLAUDE_CODE_AUTO_COMPACT_WINDOW（直接回车用 1000000 与 MiniMax-M3 1M 上下文对齐）"},
		{Step: "model", Prompt: "ANTHROPIC_MODEL（直接回车用 MiniMax-M3[1m]）"},
		{Step: "sonnet", Prompt: "ANTHROPIC_DEFAULT_SONNET_MODEL（直接回车用 MiniMax-M3[1m]）"},
		{Step: "opus", Prompt: "ANTHROPIC_DEFAULT_OPUS_MODEL（直接回车用 MiniMax-M3[1m]）"},
		{Step: "haiku", Prompt: "ANTHROPIC_DEFAULT_HAIKU_MODEL（直接回车用 MiniMax-M3[1m]）"},
	},
	Fields: []TemplateField{
		{Key: "ANTHROPIC_BASE_URL", Prompt: "ANTHROPIC_BASE_URL", Default: "https://api.minimax.cn/anthropic", Required: true},
		{Key: "ANTHROPIC_AUTH_TOKEN", Prompt: "ANTHROPIC_AUTH_TOKEN", Required: true, Secret: true},
		{Key: "CLAUDE_CODE_AUTO_COMPACT_WINDOW", Prompt: "CLAUDE_CODE_AUTO_COMPACT_WINDOW", Default: "1000000", Required: true},
		{Key: "ANTHROPIC_MODEL", Prompt: "ANTHROPIC_MODEL", Default: "MiniMax-M3[1m]", Required: true},
		{Key: "ANTHROPIC_DEFAULT_SONNET_MODEL", Prompt: "ANTHROPIC_DEFAULT_SONNET_MODEL", Default: "MiniMax-M3[1m]", Required: true},
		{Key: "ANTHROPIC_DEFAULT_OPUS_MODEL", Prompt: "ANTHROPIC_DEFAULT_OPUS_MODEL", Default: "MiniMax-M3[1m]", Required: true},
		{Key: "ANTHROPIC_DEFAULT_HAIKU_MODEL", Prompt: "ANTHROPIC_DEFAULT_HAIKU_MODEL", Default: "MiniMax-M3[1m]", Required: true},
	},
}

// verified 2026-09-27: https://docs.bigmodel.cn/cn/coding-plan
// 智谱是 Coding Plan 套餐制，需要单独的 API Key（不是平台普通 API Key）。
var glmTpl = Template{
	Name:        "glm",
	Description: "智谱 BigModel Coding Plan Anthropic 兼容端点（open.bigmodel.cn/api/anthropic）",
	Body: `{
  "env": {
    "ANTHROPIC_AUTH_TOKEN": "<YOUR_API_KEY>",
    "ANTHROPIC_BASE_URL": "https://open.bigmodel.cn/api/anthropic",
    "ANTHROPIC_DEFAULT_HAIKU_MODEL": "glm-5.3-flash[1m]",
    "ANTHROPIC_DEFAULT_SONNET_MODEL": "glm-5.3[1m]",
    "ANTHROPIC_DEFAULT_OPUS_MODEL": "glm-5.3[1m]",
    "CLAUDE_CODE_AUTO_COMPACT_WINDOW": "1000000",
    "CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC": 1,
    "API_TIMEOUT_MS": "3000000"
  }
}`,
	Hints: []Hint{
		{Step: "name", Prompt: "提供商名称（用于文件名，如 'glm-prod'）"},
		{Step: "base_url", Prompt: "ANTHROPIC_BASE_URL（直接回车用模板默认 https://open.bigmodel.cn/api/anthropic）"},
		{Step: "auth_token", Prompt: "ANTHROPIC_AUTH_TOKEN（Coding Plan 专属 API Key，不回显）"},
		{Step: "haiku", Prompt: "ANTHROPIC_DEFAULT_HAIKU_MODEL（直接回车用 glm-5.3-flash[1m]）"},
		{Step: "sonnet", Prompt: "ANTHROPIC_DEFAULT_SONNET_MODEL（直接回车用 glm-5.3[1m]）"},
		{Step: "opus", Prompt: "ANTHROPIC_DEFAULT_OPUS_MODEL（直接回车用 glm-5.3[1m]）"},
		{Step: "auto_compact", Prompt: "CLAUDE_CODE_AUTO_COMPACT_WINDOW（直接回车用 1000000）"},
		{Step: "disable_nonessential", Prompt: "CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC（直接回车用 1）"},
		{Step: "timeout", Prompt: "API_TIMEOUT_MS（直接回车用 3000000）"},
	},
	Fields: []TemplateField{
		{Key: "ANTHROPIC_BASE_URL", Prompt: "ANTHROPIC_BASE_URL", Default: "https://open.bigmodel.cn/api/anthropic", Required: true},
		{Key: "ANTHROPIC_AUTH_TOKEN", Prompt: "ANTHROPIC_AUTH_TOKEN", Required: true, Secret: true},
		{Key: "ANTHROPIC_DEFAULT_HAIKU_MODEL", Prompt: "ANTHROPIC_DEFAULT_HAIKU_MODEL", Default: "glm-5.3-flash[1m]", Required: true},
		{Key: "ANTHROPIC_DEFAULT_SONNET_MODEL", Prompt: "ANTHROPIC_DEFAULT_SONNET_MODEL", Default: "glm-5.3[1m]", Required: true},
		{Key: "ANTHROPIC_DEFAULT_OPUS_MODEL", Prompt: "ANTHROPIC_DEFAULT_OPUS_MODEL", Default: "glm-5.3[1m]", Required: true},
		{Key: "CLAUDE_CODE_AUTO_COMPACT_WINDOW", Prompt: "CLAUDE_CODE_AUTO_COMPACT_WINDOW", Default: "1000000", Required: true},
		{Key: "CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC", Prompt: "CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC", Default: "1", Required: true},
		{Key: "API_TIMEOUT_MS", Prompt: "API_TIMEOUT_MS", Default: "3000000", Required: true},
	},
}

// verified 2026-09-27: https://help.aliyun.com/zh/model-studio/claude-code
// 阿里云百炼 Coding Plan 套餐，4 个 model 都用 qwen3.7-plus。
var aliyunCodingTpl = Template{
	Name:        "aliyun-coding",
	Description: "阿里云百炼 Coding Plan（coding.dashscope.aliyuncs.com/apps/anthropic，qwen3.7-plus）",
	Body: `{
  "env": {
    "ANTHROPIC_AUTH_TOKEN": "<Coding_Plan_API_Key>",
    "ANTHROPIC_BASE_URL": "https://coding.dashscope.aliyuncs.com/apps/anthropic",
    "ANTHROPIC_MODEL": "qwen3.7-plus",
    "ANTHROPIC_DEFAULT_HAIKU_MODEL": "qwen3.7-plus",
    "ANTHROPIC_DEFAULT_SONNET_MODEL": "qwen3.7-plus",
    "ANTHROPIC_DEFAULT_OPUS_MODEL": "qwen3.7-plus"
  }
}`,
	Hints: []Hint{
		{Step: "name", Prompt: "提供商名称（用于文件名，如 'coding-prod'）"},
		{Step: "base_url", Prompt: "ANTHROPIC_BASE_URL（直接回车用模板默认）"},
		{Step: "auth_token", Prompt: "ANTHROPIC_AUTH_TOKEN（Coding Plan 专属 API Key，不回显）"},
		{Step: "model", Prompt: "ANTHROPIC_MODEL（直接回车用 qwen3.7-plus）"},
		{Step: "haiku", Prompt: "ANTHROPIC_DEFAULT_HAIKU_MODEL（直接回车用 qwen3.7-plus）"},
		{Step: "sonnet", Prompt: "ANTHROPIC_DEFAULT_SONNET_MODEL（直接回车用 qwen3.7-plus）"},
		{Step: "opus", Prompt: "ANTHROPIC_DEFAULT_OPUS_MODEL（直接回车用 qwen3.7-plus）"},
	},
	Fields: []TemplateField{
		{Key: "ANTHROPIC_BASE_URL", Prompt: "ANTHROPIC_BASE_URL", Default: "https://coding.dashscope.aliyuncs.com/apps/anthropic", Required: true},
		{Key: "ANTHROPIC_AUTH_TOKEN", Prompt: "ANTHROPIC_AUTH_TOKEN", Required: true, Secret: true},
		{Key: "ANTHROPIC_MODEL", Prompt: "ANTHROPIC_MODEL", Default: "qwen3.7-plus", Required: true},
		{Key: "ANTHROPIC_DEFAULT_HAIKU_MODEL", Prompt: "ANTHROPIC_DEFAULT_HAIKU_MODEL", Default: "qwen3.7-plus", Required: true},
		{Key: "ANTHROPIC_DEFAULT_SONNET_MODEL", Prompt: "ANTHROPIC_DEFAULT_SONNET_MODEL", Default: "qwen3.7-plus", Required: true},
		{Key: "ANTHROPIC_DEFAULT_OPUS_MODEL", Prompt: "ANTHROPIC_DEFAULT_OPUS_MODEL", Default: "qwen3.7-plus", Required: true},
	},
}

// verified 2026-09-27: https://help.aliyun.com/zh/model-studio/claude-code
// 阿里云百炼按量计费；BASE_URL 含 {WorkspaceId} 占位符，必须由用户填入。
// Body 里的占位符是字面量，--template 打印时保留字面量；交互流强制用户填值后替换。
var aliyunPayTpl = Template{
	Name:        "aliyun-pay",
	Description: "阿里云百炼按量计费（需填 WorkspaceId；qwen3.7-max）",
	Body: `{
  "env": {
    "ANTHROPIC_AUTH_TOKEN": "<百炼_API_KEY>",
    "ANTHROPIC_BASE_URL": "https://{WorkspaceId}.cn-beijing.maas.aliyuncs.com/apps/anthropic",
    "ANTHROPIC_MODEL": "qwen3.7-max",
    "ANTHROPIC_DEFAULT_HAIKU_MODEL": "qwen3.6-flash",
    "ANTHROPIC_DEFAULT_SONNET_MODEL": "qwen3.7-max",
    "ANTHROPIC_DEFAULT_OPUS_MODEL": "qwen3.7-max",
    "CLAUDE_CODE_SUBAGENT_MODEL": "qwen3.7-max"
  }
}`,
	Hints: []Hint{
		{Step: "name", Prompt: "提供商名称（用于文件名，如 'pay-prod'）"},
		{Step: "workspace_id", Prompt: "WorkspaceId（在阿里云百炼控制台获取；必填）"},
		{Step: "base_url", Prompt: "ANTHROPIC_BASE_URL（按 WorkspaceId 自动拼装）"},
		{Step: "auth_token", Prompt: "ANTHROPIC_AUTH_TOKEN（百炼 API Key，不回显）"},
		{Step: "model", Prompt: "ANTHROPIC_MODEL（直接回车用 qwen3.7-max）"},
		{Step: "haiku", Prompt: "ANTHROPIC_DEFAULT_HAIKU_MODEL（直接回车用 qwen3.6-flash）"},
		{Step: "sonnet", Prompt: "ANTHROPIC_DEFAULT_SONNET_MODEL（直接回车用 qwen3.7-max）"},
		{Step: "opus", Prompt: "ANTHROPIC_DEFAULT_OPUS_MODEL（直接回车用 qwen3.7-max）"},
		{Step: "subagent", Prompt: "CLAUDE_CODE_SUBAGENT_MODEL（直接回车用 qwen3.7-max）"},
	},
	Fields: []TemplateField{
		{Key: "WorkspaceId", Prompt: "WorkspaceId", Required: true, Placeholder: "{WorkspaceId}"},
		{Key: "ANTHROPIC_BASE_URL", Prompt: "ANTHROPIC_BASE_URL", Default: "https://{WorkspaceId}.cn-beijing.maas.aliyuncs.com/apps/anthropic", Required: true},
		{Key: "ANTHROPIC_AUTH_TOKEN", Prompt: "ANTHROPIC_AUTH_TOKEN", Required: true, Secret: true},
		{Key: "ANTHROPIC_MODEL", Prompt: "ANTHROPIC_MODEL", Default: "qwen3.7-max", Required: true},
		{Key: "ANTHROPIC_DEFAULT_HAIKU_MODEL", Prompt: "ANTHROPIC_DEFAULT_HAIKU_MODEL", Default: "qwen3.6-flash", Required: true},
		{Key: "ANTHROPIC_DEFAULT_SONNET_MODEL", Prompt: "ANTHROPIC_DEFAULT_SONNET_MODEL", Default: "qwen3.7-max", Required: true},
		{Key: "ANTHROPIC_DEFAULT_OPUS_MODEL", Prompt: "ANTHROPIC_DEFAULT_OPUS_MODEL", Default: "qwen3.7-max", Required: true},
		{Key: "CLAUDE_CODE_SUBAGENT_MODEL", Prompt: "CLAUDE_CODE_SUBAGENT_MODEL", Default: "qwen3.7-max", Required: true},
	},
}

// verified 2026-09-27: https://help.aliyun.com/zh/model-studio/claude-code
// 阿里云百炼 Token Plan（个人版/团队版同 URL），主模型用 auto 自动路由。
var aliyunTokenTpl = Template{
	Name:        "aliyun-token",
	Description: "阿里云百炼 Token Plan（token-plan.cn-beijing.maas.aliyuncs.com/apps/anthropic，主模型 auto）",
	Body: `{
  "env": {
    "ANTHROPIC_AUTH_TOKEN": "<Token_Plan_API_Key>",
    "ANTHROPIC_BASE_URL": "https://token-plan.cn-beijing.maas.aliyuncs.com/apps/anthropic",
    "ANTHROPIC_MODEL": "auto",
    "ANTHROPIC_DEFAULT_HAIKU_MODEL": "qwen3.6-flash",
    "ANTHROPIC_DEFAULT_SONNET_MODEL": "qwen3.8-flash",
    "ANTHROPIC_DEFAULT_OPUS_MODEL": "qwen3.8-max",
    "CLAUDE_CODE_SUBAGENT_MODEL": "auto"
  }
}`,
	Hints: []Hint{
		{Step: "name", Prompt: "提供商名称（用于文件名，如 'token-individual'）"},
		{Step: "base_url", Prompt: "ANTHROPIC_BASE_URL（直接回车用模板默认）"},
		{Step: "auth_token", Prompt: "ANTHROPIC_AUTH_TOKEN（Token Plan 专属 API Key，不回显）"},
		{Step: "model", Prompt: "ANTHROPIC_MODEL（直接回车用 auto）"},
		{Step: "haiku", Prompt: "ANTHROPIC_DEFAULT_HAIKU_MODEL（直接回车用 qwen3.6-flash）"},
		{Step: "sonnet", Prompt: "ANTHROPIC_DEFAULT_SONNET_MODEL（直接回车用 qwen3.8-flash）"},
		{Step: "opus", Prompt: "ANTHROPIC_DEFAULT_OPUS_MODEL（直接回车用 qwen3.8-max）"},
		{Step: "subagent", Prompt: "CLAUDE_CODE_SUBAGENT_MODEL（直接回车用 auto）"},
	},
	Fields: []TemplateField{
		{Key: "ANTHROPIC_BASE_URL", Prompt: "ANTHROPIC_BASE_URL", Default: "https://token-plan.cn-beijing.maas.aliyuncs.com/apps/anthropic", Required: true},
		{Key: "ANTHROPIC_AUTH_TOKEN", Prompt: "ANTHROPIC_AUTH_TOKEN", Required: true, Secret: true},
		{Key: "ANTHROPIC_MODEL", Prompt: "ANTHROPIC_MODEL", Default: "auto", Required: true},
		{Key: "ANTHROPIC_DEFAULT_HAIKU_MODEL", Prompt: "ANTHROPIC_DEFAULT_HAIKU_MODEL", Default: "qwen3.6-flash", Required: true},
		{Key: "ANTHROPIC_DEFAULT_SONNET_MODEL", Prompt: "ANTHROPIC_DEFAULT_SONNET_MODEL", Default: "qwen3.8-flash", Required: true},
		{Key: "ANTHROPIC_DEFAULT_OPUS_MODEL", Prompt: "ANTHROPIC_DEFAULT_OPUS_MODEL", Default: "qwen3.8-max", Required: true},
		{Key: "CLAUDE_CODE_SUBAGENT_MODEL", Prompt: "CLAUDE_CODE_SUBAGENT_MODEL", Default: "auto", Required: true},
	},
}

// 官方直连：没有 BASE_URL；只填 AUTH_TOKEN。
// 描述保持简洁（"Anthropic 官方直连（用官方 API Key）"），--help 输出用得上。
var anthropicTpl = Template{
	Name:        "anthropic",
	Description: "Anthropic 官方直连（用官方 API Key，无 BASE_URL）",
	Body: `{
  "env": {
    "ANTHROPIC_AUTH_TOKEN": "sk-ant-xxx"
  }
}`,
	Hints: []Hint{
		{Step: "name", Prompt: "提供商名称（用于文件名，如 'official'）"},
		{Step: "auth_token", Prompt: "ANTHROPIC_AUTH_TOKEN（Anthropic 官方 API Key，不回显）"},
	},
	Fields: []TemplateField{
		{Key: "ANTHROPIC_AUTH_TOKEN", Prompt: "ANTHROPIC_AUTH_TOKEN", Required: true, Secret: true},
	},
}