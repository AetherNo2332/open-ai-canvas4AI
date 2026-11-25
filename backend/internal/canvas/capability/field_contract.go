package capability

import (
	"fmt"
	"maps"
	"math"
	"sort"
	"strings"
	"unicode/utf8"
)

// Field value kinds describe only the stored shape. Writable, Editable,
// Projected and the other flags describe consumers and are derived from the
// same declaration.
const (
	KindString     = "string"
	KindNumber     = "number"
	KindBoolean    = "boolean"
	KindStrings    = "strings"
	KindMap        = "map"
	KindCollection = "collection"
)

// NumberBounds declares the accepted interval for a number field. A nil
// pointer means unbounded.
type NumberBounds struct {
	Min float64
	Max float64
	// ExclusiveMin makes the lower bound open (for example duration > 0).
	ExclusiveMin bool
}

// Field is the single declaration of a canvas document field. Path is relative
// to the container root: a node container uses "metadata.content", while a
// future row container would use a row-relative path.
type Field struct {
	Path        string
	Kind        string
	Label       string
	Description string
	// Order determines the field order shown to models and the document printer.
	Order int
	// AddressName is the text-document address name when it differs from the
	// contract key. Empty means the key itself.
	AddressName string

	MaxRunes int
	Bounds   *NumberBounds
	MaxItems int
	Enum     []string
	Element  Fields

	Writable   bool
	Editable   bool
	Duplicable bool
	Projected  bool
	Summary    bool
	Searchable bool
	Secret     bool
	Reference  bool
	Wrap       bool

	// Default is the initial value for a new row. Zero values need not be
	// declared. Seed initializes missing parent containers before a write.
	Default any
	Seed    func(container map[string]any)
	// Read and Store override path-based reads and writes. Read and Store must
	// stay symmetrical.
	Read      func(container map[string]any) (any, bool)
	Store     func(container map[string]any, value any)
	Normalize func(raw any, path string) (any, error)
}

// Coerce validates and normalizes a value supplied by an external author.
func (f Field) Coerce(raw any, path string) (any, error) {
	if f.Normalize != nil {
		return f.Normalize(raw, path)
	}
	switch f.Kind {
	case KindString:
		return f.coerceString(raw, path)
	case KindNumber:
		return f.coerceNumber(raw, path)
	case KindBoolean:
		return f.coerceBoolean(raw, path)
	case KindStrings:
		return f.coerceStrings(raw, path)
	case KindMap:
		return f.coerceMap(raw, path)
	case KindCollection:
		return f.coerceElements(raw, path)
	}
	return nil, fmt.Errorf("%s 的类型契约无效", path)
}

func (f Field) coerceString(raw any, path string) (any, error) {
	text, ok := raw.(string)
	if !ok {
		return nil, fmt.Errorf("%s 必须是字符串", path)
	}
	measured := text
	if f.Wrap {
		measured, _ = WrapSentences(text)
	}
	if f.MaxRunes > 0 && utf8.RuneCountInString(measured) > f.MaxRunes {
		return nil, fmt.Errorf("%s 超出长度限制 %d 字", path, f.MaxRunes)
	}
	if len(f.Enum) > 0 && !containsString(f.Enum, text) {
		return nil, fmt.Errorf("%s 必须是 %s 之一", path, strings.Join(f.Enum, "、"))
	}
	return text, nil
}

func (f Field) coerceNumber(raw any, path string) (any, error) {
	number, ok := raw.(float64)
	if !ok {
		return nil, fmt.Errorf("%s 必须是数字", path)
	}
	if math.IsNaN(number) || math.IsInf(number, 0) {
		return nil, fmt.Errorf("%s 不是有效数字", path)
	}
	if err := f.Bounds.check(number); err != nil {
		return nil, fmt.Errorf("%s %s", path, err.Error())
	}
	return number, nil
}

func (f Field) coerceBoolean(raw any, path string) (any, error) {
	if _, ok := raw.(bool); !ok {
		return nil, fmt.Errorf("%s 必须是布尔值", path)
	}
	return raw, nil
}

func (f Field) coerceStrings(raw any, path string) (any, error) {
	items, err := stringItems(raw, path)
	if err != nil {
		return nil, err
	}
	if f.MaxItems > 0 && len(items) > f.MaxItems {
		return nil, fmt.Errorf("%s 最多 %d 项", path, f.MaxItems)
	}
	return items, nil
}

func (f Field) coerceMap(raw any, path string) (any, error) {
	values, ok := raw.(map[string]any)
	if !ok {
		return nil, fmt.Errorf("%s 必须是对象（键与值都是字符串）", path)
	}
	for key, value := range values {
		if _, isString := value.(string); !isString {
			return nil, fmt.Errorf("%s.%s 必须是字符串", path, key)
		}
	}
	return values, nil
}

func (f Field) coerceElements(raw any, path string) (any, error) {
	items, ok := raw.([]any)
	if !ok {
		return nil, fmt.Errorf("%s 必须是对象数组", path)
	}
	if f.MaxItems > 0 && len(items) > f.MaxItems {
		return nil, fmt.Errorf("%s 最多 %d 项", path, f.MaxItems)
	}
	out := make([]any, 0, len(items))
	for index, item := range items {
		entry, ok := item.(map[string]any)
		if !ok {
			return nil, fmt.Errorf("%s[%d] 必须是对象", path, index)
		}
		normalized := make(map[string]any, len(entry))
		for key, value := range entry {
			field, declared := f.Element[key]
			if !declared {
				return nil, fmt.Errorf("%s[%d] 不接受字段 %s", path, index, key)
			}
			coerced, err := field.Coerce(value, fmt.Sprintf("%s[%d].%s", path, index, key))
			if err != nil {
				return nil, err
			}
			normalized[key] = coerced
		}
		out = append(out, normalized)
	}
	return out, nil
}

func (b *NumberBounds) check(number float64) error {
	if b == nil {
		return nil
	}
	if b.ExclusiveMin {
		if number <= b.Min {
			return fmt.Errorf("必须大于 %g", b.Min)
		}
	} else if number < b.Min {
		return fmt.Errorf("不能小于 %g", b.Min)
	}
	if b.Max > 0 && number > b.Max {
		return fmt.Errorf("不能大于 %g", b.Max)
	}
	return nil
}

// Value reads a field value. A custom Read takes precedence over Path.
func (f Field) Value(container map[string]any) (any, bool) {
	if f.Read != nil {
		return f.Read(container)
	}
	return readPath(container, f.Path)
}

// AddressKey returns the text-document field name.
func (f Field) AddressKey(key string) string {
	if f.AddressName != "" {
		return f.AddressName
	}
	return key
}

// Write stores a value at Path and returns the number of sentence-wrap
// insertions. This is the single write point shared by document parsing,
// structured patches and duplicate operations.
func (f Field) Write(container map[string]any, value any) int {
	if f.Seed != nil {
		f.Seed(container)
	}
	wrapped := 0
	if f.Wrap && f.Kind == KindString {
		if text, ok := value.(string); ok {
			if next, count := WrapSentences(text); count > 0 {
				value = next
				wrapped = count
			}
		}
	}
	if f.Store != nil {
		f.Store(container, value)
		return wrapped
	}
	writePath(container, f.Path, value)
	return wrapped
}

// String returns the current string value, or an empty string.
func (f Field) String(container map[string]any) string {
	value, ok := f.Value(container)
	if !ok || value == nil {
		return ""
	}
	text, _ := value.(string)
	return text
}

// Fields is a container field table keyed by contract name.
type Fields map[string]Field

// ByAddressKey resolves either a contract key or an explicit address name.
func (fields Fields) ByAddressKey(name string) (Field, bool) {
	if name == "" {
		return Field{}, false
	}
	if field, ok := fields[name]; ok {
		return field, true
	}
	for key, field := range fields {
		if field.AddressKey(key) == name {
			return field, true
		}
	}
	return Field{}, false
}

// Where returns fields matching keep. Consumers order results with Ordered.
func (fields Fields) Where(keep func(Field) bool) Fields {
	out := make(Fields, len(fields))
	for key, field := range fields {
		if keep(field) {
			out[key] = field
		}
	}
	return out
}

func (fields Fields) Writable() Fields {
	return fields.Where(func(field Field) bool { return field.Writable })
}

func (fields Fields) Editable() Fields {
	return fields.Where(func(field Field) bool { return field.Editable })
}

// Projected returns the read projection. Summary restricts it to fields that
// appear in directory/summary views.
func (fields Fields) Projected(precise bool) Fields {
	return fields.Where(func(field Field) bool {
		if precise {
			return field.Projected
		}
		return field.Projected && field.Summary
	})
}

// Keys returns lexicographically sorted keys.
func (fields Fields) Keys() []string {
	keys := make([]string, 0, len(fields))
	for key := range fields {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}

// Ordered sorts by Order, then by key.
func (fields Fields) Ordered() []string {
	keys := fields.Keys()
	sort.SliceStable(keys, func(i, j int) bool {
		left, right := fields[keys[i]], fields[keys[j]]
		if left.Order != right.Order {
			return left.Order < right.Order
		}
		return keys[i] < keys[j]
	})
	return keys
}

// Defaults returns the initial values for a new row. Collection fields default
// to an empty array; other zero values are omitted.
func (fields Fields) Defaults() map[string]any {
	out := map[string]any{}
	for key, field := range fields {
		if field.Default != nil {
			out[key] = copyDefault(field.Default)
			continue
		}
		if field.Kind == KindCollection {
			out[key] = []any{}
		}
	}
	return out
}

func copyDefault(value any) any {
	switch typed := value.(type) {
	case []any:
		return append([]any(nil), typed...)
	case []string:
		return append([]string(nil), typed...)
	case map[string]any:
		return maps.Clone(typed)
	}
	return value
}

func stringItems(raw any, path string) ([]any, error) {
	switch items := raw.(type) {
	case []any:
		out := make([]any, 0, len(items))
		for index, item := range items {
			text, ok := item.(string)
			if !ok {
				return nil, fmt.Errorf("%s[%d] 必须是字符串", path, index)
			}
			out = append(out, text)
		}
		return out, nil
	case []string:
		out := make([]any, 0, len(items))
		for _, item := range items {
			out = append(out, item)
		}
		return out, nil
	}
	return nil, fmt.Errorf("%s 必须是字符串数组", path)
}

func containsString(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}

func writePath(container map[string]any, path string, value any) {
	parts := strings.Split(path, ".")
	target := container
	for _, part := range parts[:len(parts)-1] {
		child, ok := target[part].(map[string]any)
		if !ok {
			child = map[string]any{}
			target[part] = child
		}
		target = child
	}
	target[parts[len(parts)-1]] = value
}

func readPath(container map[string]any, path string) (any, bool) {
	current := any(container)
	for _, part := range strings.Split(path, ".") {
		object, ok := current.(map[string]any)
		if !ok {
			return nil, false
		}
		value, exists := object[part]
		if !exists {
			return nil, false
		}
		current = value
	}
	return current, true
}

func sortedKeys(values map[string]any) []string {
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}

var fieldKinds = []string{KindString, KindNumber, KindBoolean, KindStrings, KindMap, KindCollection}

func normalizeDeclaredFields(fields Fields) (Fields, error) {
	if fields == nil {
		return Fields{}, nil
	}
	out := make(Fields, len(fields))
	for _, key := range fields.Keys() {
		trimmed := strings.TrimSpace(key)
		if trimmed == "" {
			return nil, fmt.Errorf("canvas field key cannot be empty")
		}
		field, err := normalizeDeclaredField(fields[key])
		if err != nil {
			return nil, fmt.Errorf("field %q: %w", trimmed, err)
		}
		out[trimmed] = field
	}
	return out, nil
}

func normalizeDeclaredField(field Field) (Field, error) {
	field, err := normalizeFieldIdentity(field)
	if err != nil {
		return Field{}, err
	}
	field, err = normalizeFieldFlags(field)
	if err != nil {
		return Field{}, err
	}
	return normalizeFieldShape(field)
}

func normalizeFieldIdentity(field Field) (Field, error) {
	field.Path = strings.TrimSpace(field.Path)
	if err := validatePatchPath(field.Path); err != nil {
		return Field{}, err
	}
	field.Kind = normalizeType(field.Kind)
	if !containsString(fieldKinds, field.Kind) {
		return Field{}, fmt.Errorf("unsupported kind %q", field.Kind)
	}
	field.Label = strings.TrimSpace(field.Label)
	field.Description = strings.TrimSpace(field.Description)
	if field.Label == "" {
		return Field{}, fmt.Errorf("requires a user-facing label")
	}
	if field.Order <= 0 {
		return Field{}, fmt.Errorf("requires a positive display order")
	}
	if field.MaxRunes < 0 || field.MaxItems < 0 {
		return Field{}, fmt.Errorf("has invalid length or item limit")
	}
	return field, nil
}

func normalizeFieldFlags(field Field) (Field, error) {
	if field.Editable && field.Kind != KindString {
		return Field{}, fmt.Errorf("editable requires the string kind")
	}
	if field.Searchable && field.Kind != KindString {
		return Field{}, fmt.Errorf("searchable requires the string kind")
	}
	if field.Wrap && field.Kind != KindString {
		return Field{}, fmt.Errorf("wrap requires the string kind")
	}
	if field.Secret && field.Projected {
		return Field{}, fmt.Errorf("secret fields are never projected")
	}
	enum := make([]string, 0, len(field.Enum))
	for _, value := range field.Enum {
		if trimmed := strings.TrimSpace(value); trimmed != "" {
			enum = append(enum, trimmed)
		}
	}
	if len(enum) > 0 && field.Kind != KindString {
		return Field{}, fmt.Errorf("enum requires the string kind")
	}
	field.Enum = enum
	return field, nil
}

func normalizeFieldShape(field Field) (Field, error) {
	if field.Kind == KindCollection {
		if len(field.Element) == 0 {
			return Field{}, fmt.Errorf("collection requires element fields")
		}
		element, err := normalizeDeclaredFields(field.Element)
		if err != nil {
			return Field{}, fmt.Errorf("element %w", err)
		}
		for key, item := range element {
			if item.Kind == KindCollection {
				return Field{}, fmt.Errorf("element field %q cannot be a nested collection", key)
			}
			if item.Editable || item.Writable {
				return Field{}, fmt.Errorf("element field %q is not independently writable", key)
			}
		}
		field.Element = element
	} else if len(field.Element) > 0 {
		return Field{}, fmt.Errorf("element fields require the collection kind")
	}
	if field.Bounds != nil {
		if field.Kind != KindNumber {
			return Field{}, fmt.Errorf("bounds require the number kind")
		}
		bounds := *field.Bounds
		field.Bounds = &bounds
	}
	return field, nil
}
