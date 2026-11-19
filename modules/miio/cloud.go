package miio

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"sort"
	"strings"

	"github.com/Yonsm/misgo/account"
	misgo "github.com/Yonsm/misgo/miio"

	"github.com/Yonsm/MiHex/core"
)

var errOTPRequired = account.ErrOTPRequired

// cloud 是 MisGo 账号与云服务的薄适配层。
type cloud struct {
	account *account.MiAccount
	service *misgo.Service
}

func newCloud(user, password string, otp account.OTPCallback) *cloud {
	options := []account.Option{account.WithTokenPath(account.DefaultTokenPath)}
	if otp != nil {
		options = append(options, account.WithOTPCallback(otp))
	}
	acct := account.New(user, password, options...)
	return &cloud{account: acct, service: misgo.New(acct, "")}
}

func (c *cloud) login(ctx context.Context) error { return c.account.Login(ctx, misgo.SID) }

// request 把米家云结果解码到 result；result 为 *json.RawMessage 时保留原始 JSON。
func (c *cloud) request(ctx context.Context, path string, data any, result any) error {
	if !strings.HasPrefix(path, "/") || strings.HasPrefix(path, "//") {
		return errors.New("米家云请求路径必须以单个 / 开头")
	}
	value, err := c.service.MiIORequest(ctx, path, data)
	if err != nil {
		return err
	}
	raw, err := json.Marshal(value)
	if err != nil {
		return fmt.Errorf("无法编码米家云响应：%w", err)
	}
	if out, ok := result.(*json.RawMessage); ok {
		*out = raw
		return nil
	}
	return decodeJSON(raw, result)
}

func decodeJSON(data []byte, dst any) error {
	d := json.NewDecoder(bytes.NewReader(data))
	d.UseNumber()
	if err := d.Decode(dst); err != nil {
		return err
	}
	if d.Decode(new(json.RawMessage)) != io.EOF {
		return errors.New("JSON 末尾有多余数据")
	}
	return nil
}

func cloudString(v any) string {
	switch v := v.(type) {
	case string:
		return v
	case json.Number:
		return string(v)
	default:
		return ""
	}
}

type cloudDevice struct {
	DID, Name, Model, Host, Token string
	Members                       []string
}

type cloudHome struct {
	Name string
	DIDs []string
}

func (c *cloud) rawDeviceList(ctx context.Context, getVirtualModel bool, getHuamiDevices int) ([]map[string]any, error) {
	var out struct{ List []map[string]any }
	err := c.request(ctx, "/home/device_list", map[string]any{"getVirtualModel": getVirtualModel, "getHuamiDevices": getHuamiDevices}, &out)
	return out.List, err
}

func (c *cloud) deviceList(ctx context.Context) ([]cloudDevice, error) {
	raw, err := c.rawDeviceList(ctx, false, 0)
	if err != nil {
		return nil, err
	}
	out := make([]cloudDevice, 0, len(raw))
	for _, d := range raw {
		v := cloudDevice{
			DID: cloudString(d["did"]), Name: cloudString(d["name"]), Model: cloudString(d["model"]),
			Host: cloudString(d["localip"]), Token: cloudString(d["token"]),
		}
		extra, _ := d["extra"].(map[string]any)
		list, _ := extra["member_list"].(map[string]any)
		switch members := list["member_ship"].(type) {
		case map[string]any:
			for did := range members {
				v.Members = append(v.Members, did)
			}
			sort.Strings(v.Members)
		case []any:
			v.Members = cloudDIDs(nil, members)
		}
		out = append(out, v)
	}
	return out, nil
}

func (c *cloud) rawHomeList(ctx context.Context) ([]map[string]any, error) {
	var raw json.RawMessage
	if err := c.request(ctx, "/homeroom/gethome", struct{}{}, &raw); err != nil {
		return nil, err
	}
	var homes []map[string]any
	if b := bytes.TrimSpace(raw); len(b) > 0 && b[0] == '[' {
		err := decodeJSON(raw, &homes)
		return homes, err
	}
	var out struct{ HomeList, List []map[string]any }
	if err := decodeJSON(raw, &out); err != nil {
		return nil, err
	}
	if len(out.HomeList) != 0 {
		return out.HomeList, nil
	}
	return out.List, nil
}

func (c *cloud) homeList(ctx context.Context) ([]cloudHome, error) {
	raw, err := c.rawHomeList(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]cloudHome, 0, len(raw))
	for _, h := range raw {
		v := cloudHome{Name: cloudString(h["name"])}
		rooms, ok := h["roomlist"].([]any)
		if !ok {
			rooms, _ = h["rooms"].([]any)
		}
		for _, r := range rooms {
			room, _ := r.(map[string]any)
			dids, ok := room["dids"].([]any)
			if !ok {
				dids, _ = room["device_list"].([]any)
			}
			v.DIDs = cloudDIDs(v.DIDs, dids)
		}
		out = append(out, v)
	}
	return out, nil
}

func cloudDIDs(out []string, items []any) []string {
	for _, item := range items {
		if did := cloudString(item); did != "" {
			out = append(out, did)
		}
	}
	return out
}

// Login 验证云端会话并缓存令牌，不加载其余模块。
func Login(ctx context.Context, cfg *core.Config, in io.Reader, out io.Writer) (int, error) {
	conf := core.Map(cfg.Values["miio"])
	user := core.Str(conf["user"])
	if user == "" {
		return 0, errors.New("配置缺少 miio.user")
	}
	c := newCloud(user, core.Str(conf["pass"]), otpPrompt(in, out))
	if err := c.login(ctx); err != nil {
		return 0, err
	}
	devices, err := c.deviceList(ctx)
	if err != nil {
		return 0, fmt.Errorf("已取得米家服务令牌，但未能读取设备列表：%w", err)
	}
	return len(devices), nil
}

func otpPrompt(in io.Reader, out io.Writer) account.OTPCallback {
	input := bufio.NewReader(in)
	return func(ctx context.Context, channel string) (string, error) {
		if _, err := fmt.Fprintf(out, "米家%s验证码：\n", channel); err != nil {
			return "", err
		}
		type line struct {
			text string
			err  error
		}
		result := make(chan line, 1)
		go func() { text, err := input.ReadString('\n'); result <- line{text, err} }()
		select {
		case <-ctx.Done():
			return "", ctx.Err()
		case r := <-result:
			if r.err != nil && !(r.err == io.EOF && r.text != "") {
				return "", errors.New("未收到米家验证码")
			}
			return strings.TrimSpace(r.text), nil
		}
	}
}
