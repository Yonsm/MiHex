package miio

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"slices"
	"strings"

	"github.com/Yonsm/misgo/spec"
)

// specStore 封装 MisGo 的 MIoT spec 客户端，保留 MiHex 的自动发现解析。
type specStore struct {
	client *spec.Client
}

func newSpecs() *specStore {
	return &specStore{client: spec.New(
		spec.WithIndexCache(spec.DefaultIndexCachePath()),
		spec.WithInstanceCache(".miot_specs.json"),
	)}
}

func (s *specStore) spec(ctx context.Context, model string) (json.RawMessage, error) {
	return s.client.Raw(ctx, model)
}

func (s *specStore) command(ctx context.Context, model, format string) (any, error) {
	return s.client.Spec(ctx, model, format)
}

type specInstance struct {
	Type     string
	Services []specService
}

type specService struct {
	IID               int
	Type, Description string
	Properties        []specProperty
	Actions           []specAction
}

type specProperty struct {
	IID                             int
	Type, Description, Format, Unit string
	Access                          []string
	ValueList                       []specValue `json:"value-list"`
	ValueRange                      []any       `json:"value-range"`
}

type specAction struct {
	IID         int
	Description string
	In, Out     []int
}

type specValue struct {
	Description string
	Value       any
}

var specServiceTypes = map[string]string{
	"light": "light", "outlet": "switch", "switch": "switch", "fan": "fan",
	"fan-control": "fan", "ceiling-fan": "fan", "air-purifier": "purifier",
	"air-fresh-ventilator": "purifier", "air-freshener": "purifier",
	"air-condition-outlet": "climate", "air-conditioner": "climate", "air-condition": "climate",
	"heater": "climate", "ptc-bath-heater": "fan", "water-heater": "climate",
	"thermostat": "thermostat", "vacuum": "vacuum", "washer": "washer", "curtain": "cover",
	"window-opener": "cover", "camera": "camera", "camera-control": "camera",
	"humidifier": "humidifier", "dehumidifier": "humidifier", "magnet-sensor": "binary",
	"motion-sensor": "binary", "occupancy-sensor": "binary", "submersion-sensor": "binary",
	"smoke-sensor": "binary", "electronic-valve": "switch", "television": "switch",
	"air-fryer": "switch", "microwave-oven": "switch",
}

var specModelTypes = []struct{ prefix, typ string }{
	{"yeelink.light", "light"}, {"zhimi.airpurifier", "purifier"}, {"zhimi.fan", "fan"},
	{"zhimi.humidifier", "humidifier"}, {"chuangmi.plug", "switch"}, {"chuangmi.camera", "camera"},
	{"roborock.vacuum", "vacuum"}, {"xiaomi.aircondition", "climate"}, {"xiaomi.airp", "purifier"},
	{"xiaomi.fan", "fan"}, {"xiaomi.humidifier", "humidifier"}, {"cuco.plug", "switch"},
	{"qmi.powerstrip", "switch"}, {"zimi.powerstrip", "switch"}, {"nwt.derh", "humidifier"},
	{"deerma.humidifier", "humidifier"}, {"leshow.fan", "fan"}, {"shhf.light", "light"},
}

var specAttrs = map[string]string{
	"on": "ison", "is-on": "ison", "valve-switch": "ison", "mode": "mode",
	"fan-level": "tune", "brightness": "tune", "target-temperature": "tune",
	"color-temperature": "tune", "wind-level": "tune", "speed-level": "tune", "volume": "tune",
}

var specStats = []struct{ name, unit string }{
	{"tds-out", "tds"}, {"co2-density", "ppm"}, {"temperature", "℃"}, {"relative-humidity", "%"},
	{"pm2.5-density", "aqi"}, {"aqi", "aqi"}, {"illumination", "lx"},
}

var specUnits = map[string]string{
	"celsius": "℃", "fahrenheit": "℉", "percentage": "%", "μg/m3": "aqi",
	"μg/m³": "aqi", "lux": "lx", "ppm": "ppm",
}

var specBinaryIcons = map[string]string{
	"magnet-sensor": "door", "motion-sensor": "motion-sensor", "occupancy-sensor": "radar",
	"submersion-sensor": "water", "smoke-sensor": "smoke-detector",
}

var specTypeIcons = map[string]string{
	"router": "router-wireless", "television": "television", "electronic-valve": "fuse",
	"air-fryer": "pot-steam", "microwave-oven": "microwave",
}

var specDescOrder = map[string]int{"pm2.5-density": 1, "temperature": 2, "relative-humidity": 3}

func specURNName(urn string) string {
	parts := strings.SplitN(urn, ":", 5)
	if len(parts) > 3 {
		return parts[3]
	}
	return ""
}

func parseSpec(services []specService) (map[string]any, string) {
	var typ string
	for _, svc := range services {
		if strings.EqualFold(svc.Description, "device information") {
			continue
		}
		if t := specServiceTypes[specURNName(svc.Type)]; t != "" {
			typ = t
			if t != "light" {
				break
			}
		}
	}

	type stat struct {
		priority  int
		iid, unit string
	}
	var stats []stat
	conf := make(map[string]any)
	for _, svc := range services {
		name := specURNName(svc.Type)
		svcType := specServiceTypes[name]
		if strings.EqualFold(svc.Description, "device information") || name == "indicator-light" ||
			typ != "" && typ != "light" && svcType == "light" {
			continue
		}
		if icon := specTypeIcons[name]; icon != "" && conf["ICON"] == nil {
			conf["ICON"] = icon
		}

		for _, prop := range svc.Properties {
			urn := specURNName(prop.Type)
			read := slices.Contains(prop.Access, "read")
			write := slices.Contains(prop.Access, "write")
			if svcType == "binary" && conf["ison"] == nil && read && (prop.Format == "bool" || urn == "occupancy-status") {
				conf["ison"] = fmt.Sprintf("%d-%d", svc.IID, prop.IID)
				icon := specBinaryIcons[name]
				if icon == "" {
					icon = "bullseye"
				}
				conf["ICON"] = icon
				continue
			}
			if attr := specAttrs[urn]; write && attr != "" && conf[attr] == nil {
				conf[attr] = fmt.Sprintf("%d-%d", svc.IID, prop.IID)
				switch attr {
				case "ison":
					for _, v := range prop.ValueList {
						switch strings.ToLower(v.Description) {
						case "on", "开启", "open", "true":
							conf["voon"] = v.Value
						case "off", "关闭", "close", "false":
							conf["voff"] = v.Value
						}
					}
				case "mode":
					if len(prop.ValueList) > 0 {
						mods := make(map[string]any)
						for _, v := range prop.ValueList {
							if v.Description != "" {
								mods[v.Description] = v.Value
							}
						}
						conf["MODS"] = mods
					}
				case "tune":
					if vr := prop.ValueRange; len(vr) >= 2 {
						conf["tune_min"], conf["tune_max"] = vr[0], vr[1]
						if len(vr) > 2 && vr[2] != float64(1) && vr[2] != true {
							conf["tune_step"] = vr[2]
						}
					}
				}
			}
			if read && !write {
				for i, candidate := range specStats {
					if candidate.name != urn {
						continue
					}
					unit := specUnits[strings.ToLower(prop.Unit)]
					if unit == "" {
						unit = candidate.unit
					}
					stats = append(stats, stat{i, fmt.Sprintf("%d-%d", svc.IID, prop.IID), unit})
					break
				}
			}
		}
	}

	if typ != "binary" && len(stats) > 0 {
		primary := slices.MinFunc(stats, func(a, b stat) int { return a.priority - b.priority })
		conf["stat"], conf["UNIT"] = primary.iid, primary.unit
	}
	priority := func(s stat) int {
		if n := specDescOrder[specStats[s.priority].name]; n != 0 {
			return n
		}
		return len(specDescOrder) + 1
	}
	slices.SortStableFunc(stats, func(a, b stat) int { return priority(a) - priority(b) })
	var extras []any
	for _, s := range stats {
		if s.unit != conf["UNIT"] {
			extras = append(extras, []any{s.iid, s.unit})
		}
	}
	if len(extras) > 0 {
		conf["extra_stat"] = extras
	}
	return conf, typ
}

func (s *specStore) autoConfig(ctx context.Context, name, model string) map[string]any {
	conf := make(map[string]any)
	var typ string
	raw, err := s.spec(ctx, model)
	var instance specInstance
	if err == nil {
		err = json.Unmarshal(raw, &instance)
	}
	if err != nil {
		slog.Warn("获取 MIoT spec 失败", "设备", name, "model", model, "err", err)
	} else if instance.Services != nil {
		deviceType := specURNName(instance.Type)
		switch deviceType {
		case "repeater", "speaker", "microphone", "remote-control", "rangefinder":
			return nil
		}
		conf, typ = parseSpec(instance.Services)
		if typ == "" {
			typ = specServiceTypes[deviceType]
		}
	}
	if typ == "" {
		for _, entry := range specModelTypes {
			if strings.HasPrefix(model, entry.prefix) {
				typ = entry.typ
				break
			}
		}
	}
	if typ == "" {
		typ = "sensor"
		if conf["ison"] != nil {
			typ = "switch"
		}
	}
	conf["TYPE"] = typ
	slog.Info("自动发现 MIoT 设备", "设备", name, "model", model, "TYPE", typ,
		"ison", conf["ison"], "mode", conf["mode"], "tune", conf["tune"], "stat", conf["stat"])
	return conf
}
