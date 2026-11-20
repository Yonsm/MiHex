// Package ping 通过 ICMP 探测设备在线状态，并支持 Wake-on-LAN 唤醒与 SSH 关机。
package ping

import (
	"bytes"
	"context"
	"encoding/hex"
	"fmt"
	"log/slog"
	"net"
	"os/exec"
	"reflect"
	"runtime"
	"strings"
	"syscall"
	"time"

	"github.com/Yonsm/MiHex/core"
)

func init() {
	core.RegisterModule("ping", setup)
}

func setup(sec core.Section) ([]core.Object, error) {
	var objs []core.Object
	for _, raw := range sec.List() {
		c := core.Map(raw)
		if unknown := core.LowerUnknown(c, "host", "mac"); len(unknown) > 0 {
			slog.Warn("设备存在未知配置键", "设备", core.Str(c["NAME"]), "键", strings.Join(unknown, ", "))
		}
		objs = append(objs, &Entity{Entity: core.NewEntity(c, "device")})
	}
	return objs, nil
}

// Entity 表示一台可 ping 的设备和可选的 WoL/SSH 关机能力。
type Entity struct {
	*core.Entity
}

func (e *Entity) Update(ctx context.Context) (bool, error) {
	host := e.Str("host")
	waitFlag := "1"
	if runtime.GOOS == "darwin" {
		waitFlag = "1000"
	}
	cmd := exec.CommandContext(ctx, "ping", "-c", "1", "-W", waitFlag, "--", host)
	err := cmd.Run()
	var ison any
	if err == nil {
		ison = 1
	}
	old, _ := e.Get("ISON")
	if reflect.DeepEqual(old, ison) {
		return false, nil
	}
	e.Set("ISON", ison)
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
	var err error
	if on {
		err = e.wake()
	} else {
		err = e.shutdown(ctx)
	}
	if err != nil {
		return nil, err
	}
	var ison any = on
	if len(args) > 0 {
		ison = args[0]
	}
	e.Set("ISON", ison)
	core.Emit(e)
	return nil, nil
}

// wake 发送 Wake-on-LAN 魔术包；失败时返回错误说明。
func (e *Entity) wake() error {
	clean := strings.NewReplacer(":", "", "-", "", " ", "").Replace(e.Str("mac"))
	mac, err := hex.DecodeString(clean)
	if err != nil || len(mac) != 6 {
		return fmt.Errorf("无效 MAC 地址 %q", e.Str("mac"))
	}
	packet := append(bytes.Repeat([]byte{0xff}, 6), bytes.Repeat(mac, 16)...)
	d := net.Dialer{Control: broadcastControl}
	conn, err := d.Dial("udp4", "255.255.255.255:9")
	if err != nil {
		return err
	}
	defer conn.Close()
	if _, err := conn.Write(packet); err != nil {
		return err
	}
	return nil
}

// shutdown 通过 SSH 执行 poweroff，超时 5s。
func (e *Entity) shutdown(ctx context.Context) error {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	return exec.CommandContext(ctx, "ssh", "--", e.Str("host"), "poweroff").Run()
}

func broadcastControl(_, _ string, c syscall.RawConn) error {
	var serr error
	if err := c.Control(func(fd uintptr) {
		serr = syscall.SetsockoptInt(int(fd), syscall.SOL_SOCKET, syscall.SO_BROADCAST, 1)
	}); err != nil {
		return err
	}
	return serr
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
