// Package weather 从彩云天气获取实况与预报，缓存 1 小时。
package weather

import (
	"context"
	"fmt"
	"math"
	"math/rand"
	"sync"
	"time"

	"github.com/Yonsm/MiHex/core"
)

const defaultKey = "UR8ASaplvIwavDfR"

var icons = map[string]string{
	"CLEAR_NIGHT":         "night",
	"CLOUDY":              "cloudy",
	"FOG":                 "fog",
	"HAIL":                "hail",
	"PARTLY_CLOUDY_DAY":   "partly-cloudy",
	"PARTLY_CLOUDY_NIGHT": "night-partly-cloudy",
	"RAIN":                "rainy",
	"SNOW":                "snowy",
	"SLEET":               "snowy-rainy",
	"CLEAR_DAY":           "sunny",
	"WIND":                "windy",
	"HAZE":                "windy-variant",
}

var apiKey = defaultKey

// response 只声明用到的字段，避免把整个预报 JSON 解析进 map。
type response struct {
	Status     string `json:"status"`
	ServerTime int64  `json:"server_time"`
	Result     struct {
		Realtime struct {
			Skycon      string  `json:"skycon"`
			Temperature float64 `json:"temperature"`
			Humidity    float64 `json:"humidity"`
			Pm25        float64 `json:"pm25"`
		} `json:"realtime"`
		ForecastKeypoint string `json:"forecast_keypoint"`
	} `json:"result"`
}

func init() {
	core.RegisterModule("weather", setup)
}

func setup(sec core.Section) ([]core.Object, error) {
	conf := sec.Map()
	if k := core.Str(conf["api_key"]); k != "" {
		apiKey = k
	}
	e := &Entity{Entity: core.NewEntity(conf, "weather")}
	e.SetName("天气")
	e.Set("ICON", "weather-cloudy-alert")
	e.Set("LINK", "https://h5.caiyunapp.com/h5")
	return []core.Object{e}, nil
}

// Entity 是配置坐标处的天气实体。
type Entity struct {
	*core.Entity
	mu    sync.Mutex
	stamp int64
}

func (e *Entity) Update(ctx context.Context) (bool, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if stat, ok := e.Get("STAT"); ok && stat != nil && time.Now().Unix()-e.stamp < 3600 {
		return false, nil
	}
	resp, err := fetch(ctx, e.Str("location"))
	if err != nil {
		return false, err
	}
	rt := resp.Result.Realtime
	icon := "weather-cloudy-alert"
	if name, ok := icons[rt.Skycon]; ok {
		icon = "weather-" + name
	}
	desc := fmt.Sprintf("%.0f℃ %d%% %vμg %v",
		math.Round(rt.Temperature), int(rt.Humidity*100), rt.Pm25, resp.Result.ForecastKeypoint)

	changed := false
	if old, _ := e.Get("ICON"); old != icon {
		e.Set("ICON", icon)
		changed = true
	}
	if old, _ := e.Get("STAT"); old != 1 {
		e.Set("STAT", 1)
		changed = true
	}
	if old, _ := e.Get("DESC"); old != desc {
		e.Set("DESC", desc)
		changed = true
	}
	if resp.ServerTime > 0 {
		e.stamp = resp.ServerTime
	} else {
		e.stamp = time.Now().Unix()
	}
	return changed, nil
}

func (e *Entity) Supports(method string) bool { return method == "service" }

func (e *Entity) Call(ctx context.Context, method string, args []any) (any, error) {
	if method != "service" {
		return e.Entity.Call(ctx, method, args)
	}
	if _, err := e.Update(ctx); err != nil {
		return nil, err
	}
	desc, _ := e.Get("DESC")
	return desc, nil
}

func fetch(ctx context.Context, location string) (*response, error) {
	u := fmt.Sprintf("https://api.caiyunapp.com/v2/%s/%s/weather?lang=zh_CN&tzshift=28800&timestamp=%d&hourlysteps=384&dailysteps=16&alert=true&device_id=%012X",
		apiKey, location, time.Now().Unix(), rand.Int63n(1<<48))
	var resp response
	err := core.FetchInto(ctx, u, nil, map[string]string{
		"User-Agent":      "ColorfulCloudsPro/3.2.2 (iPhone; iOS 11.3; Scale/3.00)",
		"Accept":          "application/json",
		"Accept-Language": "zh-Hans-CN;q=1",
	}, &resp)
	if err != nil {
		return nil, err
	}
	if resp.Status != "ok" {
		return nil, fmt.Errorf("天气接口返回异常: %s", resp.Status)
	}
	return &resp, nil
}
