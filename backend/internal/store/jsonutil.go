package store

import "encoding/json"

// jsonMarshal 序列化为字符串，供存入 TEXT 列。
func jsonMarshal(v any) (string, error) {
	b, err := json.Marshal(v)
	return string(b), err
}

// jsonUnmarshal 从 TEXT 列读出的字符串反序列化。
func jsonUnmarshal(s string, v any) error {
	if s == "" {
		return nil
	}
	return json.Unmarshal([]byte(s), v)
}
