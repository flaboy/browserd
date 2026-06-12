import { connect } from "cloudflare:sockets";

const encoder = new TextEncoder();
const decoder = new TextDecoder();

export default {
  async fetch(request, env) {
    const url = new URL(request.url);
    if (url.pathname !== "/tunnel") {
      return new Response("not found", { status: 404 });
    }
    if (request.headers.get("Upgrade") !== "websocket") {
      return new Response("websocket upgrade required", { status: 426 });
    }
    const expected = env.PROXY_HOP_TOKEN;
    const got = request.headers.get("Authorization");
    if (!expected || got !== `Bearer ${expected}`) {
      return new Response("unauthorized", { status: 401 });
    }

    const pair = new WebSocketPair();
    const client = pair[0];
    const server = pair[1];
    server.accept();
    handleTunnel(server).catch((error) => {
      console.log("proxy-hop tunnel failed", safeError(error));
      tryClose(server, 1011, "proxy hop failed");
    });
    return new Response(null, { status: 101, webSocket: client });
  },
};

async function handleTunnel(ws) {
  const first = await readFirstMessage(ws);
  const req = JSON.parse(first);
  if (req.type !== "open" || !req.id || !req.target || !req.upstreamProxy) {
    ws.send(JSON.stringify({ type: "open_result", id: req.id || "", ok: false, error: "INVALID_OPEN_REQUEST" }));
    tryClose(ws, 1008, "invalid open request");
    return;
  }

  let socket;
  try {
    socket = await openUpstream(req);
  } catch (error) {
    const code = classifyOpenError(error);
    ws.send(JSON.stringify({ type: "open_result", id: req.id, ok: false, error: code }));
    tryClose(ws, 1011, code);
    return;
  }

  ws.send(JSON.stringify({ type: "open_result", id: req.id, ok: true }));
  pipeWebSocketToSocket(ws, socket).catch((error) => {
    console.log("proxy-hop ws->tcp failed", safeError(error));
    tryClose(ws, 1011, "write failed");
  });
  await pipeSocketToWebSocket(socket, ws);
}

function readFirstMessage(ws) {
  return new Promise((resolve, reject) => {
    const timeout = setTimeout(() => reject(new Error("open request timeout")), 10000);
    const onMessage = (event) => {
      clearTimeout(timeout);
      ws.removeEventListener("message", onMessage);
      if (typeof event.data !== "string") {
        reject(new Error("first message must be text"));
        return;
      }
      resolve(event.data);
    };
    ws.addEventListener("message", onMessage);
  });
}

async function openUpstream(req) {
  const upstream = new URL(req.upstreamProxy);
  if (upstream.protocol === "http:") {
    return openHTTPProxy(upstream, req);
  }
  if (upstream.protocol === "socks5:") {
    return openSOCKS5Proxy(upstream, req);
  }
  throw new Error("UNSUPPORTED_UPSTREAM_PROXY");
}

async function openHTTPProxy(upstream, req) {
  const socket = connect({ hostname: upstream.hostname, port: Number(upstream.port) });
  const writer = socket.writable.getWriter();
  const reader = socket.readable.getReader();
  const auth = req.upstreamProxyAuth;
  const headers = [
    `CONNECT ${req.target} HTTP/1.1`,
    `Host: ${req.target}`,
    "Proxy-Connection: keep-alive",
  ];
  if (auth?.username || auth?.password) {
    headers.push(`Proxy-Authorization: Basic ${btoa(`${auth.username || ""}:${auth.password || ""}`)}`);
  }
  headers.push("", "");
  await writer.write(encoder.encode(headers.join("\r\n")));
  const response = await readHTTPHeaders(reader);
  if (!response.startsWith("HTTP/1.1 200") && !response.startsWith("HTTP/1.0 200")) {
    try {
      await socket.close();
    } catch {}
    if (response.includes(" 407 ")) {
      throw new Error("UPSTREAM_PROXY_AUTH_FAILED");
    }
    throw new Error("UPSTREAM_PROXY_CONNECT_FAILED");
  }
  return { socket, reader, writer };
}

async function openSOCKS5Proxy(upstream, req) {
  const socket = connect({ hostname: upstream.hostname, port: Number(upstream.port) });
  const writer = socket.writable.getWriter();
  const reader = socket.readable.getReader();
  const auth = req.upstreamProxyAuth;
  if (auth?.username || auth?.password) {
    await writer.write(new Uint8Array([0x05, 0x01, 0x02]));
  } else {
    await writer.write(new Uint8Array([0x05, 0x01, 0x00]));
  }
  const method = await readExact(reader, 2);
  if (method[0] !== 0x05 || method[1] === 0xff) {
    throw new Error("UPSTREAM_PROXY_AUTH_FAILED");
  }
  if (method[1] === 0x02) {
    const username = encodeUTF8(auth?.username || "");
    const password = encodeUTF8(auth?.password || "");
    if (username.length > 255 || password.length > 255) {
      throw new Error("UPSTREAM_PROXY_AUTH_FAILED");
    }
    await writer.write(concatBytes(
      new Uint8Array([0x01, username.length]),
      username,
      new Uint8Array([password.length]),
      password,
    ));
    const authResult = await readExact(reader, 2);
    if (authResult[0] !== 0x01 || authResult[1] !== 0x00) {
      throw new Error("UPSTREAM_PROXY_AUTH_FAILED");
    }
  }

  const { host, port } = splitTarget(req.target);
  const hostBytes = encodeUTF8(host);
  if (hostBytes.length > 255) {
    throw new Error("UPSTREAM_PROXY_CONNECT_FAILED");
  }
  await writer.write(concatBytes(
    new Uint8Array([0x05, 0x01, 0x00, 0x03, hostBytes.length]),
    hostBytes,
    new Uint8Array([(port >> 8) & 0xff, port & 0xff]),
  ));
  const head = await readExact(reader, 4);
  if (head[0] !== 0x05 || head[1] !== 0x00) {
    throw new Error("UPSTREAM_PROXY_CONNECT_FAILED");
  }
  if (head[3] === 0x01) {
    await readExact(reader, 4);
  } else if (head[3] === 0x03) {
    const len = await readExact(reader, 1);
    await readExact(reader, len[0]);
  } else if (head[3] === 0x04) {
    await readExact(reader, 16);
  } else {
    throw new Error("UPSTREAM_PROXY_CONNECT_FAILED");
  }
  await readExact(reader, 2);
  return { socket, reader, writer };
}

async function pipeWebSocketToSocket(ws, upstream) {
  ws.addEventListener("message", async (event) => {
    if (typeof event.data === "string") {
      return;
    }
    const chunk = event.data instanceof ArrayBuffer
      ? new Uint8Array(event.data)
      : new Uint8Array(await event.data.arrayBuffer());
    await upstream.writer.write(chunk);
  });
  ws.addEventListener("close", () => {
    try {
      upstream.socket.close();
    } catch {}
  });
}

async function pipeSocketToWebSocket(upstream, ws) {
  try {
    while (true) {
      const { value, done } = await upstream.reader.read();
      if (done) {
        break;
      }
      if (value?.byteLength) {
        ws.send(value);
      }
    }
  } finally {
    tryClose(ws, 1000, "upstream closed");
    try {
      await upstream.socket.close();
    } catch {}
  }
}

async function readHTTPHeaders(reader) {
  let chunks = new Uint8Array(0);
  while (chunks.length < 65536) {
    const { value, done } = await reader.read();
    if (done) {
      break;
    }
    chunks = concatBytes(chunks, value);
    const text = decoder.decode(chunks);
    if (text.includes("\r\n\r\n")) {
      return text.slice(0, text.indexOf("\r\n\r\n") + 4);
    }
  }
  throw new Error("UPSTREAM_PROXY_CONNECT_FAILED");
}

async function readExact(reader, size) {
  let out = new Uint8Array(0);
  while (out.length < size) {
    const { value, done } = await reader.read();
    if (done) {
      throw new Error("UPSTREAM_PROXY_CONNECT_FAILED");
    }
    out = concatBytes(out, value);
  }
  if (out.length > size) {
    throw new Error("UPSTREAM_PROXY_CONNECT_FAILED");
  }
  return out;
}

function splitTarget(target) {
  const index = target.lastIndexOf(":");
  if (index <= 0) {
    throw new Error("UPSTREAM_PROXY_CONNECT_FAILED");
  }
  const host = target.slice(0, index);
  const port = Number(target.slice(index + 1));
  if (!host || !Number.isInteger(port) || port <= 0 || port > 65535) {
    throw new Error("UPSTREAM_PROXY_CONNECT_FAILED");
  }
  return { host, port };
}

function encodeUTF8(value) {
  return encoder.encode(value);
}

function concatBytes(...parts) {
  const size = parts.reduce((sum, part) => sum + part.length, 0);
  const out = new Uint8Array(size);
  let offset = 0;
  for (const part of parts) {
    out.set(part, offset);
    offset += part.length;
  }
  return out;
}

function classifyOpenError(error) {
  const message = String(error?.message || error);
  if (message.includes("UPSTREAM_PROXY_AUTH_FAILED")) {
    return "UPSTREAM_PROXY_AUTH_FAILED";
  }
  if (message.includes("UPSTREAM_PROXY_CONNECT_FAILED")) {
    return "UPSTREAM_PROXY_CONNECT_FAILED";
  }
  return "PROXY_HOP_CONNECT_FAILED";
}

function safeError(error) {
  return String(error?.message || error);
}

function tryClose(ws, code, reason) {
  try {
    ws.close(code, reason);
  } catch {}
}
