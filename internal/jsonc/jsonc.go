// Package jsonc 解析带注释的 JSON（JSONC）。
//
// **为什么需要**：provider 的配置文件要能贴说明——「这个 accessKey 去哪申请」
// 「这个 endpoint 是私有网关」这类信息只存在于人的脑子里，而 JSON 的语法
// 不允许注释（写进去就是解析失败，provider 直接加载不了）。
//
// **只支持注释，不支持尾随逗号**：JSONC 规范（jsonc.org）本身不允许尾随逗号，
// 而且尾随逗号在「编辑配置文件」时是误操作的高发区（删一个键忘了删逗号），
// 报错比容忍它更有帮助。
//
// **为什么不用现成的库**：ccl 只有两个直接依赖（bubbletea / lipgloss），是刻意
// 保持轻量的。剥注释本身只有几十行状态机。
package jsonc

import (
	"encoding/json"
	"errors"
	"fmt"
)

// SyntaxError 是**带位置**的解析错误。
//
// **为什么要它**：标准 `json.Unmarshal` 的错误信息**只有字符描述，没有行号**
// （"invalid character 'x' looking for beginning of value"）——用户拿到这句话
// 只能自己在文件里找。而 provider 配置动辄几十行、还带注释，没有行号等于
// 让人猜。
//
// **位置指向原文件**：剥注释时用等长空白替换，**每个字节的位置不变**，所以
// 标准库给的字节偏移仍然对得上原文件。
type SyntaxError struct {
	// Line 是出错行（1 起）。
	Line int
	// Col 是出错列（1 起）。
	Col int
	// Err 是原始错误（`*json.SyntaxError` 或 `*json.UnmarshalTypeError`）。
	Err error
}

// Error 实现 error。
func (e *SyntaxError) Error() string {
	return fmt.Sprintf("第 %d 行第 %d 列: %v", e.Line, e.Col, e.Err)
}

// Unwrap 返回原始错误——**调用方仍能用 errors.As 拿到 *json.SyntaxError**。
func (e *SyntaxError) Unwrap() error { return e.Err }

// Unmarshal 解析带注释的 JSON：先剥注释，再走标准 json.Unmarshal。
//
// **错误语义与 json.Unmarshal 一致**（都是返回 error），所以调用方可以无痛替换；
// 唯一的差别是错误信息里**多了行号与列号**。
func Unmarshal(data []byte, v any) error {
	stripped, err := StripComments(data)
	if err != nil {
		return err
	}
	if err := json.Unmarshal(stripped, v); err != nil {
		if off, ok := offsetOf(err); ok {
			line, col := lineCol(data, off)
			return &SyntaxError{Line: line, Col: col, Err: err}
		}
		return err
	}
	return nil
}

// offsetOf 从标准库的解析错误里取字节偏移。
func offsetOf(err error) (int64, bool) {
	var se *json.SyntaxError
	if errors.As(err, &se) {
		return se.Offset, true
	}
	var ute *json.UnmarshalTypeError
	if errors.As(err, &ute) {
		return ute.Offset, true
	}
	return 0, false
}

// lineCol 把字节偏移换算成行列（1 起）。
func lineCol(data []byte, offset int64) (int, int) {
	if offset > int64(len(data)) {
		offset = int64(len(data))
	}
	line, col := 1, 1
	for i := int64(0); i < offset; i++ {
		if data[i] == '\n' {
			line++
			col = 1
		} else {
			col++
		}
	}
	return line, col
}

// StripComments 把 JSONC 里的注释替换成**等长空白**。
//
// **为什么是等长空白而不是删掉**：删掉会让后面所有行往上移，`json.Unmarshal`
// 报的**行号全错**——用户看到"第 3 行出错"，实际是第 47 行写的注释出了问题，
// 全靠猜。等长空白让每个字节的位置不变，**行号（与列号）仍指向原文件**。
//
// **换行符原样保留**：块注释跨多行时，那几行的行号也必须对得上。
//
// 非法输入（块注释没有闭合）返回 error。
func StripComments(data []byte) ([]byte, error) {
	out := make([]byte, len(data))
	copy(out, data)

	const (
		stateNormal = iota
		stateString
		stateLineComment
		stateBlockComment
	)
	state := stateNormal

	for i := 0; i < len(out); i++ {
		c := out[i]

		switch state {
		case stateNormal:
			switch {
			case c == '"':
				state = stateString
			case c == '/' && i+1 < len(out) && out[i+1] == '/':
				// 行注释：两个斜杠都换成空格
				out[i] = ' '
				out[i+1] = ' '
				i++
				state = stateLineComment
			case c == '/' && i+1 < len(out) && out[i+1] == '*':
				// 块注释：两个斜杠星号都换成空格
				out[i] = ' '
				out[i+1] = ' '
				i++
				state = stateBlockComment
			}

		case stateString:
			// **字符串里的 `//` 不是注释**——provider 配置里全是 URL
			// （https://ark.cn-beijing.volces.com/api/plan），判错就整行废了。
			if c == '\\' {
				// 转义：下一个字节原样跳过（\" \\ \/ …）
				i++
				continue
			}
			if c == '"' {
				state = stateNormal
			}

		case stateLineComment:
			if c == '\n' {
				// 行尾：换行保留，退出注释态
				state = stateNormal
				continue
			}
			out[i] = ' '

		case stateBlockComment:
			if c == '\n' {
				// **块注释里的换行必须保留**（后面那几行的行号要对得上）
				continue
			}
			if c == '*' && i+1 < len(out) && out[i+1] == '/' {
				out[i] = ' '
				out[i+1] = ' '
				i++
				state = stateNormal
				continue
			}
			out[i] = ' '
		}
	}

	if state == stateBlockComment {
		return nil, fmt.Errorf("块注释没有闭合（缺少 */）")
	}
	if state == stateString {
		// 字符串没闭合：交给 json.Unmarshal 去报（它的信息更准：带行号）
		return out, nil
	}
	return out, nil
}

// Strip 是 StripComments 的字符串版本。
func Strip(s string) (string, error) {
	out, err := StripComments([]byte(s))
	if err != nil {
		return "", err
	}
	return string(out), nil
}
