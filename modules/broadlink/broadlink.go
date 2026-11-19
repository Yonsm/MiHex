// Package broadlink 通过局域网控制博联插座和红外/射频遥控器。
package broadlink

import (
	"context"
	"embed"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"log/slog"
	"maps"
	"net"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/Yonsm/MiHex/core"
	"gopkg.in/yaml.v3"
)

//go:embed codes/*.yml
var codes embed.FS

func init() { core.RegisterModule("broadlink", setup) }

func setup(sec core.Section) ([]core.Object, error) {
	var objs []core.Object
	for _, raw := range sec.List() {
		cfg := core.Map(raw)
		n := core.Int(cfg["type"], -1)
		host := core.Str(cfg["host"])
		mac, err := hex.DecodeString(strings.Join(strings.Fields(core.Str(cfg["mac"])), ""))
		if n < 0 || n > 65535 || host == "" || err != nil || len(mac) != 6 {
			slog.Warn("跳过无效 Broadlink 设备配置")
			continue
		}
		d := &device{addr: net.JoinHostPort(host, "80"), mac: mac, typ: uint16(n), family: model(uint16(n)), gate: make(chan struct{}, 1)}
		if _, ok := cfg["NAME"]; ok {
			if d.family < sp1 || d.family > sp4b {
				slog.Warn("不支持的 Broadlink 插座型号", "type", n)
				continue
			}
			ent := &switchEntity{Entity: core.NewEntity(cfg, "switch"), device: d}
			if d.family == sp1 {
				objs = append(objs, ent)
			} else {
				objs = append(objs, &pollSwitch{ent})
			}
			continue
		}
		if d.family != rm && d.family != rm4 {
			slog.Warn("不支持的 Broadlink 遥控器型号", "type", n)
			continue
		}
		for _, kind := range slices.Sorted(maps.Keys(cfg)) {
			for _, v := range core.List(cfg[kind]) {
				c := core.Map(v)
				if c == nil {
					continue
				}
				var cmds any = core.Str(c["code"])
				if data, err := codes.ReadFile("codes/" + core.Str(cmds) + ".yml"); err == nil {
					if err := yaml.Unmarshal(data, &cmds); err != nil {
						slog.Warn("Broadlink 遥控码解析失败", "设备", core.Str(c["NAME"]), "err", err)
						continue
					}
				}
				objs = append(objs, &remoteEntity{Entity: core.NewEntity(c, kind), device: d, cmds: cmds})
			}
		}
	}
	return objs, nil
}

type switchEntity struct {
	*core.Entity
	*device
}
type pollSwitch struct{ *switchEntity }

func (e *pollSwitch) Update(ctx context.Context) (bool, error) {
	emit := false
	err := e.use(ctx, func(s *session) error {
		state, err := s.power(nil)
		if err == nil {
			old, _ := e.Get("ISON")
			if old != state {
				e.Set("ISON", state)
				emit = true
			}
		}
		return err
	})
	if err != nil {
		if old, _ := e.Get("ISON"); old != nil {
			e.Set("ISON", nil)
			core.Emit(e)
		}
	}
	return emit, err
}

func (e *switchEntity) Call(ctx context.Context, method string, args []any) (any, error) {
	if method != "on_turn" {
		return e.Entity.Call(ctx, method, args)
	}
	on, arg, err := powerArg(args)
	if err != nil {
		return nil, err
	}
	err = e.use(ctx, func(s *session) error {
		if _, err := s.power(&on); err != nil {
			return err
		}
		e.Set("ISON", arg)
		core.Emit(e)
		return nil
	})
	return nil, err
}

type remoteEntity struct {
	*core.Entity
	*device
	cmds any
}

func (e *remoteEntity) Call(ctx context.Context, method string, args []any) (any, error) {
	if method == "service" {
		return nil, e.sendCode(ctx, args, nil)
	}
	if method != "on_turn" {
		return e.Entity.Call(ctx, method, args)
	}
	on, arg, err := powerArg(args)
	if err != nil {
		return nil, err
	}
	command := "关闭"
	if on {
		command = "打开"
	}
	return nil, e.sendCode(ctx, []any{command}, func() { e.Set("ISON", arg); core.Emit(e) })
}

func powerArg(args []any) (bool, any, error) {
	if len(args) > 0 {
		s := core.Str(args[0])
		if s == "0" {
			return false, args[0], nil
		}
		if s == "1" {
			return true, args[0], nil
		}
		if b, err := strconv.ParseBool(s); err == nil {
			return b, args[0], nil
		}
	}
	return false, nil, errors.New("Broadlink 开关需要布尔值或 0/1")
}

func (e *remoteEntity) sendCode(ctx context.Context, args []any, done func()) error {
	cmd := e.cmds
	var parent any
	// 内联 Base64 无需命令名；on_turn 可直接发送配置的固定码。
	if _, inline := cmd.(string); !inline {
		for _, arg := range args {
			parent = cmd
			cmd = lookup(cmd, arg)
		}
	}
	str, ok := cmd.(string)
	if !ok || str == "" {
		return errors.New("未找到 Broadlink 遥控码")
	}
	if ref, ok := strings.CutPrefix(str, "="); ok {
		str, ok = lookup(parent, ref).(string)
		if !ok || str == "" {
			return errors.New("无效 Broadlink 遥控码引用")
		}
	}
	repeat := 1
	if i := strings.LastIndexByte(str, '@'); i >= 0 {
		n, err := strconv.Atoi(str[i+1:])
		if err != nil || n < 1 || n > 30 {
			return errors.New("Broadlink 遥控码重复次数须为 1–30")
		}
		str, repeat = str[:i], n
	}
	data, err := base64.StdEncoding.DecodeString(strings.Join(strings.Fields(str), ""))
	if err != nil {
		return fmt.Errorf("无效 Broadlink 遥控码: %w", err)
	}
	if len(data) == 0 {
		return errors.New("Broadlink 遥控码为空")
	}
	return e.use(ctx, func(s *session) error {
		for i := range repeat {
			if i > 0 {
				timer := time.NewTimer(time.Second)
				select {
				case <-ctx.Done():
					timer.Stop()
					return ctx.Err()
				case <-timer.C:
				}
			}
			if err := s.transmit(data); err != nil {
				return err
			}
		}
		if done != nil {
			done()
		}
		return nil
	})
}

func lookup(node, arg any) any {
	key := core.Str(arg)
	switch m := node.(type) {
	case map[string]any:
		return m[key]
	case map[any]any:
		if v, ok := m[key]; ok {
			return v
		}
		for k, v := range m {
			if core.Str(k) == key {
				return v
			}
		}
	}
	return nil
}
