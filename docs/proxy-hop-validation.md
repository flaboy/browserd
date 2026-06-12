# Proxy Hop Validation

This validation uses temporary k8s pods only. It does not use local Docker.

Topology:

```text
Chromium in temporary browserd pod
-> browserd local HTTP proxy adapter
-> Cloudflare Worker WebSocket
-> Worker outbound TCP
-> temporary authenticated proxy pod
-> test target
```

## Prerequisites

- A deployed Worker running `worker/proxy-hop-worker.js`.
- `PROXY_HOP_TOKEN` configured on that Worker.
- A browserd image containing the proxy-hop implementation.
- Use `/Users/wanglei/Library/bin/kubectl` for k8s operations.

## Temporary Authenticated Proxy Pod

Start a temporary proxy pod that exposes:

- HTTP proxy with Basic auth on `18080`
- SOCKS5 proxy with username/password auth on `18081`
- an internal test target host `proxy-auth-check.local`

Use test-only credentials:

```text
proxyuser / proxypass
```

Create the proxy pod and service with `kubectl apply -f -`. The proxy implementation can be the same Python test proxy used during investigation: it must require auth, log only auth success/failure and target host, and never log passwords.

## Temporary Browserd Pod

Start a separate browserd pod with:

```text
BROWSERD_PORT=7011
BROWSERD_PROFILE_STORE=local
BROWSERD_PROXY_HOP=cloudflare-worker
BROWSERD_PROXY_WORKER_URL=wss://<worker-host>/tunnel
BROWSERD_PROXY_WORKER_TOKEN=<token>
```

Do not modify the production `aiworker-browserd` deployment for this validation.

## Session Validation

Exec into the temporary browserd pod and create two sessions:

```bash
/Users/wanglei/Library/bin/kubectl --context octopus-01 -n aiworker exec browserd-proxy-hop-test -- sh -lc 'python3 /tmp/create_session_and_navigate.py http'
/Users/wanglei/Library/bin/kubectl --context octopus-01 -n aiworker exec browserd-proxy-hop-test -- sh -lc 'python3 /tmp/create_session_and_navigate.py socks5'
```

Expected:

- HTTP upstream proxy session navigates to `http://proxy-auth-check.local/`.
- SOCKS5 upstream proxy session navigates to `http://proxy-auth-check.local/`.
- Both navigations return title `Proxy Auth Check`.
- Temporary proxy pod logs show authenticated HTTP/SOCKS5 upstream handshakes.
- browserd pod logs do not include upstream proxy passwords.
- Worker logs do not include upstream proxy passwords or `Proxy-Authorization`.

## Cleanup

```bash
/Users/wanglei/Library/bin/kubectl --context octopus-01 -n aiworker delete pod browserd-proxy-hop-test proxy-auth-test --ignore-not-found=true
/Users/wanglei/Library/bin/kubectl --context octopus-01 -n aiworker delete svc proxy-auth-test --ignore-not-found=true
/Users/wanglei/Library/bin/kubectl --context octopus-01 -n aiworker delete configmap proxy-auth-test-script --ignore-not-found=true
```

## Notes

- This validates the Worker hop path only. Existing direct HTTP proxy behavior remains the default when `BROWSERD_PROXY_HOP` is unset.
- `https://` proxy scheme remains unsupported and should continue to fail fast with `INVALID_PROXY_SERVER`.
- Do not trigger GitHub Actions or update production deployments without explicit approval.
