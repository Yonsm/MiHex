package miio

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/Yonsm/MiHex/core"
)

// 模式值对应同型号的 MIoT 规范；- 表示该值未定义。
var washerModes = map[string]string{
	"viomi.washer.v5":  "- 黄金烘 低温烘 空气洗 黄金洗 超快洗 棉织物 羊毛 羽绒服 洁桶洗 除菌洗 漂加脱 单脱水 快洗 衬衣 牛仔 内衣",
	"viomi.washer.v13": "黄金洗 黄金烘 洗加烘 超快洗 单脱水 空气洗 低温烘 暖烘 快速洗烘 棉织物 快洗 衬衣 牛仔 内衣 羊毛 羽绒服 除菌洗 童衣洗 精细织物 运动服 雪纺 棉节能 漂加脱 桶清洁",
	"viomi.washer.v33": "黄金洗 黄金烘 洗加烘 超快洗 单脱水 空气洗 - - - 棉织物 - 衬衣 牛仔 - 羊毛 羽绒服 除菌洗 - - 户外 - - 漂加脱 桶清洁 - 大物件 丝绸 护色",
}

func (e *entity) washer(ctx context.Context, args []any) (any, bool, error) {
	cmd := "预约洗衣"
	if len(args) > 0 {
		cmd = core.Str(args[0])
	}
	if cmd != "预约洗衣" && cmd != "预约取消" {
		return nil, false, nil
	}
	model := e.Str("model")
	modes, ok := washerModes[model]
	if !ok {
		return nil, true, fmt.Errorf("不支持的云米洗衣机型号 %q", model)
	}
	d := e.dev
	d.mu.Lock()
	defer d.mu.Unlock()
	t := d.fallback
	if t == nil {
		return nil, true, errNoCloud
	}
	power, modeID, appoint := iid{siid: 2, piid: 1}, iid{siid: 2, piid: 3}, iid{siid: 4, piid: 10}
	v5, v13 := model == "viomi.washer.v5", model == "viomi.washer.v13"
	if cmd == "预约取消" {
		if !v5 {
			if err := t.set(ctx, appoint, 0); err != nil {
				return nil, true, err
			}
			if err := wait(ctx, 5*time.Second); err != nil {
				return nil, true, err
			}
		}
		return "预约洗衣已取消", true, t.set(ctx, power, false)
	}
	name := "黄金洗"
	if len(args) > 1 && truthy(args[1]) {
		name = core.Str(args[1])
	}
	mode := -1
	for i, m := range strings.Fields(modes) {
		if m != "-" && m == name {
			mode = i
			break
		}
	}
	if mode < 0 {
		return nil, true, fmt.Errorf("不支持的洗衣模式 %q", name)
	}
	hours := -8
	if len(args) > 2 {
		v, err := strconv.Atoi(core.Str(args[2]))
		if err != nil || v < -23 {
			return nil, true, fmt.Errorf("无效洗衣预约时间 %v", args[2])
		}
		if v != 0 {
			hours = v
		}
	}
	if err := t.set(ctx, power, true); err != nil {
		return nil, true, err
	}
	if err := wait(ctx, 500*time.Millisecond); err != nil {
		return nil, true, err
	}
	if err := t.set(ctx, modeID, mode); err != nil {
		return nil, true, err
	}
	if err := wait(ctx, 1500*time.Millisecond); err != nil {
		return nil, true, err
	}
	now := time.Now()
	delay := int64(hours)
	status := fmt.Sprintf("预约%d小时后完成洗衣", hours)
	if hours < 0 {
		clock := -hours
		status = fmt.Sprintf("预约%d点钟完成洗衣", clock)
		if v13 {
			next := time.Date(now.Year(), now.Month(), now.Day(), clock, 0, 0, 0, now.Location())
			if next.Before(now) {
				next = next.AddDate(0, 0, 1)
			}
			delay = next.Unix()
			if next.Sub(now) > 12*time.Hour {
				hours = 0
			}
		} else {
			hour := now.Hour()
			if now.Minute() > 10 {
				hour++
			}
			delay = int64((clock - hour + 24) % 24)
			if delay > 12 {
				hours = 0
			}
		}
	} else if v13 {
		delay = now.Unix() + int64(hours)*3600
	}
	if hours == 0 {
		code, err := t.action(ctx, power, []any{mode})
		if err == nil && code != 0 {
			err = fmt.Errorf("米家云端启动洗衣未获确认: %d", code)
		}
		return fmt.Sprintf("预约时间%d>12小时，改为立即开始", delay), true, err
	}
	if !v5 {
		return status, true, t.set(ctx, appoint, delay)
	}
	return status, true, nil
}

func wait(ctx context.Context, d time.Duration) error {
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}
