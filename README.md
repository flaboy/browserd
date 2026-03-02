# browserd

独立浏览器执行服务（与 `botworks` 解耦），提供 `use_browser` 运行时所需的最小 HTTP 接口。

## 当前能力（V1）
- `POST /v1/sessions`
- `POST /v1/sessions/{runtimeSessionId}/commit`
- `DELETE /v1/sessions/{runtimeSessionId}`
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
