// 快/慢两种节奏的实体轮询与状态广播。
//
// 有 WebSocket 客户端连接时用 fast（默认 60s），全部断开后回到 slow
// （默认 3600s），与 Python 版 track() 的调度语义一致。
package core

import (
	"context"
	"fmt"
	"log/slog"
	"sync"
	"time"
)

var (
	trackerMu sync.Mutex
	fastGap   = 60 * time.Second
	slowGap   = time.Hour
	gap       = time.Hour
	runLock   sync.Mutex
	wake      = make(chan struct{}, 1)
)

func init() {
	RegisterModule("tracker", trackerSetup)
}

func trackerSetup(sec Section) ([]Object, error) {
	m := sec.Map()
	fastGap = seconds(m["fast"], 60)
	slowGap = seconds(m["slow"], 3600)
	if fastGap <= 0 || slowGap <= 0 {
		return nil, fmt.Errorf("fast 和 slow 轮询间隔必须大于零")
	}
	trackerMu.Lock()
	gap = slowGap
	trackerMu.Unlock()
	go loop()
	return nil, nil
}

func seconds(v any, def int) time.Duration {
	return time.Duration(Int(v, def)) * time.Second
}

// Fast 切换到快速轮询并立刻唤醒一轮。
func Fast() { setGap(fastGap, "快速") }

// Slow 切回慢速轮询。
func Slow() { setGap(slowGap, "慢速") }

func setGap(d time.Duration, name string) {
	trackerMu.Lock()
	gap = d
	trackerMu.Unlock()
	Wake()
	slog.Info("轮询节奏", "模式", name, "间隔", d)
}

// Wake 立即请求一轮轮询，不改变当前节奏。
func Wake() {
	select {
	case wake <- struct{}{}:
	default:
	}
}

func currentGap() time.Duration {
	trackerMu.Lock()
	defer trackerMu.Unlock()
	return gap
}

func loop() {
	for {
		Track(context.Background())
		select {
		case <-wake:
		case <-time.After(currentGap()):
		}
	}
}

// Track 并发更新一轮所有实体；单个实体超时 15s。
func Track(ctx context.Context) {
	runLock.Lock()
	defer runLock.Unlock()
	var wg sync.WaitGroup
	for _, o := range Objects() {
		p, ok := o.(Poller)
		if !ok {
			continue
		}
		wg.Add(1)
		go func(o Object, p Poller) {
			defer wg.Done()
			update(ctx, o, p)
		}(o, p)
	}
	wg.Wait()
}

func update(ctx context.Context, o Object, p Poller) {
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	changed, err := p.Update(ctx)
	if err != nil {
		slog.Error("更新失败", "实体", o.Name(), "err", err)
		return
	}
	if changed {
		Emit(o)
	}
}
