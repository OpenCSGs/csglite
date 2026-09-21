// Copyright (c) OpenCSG. Licensed under the CSGLite Enterprise Edition License.
// See ee/LICENSE for details.

package cluster

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
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

var rpcPortPattern = regexp.MustCompile(`127\.0\.0\.1:(\d+)`)

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
	args := []string{"-H", "127.0.0.1", "-p", "0"}
	if w.cacheDir != "" {
		// Without a cache every load pushes the whole shard over the network
		// again: a 19 GB model means about 13 GB to two workers, which is
		// minutes on a wireless link. With it, only the first load pays.
		args = append(args, "-c", w.cacheDir)
	}
	cmd := exec.CommandContext(ctx, binary, args...)
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return 0, err
	}
	cmd.Stderr = cmd.Stdout
	if err := cmd.Start(); err != nil {
		return 0, fmt.Errorf("starting %s: %w", rpcWorkerBinary, err)
	}

	portCh := make(chan int, 1)
	go func() {
		scanner := bufio.NewScanner(stdout)
		reported := false
		for scanner.Scan() {
			line := scanner.Text()
			if !reported {
				if m := rpcPortPattern.FindStringSubmatch(line); len(m) == 2 {
					if p, err := strconv.Atoi(m[1]); err == nil && p > 0 {
						reported = true
						portCh <- p
					}
				}
			}
			w.logf("cluster: rpc worker: %s", line)
		}
	}()

	select {
	case port := <-portCh:
		w.cmd = cmd
		w.port = port
		w.lastUsed = time.Now()
		w.users++
		w.logf("cluster: RPC worker listening on 127.0.0.1:%d, reachable only through the cluster channel", port)
		return port, nil
	case <-time.After(rpcWorkerStartTimeout):
		_ = cmd.Process.Kill()
		return 0, fmt.Errorf("%s did not report a port within %s", rpcWorkerBinary, rpcWorkerStartTimeout)
	case <-ctx.Done():
		_ = cmd.Process.Kill()
		return 0, ctx.Err()
	}
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
