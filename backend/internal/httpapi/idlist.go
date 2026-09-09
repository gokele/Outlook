package httpapi

import (
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
)

// IDList 是一组账号 ID，同时接受数字与字符串两种形式。
//
// 浏览器端的表格组件把行键统一成 string | number，JSON 序列化后可能是 "12" 而不是 12；
// 第三方调用方也常把 ID 当字符串传。与其让这类请求以 400 失败，不如在解析层容忍，
// 因为语义上没有歧义。空串与 null 元素被忽略，真正非法的值仍会报错。
type IDList []int64

// UnmarshalJSON 解析混合了数字与字符串的 ID 数组。
func (l *IDList) UnmarshalJSON(b []byte) error {
	var raw []json.RawMessage
	if err := json.Unmarshal(b, &raw); err != nil {
		return fmt.Errorf("ids 必须是数组: %w", err)
	}
	out := make([]int64, 0, len(raw))
	for i, item := range raw {
		// null 必须先判掉：Go 的 JSON 解码把 null 视为不赋值并返回 nil 错误，
		// 若交给下面的数字分支会被静默当成 0。
		if string(item) == "null" {
			continue
		}
		var n int64
		if err := json.Unmarshal(item, &n); err == nil {
			out = append(out, n)
			continue
		}
		var s string
		if err := json.Unmarshal(item, &s); err == nil {
			if s == "" {
				continue
			}
			v, perr := strconv.ParseInt(s, 10, 64)
			if perr != nil {
				return fmt.Errorf("ids[%d] 不是合法的账号 ID: %q", i, s)
			}
			out = append(out, v)
			continue
		}
		return fmt.Errorf("ids[%d] 必须是数字或数字字符串，实际为 %s", i, string(item))
	}
	*l = out
	return nil
}

// NullableID 是可为空的单个 ID，同时接受数字、数字字符串与 null。
// 与 IDList 同理：前端表单控件常把选中值带成字符串，语义上没有歧义，
// 在解析层容忍好过让请求以 400 失败。
type NullableID struct {
	Value *int64
}

// UnmarshalJSON 解析数字、数字字符串、null 与空串。
func (n *NullableID) UnmarshalJSON(b []byte) error {
	s := string(b)
	if s == "null" || s == `""` {
		n.Value = nil
		return nil
	}
	var v int64
	if err := json.Unmarshal(b, &v); err == nil {
		n.Value = &v
		return nil
	}
	var str string
	if err := json.Unmarshal(b, &str); err == nil {
		parsed, perr := strconv.ParseInt(str, 10, 64)
		if perr != nil {
			return fmt.Errorf("不是合法的 ID: %q", str)
		}
		n.Value = &parsed
		return nil
	}
	return fmt.Errorf("必须是数字、数字字符串或 null，实际为 %s", s)
}

// MarshalJSON 让该类型也能正确序列化，便于回显。
func (n NullableID) MarshalJSON() ([]byte, error) {
	if n.Value == nil {
		return []byte("null"), nil
	}
	return json.Marshal(*n.Value)
}

// maxQueryIDs 限制查询串里能带的 ID 个数。
// 上限取导出的单批容量，超出这个量应当改用筛选条件而不是逐个勾选。
const maxQueryIDs = 2000

// parseQueryIDs 解析查询串里逗号分隔的 ID 列表，如 ids=1,2,3。
//
// 与 IDList 的区别：那个用于 JSON 请求体，这里用于 GET 的查询串
// （导出走浏览器直接下载，只能用 GET）。
func parseQueryIDs(raw string) ([]int64, error) {
	parts := strings.Split(raw, ",")
	if len(parts) > maxQueryIDs {
		return nil, fmt.Errorf("一次最多导出 %d 个账号，更多请改用筛选条件", maxQueryIDs)
	}
	out := make([]int64, 0, len(parts))
	seen := make(map[int64]bool, len(parts))
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p == "" {
			continue
		}
		id, err := strconv.ParseInt(p, 10, 64)
		if err != nil {
			return nil, fmt.Errorf("ids 含非法值 %q", p)
		}
		if !seen[id] {
			seen[id] = true
			out = append(out, id)
		}
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("ids 为空")
	}
	return out, nil
}
