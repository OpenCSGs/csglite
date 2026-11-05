// Copyright (c) OpenCSG. Licensed under the CSGLite Enterprise Edition License.
// See ee/LICENSE for details.

package cluster

import (
	"bufio"
	"crypto/tls"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"
)

// The RPC worker speaks a plain, unauthenticated TCP protocol, and upstream is
// explicit that it must never be exposed on a network. It is therefore bound to
// loopback on the machine that runs it, and every byte between machines travels
// inside the cluster's own mutual-TLS connection, the same one that carries
// forwarded inference and is pinned to each member's certificate.
//
// The shape is a tunnel. On the node that serves the request a listener is
// opened on loopback and handed to llama-server as an ordinary --rpc endpoint.
// Each connection it makes is dialled through to the member as an HTTP request
// that both ends then stop treating as HTTP and use as a raw stream.

// tunnelHandshake is the response line a member sends before the stream turns
// into raw RPC traffic, so the caller knows the far side really did switch.
const tunnelHandshake = "CSGLITE-RPC-TUNNEL/1"

// handlePeerRPCTunnel connects a member to this node's local RPC worker. The
// request is authenticated the same way as every other peer call, by the client
// certificate that requirePeer has already checked.
func (a *peerAPI) handlePeerRPCTunnel(w http.ResponseWriter, r *http.Request) {
	port := a.m.rpcWorkerPort()
	if port == 0 {
		writeCodedError(w, http.StatusConflict, "this node is not running an RPC worker", "no_rpc_worker")
		return
	}
	hijacker, ok := w.(http.Hijacker)
	if !ok {
		writeCodedError(w, http.StatusInternalServerError, "this server cannot hand over the connection", "no_hijack")
		return
	}
	upstream, err := net.DialTimeout("tcp", fmt.Sprintf("127.0.0.1:%d", port), 5*time.Second)
	if err != nil {
		writeCodedError(w, http.StatusBadGateway, "the local RPC worker did not accept a connection", "rpc_worker_unreachable")
		return
	}
	conn, buf, err := hijacker.Hijack()
	if err != nil {
		upstream.Close()
		return
	}
	defer conn.Close()
	defer upstream.Close()
	a.m.trackWorkerConn()
	defer a.m.untrackWorkerConn()

	if _, err := buf.WriteString("HTTP/1.1 101 Switching Protocols\r\nUpgrade: " + tunnelHandshake + "\r\nConnection: Upgrade\r\n\r\n"); err != nil {
		return
	}
	if err := buf.Flush(); err != nil {
		return
	}
	// Anything the client already sent past the request headers belongs to the
	// worker, not to us.
	if n := buf.Reader.Buffered(); n > 0 {
		if pending, err := buf.Reader.Peek(n); err == nil {
			_, _ = upstream.Write(pending)
			_, _ = buf.Reader.Discard(n)
		}
	}
	pipe(conn, upstream)
}

// pipe copies in both directions until either side finishes, then closes both.
func pipe(a, b net.Conn) {
	var once sync.Once
	stop := func() {
		once.Do(func() {
			_ = a.Close()
			_ = b.Close()
		})
	}
	var wg sync.WaitGroup
	wg.Add(2)
	go func() { defer wg.Done(); defer stop(); _, _ = io.Copy(b, a) }()
	go func() { defer wg.Done(); defer stop(); _, _ = io.Copy(a, b) }()
	wg.Wait()
}

// rpcTunnel is a loopback endpoint on this node that stands in for a member's
// RPC worker. llama-server is given its address and never learns that the
// traffic crosses a machine boundary.
type rpcTunnel struct {
	member   Member
	addr     string
	listener net.Listener
	identity *Identity
	logf     func(string, ...any)

	mu     sync.Mutex
	closed bool
}

// Endpoint is the host:port to hand to llama-server's --rpc.
func (t *rpcTunnel) Endpoint() string { return t.listener.Addr().String() }

// openRPCTunnel starts a loopback listener whose connections are forwarded to
// mem's RPC worker over the cluster's mutual-TLS channel.
func (m *Manager) openRPCTunnel(mem Member, addr string) (*rpcTunnel, error) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return nil, fmt.Errorf("opening a local endpoint for %s: %w", mem.Name, err)
	}
	t := &rpcTunnel{member: mem, addr: addr, listener: ln, identity: m.identity, logf: m.logf}
	go t.serve()
	return t, nil
}

func (t *rpcTunnel) serve() {
	for {
		local, err := t.listener.Accept()
		if err != nil {
			return
		}
		go func() {
			remote, err := t.dial()
			if err != nil {
				t.logf("cluster: RPC tunnel to %s failed: %v", t.member.Name, err)
				_ = local.Close()
				return
			}
			pipe(local, remote)
		}()
	}
}

// dial opens one mutual-TLS connection to the member and turns it into a raw
// stream. The certificate is checked exactly as it is for every other call
// between members, so reaching the worker at all requires being a paired node.
func (t *rpcTunnel) dial() (net.Conn, error) {
	conn, err := tls.DialWithDialer(&net.Dialer{Timeout: 10 * time.Second}, "tcp", t.addr,
		clientTLSConfig(t.identity, t.member.UUID, t.member.CertFingerprint))
	if err != nil {
		return nil, err
	}
	req := "POST " + peerPathRPCTunnel + " HTTP/1.1\r\n" +
		"Host: " + t.addr + "\r\n" +
		"Connection: Upgrade\r\n" +
		"Upgrade: " + tunnelHandshake + "\r\n\r\n"
	if err := conn.SetDeadline(time.Now().Add(20 * time.Second)); err != nil {
		conn.Close()
		return nil, err
	}
	if _, err := conn.Write([]byte(req)); err != nil {
		conn.Close()
		return nil, err
	}
	reader := bufio.NewReader(conn)
	status, err := reader.ReadString('\n')
	if err != nil {
		conn.Close()
		return nil, err
	}
	if !strings.Contains(status, "101") {
		conn.Close()
		return nil, fmt.Errorf("%s refused the tunnel: %s", t.member.Name, strings.TrimSpace(status))
	}
	for {
		line, err := reader.ReadString('\n')
		if err != nil {
			conn.Close()
			return nil, err
		}
		if strings.TrimSpace(line) == "" {
			break
		}
	}
	if err := conn.SetDeadline(time.Time{}); err != nil {
		conn.Close()
		return nil, err
	}
	// The reader may hold bytes the worker already sent; keep them.
	if n := reader.Buffered(); n > 0 {
		head, _ := reader.Peek(n)
		return &prefixedConn{Conn: conn, prefix: append([]byte(nil), head...)}, nil
	}
	return conn, nil
}

func (t *rpcTunnel) Close() {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.closed {
		return
	}
	t.closed = true
	_ = t.listener.Close()
}

// prefixedConn replays bytes that were read into a buffer before the stream
// was handed on, so nothing the far side sent early is lost.
type prefixedConn struct {
	net.Conn
	prefix []byte
}

func (c *prefixedConn) Read(p []byte) (int, error) {
	if len(c.prefix) > 0 {
		n := copy(p, c.prefix)
		c.prefix = c.prefix[n:]
		return n, nil
	}
	return c.Conn.Read(p)
}

// probeTunnel checks that a tunnel endpoint reaches a live worker. Opening the
// connection is enough: the member dials its own worker before handing the
// stream over, so a refusal here means the worker is not there.
func probeTunnel(endpoint string) error {
	conn, err := net.DialTimeout("tcp", endpoint, 15*time.Second)
	if err != nil {
		return err
	}
	defer conn.Close()
	if err := conn.SetReadDeadline(time.Now().Add(2 * time.Second)); err != nil {
		return err
	}
	// A live worker says nothing until spoken to, so a read that times out is
	// the healthy answer; end of file means the far side hung up.
	var one [1]byte
	_, err = conn.Read(one[:])
	var netErr net.Error
	if errors.As(err, &netErr) && netErr.Timeout() {
		return nil
	}
	if err == io.EOF {
		return errors.New("the worker closed the connection immediately")
	}
	return nil
}
