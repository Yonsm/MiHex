// Package scene 实现场景/自动化：时间（cron）或设备状态变化触发一组动作。
package scene

import (
	"context"
	"encoding/json"
	"log/slog"
	"strconv"
	"strings"
	"time"

	"github.com/Yonsm/MiHex/core"
)

const (
	cronInterval  = 15 * time.Second
	stateInterval = 15 * time.Second
	actionTimeout = 30 * time.Second
)

type rule struct {
	name    string
	spec    string
	entity  string
	attr    string
	op      string
	value   any
	actions []any
	prev    any
	matched bool
}

var (
	timeRules  []*rule
	stateRules []*rule
)

func init() {
	core.RegisterModule("scene", setup)
}

func setup(sec core.Section) ([]core.Object, error) {
	if sec.AfterLoad == nil {
		return nil, nil
	}
	for _, raw := range sec.List() {
		conf := core.Map(raw)
		name := core.StrDef(conf["name"], "?")
		actions := normalizeActions(conf["then"])
		if len(actions) == 0 {
			slog.Warn("场景缺少动作，已跳过", "场景", name)
			continue
		}
		when := core.Map(conf["when"])
		switch {
		case when["at"] != nil || when["cron"] != nil:
			for _, spec := range cronSpecs(name, when) {
				timeRules = append(timeRules, &rule{name: name, spec: spec, actions: actions})
			}
		case when["entity"] != nil:
			op, value := condition(when)
			if op == "" {
				slog.Warn("场景条件无效，已跳过", "场景", name)
				continue
			}
			stateRules = append(stateRules, &rule{
				name: name, entity: core.Str(when["entity"]), attr: core.StrDef(when["attr"], "STAT"),
				op: op, value: value, actions: actions,
			})
		default:
			slog.Warn("场景触发器无法识别，已跳过", "场景", name)
		}
	}
	if len(timeRules) > 0 || len(stateRules) > 0 {
		ctx, cancel := context.WithCancel(context.Background())
		core.OnShutdown(func(context.Context) error { cancel(); return nil })
		if len(timeRules) > 0 {
			slog.Info("时间规则", "数量", len(timeRules))
			sec.AfterLoad(func() { go cronLoop(ctx) })
		}
		if len(stateRules) > 0 {
			slog.Info("状态规则", "数量", len(stateRules))
			sec.AfterLoad(func() { go stateLoop(ctx) })
		}
	}
	return nil, nil
}

// normalizeActions 接受单动作 [名, 方法, 参数...] 或动作列表 [[...], ...]。
func normalizeActions(v any) []any {
	list := core.List(v)
	if len(list) == 0 {
		return nil
	}
	switch list[0].(type) {
	case string, int, float64, bool:
		return []any{list}
	}
	return list
}

func cronSpecs(name string, when map[string]any) []string {
	if spec := core.Str(when["cron"]); spec != "" {
		return []string{spec}
	}
	at := when["at"]
	times := core.List(at)
	if times == nil {
		times = []any{at}
	}
	specs := make([]string, 0, len(times))
	for _, t := range times {
		raw := core.Str(t)
		hh, mm, ok := strings.Cut(raw, ":")
		if !ok {
			slog.Warn("场景时间格式无效，已跳过", "场景", name, "at", raw)
			continue
		}
		h, err1 := strconv.Atoi(strings.TrimSpace(hh))
		m, err2 := strconv.Atoi(strings.TrimSpace(mm))
		if err1 != nil || err2 != nil || h < 0 || h > 23 || m < 0 || m > 59 {
			slog.Warn("场景时间超出范围，已跳过", "场景", name, "at", raw)
			continue
		}
		specs = append(specs, strconv.Itoa(m)+" "+strconv.Itoa(h)+" * * *")
	}
	return specs
}

func condition(when map[string]any) (string, any) {
	for _, op := range []string{"below", "above", "equals", "not_equals"} {
		if v, ok := when[op]; ok {
			return op, v
		}
	}
	if core.Bool(when["changed"], false) {
		return "changed", nil
	}
	return "", nil
}

func cronLoop(ctx context.Context) {
	last := ""
	ticker := time.NewTicker(cronInterval)
	defer ticker.Stop()
	for {
		now := time.Now()
		key := now.Format("2006-01-02 15:04")
		if key != last {
			last = key
			for _, r := range timeRules {
				if cronMatches(r.spec, now) {
					slog.Info("场景触发（时间）", "场景", r.name)
					go runActions(ctx, r.name, r.actions)
				}
			}
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

func stateLoop(ctx context.Context) {
	ticker := time.NewTicker(stateInterval)
	defer ticker.Stop()
	for {
		for _, r := range stateRules {
			obj := core.Find(r.entity)
			if obj == nil {
				continue
			}
			value, ok := getAttr(obj, r.attr)
			if !ok || value == nil {
				continue
			}
			fired := false
			if r.op == "changed" {
				fired = r.prev != nil && !equal(value, r.prev)
				r.prev = value
			} else {
				matched := evalCondition(r.op, r.value, value)
				fired = matched && !r.matched
				r.matched = matched
			}
			if fired {
				slog.Info("场景触发（状态）", "场景", r.name)
				go runActions(ctx, r.name, r.actions)
			}
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

func getAttr(obj core.Object, key string) (any, bool) {
	if co, ok := obj.(core.ConfObject); ok {
		return co.Get(key)
	}
	v, ok := obj.State()[key]
	return v, ok
}

func runActions(parent context.Context, name string, actions []any) {
	for _, a := range actions {
		ctx, cancel := context.WithTimeout(parent, actionTimeout)
		_, err := core.Exec(ctx, core.List(a))
		cancel()
		if err != nil {
			slog.Error("场景动作失败", "场景", name, "err", err)
		}
	}
}

func evalCondition(op string, target, value any) bool {
	switch op {
	case "equals":
		return equal(target, value)
	case "not_equals":
		return !equal(target, value)
	}
	t, ok1 := toFloat(target)
	v, ok2 := toFloat(value)
	if !ok1 || !ok2 {
		return false
	}
	switch op {
	case "below":
		return v < t
	case "above":
		return v > t
	}
	return false
}

func equal(a, b any) bool {
	if af, ok := toFloat(a); ok {
		if bf, ok := toFloat(b); ok {
			return af == bf
		}
	}
	return core.Str(a) == core.Str(b)
}

func toFloat(v any) (float64, bool) {
	switch x := v.(type) {
	case json.Number:
		f, err := x.Float64()
		return f, err == nil
	case float64:
		return x, true
	case int:
		return float64(x), true
	case int64:
		return float64(x), true
	case string:
		f, err := strconv.ParseFloat(x, 64)
		return f, err == nil
	}
	return 0, false
}

func cronMatches(spec string, now time.Time) bool {
	fields := strings.Fields(spec)
	if len(fields) != 5 {
		slog.Error("cron 表达式无效", "spec", spec)
		return false
	}
	return cronField(fields[0], 0, 59, now.Minute(), false) &&
		cronField(fields[1], 0, 23, now.Hour(), false) &&
		cronField(fields[2], 1, 31, now.Day(), false) &&
		cronField(fields[3], 1, 12, int(now.Month()), false) &&
		cronField(fields[4], 0, 6, int(now.Weekday()), true)
}

func cronField(field string, lo, hi, value int, weekday bool) bool {
	for _, part := range strings.Split(field, ",") {
		base, stepStr, hasStep := strings.Cut(part, "/")
		step := 1
		if hasStep {
			n, err := strconv.Atoi(stepStr)
			if err != nil || n <= 0 {
				continue
			}
			step = n
		}
		start, end := lo, hi
		if base != "*" {
			if a, b, ok := strings.Cut(base, "-"); ok {
				s, err1 := strconv.Atoi(a)
				e, err2 := strconv.Atoi(b)
				if err1 != nil || err2 != nil {
					continue
				}
				start, end = s, e
			} else {
				n, err := strconv.Atoi(base)
				if err != nil {
					continue
				}
				start, end = n, n
			}
		}
		for i := start; i <= end; i += step {
			v := i
			if weekday && v == 7 {
				v = 0
			}
			if v == value {
				return true
			}
		}
	}
	return false
}
