# MiHex

**中文** | [English](README_en.md)

[![License](https://img.shields.io/badge/license-GPL--3.0-blue)](LICENSE)

**版本**：v1.5.0（[更新日志](CHANGELOG.md)）

MIni Home EXecutor — 轻量级智能家居管理服务。Go 实现的单二进制版本，内置 HTTP/WebSocket 服务器与实时仪表盘，运行于 macOS、Linux、OpenWrt、MiWiFi。

通过 MQTT、MiIO、Broadlink 等协议统一管理智能设备，并提供设备追踪与场景自动化。

![Dashboard](python/mihex/www/dash.png)

## 功能

- **Web 仪表盘** — 单页 UI，WebSocket 实时推送状态变更
- **MQTT** — 订阅主题获取传感器数据，发布指令控制设备
- **MiIO** — 接入米家设备，支持状态查询与控制、米家云 OTP 登录；优先局域网直连（从云端设备列表的 `localip`/`token` 自动获取，或手动指定 `host`/`token`），失败自动降级云端；可按 MIoT 规格自动发现并配置未手动声明的设备（实验性）
- **Broadlink** — 红外/射频遥控非智能设备，以及博联插座
- **天气** — 彩云天气 API：温湿度、PM2.5、天气预报
- **Ping** — ICMP 探测设备在线状态，支持 Wake-on-LAN 与 SSH 远程关机
- **Shell** — 通过本地 Shell/SSH 命令读写设备状态
- **钉钉** — 钉钉机器人推送消息
- **语音网关** — 米家局域网网关语音指令转发
- **设备追踪** — 定时轮询，WebSocket 连接时自动切换快速模式
- **场景/自动化** — 定时（cron 风格）或设备状态变化触发一组动作，动作复用现有的实体调用格式，无需额外语法

> **这是 Go 重写版（v1.5）**，仓库主开发线。`modbus`、`saswell` 与 `customs/` 暂仅在 Python 版提供。
> 已冻结的 Python 版（v1.2.4）见 [python/](python/)。

## 安装

从源码构建（需要 Go 1.24+）：

```sh
git clone https://github.com/Yonsm/MiHex.git
cd MiHex
go build -o mihex ./cmd/mihex
```

也可以直接安装：`go install github.com/Yonsm/MiHex/cmd/mihex@latest`（二进制名为 `mihex`）。

交叉编译到路由器等平台：

```sh
GOOS=linux GOARCH=arm64 go build -o mihex ./cmd/mihex
GOOS=linux GOARCH=mipsle GOMIPS=softfloat go build -o mihex ./cmd/mihex
```

## 使用

```sh
mihex                          # 自动查找配置（见「配置」）
mihex -c /path/to/mihex.yml   # 指定配置

mihex -v5                      # 详细日志（-v0~5）
mihex -V5                      # 日志写入 /tmp/mihex.log

mihex login                    # 交互验证米家云并缓存令牌
mihex MQTT device/topic "msg" # 单次命令
mihex DING "通知文本"
mihex 设备名称 MiIO命令
```

启动后访问 `http://<IP>:<port>/` 打开仪表盘。

## 配置

仓库自带一份可直接运行的 [`mihex.yml`](mihex.yml) 样板，配置按以下顺序查找，先找到者生效：

1. `-c` 指定的文件
2. 当前目录的 `mihex.yml`
3. `~/.mihex.yml`
4. 可执行文件所在目录的 `mihex.yml`

个人配置（账号、Token 等）建议放到 `~/.mihex.yml` 或用 `-c` 指定，避免把隐私写进仓库样板。配置段：

| 配置段 | 说明 |
|--------|------|
| `server` | `bind`、`port`（默认 88）、Token 鉴权、HTTPS 证书、对外展示主机名，以及任意快捷链接分组 |
| `mqtt` | Broker 地址及 weather/sensor/light/switch 设备 |
| `miio` | 米家账号及设备类型映射；`auto: true` 自动发现全部未配置设备，`auto: 家庭名称` 仅发现该家庭下设备，`auto: [设备名, ...]` 仅发现指定设备 |
| `broadlink` | Broadlink 设备及红外遥控码（内置 `codes/` 中的码表可按文件名引用） |
| `ding` | 钉钉机器人 Token 和 Secret |
| `weather` | 彩云天气坐标，可选 `api_key` |
| `gate` | 米家局域网网关 |
| `ping` | 设备 IP 及可选 MAC（WoL）、SSH 关机 |
| `shell` | 命令读写状态：`stat`/`toon`/`toff`/`exec` |
| `scene` | 场景/自动化规则，见下 |
| `runner` | 命令模板，`{{}}` 嵌入动态变量 |
| `tracker` | 轮询间隔（`fast` 60s / `slow` 3600s） |

鉴权：`server.token` 设置后访问需 HTTP Basic Authentication，密码填 token 值，用户名任意；也可用 `?<token>` 查询参数。

场景示例：

```yaml
scene:
  - name: 温度过低自动开地暖
    when: {entity: 客厅温度, attr: STAT, below: 18}
    then: [[客厅地暖, on_turn, 1]]

  - name: 每天 8 点开新风机
    when: {at: "08:00"}          # 或 {at: ["08:00", "20:00"]}、{cron: "0 8 * * *"}
    then: [[客厅新风机, on_turn, 1]]
```

## 项目结构

```
.
├── cmd/mihex/main.go         # 入口：参数、日志、一次性命令
├── core/                    # 配置、实体与注册表、模块加载、命令分发、轮询
├── server/                  # HTTP/WebSocket 与仪表盘（gzip 资源内嵌）
├── modules/                 # mqtt / miio / broadlink / weather / ping / shell / gate / ding / scene
├── python/                  # Python 版（已冻结）
└── mihex.yml
```

仪表盘前端资源位于 `python/mihex/www/`，由 `go generate ./server` 压缩后内嵌进二进制；修改前端后重新生成并编译即可。

MiIO 协议层依赖 [MisGo](https://github.com/Yonsm/misgo)（`github.com/Yonsm/misgo v0.1.1`）。

## 架构

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

- **加载**：`main.go` 读取配置后调用 `core.LoadModules`，按 `mihex.yml` 中配置段的书写顺序加载模块；各模块 `setup()` 返回的对象注册进 `core` 注册表，命令按 `NAME` 查找
- **轮询**：`tracker` 遍历实现 `Poller` 的对象，状态变化时通过 `core.Emit` 推送给 WebSocket 客户端；有连接时用 `fast`，全部断开后回到 `slow`
- **命令**：仪表盘与 CLI 都通过 `runner` 分发 `[NAME, method, ...args]`，文本中的 `{{...}}` 模板会展开为实体状态或调用结果
- **服务**：`server` 提供静态资源、Token/Basic 鉴权与 WebSocket；`finale` 在退出时按注册逆序执行清理

## 安全建议

- `server.token` 鉴权基于 HTTP Basic / URL query 参数，未配置 `cert`/`key` 时以明文 HTTP 传输。设计定位是可信局域网内使用；若需公网访问，请务必配置 `cert`/`key` 启用 HTTPS，或放在已有 VPN/反向代理之后。
- `mihex.yml`（含账号密码、Token 等敏感信息）与运行时生成的 `.mi.token`（小米云 session 缓存）、`.miot_specs.json`（MIoT spec 本地缓存，不含敏感信息，删除后会自动重新拉取）已在 `.gitignore` 中，注意不要手动提交。

## 致谢

- [MisGo](https://github.com/Yonsm/misgo) — MiIO 协议库（MiService 的 Go 实现），MiHex 的 miio 协议层
- [MiService](https://github.com/Yonsm/MiService) — 米家 MiIO 协议（Python 版参考实现）
- [python-broadlink](https://github.com/mjg59/python-broadlink) — Broadlink 设备控制
- [paho.mqtt.golang](https://github.com/eclipse/paho.mqtt.golang) — MQTT 客户端
- [彩云天气](https://caiyunapp.com/) — 天气数据

## 许可证

[GPL-3.0](LICENSE)
