# Cloudflare Worker Proxy Hop

This Worker is a browserd tunnel endpoint. It is not a browser-facing HTTP or SOCKS proxy.

Topology:

```text
Chromium
-> browserd local HTTP proxy
-> browserd WebSocket tunnel client
-> Cloudflare Worker
-> configured upstream proxy
-> target site
```

Required secret:

```text
PROXY_HOP_TOKEN
```

Endpoint:

```text
/tunnel
```

The first WebSocket message from browserd must be JSON:

```json
{
  "type": "open",
  "id": "connect-id",
  "target": "news.163.com:443",
  "upstreamProxy": "http://proxy.example.com:8080",
  "upstreamProxyAuth": {
    "username": "user",
    "password": "pass"
  }
}
```

Supported upstream proxies:

- `http://host:port` with optional Basic proxy auth
- `socks5://host:port` with optional username/password auth

Do not log upstream proxy credentials, `Proxy-Authorization`, or full upstream proxy URLs containing credentials.
