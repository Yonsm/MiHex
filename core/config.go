// 配置加载：读取 mihex.yml，并保留顶层配置段的声明顺序。
package core

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"unicode"
	"unicode/utf8"

	"gopkg.in/yaml.v3"
)

// Config 是一份已解析的配置：Order 为顶层段的书写顺序，Values 为各段内容。
type Config struct {
	Path   string
	Dir    string
	Order  []string
	Values map[string]any
	Keys   map[string][]string // 各段内部的键顺序
}

// Section 是交给模块的一段配置，保留段内键的书写顺序。
type Section struct {
	Name  string
	Value any
	Keys  []string
	// AfterLoad 在服务模式下将回调推迟到所有模块载入之后；单次命令模式为 nil。
	AfterLoad func(func())
}

func (s Section) Map() map[string]any { return Map(s.Value) }

func (s Section) List() []any { return List(s.Value) }

func Load(path string) (*Config, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var doc yaml.Node
	if err := yaml.Unmarshal(raw, &doc); err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	abs, err := filepath.Abs(path)
	if err != nil {
		abs = path
	}
	c := &Config{Path: abs, Dir: filepath.Dir(abs), Values: map[string]any{}, Keys: map[string][]string{}}
	if len(doc.Content) == 0 {
		return c, nil
	}
	m := doc.Content[0]
	if m.Kind != yaml.MappingNode {
		return nil, fmt.Errorf("%s: 顶层必须是映射", path)
	}
	for i := 0; i+1 < len(m.Content); i += 2 {
		var v any
		if err := m.Content[i+1].Decode(&v); err != nil {
			return nil, fmt.Errorf("%s: %s: %w", path, m.Content[i].Value, err)
		}
		name := m.Content[i].Value
		c.Order = append(c.Order, name)
		c.Values[name] = v
		if sub := m.Content[i+1]; sub.Kind == yaml.MappingNode {
			for j := 0; j+1 < len(sub.Content); j += 2 {
				c.Keys[name] = append(c.Keys[name], sub.Content[j].Value)
			}
		}
	}
	return c, nil
}

// Map 把配置段取为字典；不是字典时返回 nil。
func Map(v any) map[string]any {
	m, _ := v.(map[string]any)
	return m
}

// List 把配置段取为列表；不是列表时返回 nil。
func List(v any) []any {
	l, _ := v.([]any)
	return l
}

// Str 把标量转成字符串（NAME 等字段允许写数字）。
func Str(v any) string {
	switch x := v.(type) {
	case nil:
		return ""
	case string:
		return x
	default:
		return fmt.Sprint(x)
	}
}

func StrDef(v any, def string) string {
	if s := Str(v); s != "" {
		return s
	}
	return def
}

// LowerUnknown 返回 conf 中既不在 known 里、又不是大写状态键的配置项。
// 大写键（NAME/ICON/UNIT 等）是自由状态字段，不参与拼写校验。
func LowerUnknown(conf map[string]any, known ...string) []string {
	set := make(map[string]struct{}, len(known))
	for _, k := range known {
		set[k] = struct{}{}
	}
	var out []string
	for k := range conf {
		if _, ok := set[k]; ok {
			continue
		}
		if r, _ := utf8.DecodeRuneInString(k); unicode.IsUpper(r) {
			continue
		}
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

func Int(v any, def int) int {
	switch x := v.(type) {
	case int:
		return x
	case int64:
		return int(x)
	case float64:
		return int(x)
	case string:
		if n, err := strconv.Atoi(x); err == nil {
			return n
		}
	}
	return def
}

func Bool(v any, def bool) bool {
	switch x := v.(type) {
	case bool:
		return x
	case int:
		return x != 0
	case string:
		b, err := strconv.ParseBool(x)
		if err == nil {
			return b
		}
	}
	return def
}
