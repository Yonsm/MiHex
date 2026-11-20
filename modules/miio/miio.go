package miio

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"math"
	"reflect"
	"strconv"
	"strings"
	"sync"

	"github.com/Yonsm/MiHex/core"
)

var attrs = [...]string{"stat", "ison", "mode", "tune", "togl", "togr"}

type iid struct {
	name       string
	siid, piid int
}

func parseIID(v any) (iid, error) {
	s := core.Str(v)
	if s == "" {
		return iid{}, nil
	}
	if s[0] < '0' || s[0] > '9' {
		return iid{name: s}, nil
	}
	a, b, pair := strings.Cut(s, "-")
	if !pair {
		b = "1"
	}
	x, e := strconv.Atoi(a)
	y, f := strconv.Atoi(b)
	if e != nil || f != nil || x < 1 || y < 1 {
		return iid{}, fmt.Errorf("无效 MiIO 属性 %q", s)
	}
	return iid{siid: x, piid: y}, nil
}
func (i iid) String() string {
	if i.name != "" {
		return i.name
	}
	return strconv.Itoa(i.siid) + "-" + strconv.Itoa(i.piid)
}
func legacyNames(ids []iid) []string {
	out := make([]string, len(ids))
	for n, i := range ids {
		out[n] = i.name
	}
	return out
}

type device struct {
	mu       sync.Mutex
	local    *localDevice
	fallback *cloudTarget
	service  *miioService
	name     string
	entities []*entity
	props    []reading
}
type entity struct {
	*core.Entity
	dev *device
	ids map[string]iid
}
type primary struct{ *entity }
type reading struct {
	ent  *entity
	key  string
	id   iid
	unit string
}

func init() { core.RegisterModule("miio", setup) }

func manualKey(k string) bool { return k != "user" && k != "pass" && k != "auto" }

func newDevice(name string, raw any, local *localDevice) (*device, error) {
	entries := core.List(raw)
	if entries == nil {
		if s, ok := raw.(string); ok {
			raw = map[string]any{"TYPE": s}
		}
		entries = []any{raw}
	}
	if len(entries) == 0 {
		return nil, errors.New("设备配置为空")
	}
	d := &device{local: local, name: name}
	var extras []reading
	for _, item := range entries {
		conf := core.Map(item)
		if conf == nil {
			return nil, errors.New("设备配置必须是映射")
		}
		if p := core.Str(conf["platform"]); p != "" && p != "viomi_washer" {
			return nil, fmt.Errorf("未知 MiIO platform %q", p)
		}
		e := &entity{Entity: core.NewEntity(conf, "sensor"), dev: d, ids: map[string]iid{}}
		if mods, ok := conf["MODS"].(map[any]any); ok {
			labels := make(map[string]any, len(mods))
			for k, v := range mods {
				labels[core.Str(k)] = v
			}
			e.Set("MODS", labels)
		}
		if _, ok := conf["NAME"]; !ok {
			e.SetName(name)
		}
		d.entities = append(d.entities, e)
		for _, key := range attrs {
			id, err := parseIID(conf[key])
			if err != nil {
				return nil, err
			}
			_, hasOn := conf["ison"]
			_, hasStat := conf["stat"]
			if key == "ison" && !hasOn && !hasStat && e.Str("TYPE") != "binary" {
				id = iid{siid: 2, piid: 1}
			}
			if id != (iid{}) {
				e.ids[key] = id
				d.props = append(d.props, reading{ent: e, key: strings.ToUpper(key), id: id})
			}
		}
		if id, err := parseIID(conf["tune_set"]); err != nil {
			return nil, err
		} else if id != (iid{}) {
			e.ids["tune_set"] = id
		}
		for _, raw := range core.List(conf["extra_stat"]) {
			pair := core.List(raw)
			if len(pair) != 2 {
				return nil, errors.New("extra_stat 必须包含属性与单位")
			}
			id, err := parseIID(pair[0])
			if err != nil {
				return nil, err
			}
			if id != (iid{}) {
				extras = append(extras, reading{ent: e, id: id, unit: core.Str(pair[1])})
			}
		}
		if host, token := e.Str("host"), e.Str("token"); host != "" && token != "" {
			l, err := newLocal(host, token)
			if err != nil {
				slog.Warn("MiIO 本地配置不可用", "设备", name, "err", err)
			} else {
				d.local = l
			}
		}
	}
	d.props = append(d.props, extras...)
	return d, nil
}

func (d *device) objects() []core.Object {
	objs := []core.Object{&primary{d.entities[0]}}
	for _, e := range d.entities[1:] {
		objs = append(objs, e)
	}
	return objs
}

func (d *device) read(ctx context.Context, ids []iid) ([]any, error) {
	if d.local != nil {
		vals, err := d.local.read(ctx, ids)
		if err == nil && len(vals) == len(ids) {
			return vals, nil
		}
		if err == nil {
			err = errors.New("MiIO 属性响应长度不符")
		}
		if d.fallback == nil {
			return nil, err
		}
		slog.Debug("MiIO 本地读取失败，转用云", "err", err)
	}
	if d.fallback != nil {
		return d.fallback.read(ctx, ids)
	}
	return nil, errors.New("无 MiIO 连接")
}
func (d *device) set(ctx context.Context, id iid, val any) error {
	if id == (iid{}) {
		return errors.New("未配置 MiIO 控制属性")
	}
	if d.local != nil {
		if err := d.local.set(ctx, id, val); err == nil || d.fallback == nil {
			return err
		} else {
			slog.Debug("MiIO 本地写入失败，转用云", "err", err)
		}
	}
	if d.fallback != nil {
		return d.fallback.set(ctx, id, val)
	}
	return errors.New("无 MiIO 连接")
}

func (p *primary) Update(ctx context.Context) (bool, error) {
	d := p.dev
	d.mu.Lock()
	defer d.mu.Unlock()
	vals := make([]any, len(d.props))
	loaded := make([]bool, len(vals))
	var lastErr error
	for _, legacy := range []bool{true, false} {
		var ids []iid
		var indices []int
		for i, r := range d.props {
			if (r.id.name != "") == legacy {
				ids = append(ids, r.id)
				indices = append(indices, i)
			}
		}
		if len(ids) == 0 {
			continue
		}
		got, err := d.read(ctx, ids)
		if err == nil && len(got) != len(ids) {
			err = errors.New("MiIO 属性响应长度不符")
		}
		if err != nil {
			lastErr = err
			continue
		}
		for i, j := range indices {
			vals[j], loaded[j] = got[i], true
		}
	}
	desc := map[*entity][]string{}
	changed := map[*entity]bool{}
	for i, r := range d.props {
		v := vals[i]
		if !loaded[i] {
			if r.key == "STAT" || r.key == "ISON" {
				changed[r.ent] = updateProp(r.ent, r.key, nil) || changed[r.ent]
			}
			continue
		}
		if v == nil {
			continue
		}
		if r.id.name == "temp_dec" {
			if n, ok := number(v); ok {
				v = n / 10
			}
		}
		if r.key == "" {
			s := core.Str(v)
			if r.unit == "℃" {
				if n, ok := number(v); ok {
					s = fmt.Sprintf("%.1f", n)
				}
			}
			desc[r.ent] = append(desc[r.ent], s+r.unit)
		} else {
			changed[r.ent] = updateProp(r.ent, r.key, v) || changed[r.ent]
		}
	}
	for e, parts := range desc {
		changed[e] = updateProp(e, "DESC", strings.Join(parts, " ")) || changed[e]
	}
	var emit []core.Object
	for _, e := range d.entities {
		if changed[e] {
			emit = append(emit, e)
		}
	}
	core.Emit(emit...)
	return false, lastErr
}

func number(v any) (float64, bool) {
	switch v.(type) {
	case int, int64, uint64, float64, json.Number:
		n, err := strconv.ParseFloat(core.Str(v), 64)
		return n, err == nil
	}
	return 0, false
}
func equal(a, b any) bool {
	if x, ok := number(a); ok {
		if y, ok := number(b); ok {
			return x == y
		}
	}
	return reflect.DeepEqual(a, b)
}
func updateProp(e *entity, key string, val any) bool {
	old, _ := e.Get(key)
	if equal(old, val) {
		return false
	}
	e.Set(key, val)
	return true
}
func valueOr(m map[string]any, key string, def any) any {
	if v, ok := m[key]; ok {
		return v
	}
	return def
}

func (e *entity) Call(ctx context.Context, method string, args []any) (any, error) {
	if method == "service" {
		if e.Str("platform") == "viomi_washer" {
			if result, handled, err := e.washer(ctx, args); handled {
				return result, err
			}
		}
		result, err := e.dev.service.command(ctx, e.dev.name, args)
		if vals, ok := result.([]any); ok && len(vals) == 1 {
			return vals[0], err
		}
		return result, err
	}
	if len(args) == 0 {
		return nil, fmt.Errorf("%s 缺少参数", method)
	}
	d := e.dev
	d.mu.Lock()
	defer d.mu.Unlock()
	arg := args[0]
	var key string
	var val any
	var id iid
	switch method {
	case "on_turn":
		key = "ISON"
		id = e.ids["ison"]
		on := truthy(arg)
		if s, ok := arg.(string); ok {
			on = core.Bool(s, on)
		}
		confKey := "voff"
		if on {
			confKey = "voon"
		}
		if err := d.set(ctx, id, valueOr(e.Conf, confKey, on)); err != nil {
			return nil, err
		}
		e.Set(key, arg)
		core.Emit(e)
		return nil, nil
	case "on_mode":
		key = "MODE"
		id = e.ids["mode"]
		if s, ok := arg.(string); ok && id.name == "" {
			arg = commandValue(s)
		}
		val = arg
	case "on_tune":
		switch core.Str(arg) {
		case "▽", "△":
			key = "TUNE"
			id = e.ids["tune_set"]
			if id == (iid{}) {
				id = e.ids["tune"]
			}
			old, _ := e.Get(key)
			n, ok := number(old)
			if !ok {
				return nil, errors.New("MiIO 调节值尚不可用")
			}
			lo, _ := number(valueOr(e.Conf, "tune_min", 0))
			hi, _ := number(valueOr(e.Conf, "tune_max", 100))
			step, _ := number(valueOr(e.Conf, "tune_step", math.Ceil((hi-lo)/20)))
			if arg == "▽" {
				step = -step
			}
			val = min(hi, max(lo, n+step))
		case "◉", "◌", "◑", "◐":
			key = "TOGL"
			if arg == "◑" || arg == "◐" {
				key = "TOGR"
			}
			id = e.ids[strings.ToLower(key)]
			val = valueOr(e.Conf, key+"_ON", "on")
			if old, _ := e.Get(key); equal(old, val) {
				val = valueOr(e.Conf, key+"_OFF", "off")
			}
		default:
			return nil, nil
		}
	default:
		return nil, fmt.Errorf("未知 MiIO 方法 %s", method)
	}
	if err := d.set(ctx, id, val); err != nil {
		return nil, err
	}
	e.Set(key, val)
	core.Emit(e)
	return nil, nil
}
