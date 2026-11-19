# MiHex

[中文](README.md) | **English**

[![PyPI](https://img.shields.io/pypi/v/mihex)](https://pypi.org/project/mihex/)
[![License](https://img.shields.io/badge/license-GPL--3.0-blue)](LICENSE)

**Version**: v1.2.4 (frozen, [Changelog](../CHANGELOG.md))

> This directory holds the frozen Python implementation (PyPI package `mihex`), receiving only critical fixes. The Go rewrite (v1.5) is the mainline — see [README](../README.md).

MIni Home EXecutor — a lightweight smart home management service for macOS, Linux, OpenWrt, and MiWiFi.

Unified device management via MQTT, MiIO, Broadlink and other protocols, with a built-in pure-Python HTTP/WebSocket server providing a real-time dashboard and device tracking.

![Dashboard](mihex/www/dash.png)

## Features

- **Web Dashboard** — single-page UI with WebSocket real-time state push
- **MQTT** — subscribe to topics for sensor data, publish commands to control devices
- **MiIO** — integrate Xiaomi devices, support status query and control; auto-discover and configure undeclared devices via MIoT specs (auto-discovery is not thoroughly tested). Prefers LAN-local control (auto-derived from the cloud device list's `localip`/`token`, or a manual `host`/`token` override per device) and falls back to the cloud transparently on any failure; the local protocol is a from-scratch implementation not yet verified against real hardware — test on your own devices before relying on it
- **Broadlink** — IR/RF remote control for non-smart devices
- **Weather** — Caiyun Weather API: temperature, humidity, PM2.5, forecasts
- **Ping** — ICMP probe for device online status, with Wake-on-LAN support
- **Shell** — SSH/Shell command control for remote devices
- **Modbus** — Modbus TCP devices (e.g. central AC) via coil/register read-write (untested)
- **Saswell** — Saswell underfloor heating thermostat status and control (untested)
- **DingTalk** — DingTalk bot message push
- **Voice Gateway** — Xiaomi LAN gateway voice command forwarding
- **Device Tracking** — periodic polling; auto fast-mode when WebSocket connected
- **Scenes/Automation** — fire a set of actions on a time (cron-style) or device state-change trigger; actions reuse the existing entity call format, no new syntax to learn

## Installation

```sh
pip install mihex
```

Optional dependencies:

```sh
pip install mihex[broadlink]  # Broadlink IR/RF
pip install mihex[modbus]     # Modbus TCP
pip install mihex[all]        # All optional dependencies
```

<details>
<summary>Other installation methods</summary>

**From source**:

```sh
git clone https://github.com/Yonsm/MiHex.git
cd MiHex
pip install -e .
```

**OpenWrt / MiWiFi routers**:

```sh
sh setup.sh
```

**Without pip**:

```sh
pip3 install -r requirements.txt
cp mihex/mihex.yaml mihex.yaml
```

</details>

## Usage

```sh
mihex                         # mihex.yaml in current directory
mihex -c /path/to/mihex.yaml  # specify config
python -m mihex               # equivalent

mihex -v5                     # verbose logging (-v0~5)
mihex -V5                     # log to /tmp/mihex.log

mihex MQTT device/topic "msg" # single command
mihex DING "notification text"
mihex device_name MiIO_command
```

After launch, visit `http://<IP>:<port>/` to open the dashboard.

## Configuration

Copy `mihex/mihex.yaml` to `mihex.yaml` and edit as needed. Configuration sections:

| Section | Description |
|---------|-------------|
| `server` | Port, token auth, HTTPS cert, quick links |
| `mqtt` | Broker address and weather/sensor/light/switch devices |
| `miio` | Xiaomi account and device type mapping; `auto: true` discovers every device, `auto: <home name>` scopes to one home, `auto: [name, ...]` discovers only the listed devices |
| `broadlink` | Broadlink devices and IR remote codes |
| `ding` | DingTalk bot token and secret |
| `weather` | Caiyun Weather coordinates, optional `api_key` |
| `gate` | Xiaomi LAN gateway |
| `ping` | Device IPs and optional MAC (WoL) |
| `shell` | SSH/Shell command control |
| `modbus` | Modbus TCP host address and coil/register mapping |
| `saswell` | Saswell thermostat address and device ID |
| `scene` | Scene/automation rules: time (`at`/`cron`) or device state-change (`entity`+`attr`+`below`/`above`/`equals`/`not_equals`/`changed`) triggers; `then` reuses the same `[EntityName, Method, arg...]` call format as the dashboard/CLI |
| `runner` | Command templates, `{{}}` for dynamic variables |
| `tracker` | Polling intervals (`fast` 60s / `slow` 3600s)|

Auth: when `server.token` is set, access requires HTTP Basic Authentication (password = token, any username).

## Project Structure

```
mihex/
├── __init__.py         # Version
├── __main__.py         # CLI entry
├── entity.py           # Entity base class & async utilities
├── loader.py           # Dynamic module loader
├── server.py           # HTTP/WebSocket server
├── runner.py           # Command dispatch & template expansion
├── tracker.py          # Device state polling
├── modules/            # Feature modules
│   ├── mqtt.py
│   ├── miio/
│   │   ├── discover.py  # Auto-discovery (MIoT spec parsing)
│   │   └── viomi_washer/
│   ├── broadlink.py
│   ├── ding.py
│   ├── weather.py
│   ├── ping.py
│   ├── shell.py
│   ├── gate.py
│   ├── modbus.py
│   ├── saswell.py
│   └── codes/           # IR remote codes
├── www/                # Dashboard frontend
```

## Architecture

```
                                               ┌────────────┐
                                               │ mihex.yaml │
                                               └────────────┘
                                                     │
                                                     ▼
                                  ┌────────┐   ┌────────────────────────┐
                                  │ loader │──▶│ _OBJS                  │
                                  └────────┘   │ global object registry │
                                       │       └────────────────────────┘
                                       │
               ┌───────────────────────┴─────┬─────────────────────────┬─────────────────────────┐
               ▼                             ▼                         ▼                         ▼
┌─────────────────────────────┐    ┌──────────────────┐    ┌──────────────────────┐    ┌───────────────────┐
│ modules                     │    │ server           │    │ runner               │    │ tracker           │
│ mqtt / miio / broadlink     │    │ HTTP / WebSocket │    │ Command dispatch     │    │ State polling     │
│ weather / ping / shell      │    └──────────────────┘    │ Template expand {{}} │    │ fast / slow speed │
│ ding / gate / modbus        │                            │ -> dispatch via NAME │    │ -> poll _OBJS     │
│ saswell ...                 │                            └──────────────────────┘    └───────────────────┘
│ -> register Entity to _OBJS │
└─────────────────────────────┘
                                            │ ▲
                                            │ │ WebSocket action
                                            │ │ send_state() push
                                            ▼ │

                                  ┌─────────────────────┐
                                  │ Dashboard (Browser) │
                                  │ dash.htm / css / js │
                                  └─────────────────────┘
```

`loader` loads config sections from `mihex.yaml` in order: feature modules first (`mqtt`/`miio`/`broadlink`/… or same-named scripts under `customs/`) — each module's `setup(conf)` returns `Entity` instances that get registered into the global `_OBJS` list — followed by the three built-in modules `server`, `runner`, and `tracker`; `server` talks to the browser dashboard bidirectionally over WebSocket.

- **Loading**: `_OBJS` is the global registry mixing module objects and `Entity` instances; `runner.dispatch()` looks up the target by its `NAME` attribute
- **Polling**: `tracker` iterates every object in `_OBJS` that has an `update()` method, pushing state changes via `server.send_state()` to all WebSocket clients. Interval is `slow` (3600s default) with no client connected, and automatically switches to `fast` (60s default) once one connects
- **Command dispatch**: the browser sends `[NAME, method, ...args]` over WebSocket; `server` forwards it to `runner.dispatch()`, which routes it to the matching `Entity` method (`on_turn`/`on_mode`/`on_tune`/`service`). `runner.expand()` additionally supports `{{}}` template syntax to embed device state or command results in text
- **Async model**: everything runs on a single asyncio event loop; blocking third-party calls (ping, SSH, Modbus, Broadlink, etc.) are offloaded to a thread pool via `entity.runin()` so the event loop never blocks
- **Dashboard**: `www/dash.*` is a dependency-free single-page app. On first connect it fetches the full `states` snapshot to render; afterwards it only receives incremental pushes. User clicks/taps trigger a WebSocket call, and the tile shows a "calling" state until the reply arrives

## Custom Module

Create `customs/<section_name>.py` next to `mihex.yaml`, implementing `setup(conf)` (a custom file takes precedence over a built-in module with the same name):

```python
from mihex.entity import Entity

async def setup(conf):
    return [MyEntity(c) for c in conf.get('items', [])]

class MyEntity(Entity):
    async def update(self):
        """Called by tracker; return True if state changed"""
        pass

    async def on_turn(self, arg):
        """Dashboard switch; arg is 0/1"""
        await super().on_turn(arg)
```

Add a same-named config section in `mihex.yaml` and it will be auto-loaded.

## Security Notes

- `server.token` auth is based on HTTP Basic / URL query parameter, and is transmitted in plain HTTP unless `key`/`cert` are configured. It's designed for use on a trusted LAN; if you need remote access, configure `key`/`cert` to enable HTTPS, or put it behind an existing VPN/reverse proxy.
- `mihex.yaml` (account passwords, tokens, etc.), the runtime-generated `.mi.token` (Xiaomi cloud session cache), and `.miot_specs.json` (local MIoT spec cache — holds no sensitive data, safe to delete to force a refresh) are already listed in `.gitignore` — avoid committing them manually.

## Acknowledgements

- [MiService](https://github.com/Yonsm/MiService) — Xiaomi MiIO protocol
- [broadlink](https://github.com/mjg59/python-broadlink) — Broadlink device control
- [paho-mqtt](https://github.com/eclipse/paho.mqtt.python) — MQTT client
- [Caiyun Weather](https://caiyunapp.com/) — Weather data

## License

[GPL-3.0](LICENSE)