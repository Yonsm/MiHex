# MiHex

**中文** | [English](README_en.md)

[![PyPI](https://img.shields.io/pypi/v/mihex)](https://pypi.org/project/mihex/)
[![License](https://img.shields.io/badge/license-GPL--3.0-blue)](LICENSE)

**版本**：v1.2.3（[更新日志](CHANGELOG.md)）

MIni Home EXecutor — 轻量级智能家居管理服务，支持 macOS、Linux、OpenWrt、MiWiFi。

通过 MQTT、MiIO、Broadlink 等协议统一管理智能设备，内置纯 Python 实现的 HTTP/WebSocket 服务器，提供实时仪表盘与设备追踪。

![Dashboard](mihex/www/dash.png)

## 功能

- **Web 仪表盘** — 单页 UI，WebSocket 实时推送状态变更
- **MQTT** — 订阅主题获取传感器数据，发布指令控制设备
- **MiIO** — 接入米家设备，支持状态查询与控制；支持按 MIoT 规格自动发现并配置未手动声明的设备（自动发现功能未经充分测试）
- **Broadlink** — 红外/射频遥控非智能设备
- **天气** — 彩云天气 API，温湿度、PM2.5、天气预报
- **Ping** — ICMP 探测设备在线状态，支持 Wake-on-LAN
- **Shell** — SSH/Shell 命令控制远端设备
- **Modbus** — Modbus TCP 设备（如中央空调）线圈/寄存器读写（未经测试）
- **Saswell** — 赛沃地暖温控器状态查询与控制（未经测试）
- **钉钉** — 钉钉机器人推送消息
- **语音网关** — 米家局域网网关语音指令转发
- **设备追踪** — 定时轮询，WebSocket 连接时自动快速模式

## 安装

```sh
pip install mihex
```

可选依赖：

```sh
pip install mihex[broadlink]  # Broadlink 红外/射频
pip install mihex[modbus]     # Modbus TCP
pip install mihex[all]        # 全部可选依赖
```

<details>
<summary>其他安装方式</summary>

**源码安装**：

```sh
git clone https://github.com/Yonsm/MiHex.git
cd MiHex
pip install -e .
```

**OpenWrt / MiWiFi 路由器**：

```sh
sh setup.sh
```

**不使用 pip**：

```sh
pip3 install -r requirements.txt
cp mihex/conf.yaml.example conf.yaml
```

</details>

## 使用

```sh
mihex                         # 当前目录 conf.yaml
mihex -c /path/to/conf.yaml  # 指定配置
python -m mihex               # 等效

mihex -v5                     # 详细日志（-v0~5）
mihex -V5                     # 日志写入 /tmp/mihex.log

mihex MQTT device/topic "msg" # 单次命令
mihex DING "通知文本"
mihex 设备名称 MiIO命令
```

启动后访问 `http://<IP>:<port>/` 打开仪表盘。

## 配置

复制 `conf.yaml.example` 为 `conf.yaml`，按需编辑。配置段：

| 配置段 | 说明 |
|--------|------|
| `server` | 端口、Token 鉴权、HTTPS 证书、快捷链接 |
| `mqtt` | Broker 地址及 weather/sensor/light/switch 设备 |
| `miio` | 米家账号及设备类型映射；`auto: true` 自动发现全部未配置设备，`auto: 家庭名称` 仅发现该家庭下设备，`auto: [设备名, ...]` 仅发现指定设备 |
| `broadlink` | Broadlink 设备及红外遥控码 |
| `ding` | 钉钉机器人 Token 和 Secret |
| `weather` | 彩云天气坐标，可选 `api_key` |
| `gate` | 米家局域网网关 |
| `ping` | 设备 IP 及可选 MAC（WoL） |
| `shell` | SSH/Shell 命令控制 |
| `modbus` | Modbus TCP 主机地址及线圈/寄存器映射 |
| `saswell` | 赛沃地暖温控器地址及设备 ID |
| `runner` | 命令模板，`{{}}` 嵌入动态变量 |
| `tracker` | 轮询间隔（`fast` 60s / `slow` 3600s）|

鉴权：`server.token` 设置后访问需 HTTP Basic Authentication，密码填 token 值，用户名任意。

## 项目结构

```
mihex/
├── __init__.py         # 版本号
├── __main__.py         # CLI 入口
├── entity.py           # Entity 基类与异步工具
├── loader.py           # 动态模块加载
├── server.py           # HTTP/WebSocket 服务器
├── runner.py           # 命令分发与模板展开
├── tracker.py          # 设备状态轮询
├── modules/            # 功能模块
│   ├── mqtt.py
│   ├── miio/
│   │   ├── discover.py  # 设备自动发现（MIoT 规格解析）
│   │   └── viomi_washer/
│   ├── broadlink.py
│   ├── ding.py
│   ├── weather.py
│   ├── ping.py
│   ├── shell.py
│   ├── gate.py
│   ├── modbus.py
│   ├── saswell.py
│   └── codes/           # 红外遥控码
├── www/                # 仪表盘前端
```

## 架构

```
                                               ┌───────────┐
                                               │ conf.yaml │
                                               └───────────┘
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

`loader` 按 `conf.yaml` 的配置段依次加载：先加载各个业务模块（`mqtt`/`miio`/`broadlink`/… 或 `customs/` 下的同名脚本），每个模块的 `setup(conf)` 返回若干 `Entity` 实例并注册进全局列表 `_OBJS`；随后固定加载内置的 `server`、`runner`、`tracker` 三个模块；`server` 通过 WebSocket 与浏览器仪表盘双向通信。

- **加载**：`_OBJS` 是贯穿全局的注册表，混合存放模块对象与 `Entity` 实例，`runner.dispatch()` 按 `NAME` 属性在其中查找目标
- **轮询**：`tracker` 遍历 `_OBJS` 中带 `update()` 方法的对象，状态变化时通过 `server.send_state()` 推送给所有 WebSocket 客户端；无连接时轮询间隔为 `slow`（默认 3600s），有连接时自动切到 `fast`（默认 60s）
- **命令分发**：浏览器通过 WebSocket 发送 `[NAME, method, ...args]`，`server` 转交 `runner.dispatch()` 路由到对应 `Entity` 的方法（`on_turn`/`on_mode`/`on_tune`/`service`）；`runner.expand()` 额外支持 `{{}}` 模板语法，可在文本中内嵌设备状态或指令结果
- **异步模型**：全程基于 asyncio 单事件循环；阻塞的第三方调用（ping、SSH、Modbus、Broadlink 等）通过 `entity.runin()` 丢进线程池，避免阻塞事件循环
- **仪表盘**：`www/dash.*` 是零依赖单页应用，首次连接拉取 `states` 全量渲染，之后仅接收增量推送；用户点击/滑动触发 WebSocket 调用，回包前网格会显示"调用中"状态

## 自定义模块

在 `conf.yaml` 同级目录下新建 `customs/<配置段名>.py`，实现 `setup(conf)`（存在同名自定义文件时优先于内置模块加载）：

```python
from mihex.entity import Entity

async def setup(conf):
    return [MyEntity(c) for c in conf.get('items', [])]

class MyEntity(Entity):
    async def update(self):
        """由 tracker 调用，返回 True 表示状态有变更"""
        pass

    async def on_turn(self, arg):
        """仪表盘开关，arg 为 0/1"""
        await super().on_turn(arg)
```

在 `conf.yaml` 中添加同名配置段即可自动加载。

## 安全建议

- `server.token` 鉴权基于 HTTP Basic / URL query 参数，未配置 `key`/`cert` 时以明文 HTTP 传输。设计定位是可信局域网内使用；若需公网访问，请务必配置 `key`/`cert` 启用 HTTPS，或放在已有 VPN/反向代理之后。
- `conf.yaml`（含账号密码、Token 等敏感信息）与运行时生成的 `.mi.token`（小米云 session 缓存）已在 `.gitignore` 中，注意不要手动提交。

## 致谢

- [MiService](https://github.com/Yonsm/MiService) — 米家 MiIO 协议
- [broadlink](https://github.com/mjg59/python-broadlink) — Broadlink 设备控制
- [paho-mqtt](https://github.com/eclipse/paho.mqtt.python) — MQTT 客户端
- [彩云天气](https://caiyunapp.com/) — 天气数据

## 许可证

[GPL-3.0](LICENSE)