// Package web 实现用量看板的 HTTP 面：/ui 页面 + /api/stats JSON。
//
// 设计要点（web-usage-stats-ui design.md §D3-D5）：
//   - 看板挂在 daemon 已监听的 :4318 上，不新开端口、不新增进程
//   - token 鉴权：URL 一次性换 session cookie，token 不留在地址栏
//   - session cookie 是 HMAC 签名的时间戳，key = token → 轮换 token 即吊销
//     所有旧 cookie
package web

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

const (
	// TokenFileName 是 token 文件名（放在 ~/.cc-launcher/ 下）。
	TokenFileName = "web-token"

	// SessionCookieName 是 session cookie 的名字。
	SessionCookieName = "ccl_web"

	// SessionTTL 是 session cookie 的有效期（也是签名有效窗口）。
	SessionTTL = 7 * 24 * time.Hour

	// sessionMsgPrefix 是 HMAC 消息前缀（版本标记；换前缀即吊销全部旧 cookie）。
	sessionMsgPrefix = "ccl-web-session-v1|"

	// tokenBytes 是 token 的随机字节数（渲染成 64 个 hex 字符）。
	tokenBytes = 32
)

// LoadOrCreateToken 读 token 文件；文件不存在则生成一个新的并原子写入。
//
// 幂等：文件已存在时**原样保留**（逐字节），不重新生成。
// 写入权限 0600（只有属主可读写）；原子写（tmp + rename）避免半写状态。
func LoadOrCreateToken(path string) (string, error) {
	data, err := os.ReadFile(path)
	if err == nil {
		tok := strings.TrimSpace(string(data))
		if tok != "" {
			return tok, nil
		}
	} else if !os.IsNotExist(err) {
		return "", fmt.Errorf("读 token 文件失败: %w", err)
	}

	tok, err := generateToken()
	if err != nil {
		return "", err
	}
	if err := writeTokenFile(path, tok); err != nil {
		return "", err
	}
	return tok, nil
}

// RotateToken 重新生成 token 并覆盖旧文件。
//
// 覆盖后，旧 token 与由它派生的全部 session cookie 立即失效
// （cookie 的 HMAC key 就是 token）。
func RotateToken(path string) (string, error) {
	tok, err := generateToken()
	if err != nil {
		return "", err
	}
	if err := writeTokenFile(path, tok); err != nil {
		return "", err
	}
	return tok, nil
}

// generateToken 生成 32 字节随机 hex token（64 个小写 hex 字符）。
func generateToken() (string, error) {
	buf := make([]byte, tokenBytes)
	if _, err := rand.Read(buf); err != nil {
		return "", fmt.Errorf("生成随机 token 失败: %w", err)
	}
	return hex.EncodeToString(buf), nil
}

// writeTokenFile 原子写 token 文件（tmp + rename），权限 0600。
func writeTokenFile(path, token string) error {
	if dir := filepath.Dir(path); dir != "" {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return fmt.Errorf("建 token 目录失败: %w", err)
		}
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, []byte(token+"\n"), 0o600); err != nil {
		return fmt.Errorf("写 token 临时文件失败: %w", err)
	}
	// rename 前显式 chmod：某些平台上 umask 会影响创建时的权限位。
	if err := os.Chmod(tmp, 0o600); err != nil {
		_ = os.Remove(tmp)
		return fmt.Errorf("设置 token 文件权限失败: %w", err)
	}
	if err := os.Rename(tmp, path); err != nil {
		_ = os.Remove(tmp)
		return fmt.Errorf("重命名 token 文件失败: %w", err)
	}
	return nil
}

// SignSession 计算 session cookie 的值：`<issuedAtUnix>.<hex(HMAC)>`。
//
// HMAC 的 key 是 token 本身 → 服务端不需要额外的 session 状态，
// 轮换 token 即吊销全部旧 cookie。
func SignSession(token string, issuedAt time.Time) string {
	return strconv.FormatInt(issuedAt.Unix(), 10) + "." + sessionMAC(token, issuedAt)
}

// sessionMAC 计算 session 签名的 hex 形式。
func sessionMAC(token string, issuedAt time.Time) string {
	mac := hmac.New(sha256.New, []byte(token))
	mac.Write([]byte(sessionMsgPrefix + strconv.FormatInt(issuedAt.Unix(), 10)))
	return hex.EncodeToString(mac.Sum(nil))
}

// VerifySession 校验 session cookie 值是否有效。
//
// 校验点：格式、签发时间不超前、有效期未过、HMAC 恒定时间比对相等。
func VerifySession(token, value string, now time.Time) bool {
	if token == "" || value == "" {
		return false
	}
	issuedRaw, sig, ok := strings.Cut(value, ".")
	if !ok {
		return false
	}
	issuedUnix, err := strconv.ParseInt(issuedRaw, 10, 64)
	if err != nil {
		return false
	}
	issuedAt := time.Unix(issuedUnix, 0)
	// 签发时间在未来（超过 1 分钟容差）→ 视为无效
	if issuedAt.After(now.Add(time.Minute)) {
		return false
	}
	if now.Sub(issuedAt) > SessionTTL {
		return false
	}
	return hmac.Equal([]byte(sig), []byte(sessionMAC(token, issuedAt)))
}

// tokenEqual 常量时间比较两个 token。
func tokenEqual(a, b string) bool {
	return hmac.Equal([]byte(a), []byte(b))
}
