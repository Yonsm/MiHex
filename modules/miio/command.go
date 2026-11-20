package miio

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"net/url"
	"strconv"
	"strings"

	misgo "github.com/Yonsm/misgo/miio"
	"github.com/Yonsm/misgo/miio/decode"

	"github.com/Yonsm/MiHex/core"
)

var errNoCloud = errors.New("MiIO 云命令需要配置米家账号")

type cloudTarget struct {
	cloud *cloud
	did   string
}

type propWrite struct {
	id  iid
	val any
}

func (t *cloudTarget) rpc(ctx context.Context, method string, params any, result any) error {
	return t.cloud.request(ctx, "/home/rpc/"+url.PathEscape(t.did), map[string]any{"id": 1, "accessKey": "IOS00026747c5acafc2", "method": method, "params": params}, result)
}
func (t *cloudTarget) miot(ctx context.Context, method string, params any, result any) error {
	return t.cloud.request(ctx, "/miotspec/"+method, map[string]any{"params": params}, result)
}
func (t *cloudTarget) read(ctx context.Context, ids []iid) ([]any, error) {
	if ids[0].name != "" {
		return t.cloud.service.HomeGetProps(ctx, t.did, legacyNames(ids))
	}
	iids := make([]misgo.IID, len(ids))
	for i, id := range ids {
		iids[i] = misgo.IID{SIID: id.siid, PIID: id.piid}
	}
	return t.cloud.service.MIoTGetProps(ctx, t.did, iids)
}

func (t *cloudTarget) write(ctx context.Context, props []propWrite) ([]any, error) {
	out := make([]any, 0, len(props))
	if props[0].id.name != "" {
		for _, p := range props {
			r, err := t.cloud.service.HomeSetProp(ctx, t.did, p.id.name, p.val)
			if err != nil {
				return nil, err
			}
			out = append(out, r)
		}
		return out, nil
	}
	iids := make([]misgo.MIoTProperty, len(props))
	for i, p := range props {
		iids[i] = misgo.MIoTProperty{IID: misgo.IID{SIID: p.id.siid, PIID: p.id.piid}, Value: p.val}
	}
	codes, err := t.cloud.service.MIoTSetProps(ctx, t.did, iids)
	if err != nil {
		return nil, err
	}
	for _, code := range codes {
		out = append(out, code)
	}
	return out, nil
}

func (t *cloudTarget) set(ctx context.Context, id iid, val any) error {
	values, err := t.write(ctx, []propWrite{{id, val}})
	if err != nil {
		return err
	}
	if len(values) == 0 || !equal(values[0], 0) {
		return fmt.Errorf("米家云端写入 %s 未获确认", id)
	}
	return nil
}

func (t *cloudTarget) action(ctx context.Context, id iid, args []any) (int, error) {
	return t.cloud.service.MIoTAction(ctx, t.did, misgo.IID{SIID: id.siid, PIID: id.piid}, args)
}

const commandHelp = `Get Props: <siid[-piid]>[,...] or legacy property names
Set Props: <siid[-piid]=[#]value>[,...]
Do Action: <siid[-aiid]> <arg1|[]> [...]
Call MIoT: <prop/get|prop/set|action> <params>
Call MiIO: /<uri> <data>
Devs List: list [name_keyword|full] [getVirtualModel] [getHuamiDevices]
Home List: home [name_keyword|full]
MIoT Spec: spec [model_keyword|type_urn] [text|python]
MIoT Decode: decode <ssecurity> <nonce> <data> [gzip]`

func (s *miioService) command(ctx context.Context, name string, args []any) (any, error) {
	parts := make([]string, len(args))
	for i, a := range args {
		parts[i] = core.Str(a)
	}
	cmd, arg, hasArg := strings.Cut(strings.Join(parts, " "), " ")
	argv := strings.Fields(arg)
	get := func(i int) string {
		if i < len(argv) {
			return argv[i]
		}
		return ""
	}
	switch cmd {
	case "", "?", "？", "help", "-h", "--help":
		return commandHelp, nil
	case "spec":
		return s.specs.command(ctx, get(0), get(1))
	case "decode":
		return decode.Decode(get(0), get(1), get(2), get(3) == "gzip")
	}
	if s.cloud == nil {
		return nil, errNoCloud
	}
	switch cmd {
	case "list":
		huami := 0
		if v := get(2); v != "" {
			n, err := strconv.Atoi(v)
			if err != nil {
				return nil, fmt.Errorf("无效 getHuamiDevices: %w", err)
			}
			huami = n
		}
		list, err := s.cloud.rawDeviceList(ctx, truthy(commandValue(get(1))), huami)
		if err != nil {
			return nil, err
		}
		out := []any{}
		for _, d := range list {
			switch name := get(0); {
			case name == "full":
				out = append(out, d)
			case strings.Contains(core.Str(d["name"]), name):
				out = append(out, fields(d, "name", "model", "did", "token"))
			}
		}
		return out, nil
	case "home":
		list, err := s.cloud.rawHomeList(ctx)
		if err != nil {
			return nil, err
		}
		out := []any{}
		for _, h := range list {
			switch name := get(0); {
			case name == "full":
				out = append(out, h)
			case strings.Contains(core.Str(h["name"]), name):
				rooms := []any{}
				for _, raw := range core.List(valueOr(h, "roomlist", h["rooms"])) {
					room := core.Map(raw)
					r := fields(room, "id", "name")
					r["dids"] = valueOr(room, "dids", valueOr(room, "device_list", []any{}))
					rooms = append(rooms, r)
				}
				h := fields(h, "id", "name")
				h["rooms"] = rooms
				out = append(out, h)
			}
		}
		return out, nil
	}
	var raw json.RawMessage
	if strings.HasPrefix(cmd, "/") {
		var data any
		if hasArg {
			data = arg
		}
		err := s.cloud.request(ctx, cmd, data, &raw)
		return normalizeResult(raw), err
	}
	if strings.HasPrefix(cmd, "prop") || cmd == "action" {
		var data json.RawMessage
		if arg != "" {
			data = json.RawMessage(arg)
			if !json.Valid(data) {
				return nil, errors.New("MiIO 命令参数必须是 JSON")
			}
		}
		err := (&cloudTarget{cloud: s.cloud}).miot(ctx, cmd, data, &raw)
		return normalizeResult(raw), err
	}
	did, err := s.lookup(ctx, name)
	if err != nil {
		return nil, err
	}
	if did == "" {
		return "Device not found: " + name, nil
	}
	t := &cloudTarget{cloud: s.cloud, did: did}
	var ids []iid
	var props []propWrite
	for _, item := range strings.Split(cmd, ",") {
		key, value, write := strings.Cut(item, "=")
		id, err := parseIID(key)
		if err != nil || id == (iid{}) || (len(ids) > 0 && (id.name == "") != (ids[0].name == "")) {
			return "Invalid command: " + cmd, nil
		}
		ids = append(ids, id)
		if write {
			props = append(props, propWrite{id, commandValue(value)})
		}
	}
	if len(props) > 0 {
		if len(props) != len(ids) {
			return "Invalid command: " + cmd, nil
		}
		return t.write(ctx, props)
	}
	if ids[0].name == "" && arg != "" {
		if len(ids) > 1 {
			return "Invalid command: " + cmd, nil
		}
		vals := []any{}
		if arg != "[]" {
			for _, a := range argv {
				vals = append(vals, commandValue(a))
			}
		}
		return t.action(ctx, ids[0], vals)
	}
	return t.read(ctx, ids)
}

func fields(m map[string]any, keys ...string) map[string]any {
	out := make(map[string]any, len(keys))
	for _, k := range keys {
		out[k] = m[k]
	}
	return out
}

func normalizeResult(raw json.RawMessage) any {
	var v any
	d := json.NewDecoder(bytes.NewReader(raw))
	d.UseNumber()
	if d.Decode(&v) != nil {
		return nil
	}
	return v
}

func truthy(v any) bool {
	switch x := v.(type) {
	case nil:
		return false
	case bool:
		return x
	case string:
		return x != ""
	case []any:
		return len(x) > 0
	case map[string]any:
		return len(x) > 0
	}
	if n, ok := number(v); ok {
		return n != 0
	}
	return true
}

func commandValue(s string) any {
	if s != "" && strings.ContainsRune(`"'#`, rune(s[0])) {
		if len(s) == 1 {
			return ""
		}
		if strings.ContainsRune(`"'#`, rune(s[len(s)-1])) {
			return s[1 : len(s)-1]
		}
		return s[1:]
	}
	switch s {
	case "null":
		return nil
	case "false":
		return false
	case "true":
		return true
	}
	if n, err := strconv.ParseInt(s, 10, 64); err == nil {
		return n
	}
	if n, err := strconv.ParseFloat(s, 64); err == nil && !math.IsNaN(n) && !math.IsInf(n, 0) {
		return n
	}
	return s
}
