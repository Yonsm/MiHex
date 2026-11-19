package miio

import (
	"context"
	"errors"
	"log/slog"
	"strings"
	"sync"
	"time"

	"github.com/Yonsm/MiHex/core"
)

type miioService struct {
	cloud *cloud
	specs *specStore
	mu    sync.RWMutex
	dids  map[string]string
}

func setup(sec core.Section) ([]core.Object, error) {
	conf := sec.Map()
	svc := &miioService{specs: newSpecs(), dids: map[string]string{}}
	if user := core.Str(conf["user"]); user != "" {
		svc.cloud = newCloud(user, core.Str(conf["pass"]), nil)
	}
	manual := map[string]*device{}
	var local []core.Object
	for _, name := range sec.Keys {
		if !manualKey(name) {
			continue
		}
		d, err := newDevice(name, conf[name], nil)
		if err != nil {
			slog.Warn("跳过 MiIO 设备", "设备", name, "err", err)
			manual[name] = nil
			continue
		}
		manual[name] = d
		d.service = svc
		if d.local != nil {
			local = append(local, d.objects()...)
		}
	}
	if svc.cloud == nil {
		return local, nil
	}
	if sec.AfterLoad == nil {
		ctx, cancel := context.WithTimeout(context.Background(), 12*time.Second)
		defer cancel()
		return append(local, svc.discover(ctx, conf["auto"], manual)...), nil
	}
	sec.AfterLoad(func() {
		ctx, cancel := context.WithCancel(context.Background())
		core.OnShutdown(func(context.Context) error { cancel(); return nil })
		go func() {
			defer cancel()
			for i := 1; i <= 3; i++ {
				attempt, stop := context.WithTimeout(ctx, 8*time.Second)
				devices, err := svc.cloud.deviceList(attempt)
				stop()
				if err == nil {
					scan, done := context.WithTimeout(ctx, 45*time.Second)
					defer done()
					objs := svc.configure(scan, conf["auto"], manual, devices)
					if ctx.Err() == nil && len(objs) > 0 {
						core.Register(objs...)
						core.Emit(objs...)
						core.Wake()
					}
					return
				}
				if ctx.Err() != nil {
					return
				}
				slog.Warn("MiIO 云端设备列表不可用，本地设备继续运行", "尝试", i, "err", err)
				if errors.Is(err, errOTPRequired) {
					return
				}
				if i < 3 {
					select {
					case <-ctx.Done():
						return
					case <-time.After(time.Duration(i) * 2 * time.Second):
					}
				}
			}
		}()
	})
	return local, nil
}

func (s *miioService) discover(ctx context.Context, auto any, manual map[string]*device) []core.Object {
	devices, err := s.cloud.deviceList(ctx)
	if err != nil {
		slog.Warn("MiIO 云端设备列表不可用，本地设备继续运行", "err", err)
		return nil
	}
	return s.configure(ctx, auto, manual, devices)
}

func (s *miioService) configure(ctx context.Context, auto any, manual map[string]*device, devices []cloudDevice) []core.Object {
	if !truthy(auto) && len(manual) == 0 {
		auto = true
	}
	members := map[string]bool{}
	for _, d := range devices {
		for _, id := range d.Members {
			members[id] = true
		}
	}
	var homeDIDs map[string]bool
	if name, ok := auto.(string); ok && name != "" {
		homeDIDs = map[string]bool{}
		if homes, err := s.cloud.homeList(ctx); err != nil {
			slog.Warn("MiIO 云端家庭列表不可用", "err", err)
		} else {
			for _, h := range homes {
				if h.Name == name {
					for _, id := range h.DIDs {
						homeDIDs[id] = true
					}
					break
				}
			}
		}
	}
	var selected map[string]bool
	if names := core.List(auto); names != nil {
		selected = map[string]bool{}
		for _, n := range names {
			selected[core.Str(n)] = true
		}
	}
	var objs []core.Object
	var pending []cloudDevice
	seen := map[string]bool{}
	for _, dev := range devices {
		if dev.Name == "" || dev.DID == "" {
			continue
		}
		s.mu.Lock()
		if s.dids[dev.Name] == "" {
			s.dids[dev.Name] = dev.DID
		}
		s.mu.Unlock()
		if d, ok := manual[dev.Name]; ok {
			if d == nil || seen[dev.Name] {
				continue
			}
			seen[dev.Name] = true
			d.mu.Lock()
			wasLocal := d.local != nil
			d.fallback = &cloudTarget{cloud: s.cloud, did: dev.DID}
			if !wasLocal {
				d.local = cloudLocal(dev)
			}
			d.mu.Unlock()
			if !wasLocal {
				objs = append(objs, d.objects()...)
			}
		} else if truthy(auto) && !members[dev.DID] && (homeDIDs == nil || homeDIDs[dev.DID]) && (selected == nil || selected[dev.Name]) {
			pending = append(pending, dev)
		}
	}
	for name, d := range manual {
		if d != nil && d.local == nil && !seen[name] {
			slog.Warn("米家账号中未找到手动设备", "设备", name)
		}
	}
	cfgs := make([]map[string]any, len(pending))
	var wg sync.WaitGroup
	jobs := make(chan int)
	for range min(5, len(pending)) {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := range jobs {
				d := pending[i]
				cfgs[i] = s.specs.autoConfig(ctx, d.Name, d.Model)
			}
		}()
	}
	for i := range pending {
		if ctx.Err() != nil {
			break
		}
		jobs <- i
	}
	close(jobs)
	wg.Wait()
	for i, cfg := range cfgs {
		if cfg == nil {
			continue
		}
		dev := pending[i]
		d, err := newDevice(dev.Name, cfg, cloudLocal(dev))
		if err != nil {
			slog.Warn("跳过 MIoT 自动设备", "设备", dev.Name, "err", err)
			continue
		}
		d.service = s
		d.fallback = &cloudTarget{cloud: s.cloud, did: dev.DID}
		objs = append(objs, d.objects()...)
	}
	return objs
}

func cloudLocal(d cloudDevice) *localDevice {
	if d.Host == "" || d.Token == "" {
		return nil
	}
	l, err := newLocal(d.Host, d.Token)
	if err != nil {
		slog.Debug("MiIO 云端记录无有效本地 token", "设备", d.Name)
	}
	return l
}

func (s *miioService) lookup(ctx context.Context, name string) (string, error) {
	s.mu.RLock()
	did := s.dids[name]
	s.mu.RUnlock()
	if did != "" {
		return did, nil
	}
	if s.cloud == nil {
		return "", nil
	}
	devices, err := s.cloud.deviceList(ctx)
	if err != nil {
		return "", err
	}
	for _, d := range devices {
		if strings.Contains(d.Name, name) {
			return d.DID, nil
		}
	}
	return "", nil
}
