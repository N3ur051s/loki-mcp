# Loki MCP Server

[![CI](https://github.com/N3ur051s/loki-mcp/actions/workflows/ci.yml/badge.svg)](https://github.com/N3ur051s/loki-mcp/actions/workflows/ci.yml)
[![Release](https://github.com/N3ur051s/loki-mcp/actions/workflows/release.yml/badge.svg)](https://github.com/N3ur051s/loki-mcp/actions/workflows/release.yml)

基于 Go 的 [Grafana Loki](https://grafana.com/oss/loki/) Model Context Protocol (MCP) 服务，通过
**Streamable HTTP** 对外提供 Loki 查询能力。

## 功能

- Streamable HTTP 传输：`http://<host>:8080/mcp`
- 三个只读工具：
  - `loki_query`：执行 LogQL 查询
  - `loki_label_names`：获取全部标签名
  - `loki_label_values`：获取指定标签的值
- 支持 Loki Basic Auth、Bearer Token 和多租户 `X-Scope-OrgID`
- 支持 stateless / stateful 会话、CORS、心跳和健康检查
- 基于 `mcp-go v1.3.0`，兼容新旧 MCP 协议版本

## 下载预编译版本

GitHub Releases 提供 Linux、macOS 和 Windows 的 amd64 / arm64 二进制包：

<https://github.com/N3ur051s/loki-mcp/releases/latest>

下载对应平台的压缩包，解压后即可运行：

```bash
./loki-mcp-server -version
```

每个 Release 同时提供 `checksums.txt`，用于校验下载文件。

## Docker 部署

### 1. 构建镜像

```bash
docker build -t loki-mcp-server:0.2.1 .
```

### 使用预构建镜像

每次发布都会推送多架构镜像到 GitHub Container Registry：

```bash
docker pull ghcr.io/n3ur051s/loki-mcp:latest
```

如果 Package 默认为私有，需要先在 GitHub 仓库的 **Packages** 页面将镜像可见性改为 public，或使用 `docker login ghcr.io` 登录后再拉取。

### 2. 启动容器

Docker 镜像默认使用 HTTP 模式：

```bash
docker run -d \
  --name loki-mcp \
  --restart unless-stopped \
  -p 8080:8080 \
  -e LOKI_URL=http://host.docker.internal:3100 \
  loki-mcp-server:0.2.1
```

Linux 主机需要让容器能够访问宿主机上的 Loki：

```bash
docker run -d \
  --name loki-mcp \
  --restart unless-stopped \
  --add-host=host.docker.internal:host-gateway \
  -p 8080:8080 \
  -e LOKI_URL=http://host.docker.internal:3100 \
  loki-mcp-server:0.2.1
```

如果 Loki 在另一个容器中，把 `LOKI_URL` 改成对应的服务地址即可：

```bash
-e LOKI_URL=http://loki:3100
```

### 3. 验证服务

```bash
curl http://localhost:8080/healthz
```

返回示例：

```json
{
  "service": "loki-mcp-server",
  "status": "ok",
  "time": "2026-10-23T12:00:00Z",
  "version": "0.2.1"
}
```

Streamable HTTP 端点：

```text
http://localhost:8080/mcp
```

### 4. Docker Compose

仓库自带一个完整的演示环境，包含 Loki、Grafana、Promtail、日志生成器和 MCP 服务：

```bash
docker compose up -d --build
```

启动后：

| 服务 | 地址 |
| --- | --- |
| Loki MCP Server | `http://localhost:8080/mcp` |
| 健康检查 | `http://localhost:8080/healthz` |
| Loki | `http://localhost:3100` |
| Grafana | `http://localhost:3000` |

查看日志：

```bash
docker compose logs -f loki-mcp-server
```

停止并删除容器：

```bash
docker compose down
```

### 5. 使用外部 Loki

生产环境通常不需要 Compose 里的 Loki/Grafana。可以只启动 MCP 服务：

```bash
docker compose up -d loki-mcp-server
```

然后通过环境变量指定外部 Loki：

```yaml
services:
  loki-mcp-server:
    image: loki-mcp-server:0.2.1
    ports:
      - "8080:8080"
    environment:
      MCP_TRANSPORT: http
      MCP_HTTP_ADDR: 0.0.0.0:8080
      LOKI_URL: https://loki.example.com
      LOKI_ORG_ID: tenant-1
      LOKI_TOKEN: your-bearer-token
```

## 环境变量

### 服务端

| 变量 | 默认值 | 说明 |
| --- | --- | --- |
| `MCP_TRANSPORT` | `http` | Docker 镜像已设置为 HTTP 模式 |
| `MCP_HTTP_ADDR` | 空 | 完整监听地址，例如 `0.0.0.0:8080` |
| `PORT` | `8080` | 未设置 `MCP_HTTP_ADDR` 时使用的端口 |
| `MCP_STREAMABLE_PATH` | `/mcp` | Streamable HTTP 端点路径 |
| `MCP_SESSION_MODE` | `stateless` | `stateless` 或 `stateful` |
| `MCP_SESSION_IDLE_TTL` | `30m` | stateful 会话空闲超时 |
| `MCP_HEARTBEAT_INTERVAL` | `30s` | Streamable HTTP 心跳间隔，`0` 表示关闭 |
| `MCP_CORS_ORIGINS` | `*` | 允许的跨域来源，逗号分隔；`none` 表示关闭 |
| `MCP_CORS_ALLOW_CREDENTIALS` | `false` | 是否允许携带凭据的跨域请求 |
| `MCP_CORS_MAX_AGE` | `600` | CORS 预检缓存秒数 |
| `MCP_DISABLE_DNS_REBINDING_PROTECTION` | `false` | 是否关闭本地 DNS 重绑定保护 |
| `MCP_TLS_CERT` | 空 | TLS 证书路径，需同时设置 `MCP_TLS_KEY` |
| `MCP_TLS_KEY` | 空 | TLS 私钥路径，需同时设置 `MCP_TLS_CERT` |
| `MCP_SHUTDOWN_TIMEOUT` | `10s` | 优雅关闭超时 |
| `LOG_LEVEL` | `info` | `debug`、`info`、`warn`、`error` |

### Loki 连接

| 变量 | 默认值 | 说明 |
| --- | --- | --- |
| `LOKI_URL` | `http://localhost:3100` | Loki 地址 |
| `LOKI_ORG_ID` | 空 | 多租户 ID，发送为 `X-Scope-OrgID` |
| `LOKI_USERNAME` | 空 | Basic Auth 用户名 |
| `LOKI_PASSWORD` | 空 | Basic Auth 密码 |
| `LOKI_TOKEN` | 空 | Bearer Token，优先级高于 Basic Auth |
| `LOKI_TIMEOUT` | `30s` | 请求 Loki 的超时时间 |

## Streamable HTTP 使用

### 端点

| 端点 | 方法 | 说明 |
| --- | --- | --- |
| `/mcp` | `POST`、`GET`、`DELETE` | Streamable HTTP |
| `/stream` | `POST`、`GET`、`DELETE` | `/mcp` 的兼容别名 |
| `/health`、`/healthz` | `GET`、`HEAD` | 健康检查 |

### MCP Inspector

```bash
npx @modelcontextprotocol/inspector
```

选择 **Streamable HTTP**，连接：

```text
http://localhost:8080/mcp
```

### Claude Code

```bash
claude mcp add --transport http loki http://localhost:8080/mcp
```

### Cursor

在 `~/.cursor/mcp.json` 中添加：

```json
{
  "mcpServers": {
    "loki": {
      "url": "http://localhost:8080/mcp"
    }
  }
}
```

### VS Code

在 `.vscode/mcp.json` 中添加：

```json
{
  "servers": {
    "loki": {
      "type": "http",
      "url": "http://localhost:8080/mcp"
    }
  }
}
```

### n8n

在 MCP Client Tool 节点中配置：

```text
http://your-server:8080/mcp
```

### curl 调用

初始化：

```bash
curl -sS http://localhost:8080/mcp \
  -H 'Content-Type: application/json' \
  -H 'Accept: application/json, text/event-stream' \
  -H 'MCP-Protocol-Version: 2025-03-26' \
  -d '{
    "jsonrpc": "2.0",
    "id": 1,
    "method": "initialize",
    "params": {
      "protocolVersion": "2025-03-26",
      "capabilities": {},
      "clientInfo": {"name": "curl", "version": "1.0.0"}
    }
  }'
```

调用工具：

```bash
curl -sS http://localhost:8080/mcp \
  -H 'Content-Type: application/json' \
  -H 'Accept: application/json, text/event-stream' \
  -H 'MCP-Protocol-Version: 2025-03-26' \
  -d '{
    "jsonrpc": "2.0",
    "id": 2,
    "method": "tools/call",
    "params": {
      "name": "loki_query",
      "arguments": {
        "query": "{job=\"varlogs\"} |= \"ERROR\"",
        "start": "-1h",
        "end": "now",
        "limit": 50,
        "format": "raw"
      }
    }
  }'
```

### 测试脚本

仓库提供了基于 `curl` + `jq` 的测试脚本：

```bash
chmod +x examples/streamable-http.sh
./examples/streamable-http.sh http://localhost:8080/mcp '{job="varlogs"}' raw
```

也可以直接用浏览器打开：

```text
examples/streamable-http-client.html
```

把端点填写为 `http://localhost:8080/mcp` 即可连接和调用工具。

## 工具参数

### `loki_query`

| 参数 | 必填 | 说明 |
| --- | --- | --- |
| `query` | 是 | LogQL 查询，例如 `{job="varlogs"} |= "ERROR"` |
| `url` | 否 | Loki 地址，默认使用 `LOKI_URL` |
| `start` | 否 | RFC3339、相对时间（`-1h`、`-30m`）或 `now`，默认 `-1h` |
| `end` | 否 | RFC3339、相对时间或 `now`，默认 `now` |
| `limit` | 否 | 最大条数，默认 `100` |
| `org` | 否 | 租户 ID，发送为 `X-Scope-OrgID` |
| `username` | 否 | Basic Auth 用户名 |
| `password` | 否 | Basic Auth 密码 |
| `token` | 否 | Bearer Token |
| `format` | 否 | `raw`（默认）、`json`、`text` |

### `loki_label_names`

获取全部标签名，支持与 `loki_query` 相同的连接、时间范围和格式参数。

### `loki_label_values`

获取指定标签的全部值，额外需要：

| 参数 | 必填 | 说明 |
| --- | --- | --- |
| `label` | 是 | 标签名，例如 `job` |

## 会话模式

- **`stateless`**（默认）：不保存服务端会话，适合 Docker、多副本和负载均衡部署。
- **`stateful`**：返回 `Mcp-Session-Id`，支持 `DELETE` 终止会话。多副本部署时需要粘性会话。

## 反向代理注意事项

Streamable HTTP 可能使用长连接和流式响应，反向代理需要：

- 关闭响应缓冲（`proxy_buffering off`）
- 增大读取超时（`proxy_read_timeout`）
- 保留 `Mcp-Session-Id`、`MCP-Protocol-Version`、`Last-Event-ID` 等请求头

如果代理通过回环地址转发公网 `Host`，需要重写 `Host` 头，或设置：

```bash
MCP_DISABLE_DNS_REBINDING_PROTECTION=true
```

## 安全建议

- 服务本身不提供用户认证，请通过内网、反向代理认证、mTLS 或 API Gateway 保护。
- 生产环境不要保留 `MCP_CORS_ORIGINS=*`，应配置明确的来源。
- Loki 凭据放在 Secret 或环境变量中，不要写入提示词。
- `X-Scope-OrgID` 只是租户标识，不是认证机制。
- 多副本部署优先使用 `MCP_SESSION_MODE=stateless`。

## 常见问题

### 404

确认使用 `/mcp`，不要带尾斜杠。

### 浏览器 CORS 报错

```bash
-e MCP_CORS_ORIGINS=https://your-app.example.com
```

### 容器无法连接 Loki

容器内的 `localhost` 指向容器自身：

- Docker Desktop：使用 `host.docker.internal`
- Linux：添加 `--add-host=host.docker.internal:host-gateway`
- Compose：使用 Loki 的服务名

### 会话丢失

使用单副本、粘性会话，或改为：

```bash
-e MCP_SESSION_MODE=stateless
```

## 发布新版本

项目通过 GitHub Actions 自动发布。创建并推送 `v*` 标签即可触发：

```bash
git tag -a v0.2.1 -m "v0.2.1"
git push github main --tags
```

`Release` 工作流会自动：

1. 交叉编译 Linux / macOS / Windows 的 amd64 / arm64 版本
2. 生成 `.tar.gz` / `.zip` 压缩包和 `checksums.txt`
3. 创建 GitHub Release，并根据提交记录自动生成发布说明
4. 构建多架构 Docker 镜像并推送到 `ghcr.io/n3ur051s/loki-mcp`

也可以在 GitHub 的 **Actions → Release → Run workflow** 中手动填写已存在的标签来重新发布。

如果 Release 工作流无法创建 Release，请在仓库的 **Settings → Actions → General → Workflow permissions** 中选择 **Read and write permissions**。

首次从本地仓库推送：

```bash
git remote add github https://github.com/N3ur051s/loki-mcp.git
git push github main
```

## 许可证

MIT License
