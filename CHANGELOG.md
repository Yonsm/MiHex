# 更新日志

本文件记录 MiHex 的所有重要变更。

格式基于 [Keep a Changelog](https://keepachangelog.com/en/1.1.0/)，
版本号遵循 [语义化版本](https://semver.org/lang/zh-CN/spec/v2.0.0.html)。

## [1.2.3] - 2026-07-03

### 新增

- 首个正式发布：轻量级智能家居管理服务，支持 macOS、Linux、OpenWrt、MiWiFi
- Web 仪表盘：单页 UI，WebSocket 实时推送状态变更
- MQTT、MiIO（含 MIoT 规格自动发现）、Broadlink、Modbus、赛沃地暖、天气、Ping（Wake-on-LAN）、Shell、钉钉、语音网关等设备接入模块
- 设备追踪：定时轮询，WebSocket 连接时自动切换快速模式
- 支持自定义模块（`customs/` 目录）
