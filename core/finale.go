package core

import (
	"context"
	"errors"
	"sync"
)

var (
	shutdownMu  sync.Mutex
	shutdownFns []func(context.Context) error
)

// OnShutdown 注册退出清理函数，按注册的逆序执行。
func OnShutdown(fn func(context.Context) error) {
	shutdownMu.Lock()
	shutdownFns = append(shutdownFns, fn)
	shutdownMu.Unlock()
}

// Shutdown 依次执行清理函数；超时由调用方的 ctx 控制。
func Shutdown(ctx context.Context) error {
	shutdownMu.Lock()
	fns := append([]func(context.Context) error(nil), shutdownFns...)
	shutdownMu.Unlock()
	var errs []error
	for i := len(fns) - 1; i >= 0; i-- {
		if err := fns[i](ctx); err != nil {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}
