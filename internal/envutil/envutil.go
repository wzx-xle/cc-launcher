// Package envutil 提供环境变量 slice 操作的辅助函数。
//
// 典型用法：构造 `[]string{"KEY=VALUE", ...}` 形式的进程环境列表，传入
// syscall.Exec / exec.Cmd.Env 前用 Upsert 注入 provider env（ccl
// provider-setenv-cwd-binding change）。
package envutil

import "strings"

// Upsert 在 env slice 中替换 key 对应的条目；若不存在则追加到末尾。
//
// 语义对齐 spec/launcher-provider-env §"upsertEnv helper" Scenario：
//   - 现有 key → 值被替换，env 长度不变
//   - 缺失 key → 追加到末尾
//   - nil slice → append 正常工作，返回单元素 slice
//
// 匹配规则：仅匹配以 "key=" 开头的 entry（POSIX env 格式）；env 里出现
// 多个同 key 条目（POSIX 非法但实际可能——老 settings.json 残留）时只
// 替换第一个，保留其余。
//
// 注意：Upsert 复用调用方传入的底层数组——直接修改 env[i] 而非返回新 slice。
// 调用方应避免在 Upsert 返回后仍持有旧引用。
func Upsert(env []string, key, value string) []string {
	prefix := key + "="
	for i, e := range env {
		if strings.HasPrefix(e, prefix) {
			env[i] = prefix + value
			return env
		}
	}
	return append(env, prefix+value)
}
