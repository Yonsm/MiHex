# MiHex

[中文](README.md) | **English**

[![License](https://img.shields.io/badge/license-GPL--3.0-blue)](LICENSE)

**Version**: v1.5.0 ([Changelog](CHANGELOG.md))

MIni Home EXecutor — a lightweight smart home management service. Single-binary Go implementation with a built-in HTTP/WebSocket server and real-time dashboard, running on macOS, Linux, OpenWrt, and MiWiFi.

Unified device management via MQTT, MiIO, Broadlink and other protocols, with device tracking and scene automation.

![Dashboard](python/mihex/www/dash.png)

## Features

- **Web Dashboard** — single-page UI with WebSocket real-time state push
- **MQTT** — subscribe to topics for sensor data, publish commands to control devices
- **MiIO** — integrate Xiaomi devices, with status query and control plus Mi cloud OTP login; prefers LAN-local control (auto-derived from the cloud device list's `localip`/`token`, or a manual `host`/`token` override) and falls back to the cloud transparently; auto-discovers and configures undeclared devices via MIoT specs (experimental)
- **Broadlink** — IR/RF remote control for non-smart devices, plus Broadlink smart plugs
- **Weather** — Caiyun Weather API: temperature, humidity, PM2.5, forecasts
- **Ping** — ICMP probe for device online status, with Wake-on-LAN and SSH remote shutdown
- **Shell** — read/write device state through local Shell/SSH commands
- **DingTalk** — DingTalk bot message push
- **Voice Gateway** — Xiaomi LAN gateway voice command forwarding
- **Device Tracking** — periodic polling; auto fast-mode when WebSocket connected
- **Scenes/Automation** — fire a set of actions on a time (cron-style) or device state-change trigger; actions reuse the existing entity call format, no new syntax to learn

> **This is the Go rewrite (v1.5)**, the mainline. `modbus`, `saswell` and `customs/` are Python-only for now.
> The frozen Python version (v1.2.4) lives in [python/](python/).

## Installation

Build from source (requires Go 1.24+):

```sh
git clone https://github.com/Yonsm/MiHex.git
cd MiHex
go build -o mihex ./cmd/mihex
```

Or install directly: `go install github.com/Yonsm/MiHex/cmd/mihex@latest` (binary name `mihex`).

Cross-compile for routers and similar platforms:

```sh
GOOS=linux GOARCH=arm64 go build -o mihex ./cmd/mihex
GOOS=linux GOARCH=mipsle GOMIPS=softfloat go build -o mihex ./cmd/mihex
```

## Usage

```sh
mihex                          # auto-discover config (see Configuration)
mihex -c /path/to/mihex.yml   # specify config

mihex -v5                      # verbose logging (-v0~5)
mihex -V5                      # log to /tmp/mihex.log

mihex login                    # interactive Mi cloud auth, caches token
mihex MQTT device/topic "msg" # one-shot command
mihex DING "text"
mihex DeviceName MiIOCommand
```

After startup, open `http://<IP>:<port>/` for the dashboard.

## Configuration

The repo ships a ready-to-run [`mihex.yml`](mihex.yml) sample. Config is resolved in this order (first match wins):

1. the file passed with `-c`
2. `mihex.yml` in the current directory
3. `~/.mihex.yml`
4. `mihex.yml` next to the executable

Keep personal settings (accounts, tokens) in `~/.mihex.yml` or pass `-c`, so the repo sample stays free of secrets. Sections:

| Section | Description |
|---------|-------------|
| `server` | `bind`, `port` (default 88), token auth, HTTPS cert, advertised host, and arbitrary quick-link groups |
| `mqtt` | Broker address and weather/sensor/light/switch devices |
| `miio` | Mi account and device type mapping; `auto: true` discovers all unconfigured devices, `auto: HomeName` only that home, `auto: [DeviceName, ...]` only the listed devices |
| `broadlink` | Broadlink devices and IR codes (built-in `codes/` files can be referenced by name) |
| `ding` | DingTalk bot token and secret |
| `weather` | Caiyun coordinates, optional `api_key` |
| `gate` | Xiaomi LAN gateway |
| `ping` | Device IP with optional MAC (WoL) and SSH shutdown |
| `shell` | Command-based state: `stat`/`toon`/`toff`/`exec` |
| `scene` | Scene/automation rules, see below |
| `runner` | Command templates with `{{}}` variable expansion |
| `tracker` | Polling intervals (`fast` 60s / `slow` 3600s) |

Auth: with `server.token` set, access requires HTTP Basic Authentication (password = token, any username), or the token as a `?<token>` query parameter.

Scene example:

```yaml
scene:
  - name: Turn on heating when cold
    when: {entity: LivingRoomTemp, attr: STAT, below: 18}
    then: [[LivingRoomHeater, on_turn, 1]]

  - name: Open fresh-air unit at 8am
    when: {at: "08:00"}          # or {at: ["08:00", "20:00"]}, {cron: "0 8 * * *"}
    then: [[LivingRoomFreshAir, on_turn, 1]]
```

## Project Layout

```
.
├── cmd/mihex/main.go         # entry: flags, logging, one-shot commands
├── core/                    # config, entities and registry, module loading, dispatch, polling
├── server/                  # HTTP/WebSocket server and dashboard (gzip assets embedded)
├── modules/                 # mqtt / miio / broadlink / weather / ping / shell / gate / ding / scene
├── python/                  # Python implementation (frozen)
└── mihex.yml
```

Dashboard assets live in `python/mihex/www/`; `go generate ./server` gzips and embeds them into the binary — rerun it and rebuild after frontend changes.

The MiIO protocol layer depends on [MisGo](https://github.com/Yonsm/misgo) (`github.com/Yonsm/misgo v0.1.1`).

## Architecture

```text
                                             ┌───────────┐
                                             │ mihex.yml │
                                             └───────────┘
                                                   │
                                                   ▼
                                  ┌─────────────────────────────────┐
                                  │             main.go             │
                                  │   flags · logging · one-shot    │
                                  └─────────────────────────────────┘
                                                   │
                                                   ▼
┌─────────────────────────────────────────────────────────────────────────────────────────────────────┐
│                                                core                                                 │
│                            config · registry · runner · tracker · finale                            │
└───────────┬─────────────────────────┬─────────────────────────┬─────────────────────────┬───────────┘
            │                         │                         │                         │
            ▼                         ▼                         ▼                         ▼
┌───────────────────────┐ ┌───────────────────────┐ ┌───────────────────────┐ ┌───────────────────────┐
│modules/               │ │server/                │ │runner                 │ │tracker                │
│mqtt · miio · broadlink│ │HTTP · WebSocket       │ │command dispatch       │ │fast / slow polling    │
│weather · ping · shell │ │token auth · HTTPS     │ │{{}} expansion         │ │state push on change   │
│gate · ding · scene    │ │Hub (Broadcaster)      │ │→ dispatch by NAME     │ └───────────────────────┘
│→ register objects     │ └───────────────────────┘ └───────────────────────┘
└───────────────────────┘                          │ ▲
                                                   │ │ WebSocket action
                                                   │ │ state push
                                                   ▼ │
                                       ┌───────────────────────┐
                                       │  Dashboard (Browser)  │
                                       └───────────────────────┘
```

- **Loading**: `main.go` loads the config and calls `core.LoadModules`, which loads modules in the order their sections appear in `mihex.yml`; objects returned by each module's `setup()` are registered into the `core` registry and looked up by `NAME`
- **Polling**: `tracker` walks objects implementing `Poller` and pushes state changes to WebSocket clients through `core.Emit`; it runs at `fast` while a client is connected and falls back to `slow` otherwise
- **Commands**: both the dashboard and the CLI dispatch `[NAME, method, ...args]` through `runner`; `{{...}}` templates expand to entity state or call results
- **Services**: `server` serves static assets plus Token/Basic auth and WebSocket; `finale` runs shutdown callbacks in reverse registration order

## Security

- `server.token` auth is based on HTTP Basic / URL query parameters and travels over plain HTTP unless `cert`/`key` are configured. It is designed for a trusted LAN; for public exposure, enable HTTPS via `cert`/`key` or put it behind a VPN/reverse proxy.
- `mihex.yml` (credentials and tokens) and the runtime-generated `.mi.token` (Mi cloud session cache) and `.miot_specs.json` (MIoT spec cache, non-sensitive, safe to delete) are listed in `.gitignore` — avoid committing them manually.

## Credits

- [MisGo](https://github.com/Yonsm/misgo) — MiIO protocol library (Go implementation of MiService), backing MiHex's miio layer
- [MiService](https://github.com/Yonsm/MiService) — MiIO protocol reference (Python)
- [python-broadlink](https://github.com/mjg59/python-broadlink) — Broadlink device control
- [paho.mqtt.golang](https://github.com/eclipse/paho.mqtt.golang) — MQTT client
- [Caiyun Weather](https://caiyunapp.com/) — weather data

## License

[GPL-3.0](LICENSE)
