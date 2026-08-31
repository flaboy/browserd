# browserd

独立浏览器执行服务（与业务系统解耦），提供 `use_browser` / `browser_use` 运行时所需的最小 HTTP 接口。

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
- `BROWSERD_PROFILE_STORE`：`s3` 或 `file`
- `BROWSERD_PROFILE_BUCKET`：profile 使用的 S3 bucket，默认 `private`
- `BROWSERD_PROFILE_FILE_ROOT`：`file` 模式下 profile 逻辑路径映射到的本地根目录

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
  "profilePath": "/browser-sessions/t_1/c_1/bs_1/profile.tgz",
  "fingerprint": {
    "seed": "fp_7f8c2a0e-57f7-4f34-93d3-45830f3c6c6d",
    "locale": "en-US",
    "languages": ["en-US", "en"],
    "acceptLanguage": "en-US,en;q=0.9",
    "timezone": "America/New_York",
    "platform": "Win32",
    "os": "Windows",
    "userAgent": "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/125.0.0.0 Safari/537.36",
    "viewportWidth": 1366,
    "viewportHeight": 768,
    "screenWidth": 1366,
    "screenHeight": 768,
    "deviceScaleFactor": 1,
    "hardwareConcurrency": 8,
    "deviceMemory": 8,
    "webglVendor": "Google Inc. (Intel)",
    "webglRenderer": "ANGLE (Intel)"
  },
  "proxyServer": "http://user:pass@proxy.example.com:8080",
  "expectedVersion": "v0",
  "leaseId": "lease_1"
}
```

`POST /v1/sessions` 是同步初始化接口：
- `fingerprint` 必填，且必须传完整浏览器主体配置，包括 `seed`、`locale`、`languages`、`acceptLanguage`、`timezone`、`platform`、`os`、`userAgent`、视口/屏幕尺寸、硬件并发、内存与 WebGL 信息
- `profilePath` 必填，且只能是以 `/` 开头、以 `profile.tgz` 结尾的逻辑路径；不得传 `s3://`、`file://`、`http://` 等协议型路径
- profile 存储后端由配置层统一决定：`s3` 模式使用 `BROWSERD_PROFILE_BUCKET`，`file` 模式使用 `BROWSERD_PROFILE_FILE_ROOT`
- seed-only 请求不再支持；缺失或不完整时直接返回 `INVALID_FINGERPRINT_CONFIG`
- browserd 不从 seed 推导运行时配置；调用方必须把数据库中的完整 fingerprint 配置传入本接口
- `screenWidth` / `screenHeight` 是 live Xvfb/VNC 桌面尺寸，也是 Chromium `--window-size` 的唯一来源
- `viewportWidth` / `viewportHeight` 只用于 CDP `SetDeviceMetricsOverride` 的页面视口配置，不作为 live 桌面或 Chromium 窗口尺寸的 fallback
- `proxyServer` 可选，支持 `http://host:port`、`http://user:pass@host:port`、`socks5://host:port`、`socks5://user:pass@host:port`
- 代理认证信息只用于 Chromium 连接代理；日志、错误和 Chrome 启动参数不得包含明文密码
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
  "timeoutMs": 30000,
  "afterLoadScreenshotS3Path": "s3://browserd-snapshots/team_1/conv_1/1737373333.png"
}
```

说明：
- `navigate` 是唯一的页面跳转接口，不新增 `relocation` 同义接口。
- `afterLoadScreenshotS3Path` 为可选字段，必须传完整 `s3://bucket/key`。
- 截图 bucket 不从 `profilePath` 推导，允许与 userdata bucket 不同。

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
  "ref": "e13",
  "motionProfile": "humanized"
}
```

点击视口坐标（用于 closed shadow DOM/custom element 内部热区等无法稳定暴露 `ref` 的页面）：

```http
POST /v1/sessions/{runtimeSessionId}/act
Content-Type: application/json

{
  "action": "click",
  "x": 742.5,
  "y": 724,
  "motionProfile": "direct"
}
```

输入并提交（一次调用完成）：

```http
POST /v1/sessions/{runtimeSessionId}/act
Content-Type: application/json

{
  "action": "type",
  "ref": "e1",
  "text": "复旦附中",
  "clear": true,
  "submit": true
}
```

约束：
- `click` / `fill` / `press` / `hover` / `select` / `waitFor` 只接受 `e*`
- `click` 可不传 `ref`，改传视口坐标 `x` / `y`；坐标点击同样通过 CDP mouse events 执行
- `click` 通过 CDP mouse events 执行，默认 `motionProfile=humanized` 会先生成 mousemove 轨迹再 press/release
- live viewer 会显示 browserd 内部虚拟鼠标；虚拟鼠标由 `/act` 的 mousemove/press/release 状态驱动，不修改目标页面 DOM，也不改变 `/v1/sessions/{runtimeSessionId}/act` 的外部请求结构
- `type` 必须显式指定 `ref`，先聚焦目标，再通过 CDP `Input.insertText` 插入文本；不依赖当前焦点
- `type` 的 `submit` 为 `true` 时，在同一次 CDP 往返里紧接着按下 Enter。提交表单或触发搜索一律用它，
  不要用 `type` 后再发一次 `press`——两次 `/act` 之间页面可能重渲染并让 `ref` 失效
- `type` 的 `text` 不能为空，所以 `submit` 不能当作单独的「按回车」使用；那是 `press` 的职责
- `press` 的 `key` 只接受以下命名键（W3C `KeyboardEvent.key` 写法，大小写敏感）：
  `ArrowDown`、`ArrowLeft`、`ArrowRight`、`ArrowUp`、`Backspace`、`Delete`、`End`、`Enter`、`Escape`、`Home`、`PageDown`、`PageUp`、`Space`、`Tab`；
  或任意单个可打印字符，例如 `"a"`、`"/"`
- `press` 不接受别名（`Return`、`esc`、`up`）和修饰键组合（`Control+s`）；非法 `key` 返回 `INVALID_KEY`，错误信息会给出正确写法，不会把 `key` 当作文本输入
- `scrollIntoView` 接受 `e*` 与 `t*`
- 对 `t*` 执行 `click` 会返回 `INVALID_REF`

### Screenshot
```http
POST /v1/sessions/{runtimeSessionId}/screenshot
Content-Type: application/json

{
  "ref": "t1",
  "format": "png",
  "screenshotS3Prefix": "/browser-screenshots/2026-08/team_1/conv_1/"
}
```

约束：
- 不带 `ref` 时返回全页截图
- 带 `ref` 时可对 `e*` 与 `t*` 截图
- `screenshotS3Prefix` 必填，且只能是以 `/` 开头、以 `/` 结尾的逻辑路径；不得传 `s3://`、`file://`、`http://` 等协议型路径
- browserd 使用 `BROWSERD_PROFILE_BUCKET` 指定的 bucket 上传截图，返回 `screenshotId` 为 `<uuid>.png`，返回 `s3Path` 为逻辑路径
