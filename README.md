# browserd

独立浏览器执行服务（与 `botworks` 解耦），提供 `use_browser` / `browser_use` 运行时所需的最小 HTTP 接口。

## 当前能力（V1）
- `POST /v1/sessions`
- `POST /v1/sessions/{runtimeSessionId}/commit`
- `DELETE /v1/sessions/{runtimeSessionId}`
- `POST /v1/sessions/{runtimeSessionId}/navigate`
- `GET /v1/sessions/{runtimeSessionId}/snapshot`
- `POST /v1/sessions/{runtimeSessionId}/act`
- `POST /v1/sessions/{runtimeSessionId}/screenshot`
- profile 存储约定：每个 BrowserSession 单文件 `profile.tgz`（同 key 覆盖）

## 本地运行
```bash
go run ./cmd/browserd
```

环境变量：
- `BROWSERD_PORT`（默认 `7011`）
- `BROWSERD_CDP_BASE_URL`（默认 `ws://browserd:9222/devtools/browser`）

## Docker 镜像（内置 Chromium）
- 镜像内已打包 Chromium（路径默认 `CHROME_BIN=/usr/bin/chromium-browser`），可直接用于 chromedp/DevTools 场景。
- 构建：
```bash
docker build -t browserd:dev .
```

## 测试
```bash
go test ./...
```

## API 示例

### Create
```http
POST /v1/sessions
Content-Type: application/json

{
  "s3ProfilePath": "s3://bucket/browser-sessions/t_1/c_1/bs_1/profile.tgz",
  "expectedVersion": "v0",
  "leaseId": "lease_1"
}
```

`POST /v1/sessions` 是同步初始化接口：
- 返回 200 前，Chromium 已启动且 DevTools websocket 已 ready
- Chromium 仍以 `about:blank` 启动，不额外执行首屏 navigate
- 若 readiness 失败，接口返回 `503 SESSION_INIT_FAILED`
- 失败时不会保留可继续使用的 `runtimeSessionId`

### Commit
```http
POST /v1/sessions/{runtimeSessionId}/commit
Content-Type: application/json

{
  "ifMatchVersion": "v0"
}
```

### Delete
```http
DELETE /v1/sessions/{runtimeSessionId}
```

### Navigate
```http
POST /v1/sessions/{runtimeSessionId}/navigate
Content-Type: application/json

{
  "url": "https://www.baidu.com/",
  "waitUntil": "load",
  "timeoutMs": 30000
}
```

### Snapshot
```http
GET /v1/sessions/{runtimeSessionId}/snapshot?mode=refs
```

响应示例：

```json
{
  "data": {
    "snapshotId": "snap_123",
    "page": {
      "url": "https://www.baidu.com/",
      "title": "百度一下，你就知道",
      "groups": {
        "buttons": {
          "columns": ["ref", "tag", "text"],
          "rows": [["e13", "BUTTON", "百度一下"]]
        },
        "texts": {
          "columns": ["ref", "tag", "text", "textLength"],
          "rows": [["t1", "DIV", "点我去文心助手回答，已接入DeepSeek...", 26]]
        }
      }
    }
  },
  "error": null
}
```

约束：
- `snapshot.page` 是唯一页面阅读结构
- 对外只暴露 `ref`
- `e*` 表示可操作元素
- `t*` 表示只读文本块

### Act
```http
POST /v1/sessions/{runtimeSessionId}/act
Content-Type: application/json

{
  "action": "click",
  "ref": "e13"
}
```

约束：
- `click` / `type` / `fill` / `press` / `hover` / `select` / `waitFor` 只接受 `e*`
- `scrollIntoView` 接受 `e*` 与 `t*`
- 对 `t*` 执行 `click` 会返回 `INVALID_REF`

### Screenshot
```http
POST /v1/sessions/{runtimeSessionId}/screenshot
Content-Type: application/json

{
  "ref": "t1",
  "format": "png"
}
```

约束：
- 不带 `ref` 时返回全页截图
- 带 `ref` 时可对 `e*` 与 `t*` 截图
