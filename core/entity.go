// Package core 提供配置解析、实体模型、全局注册表、模块加载、命令分发与轮询。
//
// 与 Python 版的区别：实体状态放在显式的 Props 字典里，不再依赖 setattr +
// dir() + 大写属性约定，大小写约定只保留在配置解析这一处。
package core

import (
	"context"
	"fmt"
	"log/slog"
	"sync"
	"unicode"
)

// Object 是可被命令分发与仪表盘枚举的注册对象。
type Object interface {
	Name() string
	State() map[string]any
	IsEntity() bool
	Supports(method string) bool
	Call(ctx context.Context, method string, args []any) (any, error)
}

// Poller 由 tracker 周期调用。
type Poller interface {
	Update(ctx context.Context) (changed bool, err error)
}

// Broadcaster 由 server 注入，用于把状态变更推给 WebSocket 客户端。
type Broadcaster interface {
	Send(msg any)
}

var (
	mu   sync.RWMutex
	objs []Object
	bus  Broadcaster
)

func SetBus(b Broadcaster) {
	mu.Lock()
	bus = b
	mu.Unlock()
}

func Register(o ...Object) {
	mu.Lock()
	objs = append(objs, o...)
	mu.Unlock()
}

func Objects() []Object {
	mu.RLock()
	defer mu.RUnlock()
	return append([]Object(nil), objs...)
}

// Find 按 NAME 返回首个匹配对象（与 Python 版 next(... in _OBJS) 一致）。
func Find(name string) Object {
	mu.RLock()
	defer mu.RUnlock()
	for _, o := range objs {
		if o.Name() == name {
			return o
		}
	}
	return nil
}

// Emit 广播一个或多个对象的最新状态；单个对象发对象，多个发数组。
func Emit(os ...Object) {
	mu.RLock()
	b := bus
	mu.RUnlock()
	if b == nil || len(os) == 0 {
		return
	}
	states := make([]map[string]any, 0, len(os))
	for _, o := range os {
		if s := o.State(); s != nil {
			states = append(states, s)
		}
	}
	switch len(states) {
	case 0:
	case 1:
		b.Send(states[0])
	default:
		b.Send(states)
	}
}

// Entity 是配置驱动的实体：配置项原样保留，首字母大写的键进入可视状态。
type Entity struct {
	NameStr string
	Conf    map[string]any
	mu      sync.RWMutex
	props   map[string]any
}

func NewEntity(conf map[string]any, kind string) *Entity {
	e := &Entity{Conf: conf, props: map[string]any{}}
	for k, v := range conf {
		if IsUpperKey(k) {
			e.props[k] = v
		}
	}
	if v, ok := conf["NAME"]; ok {
		e.NameStr = Str(v)
	}
	if _, ok := e.props["TYPE"]; !ok && kind != "" {
		e.props["TYPE"] = kind
	}
	return e
}

func (e *Entity) Name() string { return e.NameStr }

// SetName 同时更新可视状态里的 NAME，供固定名称的实体使用。
func (e *Entity) SetName(name string) {
	e.NameStr = name
	e.Set("NAME", name)
}

func (e *Entity) IsEntity() bool { return true }

func (e *Entity) State() map[string]any {
	e.mu.RLock()
	defer e.mu.RUnlock()
	if len(e.props) == 0 {
		return nil
	}
	out := make(map[string]any, len(e.props))
	for k, v := range e.props {
		if v == nil {
			continue
		}
		out[k] = v
	}
	return out
}

func (e *Entity) Get(key string) (any, bool) {
	e.mu.RLock()
	defer e.mu.RUnlock()
	v, ok := e.props[key]
	return v, ok
}

func (e *Entity) Set(key string, v any) {
	e.mu.Lock()
	e.props[key] = v
	e.mu.Unlock()
}

// Str/Int 读取配置项（小写键写在 conf 里）。
func (e *Entity) Str(key string) string { return Str(e.Conf[key]) }

func (e *Entity) Supports(method string) bool {
	switch method {
	case "on_turn", "on_mode", "on_tune", "service":
		return true
	}
	return false
}

func (e *Entity) Call(ctx context.Context, method string, args []any) (any, error) {
	switch method {
	case "on_turn":
		var arg any
		if len(args) > 0 {
			arg = args[0]
		}
		e.Set("ISON", arg)
		Emit(e)
		return nil, nil
	case "on_mode", "on_tune", "service":
		return nil, nil
	}
	return nil, fmt.Errorf("%s: 未知方法 %s", e.Name(), method)
}

// IsUpperKey 判断配置键是否应暴露为可视状态（等价于 Python 的 str.isupper()）。
func IsUpperKey(k string) bool {
	upper := false
	for _, r := range k {
		if unicode.IsLetter(r) {
			upper = true
			if !unicode.IsUpper(r) {
				return false
			}
		}
	}
	return upper
}

// MethodArgs 按 Python 版规则拆出方法名与参数。
func MethodArgs(args []any, obj Object) (string, []any) {
	if len(args) > 1 {
		if m, ok := args[1].(string); ok && obj.Supports(m) {
			switch len(args) {
			case 2:
				return m, nil
			case 3:
				return m, []any{args[2]}
			default:
				return m, args[2:]
			}
		}
	}
	return "service", args[1:]
}

// Dispatch 执行 [NAME, method?, args...] 形式的命令；找不到对象返回 nil。
func Dispatch(ctx context.Context, args []any) (any, error) {
	if len(args) == 0 {
		return nil, nil
	}
	name, ok := args[0].(string)
	if !ok {
		return nil, fmt.Errorf("命令首项必须是实体名: %v", args[0])
	}
	obj := Find(name)
	if obj == nil {
		slog.Debug("未找到实体", "name", name)
		return nil, nil
	}
	method, rest := MethodArgs(args, obj)
	return obj.Call(ctx, method, rest)
}

// ConfObject 是带配置字典的实体，供 MQTT 等按配置匹配的设备模块使用。
type ConfObject interface {
	Object
	Config() map[string]any
	Get(key string) (any, bool)
	Set(key string, v any)
}

// Mod 是配置段对应的模块对象（如 MQTT、DING），可被命令分发调用。
type Mod struct {
	ModName string
	Props   map[string]any
	Methods map[string]bool
	CallFn  func(ctx context.Context, method string, args []any) (any, error)
}

func NewMod(name string) *Mod {
	return &Mod{ModName: name, Props: map[string]any{"NAME": name}, Methods: map[string]bool{}}
}

func (m *Mod) Name() string { return m.ModName }

func (m *Mod) State() map[string]any { return m.Props }

func (m *Mod) IsEntity() bool { return false }

func (m *Mod) Supports(method string) bool { return m.Methods[method] }

func (m *Mod) Call(ctx context.Context, method string, args []any) (any, error) {
	if m.CallFn != nil {
		return m.CallFn(ctx, method, args)
	}
	return nil, nil
}

// Config 让 Entity 满足 ConfObject。
func (e *Entity) Config() map[string]any { return e.Conf }
