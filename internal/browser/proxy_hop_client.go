package browser

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"sync"

	"github.com/gobwas/ws"
	"github.com/gobwas/ws/wsutil"
)

type proxyHopClientOptions struct {
	WorkerURL   string
	WorkerToken string
}

type proxyHopClient struct {
	opts proxyHopClientOptions
}

func newProxyHopClient(opts proxyHopClientOptions) *proxyHopClient {
	return &proxyHopClient{opts: opts}
}

func (c *proxyHopClient) Open(ctx context.Context, id string, target string, proxy ProxyConfig) (io.ReadWriteCloser, error) {
	header := http.Header{}
	if c.opts.WorkerToken != "" {
		header.Set("Authorization", "Bearer "+c.opts.WorkerToken)
	}
	conn, br, _, err := ws.Dialer{
		Header: ws.HandshakeHeaderHTTP(header),
	}.Dial(ctx, c.opts.WorkerURL)
	if err != nil {
		return nil, fmt.Errorf("%w: worker websocket dial failed: %v", ErrProxyHopConnectFailed, err)
	}

	tunnel := newWebSocketTunnel(conn, br)
	req := newProxyHopOpenRequest(id, target, proxy)
	reqBytes, err := json.Marshal(req)
	if err != nil {
		_ = tunnel.Close()
		return nil, err
	}
	if err := wsutil.WriteClientText(conn, reqBytes); err != nil {
		_ = tunnel.Close()
		return nil, fmt.Errorf("%w: worker open request failed: %v", ErrProxyHopConnectFailed, err)
	}

	respBytes, err := tunnel.readText()
	if err != nil {
		_ = tunnel.Close()
		return nil, fmt.Errorf("%w: worker open result failed: %v", ErrProxyHopConnectFailed, err)
	}
	var resp proxyHopOpenResponse
	if err := json.Unmarshal(respBytes, &resp); err != nil {
		_ = tunnel.Close()
		return nil, fmt.Errorf("%w: invalid worker open result: %v", ErrProxyHopConnectFailed, err)
	}
	if !resp.OK {
		_ = tunnel.Close()
		return nil, mapProxyHopOpenError(resp.Error)
	}
	return tunnel, nil
}

func mapProxyHopOpenError(detail string) error {
	switch detail {
	case "UPSTREAM_PROXY_AUTH_FAILED":
		return ErrUpstreamProxyAuthFailed
	case "UPSTREAM_PROXY_CONNECT_FAILED":
		return ErrUpstreamProxyConnectFail
	default:
		if detail == "" {
			return ErrProxyHopConnectFailed
		}
		return fmt.Errorf("%w: %s", ErrProxyHopConnectFailed, detail)
	}
}

type webSocketTunnel struct {
	conn net.Conn
	rw   io.ReadWriter

	readMu  sync.Mutex
	writeMu sync.Mutex
	readBuf []byte
}

func newWebSocketTunnel(conn net.Conn, br *bufio.Reader) *webSocketTunnel {
	var reader io.Reader = conn
	if br != nil && br.Buffered() > 0 {
		reader = io.MultiReader(br, conn)
	}
	return &webSocketTunnel{
		conn: conn,
		rw:   websocketReadWriter{reader: reader, writer: conn},
	}
}

func (t *webSocketTunnel) Read(p []byte) (int, error) {
	t.readMu.Lock()
	defer t.readMu.Unlock()

	for len(t.readBuf) == 0 {
		data, op, err := wsutil.ReadServerData(t.rw)
		if err != nil {
			return 0, err
		}
		if op != ws.OpBinary {
			continue
		}
		t.readBuf = data
	}
	n := copy(p, t.readBuf)
	t.readBuf = t.readBuf[n:]
	return n, nil
}

func (t *webSocketTunnel) Write(p []byte) (int, error) {
	t.writeMu.Lock()
	defer t.writeMu.Unlock()

	if err := wsutil.WriteClientBinary(t.conn, p); err != nil {
		return 0, err
	}
	return len(p), nil
}

func (t *webSocketTunnel) Close() error {
	return t.conn.Close()
}

func (t *webSocketTunnel) readText() ([]byte, error) {
	t.readMu.Lock()
	defer t.readMu.Unlock()

	for {
		data, op, err := wsutil.ReadServerData(t.rw)
		if err != nil {
			return nil, err
		}
		if op == ws.OpText {
			return data, nil
		}
	}
}

type websocketReadWriter struct {
	reader io.Reader
	writer io.Writer
}

func (rw websocketReadWriter) Read(p []byte) (int, error) {
	return rw.reader.Read(p)
}

func (rw websocketReadWriter) Write(p []byte) (int, error) {
	return rw.writer.Write(p)
}
