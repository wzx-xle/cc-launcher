// Package settingsschema 校验 ccl 交付给 Claude Code 的 settings 对象。
//
// 背景（provider-settings-load-guard）：
//
// Claude Code 的 settings 文件用 zod 校验，**一个已知键的类型不对，整个文件
// 被丢弃**（"These settings files did not load, so nothing in them is in
// effect, including hooks and permission rules"）。对 ccl 来说这是致命的——
// provider 的凭证就住在那个文件里，文件一丢，Claude Code 会静默退回用户自己
// 的 provider，ccl 选中的 provider 根本没生效，而 ccl 一个字都不说。
//
// Unix 上 ccl 用 syscall.Exec 替换进程（internal/launcher/launcher_unix.go），
// 没有观察 Claude Code 输出的时机，所以校验必须发生在 ccl 侧。
//
// 校验的分级（宁可不拦，也不误拦）：
//
//   - 已知键的值类型明显不对 → **报错，阻止启动**（会让 Claude Code 丢整个文件）
//   - env 块里的值不是字符串 → **报错，阻止启动**（最不能坏的键）
//   - 元素类型为 string 的已知数组里混入坏元素 → **剔除该元素 + 警告**
//     （Claude Code 自己也容忍这种情况，只丢该元素）
//   - 未知键 → **放行**（Claude Code 对未知顶层键是静默忽略；若把未知键当错误，
//     用户一升级 Claude Code，ccl 就会突然拒绝启动）
package settingsschema

import (
	"encoding/json"
	"fmt"
	"sort"
)

// Kind 是 settings 值的类型大类。
type Kind int

const (
	// KindAny 不校验（未知键、元素类型不确定的数组）。
	KindAny Kind = iota
	// KindString 字符串。
	KindString
	// KindNumber 数字。
	KindNumber
	// KindBool 布尔。
	KindBool
	// KindArray 数组。
	KindArray
	// KindObject 对象。
	KindObject
)

// String 返回类型的中文名（用于错误信息）。
func (k Kind) String() string {
	switch k {
	case KindString:
		return "字符串"
	case KindNumber:
		return "数字"
	case KindBool:
		return "布尔值"
	case KindArray:
		return "数组"
	case KindObject:
		return "对象"
	default:
		return "任意类型"
	}
}

// Node 描述一个 settings 值的形状。
type Node struct {
	// Kind 值的大类。
	Kind Kind
	// Elem 是数组的元素类型（KindArray 时有效）。nil 表示不校验元素。
	Elem *Node
	// Fields 是对象的字段类型（KindObject 时有效）。nil 表示不校验内部。
	Fields map[string]*Node
}

// Result 是一次校验的结果。
type Result struct {
	// Cleaned 是通过校验（并剔除可容忍的坏元素）后的对象。
	Cleaned map[string]any
	// Errors 是必须阻止启动的错误。
	Errors []string
	// Warnings 是不阻止启动的警告。
	Warnings []string
}

// OK 报告校验是否通过（没有阻断性错误）。
func (r Result) OK() bool { return len(r.Errors) == 0 }

// Validate 校验一个 settings 对象。
//
// obj 为 nil 时返回空结果（无错误）。
func Validate(obj map[string]any) Result {
	res := Result{Cleaned: map[string]any{}}
	if obj == nil {
		return res
	}
	for k, v := range obj {
		n, known := topLevel[k]
		if !known {
			// 未知顶层键 → 放行（Claude Code 自己也是静默忽略）。
			res.Cleaned[k] = v
			continue
		}
		cleaned, ok := validateNode(k, n, v, &res, true)
		if !ok {
			continue
		}
		res.Cleaned[k] = cleaned
	}
	// 错误与警告按字典序输出（map 遍历顺序随机，不排序则每次报错内容都不一样）。
	sort.Strings(res.Errors)
	sort.Strings(res.Warnings)
	return res
}

// validateNode 按节点描述校验一个值。
//
// fatal 决定类型不符时的后果：
//   - true（顶层键）：**报错并阻止启动**——Claude Code 会因此丢弃整个
//     settings 文件，连 provider 的凭证一起消失。
//   - false（对象字段 / 数组元素）：**丢弃该节点 + 警告**——Claude Code 对
//     这种粒度的错误也只丢该节点，文件其余部分仍然生效。
//
// 返回 (清洗后的值, 是否保留)。返回 false 时该节点已被丢弃。
func validateNode(path string, n *Node, v any, res *Result, fatal bool) (any, bool) {
	if v == nil {
		return nil, true // null 一律放行
	}
	mismatch := func(want string) (any, bool) {
		msg := fmt.Sprintf("%s: 期望%s，实际是 %s", path, want, typeName(v))
		if fatal {
			res.Errors = append(res.Errors, msg)
		} else {
			res.Warnings = append(res.Warnings, msg+"（该值已被丢弃）")
		}
		return nil, false
	}
	switch n.Kind {
	case KindAny:
		return v, true

	case KindString:
		if _, ok := v.(string); !ok {
			return mismatch("字符串")
		}
		return v, true

	case KindNumber:
		if _, ok := toFloat(v); !ok {
			return mismatch("数字")
		}
		return v, true

	case KindBool:
		if _, ok := v.(bool); !ok {
			return mismatch("布尔值")
		}
		return v, true

	case KindArray:
		arr, ok := v.([]any)
		if !ok {
			return mismatch("数组")
		}
		if n.Elem == nil {
			return v, true // 元素类型不确定 → 不校验元素
		}
		out := make([]any, 0, len(arr))
		for i, item := range arr {
			cleaned, ok := validateElem(fmt.Sprintf("%s[%d]", path, i), n.Elem, item, res)
			if !ok {
				continue // 坏元素已剔除并记入警告
			}
			out = append(out, cleaned)
		}
		return out, true

	case KindObject:
		obj, ok := v.(map[string]any)
		if !ok {
			return mismatch("对象")
		}
		if n.Fields == nil {
			return v, true // 内部结构不校验
		}
		out := make(map[string]any, len(obj))
		for k, item := range obj {
			f, known := n.Fields[k]
			if !known {
				out[k] = item // 对象里的未知字段 → 放行
				continue
			}
			// 对象字段级 → 容忍（丢弃该字段 + 警告），不阻断整个文件。
			cleaned, ok := validateNode(path+"."+k, f, item, res, false)
			if !ok {
				continue
			}
			out[k] = cleaned
		}
		return out, true
	}
	return v, true
}

// validateElem 校验数组里的一个元素。
//
// 元素类型不符是**可容忍**的：剔除该元素 + 警告。Claude Code 对这种情况也只
// 丢这个元素，文件其余部分仍然生效（与"整个文件被丢弃"是完全不同的量级）。
func validateElem(path string, n *Node, v any, res *Result) (any, bool) {
	if v == nil {
		return nil, true
	}
	switch n.Kind {
	case KindAny:
		return v, true
	case KindString:
		if _, ok := v.(string); !ok {
			res.Warnings = append(res.Warnings,
				fmt.Sprintf("%s: 非字符串元素已剔除", path))
			return nil, false
		}
	case KindNumber:
		if _, ok := toFloat(v); !ok {
			res.Warnings = append(res.Warnings,
				fmt.Sprintf("%s: 非数字元素已剔除", path))
			return nil, false
		}
	case KindBool:
		if _, ok := v.(bool); !ok {
			res.Warnings = append(res.Warnings,
				fmt.Sprintf("%s: 非布尔元素已剔除", path))
			return nil, false
		}
	default:
		return v, true
	}
	return v, true
}

// toFloat 把 JSON 里的数字转成 float64。
func toFloat(v any) (float64, bool) {
	switch n := v.(type) {
	case float64:
		return n, true
	case float32:
		return float64(n), true
	case int:
		return float64(n), true
	case int32:
		return float64(n), true
	case int64:
		return float64(n), true
	case json.Number:
		f, err := n.Float64()
		return f, err == nil
	}
	return 0, false
}

// typeName 返回 JSON 值的类型中文名（用于错误信息）。
func typeName(v any) string {
	switch v.(type) {
	case string:
		return "字符串"
	case bool:
		return "布尔值"
	case float64, float32, int, int32, int64, json.Number:
		return "数字"
	case []any:
		return "数组"
	case map[string]any:
		return "对象"
	case nil:
		return "null"
	}
	return fmt.Sprintf("%T", v)
}
