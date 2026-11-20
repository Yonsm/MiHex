// Package mqtt 订阅 MQTT 主题获取传感器数据，发布指令控制设备。
package mqtt

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net"
	"reflect"
	"strconv"
	"strings"
	"sync"
	"time"

	mqttlib "github.com/eclipse/paho.mqtt.golang"

	"github.com/Yonsm/MiHex/core"
)

const (
	maxMsgs    = 1000
	maxPayload = 10000
)

var (
	mu     sync.Mutex
	msgs   = map[string]string{}
	order  []string
	client mqttlib.Client
)

func init() {
	core.RegisterModule("mqtt", setup)
}

func setup(sec core.Section) ([]core.Object, error) {
	conf := sec.Map()
	host := core.StrDef(conf["host"], "localhost")
	port := core.Int(conf["port"], 1883)
	topic := core.StrDef(conf["topic"], "#")

	c, connected := Subscribe(host, port, topic, onMessage)
	// 保持初次连接的有界等待，供单次命令发送 MQTT 消息。
	if !connected.WaitTimeout(3 * time.Second) {
		slog.Warn("MQTT 连接超时，后台重试")
	} else if connected.Error() != nil {
		slog.Warn("MQTT 连接失败，后台重试", "err", connected.Error())
	}
	client = c
	core.OnShutdown(func(context.Context) error {
		if client != nil {
			client.Disconnect(250)
		}
		return nil
	})

	mod := core.NewMod("MQTT")
	mod.Methods["service"] = true
	mod.CallFn = func(_ context.Context, _ string, args []any) (any, error) {
		return handle(args)
	}

	objs := []core.Object{mod}
	for _, k := range sec.Keys {
		for _, raw := range core.List(conf[k]) {
			c := core.Map(raw)
			if unknown := core.LowerUnknown(c, "stat", "json", "turn"); len(unknown) > 0 {
				slog.Warn("设备存在未知配置键", "设备", core.Str(c["NAME"]), "键", strings.Join(unknown, ", "))
			}
			objs = append(objs, &Entity{Entity: core.NewEntity(c, k)})
		}
	}
	return objs, nil
}

// Subscribe 建立独立的 MQTT 订阅连接，断线后重新订阅。
func Subscribe(host string, port int, topic string, handler mqttlib.MessageHandler) (mqttlib.Client, mqttlib.Token) {
	addr := net.JoinHostPort(host, strconv.Itoa(port))
	opts := mqttlib.NewClientOptions().
		AddBroker("tcp://" + addr).
		SetClientID(fmt.Sprintf("mihex-%d", time.Now().UnixNano())).
		SetAutoReconnect(true).
		SetConnectRetry(true).
		SetConnectRetryInterval(2 * time.Second).
		SetMaxReconnectInterval(120 * time.Second).
		SetKeepAlive(30 * time.Second).
		SetDefaultPublishHandler(handler)

	opts.SetOnConnectHandler(func(c mqttlib.Client) {
		t := c.Subscribe(topic, 0, nil)
		if !t.WaitTimeout(5 * time.Second) {
			slog.Warn("MQTT 订阅超时", "addr", addr, "topic", topic)
			return
		}
		if err := t.Error(); err != nil {
			slog.Error("MQTT 订阅失败", "addr", addr, "topic", topic, "err", err)
			return
		}
		slog.Info("MQTT 已连接", "addr", addr, "topic", topic)
	})
	opts.SetConnectionLostHandler(func(_ mqttlib.Client, err error) {
		slog.Warn("MQTT 连接断开，自动重连", "err", err)
	})

	c := mqttlib.NewClient(opts)
	return c, c.Connect()
}

// service 发布消息，或读取缓存的主题值（支持 topic.field 形式的 JSON 路径）。
func handle(args []any) (any, error) {
	if len(args) > 1 {
		return nil, publish(core.Str(args[0]), args[1])
	}
	if len(args) == 1 {
		parts := strings.SplitN(core.Str(args[0]), ".", 2)
		mu.Lock()
		val, ok := msgs[parts[0]]
		mu.Unlock()
		if !ok || val == "" {
			return nil, nil
		}
		if len(parts) == 1 {
			return val, nil
		}
		var m map[string]any
		if json.Unmarshal([]byte(val), &m) != nil {
			return nil, nil
		}
		return m[parts[1]], nil
	}
	return nil, nil
}

func publish(topic string, payload any) error {
	if client == nil || !client.IsConnected() {
		return fmt.Errorf("MQTT 未连接，无法发布到 %s", topic)
	}
	t := client.Publish(topic, 0, true, fmt.Sprint(payload))
	if !t.WaitTimeout(2 * time.Second) {
		slog.Warn("MQTT 发布超时", "topic", topic)
		return fmt.Errorf("MQTT 发布超时: %s", topic)
	}
	if err := t.Error(); err != nil {
		slog.Error("MQTT 发布失败", "topic", topic, "err", err)
		return err
	}
	return nil
}

func store(topic, payload string) {
	mu.Lock()
	defer mu.Unlock()
	if _, ok := msgs[topic]; !ok {
		order = append(order, topic)
		if len(order) > maxMsgs {
			delete(msgs, order[0])
			order = order[1:]
		}
	}
	msgs[topic] = payload
}

func onMessage(_ mqttlib.Client, m mqttlib.Message) {
	topic := m.Topic()
	payload := m.Payload()
	if len(payload) > maxPayload {
		slog.Warn("MQTT 消息过大，已丢弃", "topic", topic, "size", len(payload))
		return
	}
	store(topic, string(payload))

	var changed []core.Object
	for _, o := range core.Objects() {
		co, ok := o.(core.ConfObject)
		if !ok || core.Str(co.Config()["stat"]) != topic {
			continue
		}
		stat, ok := parseStat(co.Config(), payload)
		if !ok {
			continue
		}
		if old, _ := co.Get("STAT"); reflect.DeepEqual(old, stat) {
			continue
		}
		co.Set("STAT", stat)
		changed = append(changed, o)
	}
	if len(changed) > 0 {
		core.Emit(changed...)
	}
}

func parseStat(conf map[string]any, payload []byte) (any, bool) {
	key := core.Str(conf["json"])
	if key == "" {
		return string(payload), true
	}
	var m map[string]any
	if json.Unmarshal(payload, &m) != nil {
		return nil, false
	}
	val, ok := m[key]
	return val, ok
}

// Entity 是由 MQTT 消息驱动状态的设备实体。
type Entity struct {
	*core.Entity
}

func (e *Entity) Supports(method string) bool {
	return method == "on_turn" || method == "service"
}

func (e *Entity) Call(ctx context.Context, method string, args []any) (any, error) {
	switch method {
	case "on_turn":
		topic := core.StrDef(e.Conf["turn"], e.Str("stat"))
		var payload any
		if len(args) > 0 {
			payload = args[0]
		} else {
			stat, _ := e.Get("STAT")
			payload = !toBool(stat)
		}
		return nil, publish(topic, payload)
	case "service":
		topic := e.Str("stat")
		if len(args) > 0 {
			if turn := e.Str("turn"); turn != "" {
				topic = turn
			}
		}
		return handle(append([]any{topic}, args...))
	}
	return e.Entity.Call(ctx, method, args)
}

func toBool(v any) bool {
	switch x := v.(type) {
	case bool:
		return x
	case string:
		return x != "" && x != "0" && x != "false"
	case int:
		return x != 0
	case float64:
		return x != 0
	}
	return false
}
