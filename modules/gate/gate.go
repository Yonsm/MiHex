// Package gate 将米家局域网网关的语音事件转交给 runner。
package gate

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"strings"
	"time"
	"unicode/utf8"

	mqttlib "github.com/eclipse/paho.mqtt.golang"

	"github.com/Yonsm/MiHex/core"
	"github.com/Yonsm/MiHex/modules/mqtt"
)

const topic = "local/lan/devMsg"

var substitute = strings.NewReplacer("文本", "#", "设为", "=", "也", ",", "者", "/", "乎", ".")

func init() { core.RegisterModule("gate", setup) }

func setup(sec core.Section) ([]core.Object, error) {
	if sec.AfterLoad == nil {
		return nil, nil
	}
	conf := sec.Map()
	sec.AfterLoad(func() {
		ctx, cancel := context.WithCancel(context.Background())
		c, _ := mqtt.Subscribe(core.StrDef(conf["host"], "localhost"), core.Int(conf["port"], 8884), topic, func(_ mqttlib.Client, m mqttlib.Message) {
			if m.Retained() || len(m.Payload()) > 10000 {
				return
			}
			args := command(m.Payload())
			if len(args) == 0 {
				return
			}
			call, stop := context.WithTimeout(ctx, 15*time.Second)
			defer stop()
			if _, err := core.Exec(call, args); err != nil {
				slog.Warn("Gate 语音命令执行失败", "err", err)
			}
		})
		core.OnShutdown(func(context.Context) error { cancel(); c.Disconnect(250); return nil })
	})
	return nil, nil
}

func command(payload []byte) []any {
	if !utf8.Valid(payload) {
		return nil
	}
	text := string(payload)
	if start := bytes.IndexByte(payload, '{'); start >= 0 {
		end := bytes.LastIndexByte(payload, '}')
		if end < start {
			return nil
		}
		var event struct {
			Method string
			Params struct {
				SIID, EIID int
				Arguments  []struct{ Value string }
			}
		}
		if json.Unmarshal(payload[start:end+1], &event) != nil || event.Method != "event_occured" || event.Params.SIID != 7 || event.Params.EIID != 1 || len(event.Params.Arguments) == 0 {
			return nil
		}
		text = event.Params.Arguments[0].Value
	}
	if strings.TrimSpace(text) == "" {
		return nil
	}
	parts := strings.Split(substitute.Replace(text), "之")
	args := make([]any, len(parts))
	for i, p := range parts {
		args[i] = p
	}
	return args
}
