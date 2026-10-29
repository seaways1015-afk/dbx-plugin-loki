# Loki

[English](README.en.md) | 简体中文

Loki 是 [DBX](https://github.com/t8y2/dbx)（开源数据库工作台）的插件。它让你在 DBX 里直接用 LogQL 查询、浏览和实时跟踪 [Grafana Loki](https://grafana.com/oss/loki/) 日志，不用另外部署 Grafana。对应需求：[t8y2/dbx#13126](https://github.com/t8y2/dbx/issues/13126)。

[![License: Apache-2.0](https://img.shields.io/badge/license-Apache--2.0-blue.svg)](LICENSE)

## 功能

**连接**

- HTTP / HTTPS，支持反向代理路径前缀（如 `/loki-gateway`）。
- 多租户：按需填写 `X-Scope-OrgID`。
- 认证：无、Basic 认证或 Bearer Token；HTTPS 下可选跳过 TLS 证书校验。
- 支持 DBX 的 SSH / 代理隧道。配置了隧道但宿主没给出端点时直接报错，不会绕过隧道直连。

**浏览与查询**

- 标签浏览：左侧列出标签和标签值，点击把 `label="value"` 加进查询，悬停可选「排除」。工作台宽度小于 720px 时，标签栏收进查询栏的「标签」按钮，以抽屉形式打开。
- LogQL 编辑器：语法高亮，自动补全标签名、标签值、函数和解析器；`Shift+Enter` 或 `Ctrl/Cmd+Enter` 执行。
- 日志列表：多个流按时间合并排序，按级别着色，高亮 `|=`、`|~` 匹配的关键词；展开一行可查看全部标签、一键加入或排除筛选、复制原文。
- 时间范围预设或自定义，行数上限、排序方向、自动换行、UTC 显示，以及查询历史。

**日志量与实时跟踪**

- 日志量柱状图：按时间段统计匹配行数，点击柱子放大到该时间段。首尾不足一个时间段的部分单独精确统计，总数与日志列表一致（结果未截断时）。
- 实时跟踪：每 2 秒拉取一次新日志（基于 `query_range` 轮询，不需要 WebSocket）。每次回看 10 秒并去重，采集端批量延迟推送的日志也不会漏；最多保留 5000 行。

**指标查询**

- `rate`、`count_over_time`、`sum by` 等指标查询，返回带时间轴的折线图和序列表。

**AI**

- 「问 AI」：把当前日志样本发给 DBX 内置 AI 分析。
- 4 个只读 AI 工具：`loki_labels`、`loki_label_values`、`loki_query_logs`、`loki_query_metrics`。在 插件中心 → 已安装 → 内置 AI 工具 中开启后，Agent 模式下可以直接问「api 服务过去一小时为什么报错」。

界面支持中文和英文，跟随 DBX 的浅色 / 深色主题。

## 环境要求

- DBX 0.6.43 或更高版本。
- 可通过 HTTP API 访问的 Grafana Loki，或兼容 Loki API 的网关。已在 Loki 3.5.0 上验证。
- 发布产物覆盖 5 个平台：macOS（Apple Silicon / Intel）、Windows x64、Linux（x64 / arm64）。

## 安装

上架后，在 DBX 插件市场搜索 **Loki** 安装，安装的是经 DBX Store 签名的产物。

上架前想先试用：从 [Releases](https://github.com/seaways1015-afk/dbx-plugin-loki/releases) 下载对应平台的 `.dbxp`，在 DBX 插件中心开启「允许安装未签名开发包」后从本地安装。

## 配置连接

| 字段 | 必填 | 说明 |
| --- | --- | --- |
| 连接名称 | 是 | 在 DBX 中显示的名称。 |
| 协议 | 是 | `http` 或 `https`。 |
| 主机 | 是 | 只填主机名或 IP，不含协议、端口和路径。 |
| 端口 | 是 | 默认 `3100`。 |
| 路径前缀 | 否 | Loki 位于反向代理之后时填写，如 `/loki-gateway`；默认 `/`。 |
| 租户 ID（X-Scope-OrgID） | 否 | 多租户 Loki 必填；单租户部署留空。 |
| 认证方式 | 是 | 无、Basic 认证或 Bearer Token。 |
| 用户名 / 密码 | Basic 时必填 | 选择 Basic 认证时显示。 |
| Bearer Token | Bearer 时必填 | 选择 Bearer Token 时显示。 |
| 跳过 TLS 证书校验 | 否 | 仅在协议为 `https` 时显示，自签名证书时使用。 |

保存后，双击左侧的 Loki 连接打开「Loki 日志查询」工作台。

## 安全

- 只调用 Loki 的只读 GET 接口：`labels`、`label/<name>/values`、`query`、`query_range`、`status/buildinfo`，不写入、不删除。
- 拒绝重定向；单次响应上限 16 MiB，返回给界面的结果上限 4 MiB，单行超过 16 KiB 会截断并标记。
- 密码和 Token 由 DBX 的密码 / Secret 字段保存，不会出现在日志、工作台上下文或 AI 上下文里。
- 只申请 `host.storage`（保存界面设置和查询历史）和 `host.ai`（问 AI 与 AI 工具）两项权限；不监听本地端口、不创建子进程、不写本地文件。

## 开发

前置条件：Node.js 22+、Go 1.22+、`@dbx-app/plugin-cli` 0.1.9。

```bash
npm install --global @dbx-app/plugin-cli@0.1.9
npm ci
npm test                         # 前端单元测试
go -C backend test -race ./...   # 后端单元测试（用 httptest 模拟 Loki）
npm run build                    # 构建 ui/index.html
```

- 只看界面（模拟数据，不需要 DBX 和 Loki）：运行 `npm run dev`，打开终端里显示的地址加 `/preview.html`。
- 联调：用 `docker run -d --name loki -p 3100:3100 grafana/loki:3.5.0` 启动本地 Loki，再运行 `dbx-plugin dev --path . --port 5190`，打开 `http://127.0.0.1:5190` 新建连接（主机 `127.0.0.1`，端口 `3100`）。开发宿主不支持按认证方式显示 / 隐藏字段，这一项需要在 DBX 桌面版里检查。
- 打包当前平台的未签名候选包：`npm run build && dbx-plugin package .`，产物在 `dist/`。

## 发布（维护者）

1. 发布 GitHub Release。`.github/workflows/plugin-release.yml` 会为 5 个平台构建未签名候选包和 `release-candidates.json`。
2. 向 [t8y2/dbx-store](https://github.com/t8y2/dbx-store) 提 PR，新增 `publishers/seaways1015-afk.json`（首次提交）和 `candidates/io.github.seaways1015-afk.loki.json`，格式见 dbx-store 的 CONTRIBUTING.md。
3. 维护者审核后签名并合并，插件即出现在 DBX 插件市场。

## 许可证

[Apache-2.0](LICENSE)，另见 [NOTICE](NOTICE)。Grafana Loki 是 Grafana Labs 的商标，本项目与 Grafana Labs 无关。
