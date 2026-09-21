// Copyright (c) OpenCSG. Licensed under the CSGLite Enterprise Edition License.
// See ee/LICENSE for details.

package cluster

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"
)

// A model too large for any one machine is run by splitting its weights across
// several, using llama.cpp's RPC backend: the node that serves the request
// reads the weights and hands tensor operations to a worker process on each
// participating machine.
//
// Upstream ships that worker with no authentication at all and says plainly
// never to run it on an open network, so it is bound to loopback here and is
// only ever reached through the cluster's own mutual-TLS channel (see
// rpctunnel.go). Nothing listens on the network that a certificate has not
// already been checked for.
const (
	// rpcWorkerBinary is the upstream worker, expected beside llama-server.
	rpcWorkerBinary = "ggml-rpc-server"
	// rpcWorkerStartTimeout bounds how long a worker may take to report its
	// port before it is given up on.
	rpcWorkerStartTimeout = 30 * time.Second
	// rpcWorkerIdleTimeout stops a worker nobody has used, so a machine that
	// took part in one split does not keep a process and its tensor cache
	// warm for ever.
	rpcWorkerIdleTimeout = 30 * time.Minute
)

// rpcWorker is this node's local worker process. One is enough: llama.cpp
// addresses a worker as a single device, and several models may share it.
type rpcWorker struct {
	mu       sync.Mutex
	cmd      *exec.Cmd
	port     int
	cacheDir string
	logf     func(string, ...any)
	lastUsed time.Time
	users    int
}

func newRPCWorker(cacheDir string, logf func(string, ...any)) *rpcWorker {
	return &rpcWorker{cacheDir: cacheDir, logf: logf}
}

// RPCWorkerBinaryPath finds the worker next to llama-server, which is where the
// asset bundle puts it. It is deliberately not searched for on PATH: the
// worker's wire protocol is not stable across llama.cpp builds, so a mismatched
// copy would fail at load time or, worse, compute the wrong answer.
func RPCWorkerBinaryPath(llamaServerPath string) (string, error) {
	if strings.TrimSpace(llamaServerPath) == "" {
		return "", errors.New("llama-server was not found, so its RPC worker cannot be located either")
	}
	candidate := filepath.Join(filepath.Dir(llamaServerPath), rpcWorkerBinary)
	info, err := os.Stat(candidate)
	if err != nil {
		return "", fmt.Errorf("%s is not installed beside llama-server; a model cannot be split across machines without it", rpcWorkerBinary)
	}
	if info.IsDir() || info.Mode()&0o111 == 0 {
		return "", fmt.Errorf("%s is not executable", candidate)
	}
	return candidate, nil
}

// start launches the worker if it is not already running and returns the
// loopback port it listens on. The port is ephemeral and read back from the
// worker's own output rather than chosen here, so two nodes on one machine
// cannot collide.
func (w *rpcWorker) start(ctx context.Context, binary string) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.cmd != nil && w.cmd.Process != nil && w.port > 0 {
		w.lastUsed = time.Now()
		w.users++
		return w.port, nil
	}

	if w.cacheDir != "" {
		if err := os.MkdirAll(w.cacheDir, 0o700); err != nil {
			return 0, fmt.Errorf("creating the RPC tensor cache directory: %w", err)
		}
	}
	// The worker will not take port 0 and pick its own, so a free one is
	// found here and handed to it. The gap between closing the probe listener
	// and the worker binding is small, and a failure to bind is reported
	// rather than retried silently.
	port, err := freeLoopbackPort()
	if err != nil {
		return 0, err
	}
	args := []string{"-H", "127.0.0.1", "-p", strconv.Itoa(port)}
	if w.cacheDir != "" {
		// Without a cache every load pushes the whole shard over the network
		// again: a 19 GB model means about 13 GB to two workers, which is
		// minutes on a wireless link. With it, only the first load pays.
		// The flag switches the cache on and takes no value; where it lives is
		// set through the environment.
		args = append(args, "-c")
	}
	cmd := exec.CommandContext(ctx, binary, args...)
	if w.cacheDir != "" {
		cmd.Env = append(os.Environ(), "LLAMA_CACHE="+w.cacheDir)
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return 0, err
	}
	cmd.Stderr = cmd.Stdout
	if err := cmd.Start(); err != nil {
		return 0, fmt.Errorf("starting %s: %w", rpcWorkerBinary, err)
	}

	// Keep the worker's own output in the node log: when a split fails, what
	// it said is usually the whole explanation.
	var tail lastLines
	go func() {
		scanner := bufio.NewScanner(stdout)
		for scanner.Scan() {
			line := scanner.Text()
			tail.add(line)
			w.logf("cluster: rpc worker: %s", line)
		}
	}()

	deadline := time.Now().Add(rpcWorkerStartTimeout)
	for time.Now().Before(deadline) {
		if ctx.Err() != nil {
			_ = cmd.Process.Kill()
			return 0, ctx.Err()
		}
		if conn, err := net.DialTimeout("tcp", fmt.Sprintf("127.0.0.1:%d", port), time.Second); err == nil {
			_ = conn.Close()
			w.cmd = cmd
			w.port = port
			w.lastUsed = time.Now()
			w.users++
			w.logf("cluster: RPC worker listening on 127.0.0.1:%d, reachable only through the cluster channel", port)
			return port, nil
		}
		if cmd.ProcessState != nil && cmd.ProcessState.Exited() {
			break
		}
		time.Sleep(200 * time.Millisecond)
	}
	_ = cmd.Process.Kill()
	return 0, fmt.Errorf("%s did not start listening on 127.0.0.1:%d within %s: %s",
		rpcWorkerBinary, port, rpcWorkerStartTimeout, tail.String())
}

// freeLoopbackPort asks the kernel for a port nothing is using.
func freeLoopbackPort() (int, error) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return 0, fmt.Errorf("finding a free port for the RPC worker: %w", err)
	}
	defer ln.Close()
	return ln.Addr().(*net.TCPAddr).Port, nil
}

// lastLines keeps the tail of a process's output so a failure can quote it.
type lastLines struct {
	mu    sync.Mutex
	lines []string
}

func (l *lastLines) add(s string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.lines = append(l.lines, s)
	if len(l.lines) > 5 {
		l.lines = l.lines[len(l.lines)-5:]
	}
}

func (l *lastLines) String() string {
	l.mu.Lock()
	defer l.mu.Unlock()
	if len(l.lines) == 0 {
		return "it said nothing"
	}
	return strings.Join(l.lines, "; ")
}

// release marks one user of the worker as finished. The process stays up: a
// second split of the same model would otherwise pay the whole transfer again.
func (w *rpcWorker) release() {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.users > 0 {
		w.users--
	}
	w.lastUsed = time.Now()
}

// stopIfIdle shuts the worker down when nothing has used it for a while.
func (w *rpcWorker) stopIfIdle(now time.Time) bool {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.cmd == nil || w.users > 0 || now.Sub(w.lastUsed) < rpcWorkerIdleTimeout {
		return false
	}
	w.stopLocked()
	return true
}

// stop shuts the worker down unconditionally, for node shutdown.
func (w *rpcWorker) stop() {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.stopLocked()
}

func (w *rpcWorker) stopLocked() {
	if w.cmd == nil {
		return
	}
	if w.cmd.Process != nil {
		_ = w.cmd.Process.Kill()
		_ = w.cmd.Wait()
	}
	w.cmd = nil
	w.port = 0
	w.users = 0
}

// running reports the worker's port, or zero when it is not up.
func (w *rpcWorker) running() int {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.cmd == nil {
		return 0
	}
	return w.port
}
