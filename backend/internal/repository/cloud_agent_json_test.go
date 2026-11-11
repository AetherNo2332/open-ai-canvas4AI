package repository

import "testing"

func TestSameJSONDocumentPreservesValueTypes(t *testing.T) {

	for _, item := range []struct {
		name, left, right string
		equal             bool
	}{
		{"number spellings", `{"x":24.0,"p":[0,1e2]}`, `{"p":[0.0,100],"x":24}`, true},
		{"number versus string", `{"x":1}`, `{"x":"1"}`, false},
		{"fraction versus string", `{"x":0.5}`, `{"x":"1/2"}`, false},
		{"float round trip", `{"args":{"x":4140.7990379296753}}`, `{"args":{"x":4140.799037929675}}`, true},
		{"float versus string", `{"x":4140.7990379296753}`, `{"x":"4140.799037929675"}`, false},
		{"large integer precision", `{"x":9007199254740992}`, `{"x":9007199254740993}`, false},
		{"different floats", `{"x":4140.799037929675}`, `{"x":4140.799037929676}`, false},
		{"negative zero", `{"x":-0.0}`, `{"x":0}`, true},
		{"changed position", `{"x":24}`, `{"x":25}`, false},
	} {
		t.Run(item.name, func(t *testing.T) {
			if got := sameJSONDocument(item.left, item.right); got != item.equal {
				t.Fatalf("sameJSONDocument=%v, want %v", got, item.equal)
			}
		})
	}
}
