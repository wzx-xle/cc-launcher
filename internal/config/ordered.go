package config

import (
	"bytes"
	"encoding/json"
)

// orderedMap 保持插入顺序的 map，用于保留 settings.json 原有字段顺序。
type orderedMap struct {
	order  []string
	values map[string]any
}

func newOrderedMap() *orderedMap {
	return &orderedMap{values: map[string]any{}}
}

// MarshalJSON 实现 json.Marshaler 接口，保持 key 顺序。
func (om *orderedMap) MarshalJSON() ([]byte, error) {
	return encodeOrdered(om)
}

// UnmarshalJSON 实现 json.Unmarshaler 接口，保持 key 顺序。
func (om *orderedMap) UnmarshalJSON(data []byte) error {
	parsed, err := decodeOrdered(data)
	if err != nil {
		return err
	}
	om.order = parsed.order
	om.values = parsed.values
	return nil
}

func (om *orderedMap) get(key string) (any, bool) {
	v, ok := om.values[key]
	return v, ok
}

func (om *orderedMap) set(key string, val any) {
	if _, exists := om.values[key]; !exists {
		om.order = append(om.order, key)
	}
	om.values[key] = val
}

func (om *orderedMap) del(key string) {
	delete(om.values, key)
	for i, k := range om.order {
		if k == key {
			om.order = append(om.order[:i], om.order[i+1:]...)
			return
		}
	}
}

func (om *orderedMap) forEach(fn func(key string, val any)) {
	for _, k := range om.order {
		fn(k, om.values[k])
	}
}

func (om *orderedMap) len() int {
	return len(om.values)
}

func (om *orderedMap) toMap() map[string]any {
	m := make(map[string]any, len(om.values))
	for _, k := range om.order {
		v := om.values[k]
		if nested, ok := v.(*orderedMap); ok {
			m[k] = nested.toMap()
		} else {
			m[k] = v
		}
	}
	return m
}

func mapToOrdered(m map[string]any) *orderedMap {
	om := newOrderedMap()
	for k, v := range m {
		if nested, ok := v.(map[string]any); ok {
			om.set(k, mapToOrdered(nested))
		} else {
			om.set(k, v)
		}
	}
	return om
}

// decodeOrdered 将 JSON 字节解析为有序树。对象 → *orderedMap，数组 → []any。
func decodeOrdered(data []byte) (*orderedMap, error) {
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.UseNumber()
	val, err := decodeValue(dec)
	if err != nil {
		return nil, err
	}
	if om, ok := val.(*orderedMap); ok {
		return om, nil
	}
	return newOrderedMap(), nil
}

func decodeValue(d *json.Decoder) (any, error) {
	t, err := d.Token()
	if err != nil {
		return nil, err
	}

	switch tok := t.(type) {
	case json.Delim:
		if tok == '{' {
			om := newOrderedMap()
			for d.More() {
				keyTok, err := d.Token()
				if err != nil {
					return nil, err
				}
				key := keyTok.(string)
				val, err := decodeValue(d)
				if err != nil {
					return nil, err
				}
				om.set(key, val)
			}
			d.Token() // }
			return om, nil
		}
		if tok == '[' {
			var arr []any
			for d.More() {
				val, err := decodeValue(d)
				if err != nil {
					return nil, err
				}
				arr = append(arr, val)
			}
			d.Token() // ]
			return arr, nil
		}
	case json.Number:
		// 尝试转 int，失败则转 float64
		if i, err := tok.Int64(); err == nil {
			return i, nil
		}
		f, _ := tok.Float64()
		return f, nil
	default:
		return tok, nil
	}
	return nil, nil
}

// encodeOrdered 将 orderedMap 序列化为 JSON 字节，保持 key 顺序。
func encodeOrdered(om *orderedMap) ([]byte, error) {
	var buf bytes.Buffer
	if err := writeOrderedValue(&buf, om); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

func writeOrderedValue(buf *bytes.Buffer, v any) error {
	switch val := v.(type) {
	case *orderedMap:
		buf.WriteByte('{')
		for i, key := range val.order {
			if i > 0 {
				buf.WriteByte(',')
			}
			keyJSON, _ := json.Marshal(key)
			buf.Write(keyJSON)
			buf.WriteByte(':')
			if err := writeOrderedValue(buf, val.values[key]); err != nil {
				return err
			}
		}
		buf.WriteByte('}')
	case []any:
		buf.WriteByte('[')
		for i, item := range val {
			if i > 0 {
				buf.WriteByte(',')
			}
			if err := writeOrderedValue(buf, item); err != nil {
				return err
			}
		}
		buf.WriteByte(']')
	default:
		valJSON, err := json.Marshal(val)
		if err != nil {
			return err
		}
		buf.Write(valJSON)
	}
	return nil
}

