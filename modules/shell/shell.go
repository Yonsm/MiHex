// Package shell 通过执行本地命令检查设备状态并控制设备。
package shell

import (
	"context"
	"log/slog"
	"os/exec"
	"reflect"
	"strings"
	"time"

	"github.com/Yonsm/MiHex/core"
)

const timeout = 15 * time.Second

func init() {
	core.RegisterModule("shell", setup)
}

func setup(sec core.Section) ([]core.Object, error) {
	conf := sec.Map()
	var objs []core.Object
	for _, k := range sec.Keys {
		for _, raw := range core.List(conf[k]) {
			c := core.Map(raw)
			if unknown := core.LowerUnknown(c, "stat", "ison", "toon", "toff", "exec"); len(unknown) > 0 {
				slog.Warn("设备存在未知配置键", "设备", core.Str(c["NAME"]), "键", strings.Join(unknown, ", "))
			}
			objs = append(objs, &Entity{Entity: core.NewEntity(c, k)})
		}
	}
	return objs, nil
}

// Entity 用 stat/toon/toff 三条命令描述一个设备。
type Entity struct {
	*core.Entity
}

func (e *Entity) Update(ctx context.Context) (bool, error) {
	argv := e.argv("stat")
	if len(argv) == 0 {
		return false, nil
	}
	out, err := exec.CommandContext(ctx, argv[0], argv[1:]...).Output()
	if err != nil {
		slog.Debug("状态命令失败", "实体", e.Name(), "err", err)
		return false, nil
	}
	ison := e.Str("ison")
	stat := ison != "" && strings.HasPrefix(string(out), ison)
	if old, _ := e.Get("STAT"); reflect.DeepEqual(old, stat) {
		return false, nil
	}
	e.Set("STAT", stat)
	return true, nil
}

func (e *Entity) Supports(method string) bool {
	return method == "on_turn" || method == "service"
}

func (e *Entity) Call(ctx context.Context, method string, args []any) (any, error) {
	if method != "on_turn" {
		return e.Entity.Call(ctx, method, args)
	}
	on := len(args) > 0 && truthy(args[0])
	key := "toff"
	if on {
		key = "toon"
	}
	if argv := e.argv(key); len(argv) > 0 {
		c, cancel := context.WithTimeout(ctx, timeout)
		defer cancel()
		if err := exec.CommandContext(c, argv[0], argv[1:]...).Run(); err != nil {
			return nil, err
		}
	}
	var ison any = on
	if len(args) > 0 {
		ison = args[0]
	}
	e.Set("ISON", ison)
	core.Emit(e)
	return nil, nil
}

func (e *Entity) argv(key string) []string {
	raw := core.List(e.Conf[key])
	out := make([]string, len(raw))
	for i, v := range raw {
		out[i] = core.Str(v)
	}
	return out
}

func truthy(v any) bool {
	switch x := v.(type) {
	case bool:
		return x
	case string:
		return x != "" && x != "0" && x != "false"
	case float64:
		return x != 0
	case int:
		return x != 0
	}
	return false
}
