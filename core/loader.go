package core

import (
	"fmt"
	"log/slog"
	"strings"
)

// Module 是一个可加载的配置段处理器。
type Module struct {
	Name  string
	Setup func(sec Section) ([]Object, error)
}

// Builtins 固定最后加载：它们依赖前面注册的实体。
var Builtins = []string{"server", "runner", "tracker"}

var (
	modules  = map[string]*Module{}
	optional = map[string]bool{}
)

func RegisterModule(name string, setup func(Section) ([]Object, error)) {
	modules[name] = &Module{Name: name, Setup: setup}
}

// RegisterOptional 注册可降级模块：初始化失败只告警，不影响其余功能启动。
func RegisterOptional(name string, setup func(Section) ([]Object, error)) {
	RegisterModule(name, setup)
	optional[name] = true
}

// LoadModules 按配置书写顺序加载非内置段，再按 Builtins 顺序加载内置段。
// serve 为 false（一次性命令）时跳过 server/tracker，避免占用端口。
func LoadModules(cfg *Config, serve bool) error {
	loaded := map[string]bool{}
	var missing []string
	var callbacks []func()
	var afterLoad func(func())
	if serve {
		afterLoad = func(fn func()) { callbacks = append(callbacks, fn) }
	}
	for _, name := range cfg.Order {
		if loaded[name] || isBuiltin(name) {
			continue
		}
		mod, ok := modules[name]
		if !ok {
			missing = append(missing, name)
			continue
		}
		if err := load(mod, cfg, name, afterLoad); err != nil {
			return err
		}
		loaded[name] = true
	}
	if len(missing) > 0 {
		slog.Warn("未实现的配置段，已跳过", "段", strings.Join(missing, ", "))
	}
	for _, name := range Builtins {
		if !serve && (name == "server" || name == "tracker") {
			continue
		}
		if _, ok := cfg.Values[name]; !ok {
			continue
		}
		mod, ok := modules[name]
		if !ok {
			slog.Warn("内置模块未注册，已跳过", "模块", name)
			continue
		}
		if err := load(mod, cfg, name, afterLoad); err != nil {
			return err
		}
	}
	for _, fn := range callbacks {
		fn()
	}
	return nil
}

func isBuiltin(name string) bool {
	for _, b := range Builtins {
		if b == name {
			return true
		}
	}
	return false
}

func load(m *Module, cfg *Config, name string, afterLoad func(func())) error {
	objs, err := m.Setup(Section{Name: name, Value: cfg.Values[name], Keys: cfg.Keys[name], AfterLoad: afterLoad})
	if err != nil {
		if optional[m.Name] {
			slog.Error("模块不可用，已跳过", "模块", m.Name, "err", err)
			return nil
		}
		return fmt.Errorf("%s: %w", m.Name, err)
	}
	Register(objs...)
	return nil
}
