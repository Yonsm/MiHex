// Package ding 通过钉钉群机器人发送通知，支持加签校验。
package ding

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"fmt"
	"log/slog"
	"net/url"
	"time"

	"github.com/Yonsm/MiHex/core"
)

var (
	token  string
	secret string
)

func init() {
	core.RegisterModule("ding", setup)
}

func setup(sec core.Section) ([]core.Object, error) {
	conf := sec.Map()
	token = core.Str(conf["token"])
	secret = core.Str(conf["secret"])
	mod := core.NewMod("DING")
	mod.Methods["service"] = true
	mod.CallFn = func(ctx context.Context, _ string, args []any) (any, error) {
		return send(ctx, args)
	}
	return []core.Object{mod}, nil
}

func send(ctx context.Context, args []any) (any, error) {
	if len(args) == 0 {
		return nil, nil
	}
	u := "https://oapi.dingtalk.com/robot/send?access_token=" + token
	if secret != "" {
		ts := time.Now().UnixMilli()
		mac := hmac.New(sha256.New, []byte(secret))
		fmt.Fprintf(mac, "%d\n%s", ts, secret)
		sign := url.QueryEscape(base64.StdEncoding.EncodeToString(mac.Sum(nil)))
		u += fmt.Sprintf("&timestamp=%d&sign=%s", ts, sign)
	}
	body := map[string]any{"msgtype": "text", "text": map[string]any{"content": core.Str(args[len(args)-1])}}
	resp, err := core.Fetch(ctx, u, body, nil)
	if err != nil {
		return nil, err
	}
	if core.Int(resp["errcode"], -1) != 0 {
		slog.Error("钉钉发送失败", "resp", resp)
		return nil, nil
	}
	return resp, nil
}
