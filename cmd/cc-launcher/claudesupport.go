package main

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// claudeSupportFile 是"Claude Code 是否支持 --settings"的探测结果缓存文件。
//
// 按 claude 可执行文件的**指纹**（路径 + 大小 + mtime）缓存：换了版本或换了
// 安装位置 → 指纹变 → 重新探测；什么都没动 → 直接用缓存，不给每次启动加几百
// 毫秒。
const claudeSupportFileName = "claude-support.json"

// claudeSupport 是探测结果。
type claudeSupport struct {
	// Path 是被探测的 claude 可执行文件路径。
	Path string `json:"path"`
	// Size 是文件大小（字节）。
	Size int64 `json:"size"`
	// ModTimeUnixNano 是修改时间（纳秒）。
	ModTimeUnixNano int64 `json:"modTimeUnixNano"`
	// Supported 表示它是否接受 --settings。
	Supported bool `json:"supported"`
}

// ensureSettingsSupported 确认当前 Claude Code 接受 `--settings` 参数。
//
// **为什么要在启动前确认**：provider 的配置全靠这个参数交付。Claude Code 不认
// 它的时候，provider 的凭证根本到不了 Claude Code——会话会跑在用户自己
// settings.json 里的 provider 上，而用户以为选中的 provider 生效了
// （provider-settings-load-guard）。
//
// 探测失败（claude 找不到、跑不起来）**不算"不支持"**：那种情况让 claude 自己
// 去报错更贴切，ccl 不越权拦。
func ensureSettingsSupported() error {
	path, err := exec.LookPath("claude")
	if err != nil {
		return nil // 找不到 claude → 交给 launcher 报"未找到 claude 命令"
	}

	fp, err := claudeFileFingerprint(path)
	if err != nil {
		return nil // 拿不到指纹 → 跳过探测
	}

	cached, ok := readClaudeSupportCache(fp)
	if ok {
		if !cached.Supported {
			return fmt.Errorf("当前 Claude Code（%s）不支持 --settings 参数，"+
				"ccl 无法把选中的提供商配置交付给它。请升级 Claude Code 后重试", path)
		}
		return nil
	}

	supported := probeClaudeSettingsSupport(path)

	// 缓存失败不影响本次判断。
	_ = writeClaudeSupportCache(claudeSupport{
		Path:            fp.Path,
		Size:            fp.Size,
		ModTimeUnixNano: fp.ModTimeUnixNano,
		Supported:       supported,
	})

	if !supported {
		return fmt.Errorf("当前 Claude Code（%s）不支持 --settings 参数，"+
			"ccl 无法把选中的提供商配置交付给它。请升级 Claude Code 后重试", path)
	}
	return nil
}

// probeClaudeSettingsSupport 跑一次 `claude --help`，看输出里有没有 --settings。
func probeClaudeSettingsSupport(path string) bool {
	out, err := exec.Command(path, "--help").CombinedOutput()
	if err != nil {
		// claude 跑不起来（没装好 / 需要交互）→ 不下结论，交给 claude 自己报错。
		return true
	}
	return strings.Contains(string(out), "--settings")
}

// claudeFingerprint 是 claude 可执行文件的指纹。
type claudeFingerprint struct {
	Path            string
	Size            int64
	ModTimeUnixNano int64
}

func claudeFileFingerprint(path string) (claudeFingerprint, error) {
	info, err := os.Stat(path)
	if err != nil {
		return claudeFingerprint{}, err
	}
	return claudeFingerprint{
		Path:            path,
		Size:            info.Size(),
		ModTimeUnixNano: info.ModTime().UnixNano(),
	}, nil
}

// claudeSupportCachePath 返回探测结果缓存文件的路径。
func claudeSupportCachePath() (string, error) {
	dir, err := settingsCacheDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, claudeSupportFileName), nil
}

// readClaudeSupportCache 读缓存；指纹不匹配或文件损坏 → 返回 false。
func readClaudeSupportCache(fp claudeFingerprint) (claudeSupport, bool) {
	path, err := claudeSupportCachePath()
	if err != nil {
		return claudeSupport{}, false
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return claudeSupport{}, false
	}
	var cs claudeSupport
	if err := json.Unmarshal(data, &cs); err != nil {
		return claudeSupport{}, false
	}
	if cs.Path != fp.Path || cs.Size != fp.Size || cs.ModTimeUnixNano != fp.ModTimeUnixNano {
		return claudeSupport{}, false // claude 换了 → 重新探测
	}
	return cs, true
}

// writeClaudeSupportCache 写探测结果缓存（失败只返回错误，调用方忽略）。
func writeClaudeSupportCache(cs claudeSupport) error {
	path, err := claudeSupportCachePath()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return err
	}
	data, err := json.MarshalIndent(cs, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, data, 0600)
}
