package main

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/Yonsm/MiHex/core"
	_ "github.com/Yonsm/MiHex/modules/broadlink"
	_ "github.com/Yonsm/MiHex/modules/ding"
	_ "github.com/Yonsm/MiHex/modules/gate"
	"github.com/Yonsm/MiHex/modules/miio"
	_ "github.com/Yonsm/MiHex/modules/mqtt"
	_ "github.com/Yonsm/MiHex/modules/ping"
	_ "github.com/Yonsm/MiHex/modules/scene"
	_ "github.com/Yonsm/MiHex/modules/shell"
	_ "github.com/Yonsm/MiHex/modules/weather"
	_ "github.com/Yonsm/MiHex/server"
)

const version = "1.5.0"

func main() {
	level, logFile, confPath, rest, help := parseArgs(os.Args[1:])
	slog.SetDefault(newLogger(level, logFile))
	if help {
		usage()
		return
	}

	path := findConf(confPath)
	cfg, err := core.Load(path)
	if err != nil {
		slog.Error("加载配置失败", "err", err)
		os.Exit(1)
	}
	if err := os.Chdir(cfg.Dir); err != nil {
		slog.Error("切换目录失败", "dir", cfg.Dir, "err", err)
		os.Exit(1)
	}

	ctx := context.Background()
	if len(rest) == 1 && rest[0] == "login" {
		ctx, stop := signal.NotifyContext(ctx, os.Interrupt, syscall.SIGTERM)
		defer stop()
		ctx, cancel := context.WithTimeout(ctx, 5*time.Minute)
		defer cancel()
		n, err := miio.Login(ctx, cfg, os.Stdin, os.Stderr)
		if err != nil {
			slog.Error("米家云验证失败", "err", err)
			os.Exit(1)
		}
		fmt.Printf("米家云验证成功：%d 台设备\n", n)
		return
	}
	serve := len(rest) == 0
	if err := core.LoadModules(cfg, serve); err != nil {
		slog.Error("加载模块失败", "err", err)
		os.Exit(1)
	}

	if !serve {
		r, err := core.Exec(ctx, toAny(rest))
		if err != nil {
			slog.Error("执行失败", "err", err)
			os.Exit(1)
		}
		if r != nil {
			fmt.Println(r)
		}
		return
	}

	slog.Warn("运行中")
	sig := make(chan os.Signal, 1)
	signal.Notify(sig, os.Interrupt, syscall.SIGTERM)
	<-sig
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := core.Shutdown(ctx); err != nil {
		slog.Error("退出清理失败", "err", err)
	}
	slog.Warn("已退出")
}

func toAny(ss []string) []any {
	out := make([]any, len(ss))
	for i, s := range ss {
		out[i] = s
	}
	return out
}

func parseArgs(args []string) (level slog.Level, logFile, conf string, rest []string, help bool) {
	level = slog.LevelWarn
	for i := 0; i < len(args); i++ {
		a := args[i]
		switch {
		case a == "-h" || a == "--help":
			help = true
		case a == "-c" && i+1 < len(args):
			conf = args[i+1]
			i++
		case strings.HasPrefix(a, "-v") || strings.HasPrefix(a, "-V"):
			level = logLevel(a)
			if a[1] == 'V' {
				logFile = "/tmp/mihex.log"
			}
		default:
			rest = append(rest, a)
		}
	}
	return
}

// logLevel 与 Python 版一致：-v0 静默、-v1 CRITICAL … -v5 DEBUG，无数字时 INFO。
func logLevel(flag string) slog.Level {
	if len(flag) > 2 && flag[2] >= '0' && flag[2] <= '5' {
		n, _ := strconv.Atoi(flag[2:3])
		return []slog.Level{13, 12, slog.LevelError, slog.LevelWarn, slog.LevelInfo, slog.LevelDebug}[n]
	}
	return slog.LevelInfo
}

func newLogger(level slog.Level, file string) *slog.Logger {
	var w io.Writer = os.Stderr
	if file != "" {
		if f, err := os.Create(file); err == nil {
			w = f
		}
	}
	h := slog.NewTextHandler(w, &slog.HandlerOptions{
		Level: level,
		ReplaceAttr: func(_ []string, a slog.Attr) slog.Attr {
			if a.Key == slog.TimeKey {
				return slog.Attr{}
			}
			return a
		},
	})
	return slog.New(h)
}

// findConf 按优先级定位配置：-c 指定 > 当前目录 > ~/.mihex.yml > 可执行文件所在目录。
// 返回值可能不存在，由 core.Load 报错提示。
func findConf(explicit string) string {
	if explicit != "" {
		return explicit
	}
	candidates := []string{"mihex.yml"}
	if home, err := os.UserHomeDir(); err == nil {
		candidates = append(candidates, filepath.Join(home, ".mihex.yml"))
	}
	if exe, err := os.Executable(); err == nil {
		if abs, err := filepath.Abs(exe); err == nil {
			exe = abs
		}
		candidates = append(candidates, filepath.Join(filepath.Dir(exe), "mihex.yml"))
	}
	for _, p := range candidates {
		if info, err := os.Stat(p); err == nil && !info.IsDir() {
			return p
		}
	}
	return "mihex.yml"
}

func usage() {
	fmt.Printf(`MiHex %s

用法:
   mihex [-v|-c mihex.yml] [实体] [参数...]

   mihex MQTT <主题> [消息]
   mihex DING <消息>
   mihex login                    交互验证米家云并缓存令牌
   mihex 实体名 [方法 参数...]
`, version)
}
