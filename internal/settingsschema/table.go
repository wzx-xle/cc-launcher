package settingsschema

// topLevel 是 Claude Code settings 文件的顶层键 → 类型对照表。
//
// 来源：官方 settings-reference（https://code.claude.com/docs/en/settings-reference）
// 的逐条 Type 字段，抓取时间 2026-10-30，对应 Claude Code CLI 2.1.308。
//
// **维护约定**：Claude Code 加了新键就把新键补进来（未知键本来就不拦，补进来
// 只是让类型写错时能被更早发现）；Claude Code 改了某个键的类型，也要跟着改。
// 补表的风险是"表比实际滞后"——那只会让本该被拦的错误晚点被发现，不会误拦；
// 真正的误拦风险来自"表把某个键的类型写错了"，所以改表时只按文档改，别猜。
//
// 表里的键分五类：字符串 / 数字 / 布尔 / 数组 / 对象。
// 数组的元素类型：只有**确定**是字符串数组的键才校验元素（见 strArr），
// 其余数组的元素类型不确定 → 不校验（宁可漏剔，也不误剔合法配置）。
var topLevel = buildTopLevel()

// buildTopLevel 组装顶层键表。
func buildTopLevel() map[string]*Node {
	m := map[string]*Node{}

	// ---- 字符串（48） ----
	for _, k := range []string{
		"advisorModel", "agent", "apiKeyHelper", "askUserQuestionTimeout",
		"autoMemoryDirectory", "autoUpdatesChannel", "availableModelsMatch",
		"awsAuthRefresh", "awsCredentialExport", "browserExternalPageTools",
		"claudeMd", "crossSessionInbound", "defaultShell", "dialogExpiry",
		"disableAutoMode", "disableDeepLinkRegistration", "editorMode",
		"effortLevel", "feedbackDrafts", "forceLoginGatewayUrl", "forceLoginMethod",
		"forceLoginOrgUUID", "gcpAuthRefresh", "keybindingFlavor", "language",
		"managedSourcesBehavior", "maxEffortLevel", "minimumVersion", "model",
		"otelHeadersHelper", "outputStyle", "parentSettingsBehavior",
		"plansDirectory", "pluginTrustMessage", "prUrlTemplate",
		"preferredNotifChannel", "processWrapper", "promptCacheTtl",
		"requiredMaximumVersion", "requiredMinimumVersion", "subagentPromptCacheTtl",
		"teammateMode", "theme", "timeFormat", "timeZone", "tui", "viewMode",
		"workflowSizeGuideline",
	} {
		m[k] = &Node{Kind: KindString}
	}

	// ---- 数字（9） ----
	for _, k := range []string{
		"autoCompactWindow", "bashOutputMaxChars", "cleanupPeriodDays",
		"desktopSessionCleanupPeriodDays", "feedbackSurveyRate", "maxProseWidth",
		"skillListingBudgetFraction", "skillListingMaxDescChars", "taskOutputMaxChars",
	} {
		m[k] = &Node{Kind: KindNumber}
	}

	// ---- 布尔（67） ----
	for _, k := range []string{
		"agentPushNotifEnabled", "allowAllClaudeAiMcps",
		"allowClaudeInChromeWithManagedMcp", "allowManagedHooksOnly",
		"allowManagedMcpServersOnly", "allowManagedPermissionRulesOnly",
		"alwaysThinkingEnabled", "autoCompactEnabled", "autoContinueAtUsageLimit",
		"autoMemoryEnabled", "autoScrollEnabled", "awaySummaryEnabled",
		"axScreenReader", "bashEditDiffEnabled", "channelsEnabled", "disableAgentView",
		"disableAllHooks", "disableArtifact", "disableBrowserExternalNavigation",
		"disableBundledSkills", "disableClaudeAiConnectors",
		"disableCommandPluginSources", "disableDesktopLocalSessions",
		"disableMobileSimulatorTools", "disableRemoteControl", "disableSideloadFlags",
		"disableSkillShellExecution", "disableWorkflows", "emojiCompletionEnabled",
		"enableAllProjectMcpServers", "enableArtifact", "enableWorkflows",
		"enforceAvailableModels", "fastMode", "fastModePerSessionOptIn",
		"fileCheckpointingEnabled", "forceRemoteSettingsRefresh", "idleCompaction",
		"includeCoAuthoredBy", "includeGitInstructions", "inputNeededNotifEnabled",
		"isolatePeerMachines", "prefersReducedMotion", "promptSuggestionEnabled",
		"remoteControlAtStartup", "respectGitignore", "respondToBashCommands",
		"showClearContextOnPlanAccept", "showThinkingSummaries", "showTurnDuration",
		"skipAutoPermissionPrompt", "skipDangerousModePermissionPrompt",
		"skipWebFetchPreflight", "spinnerTipsEnabled", "switchModelsOnFlag",
		"syncClaudeAiPlugins", "syncClaudeAiSkills", "syntaxHighlightingDisabled",
		"terminalProgressBarEnabled", "terminalTitleFromRename", "ultracode",
		"useAutoModeDuringPlan", "verbose", "voiceEnabled",
		"wheelScrollAccelerationEnabled", "workflowKeywordTriggerEnabled",
		"wslInheritsWindowsSettings",
	} {
		m[k] = &Node{Kind: KindBool}
	}

	// ---- 数组（23） ----
	// 元素类型确定的（字符串数组）→ 校验元素；其余 → 不校验元素。
	for _, k := range []string{
		"allowedChannelPlugins", "allowedHttpHookUrls", "allowedMcpServers",
		"allowedProviders", "appendPlugins", "availableModels", "blockedMarketplaces",
		"claudeMdExcludes", "deniedMcpServers", "deniedModels",
		"disabledMcpjsonServers", "enabledMcpjsonServers", "fallbackModel",
		"footerLinksRegexes", "gatewayInternalNetworks", "httpHookAllowedEnvVars",
		"pluginSuggestionMarketplaces", "prependPlugins", "sshHostAllowlist",
		"strictKnownMarketplaces",
	} {
		m[k] = &Node{Kind: KindArray, Elem: &Node{Kind: KindString}}
	}
	// 元素类型不确定 → 不校验元素（宁可漏剔，也不误剔合法配置）。
	for _, k := range []string{
		"companyAnnouncements", "sshConfigs", "sshHostCommands",
	} {
		m[k] = &Node{Kind: KindArray}
	}

	// ---- 对象（26） ----
	// 内部结构不校验的（交给 Claude Code 自己判断，它对未知 hook 事件名等
	// 有自己的警告机制）。
	for _, k := range []string{
		"attribution", "autoMode", "enabledPlugins", "extraKnownMarketplaces",
		"fileSuggestion", "hooks", "managedMcpServers", "modelOverrides",
		"modelPicker", "modelPricing", "modelSettings", "pluginConfigs",
		"policyHelper", "sandbox", "skillOverrides", "spellcheck",
		"spinnerTipsOverride", "spinnerVerbs", "statusLine", "subagentStatusLine",
		"vimInsertModeRemaps", "voice", "worktree", "strictPluginOnlyCustomization",
	} {
		m[k] = &Node{Kind: KindObject}
	}

	// env 块：内部不校验。
	//
	// 实测（2026-10-30，Claude Code 2.1.308）：env 块里的值不是字符串时，
	// Claude Code **不会**丢弃整个 settings 文件，而是把那个值转成字符串写进
	// 进程环境（数字 123 → "123"）。所以 env 的值类型不是阻断项，由交付层
	// 统一转成字符串并给警告即可。
	m["env"] = &Node{Kind: KindObject}

	// permissions：allow / ask / deny / additionalDirectories 是字符串数组，
	// 其余是标量。permissions 的子键写错不会让 Claude Code 丢整个文件
	// （数组元素写错只丢该元素），所以子键的**类型**不符按可容忍处理。
	m["permissions"] = &Node{Kind: KindObject, Fields: map[string]*Node{
		"allow":                               {Kind: KindArray, Elem: &Node{Kind: KindString}},
		"ask":                                 {Kind: KindArray, Elem: &Node{Kind: KindString}},
		"deny":                                {Kind: KindArray, Elem: &Node{Kind: KindString}},
		"additionalDirectories":               {Kind: KindArray, Elem: &Node{Kind: KindString}},
		"defaultMode":                         {Kind: KindString},
		"disableBypassPermissionsMode":        {Kind: KindString},
		"disableAutoMode":                     {Kind: KindString},
		"blockReadsOutsideWorkingDirectories": {Kind: KindBool},
	}}

	return m
}

// KnownKeys 返回顶层键表里的全部键（供测试与诊断用）。
func KnownKeys() []string {
	keys := make([]string, 0, len(topLevel))
	for k := range topLevel {
		keys = append(keys, k)
	}
	return keys
}
