# Loki for DBX

[English](#english) | 简体中文

在 [DBX](https://github.com/t8y2/dbx) 里直接查询、浏览和实时跟踪 [Grafana Loki](https://grafana.com/oss/loki/) 日志，不用另外部署 Grafana。对应需求：[t8y2/dbx#13126](https://github.com/t8y2/dbx/issues/13126)。

## 功能

- **连接**：HTTP / HTTPS、反向代理路径前缀、多租户 `X-Scope-OrgID`、Basic 认证或 Bearer Token、可选跳过 TLS 校验；支持 DBX 的 SSH / 代理隧道（配置了隧道但宿主没给出端点时直接失败，不会绕过隧道直连）。
- **标签浏览**：左侧列出标签和标签值，点击即可把 `label="value"` 加进查询，悬停可选「排除」。
- **LogQL 编辑器**：语法高亮，自动补全标签名、标签值、函数和解析器；`Shift+Enter` / `Ctrl+Enter` 执行。
- **日志列表**：多个流按时间合并排序，按级别着色，高亮 `|=` / `|~` 匹配的关键词；点开一行可以看全部标签、一键加入或排除筛选、复制原文。
- **日志量柱状图**：按时间段统计匹配行数，点击柱子放大到该时间段。
- **实时跟踪**：每 2 秒拉取一次新日志（基于 `query_range` 轮询，不需要 WebSocket），每次回看 10 秒并去重，采集端批量延迟推送的日志也不会漏；最多保留 5000 行。
- **指标查询**：`rate`、`count_over_time`、`sum by` 等返回折线图和序列表。
- **AI**：
  - 「问 AI」按钮：把当前日志样本发给 DBX 内置 AI 分析。
  - 4 个只读 AI 工具（`loki_labels`、`loki_label_values`、`loki_query_logs`、`loki_query_metrics`），需要在 插件中心 → 已安装 → 内置 AI 工具 中开启。开启后 Agent 模式下可以问「api 服务过去一小时为什么报错」。
- 时间范围预设 / 自定义、行数上限、排序方向、自动换行、UTC，以及查询历史（保存在插件存储里）。
- 中英文界面，跟随 DBX 主题（浅色 / 深色）。

## 安全边界

- 只调用 Loki 的只读 GET 接口：`labels`、`label/<name>/values`、`query`、`query_range`、`status/buildinfo`，不写入、不删除。
- 拒绝重定向；单次响应上限 16 MiB，返回给界面的结果上限 4 MiB，单行超过 16 KiB 会截断并标记。
- 密码和 Token 由 DBX 的密码 / Secret 字段管理，不会出现在日志、工作台上下文或 AI 上下文里。
- 插件不监听本地端口、不创建子进程、不写本地文件。

## 开发

需要 Node.js 22+、Go 1.22+ 和 `@dbx-app/plugin-cli`：

```bash
npm install --global @dbx-app/plugin-cli@0.1.9
npm ci
```

```bash
npm test                         # 前端单元测试
go -C backend test -race ./...   # 后端单元测试（用 httptest 模拟 Loki）
npm run build                    # 构建 ui/index.html
```

**只看界面**（模拟数据，不需要 DBX 和 Loki）：运行 `npm run dev`，打开终端里显示的地址加 `/preview.html`。

**联调**（官方开发宿主 + 真实或本地 Loki）：

```bash
docker run -d --name loki -p 3100:3100 grafana/loki:3.5.0
dbx-plugin dev --path . --port 5190
```

打开 `http://127.0.0.1:5190`，新建连接（主机 `127.0.0.1`，端口 `3100`），双击连接打开工作台。

**打包**（当前平台的未签名候选包）：

```bash
npm run build && dbx-plugin package .
```

## 发布到 DBX 插件市场

1. 把本仓库推到你自己的 GitHub，然后发布一个 GitHub Release。`.github/workflows/plugin-release.yml` 会为 5 个平台构建未签名候选包和 `release-candidates.json`。
2. 向 [`t8y2/dbx-store`](https://github.com/t8y2/dbx-store) 提一个 PR，新增 `publishers/<你的 id>.json`（首次提交）和 `candidates/io.github.<你的 id>.loki.json`。格式见 dbx-store 的 CONTRIBUTING.md。
3. 维护者审核后签名，并把签名后的 catalog 更新到同一个 PR，合并后插件就会出现在市场里。

---

## English

Query, browse and live-tail Grafana Loki logs with LogQL inside DBX, without running Grafana. Addresses [t8y2/dbx#13126](https://github.com/t8y2/dbx/issues/13126).

- **Connection**: HTTP(S), path prefix, multi-tenant `X-Scope-OrgID`, basic or bearer auth, optional TLS skip-verify, and DBX SSH/proxy tunnels. It fails closed when a tunnel is configured but the host gives no endpoint.
- **Label browser**: click a value to add `label="value"`; hover to exclude it.
- **LogQL editor**: highlighting, completion for labels, values, functions and parsers; run with Shift+Enter.
- **Log list**: streams merged by timestamp, level colors, highlighted line-filter matches, and expandable rows with label filters and copy.
- **Log volume histogram**: click a bar to zoom into that time range.
- **Live tail**: polls `query_range` every 2 s, re-reading the last 10 s with de-duplication so lines that batching agents push late are not lost; keeps up to 5000 lines.
- **Metric queries**: rendered as a chart and series table.
- **AI**: an "Ask AI" button, plus four read-only tools for the DBX AI agent (opt in under Plugin Center → Installed → AI tools).

Only read-only GET endpoints are called. Redirects are refused, responses are size-capped, and secrets never leave DBX's secret store.

```bash
npm ci && npm test && go -C backend test -race ./... && npm run build && dbx-plugin package .
```

## License

Apache-2.0. See [NOTICE](NOTICE). Grafana Loki is a trademark of Grafana Labs. This project is not affiliated with Grafana Labs.
