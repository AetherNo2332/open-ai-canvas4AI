package repository

import (
	"bytes"
	"encoding/json"
	"fmt"
	"math/big"
	"reflect"
	"strconv"
	"strings"
)

// sameJSONDocument compares event records by JSON meaning rather than source
// bytes. Event payloads may contain json.RawMessage (for example tool arguments),
// so decode/re-encode can legally normalize whitespace, object key order, or
// numeric spellings such as 0.0 and 0 while preserving the immutable event
// contract.
func sameJSONDocument(left, right string) bool {
	decode := func(raw string) (any, error) {
		decoder := json.NewDecoder(bytes.NewReader([]byte(raw)))
		decoder.UseNumber()
		var value any
		if err := decoder.Decode(&value); err != nil {
			return nil, err
		}
		var extra any
		if err := decoder.Decode(&extra); err == nil {
			return nil, fmt.Errorf("multiple JSON documents")
		}
		return canonicalJSONValue(value), nil
	}
	leftValue, leftErr := decode(left)
	rightValue, rightErr := decode(right)
	if leftErr != nil || rightErr != nil {
		return left == right
	}
	return reflect.DeepEqual(leftValue, rightValue)
}

type canonicalJSONNumber string

func canonicalJSONValue(value any) any {
	switch typed := value.(type) {
	case json.Number:
		raw := typed.String()
		// 工具参数里的浮点原文（模型给出）与 float64 的最短表示可能不同，
		// 例如 4140.7990379296753 与 4140.799037929675 是同一 float64 值。
		// 写库前会按 float64 重新编码事件体，若仍用精确有理数比较，会把这种
		// 等价数值误判成"历史事件被改写"。小数/科学计数按 float64 归一，整数仍精确。
		if strings.ContainsAny(raw, ".eE") {
			if f, err := strconv.ParseFloat(raw, 64); err == nil {
				raw = strconv.FormatFloat(f, 'g', -1, 64)
			}
		}
		if rational, ok := new(big.Rat).SetString(raw); ok {
			return canonicalJSONNumber(rational.RatString())
		}
		return canonicalJSONNumber(raw)
	case []any:
		for index := range typed {
			typed[index] = canonicalJSONValue(typed[index])
		}
	case map[string]any:
		for key, nested := range typed {
			typed[key] = canonicalJSONValue(nested)
		}
	}
	return value
}
