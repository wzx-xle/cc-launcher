package web

import (
	"crypto/sha256"
	"encoding/hex"
	"strings"
)

// etagOf 计算内容的实体标签（sha256 前 8 字节 = 16 个 hex 字符）。
//
// 为什么用内容哈希而不是 mtime+size：
//   - mtime 的精度受文件系统限制（有些平台 1 秒），一秒内两次写会撞
//   - 内容哈希天生"内容相同即同一份"，复制粘贴出来的文件也算同一份
//
// 长度取 16 个 hex 字符（64 bit）：够短（放 header 里不啰嗦），够用。
//
// **这不是安全边界**——它是并发检测用的（"我读到的还是不是这一份"）。
// 安全边界是鉴权（token + cookie）。
func etagOf(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:8])
}

// etagMatches 判断客户端带来的 If-Match 是否匹配当前内容。
//
// 比较忽略引号：HTTP 的 etag 语法允许带引号（"abc"），浏览器 fetch 的
// If-Match 头通常带引号，而我们自己算出来的是裸串——两边都得认。
func etagMatches(ifMatch, current string) bool {
	if ifMatch == "" || current == "" {
		return false
	}
	return strings.Trim(ifMatch, `"`) == strings.Trim(current, `"`)
}
