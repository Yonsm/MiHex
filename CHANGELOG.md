# 更新日志

本文件记录 MiHex 的所有重要变更。

格式基于 [Keep a Changelog](https://keepachangelog.com/en/1.1.0/)，
版本号遵循 [语义化版本](https://semver.org/lang/zh-CN/spec/v2.0.0.html)。

## [1.5.0] - 2026-11-19

### 变更

- **Go 重写版成为主开发线**：仓库根目录改为 Go 项目，单二进制部署，内置 HTTP/WebSocket 服务与仪表盘，配置与命令行行为同 Python 版保持一致
- Go module 路径改为 `github.com/Yonsm/MiHex`，可直接 `go install`
- 仪表盘前端通过 `go generate` 从 `python/mihex/www` 压缩嵌入二进制
- 配置加载顺序：`-c` 指定 > 当前目录 `mihex.yml` > `~/.mihex.yml` > 可执行文件所在目录 `mihex.yml`；仓库自带可直接运行的默认 `mihex.yml` 样板
- 包结构扁平化：移除 `internal/`，`config`/`runner`/`tracker` 合并进 `core`，`server` 与 `modules` 上移到仓库根目录
- MiIO 协议层迁移到独立库 MisGo（账号、云 RPC、局域网直连、MIoT spec），MiHex 保留实体、自动发现与命令解析
- Python 实现、发布脚本与文档移入 `python/`，并**冻结在 1.2.4**：保留源码与 PyPI 发布能力，仅修复严重问题
- README 以 Go 版本为主，Python 文档移入 `python/`

### 说明

- Go 版暂未实现 `modbus`、`saswell` 模块与 Python `customs/` 自定义模块；需要这些功能请使用 `python/` 下的 Python 版
- Go 版读取 `mihex.yml`，Python 版读取 `mihex.yaml`，配置段含义一致；Go 版遇到未实现的配置段会告警并跳过，不影响其余模块启动

## [1.2.4] - 2026-07-04

### 修复

- 修复 pip 安装后无法定位 `www` 目录导致仪表盘无法访问的问题（`server.py` 不应依赖 `sys.modules['__main__'].__file__`，改用自身 `__file__` 定位包内路径）

### 新增

- MiIO 自动发现新增门磁、人体、水浸、烟雾等二进制安防传感器的识别：不仅归类到仪表盘已有的 `binary` 卡片样式，还会在设备 spec 中定位可轮询的只读布尔状态属性并绑定为 `ISON`，使状态能正确显示（原先这类传感器完全没有匹配的属性名规则，实际读取的是一个大概率错误的猜测位号，导致状态始终不更新）——仅测试性质，自行尝试、修正

## [1.2.3] - 2026-07-03

### 新增

- 首个正式发布：轻量级智能家居管理服务，支持 macOS、Linux、OpenWrt、MiWiFi
- Web 仪表盘：单页 UI，WebSocket 实时推送状态变更
- MQTT、MiIO（含 MIoT 规格自动发现）、Broadlink、Modbus、赛沃地暖、天气、Ping（Wake-on-LAN）、Shell、钉钉、语音网关等设备接入模块
- 设备追踪：定时轮询，WebSocket 连接时自动切换快速模式
- 支持自定义模块（`customs/` 目录）
