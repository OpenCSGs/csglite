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
	"runtime"
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
	// rpcWorkerHoldGrace is how long a hold survives with no traffic at all.
	// The node that split a model onto this one releases its hold when it
	// unloads, but a node that is killed outright never does, and until the
	// hold goes this machine keeps refusing ordinary work. Tensor traffic runs
	// continuously while a split model is loaded, so silence this long means
	// the far side is gone whatever it still believes.
	rpcWorkerHoldGrace = 5 * time.Minute
)

// rpcWorker is this node's local worker process. There is one, and it serves
// one split model at a time: llama.cpp addresses a worker as a single device
// with a fixed amount of memory behind it, and a second model split onto the
// same machine would ask for memory that is already spoken for.
type rpcWorker struct {
	mu       sync.Mutex
	cmd      *exec.Cmd
	port     int
	cacheDir string
	// pidFile records the running worker so that a node killed outright,
	// which cannot stop its own child, can find and stop it when it comes
	// back. Without it the orphan holds its share of a model that no longer
	// exists, on a machine that has forgotten it, until the next reboot.
	pidFile string
	logf    func(string, ...any)
	// holders counts, per member, the spans that member is running on this
	// worker. It is keyed by node rather than a bare count so that a member
	// which leaves the cluster without unloading its split model can have its
	// hold dropped on its own: it will never send the release itself, and
	// until the hold goes this machine refuses ordinary work it could do.
	holders map[string]int
	// conns is how many tunnel connections are piped into the worker right
	// now, and lastConn when there was last one. Together they are the only
	// first-hand evidence this machine has that a split model is still being
	// served; the holder's own word cannot be waited for, because a machine
	// that is switched off never withdraws it.
	conns    int
	lastConn time.Time
}

func newRPCWorker(cacheDir, pidFile string, logf func(string, ...any)) *rpcWorker {
	return &rpcWorker{cacheDir: cacheDir, pidFile: pidFile, logf: logf, holders: map[string]int{}}
}

// RPCWorkerBinaryPath finds the worker next to llama-server, which is where the
// asset bundle puts it. It is deliberately not searched for on PATH: the
// worker's wire protocol is not stable across llama.cpp builds, so a mismatched
// copy would fail at load time or, worse, compute the wrong answer.
func RPCWorkerBinaryPath(llamaServerPath string) (string, error) {
	if strings.TrimSpace(llamaServerPath) == "" {
		return "", errors.New("llama-server was not found, so its RPC worker cannot be located either")
	}
	name := rpcWorkerBinary
	if runtime.GOOS == "windows" {
		name += ".exe"
	}
	candidate := filepath.Join(filepath.Dir(llamaServerPath), name)
	info, err := os.Stat(candidate)
	if err != nil {
		return "", fmt.Errorf("%s is not installed beside llama-server; a model cannot be split across machines without it", rpcWorkerBinary)
	}
	if info.IsDir() || (runtime.GOOS != "windows" && info.Mode()&0o111 == 0) {
		return "", fmt.Errorf("%s is not executable", candidate)
	}
	return candidate, nil
}

// start launches the worker if it is not already running and returns the
// loopback port it listens on, recording holder as one of the members relying
// on it. The port is asked of the kernel here rather than left to the worker,
// which refuses to take zero and pick its own.
func (w *rpcWorker) start(ctx context.Context, binary, holder string) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.cmd != nil && w.port > 0 {
		// The worker can die on its own: it aborts when a load asks for more
		// memory than the machine has, which is exactly the situation it gets
		// used in. Handing its old port back would answer a member that then
		// finds the connection closed the moment it uses it, so the port is
		// proved before it is promised.
		if conn, err := net.DialTimeout("tcp", fmt.Sprintf("127.0.0.1:%d", w.port), 2*time.Second); err == nil {
			_ = conn.Close()
			w.hold(holder)
			return w.port, nil
		}
		w.logf("cluster: the RPC worker on 127.0.0.1:%d is no longer answering; starting a new one", w.port)
		w.stopLocked()
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

	// One goroutine owns the process's end, so nothing else calls Wait and
	// nothing reads its exit state as it is being written. The worker aborts
	// rather than returns when a machine cannot give it the memory a load asks
	// for, and this is what notices, whether that happens while it is starting
	// or hours into a split model.
	exited := make(chan struct{})
	go func() {
		_ = cmd.Wait()
		close(exited)
		w.mu.Lock()
		defer w.mu.Unlock()
		if w.cmd == cmd {
			w.logf("cluster: the RPC worker exited: %s", tail.String())
			w.cmd = nil
			w.port = 0
			w.holders = map[string]int{}
			w.conns = 0
			w.forgetPID()
		}
	}()

	deadline := time.Now().Add(rpcWorkerStartTimeout)
	for time.Now().Before(deadline) {
		if ctx.Err() != nil {
			_ = cmd.Process.Kill()
			return 0, ctx.Err()
		}
		select {
		case <-exited:
			return 0, fmt.Errorf("%s exited before it started listening: %s", rpcWorkerBinary, tail.String())
		default:
		}
		if conn, err := net.DialTimeout("tcp", fmt.Sprintf("127.0.0.1:%d", port), time.Second); err == nil {
			_ = conn.Close()
			w.cmd = cmd
			w.port = port
			w.hold(holder)
			w.recordPID(cmd.Process.Pid, binary)
			w.logf("cluster: RPC worker listening on 127.0.0.1:%d, reachable only through the cluster channel", port)
			return port, nil
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

// hold records one more span from a member. The caller holds the lock.
func (w *rpcWorker) hold(holder string) {
	if w.holders == nil {
		w.holders = map[string]int{}
	}
	w.holders[holder]++
	if w.conns == 0 {
		w.lastConn = time.Now()
	}
}

// connOpened and connClosed bracket one tunnel connection.
func (w *rpcWorker) connOpened() {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.conns++
	w.lastConn = time.Now()
}

func (w *rpcWorker) connClosed() {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.conns > 0 {
		w.conns--
	}
	w.lastConn = time.Now()
}

// releaseStale drops every hold when nothing has been served for a while. It
// is the backstop for a member that is powered off mid-split: it will never
// send a release, and this machine would go on refusing work it could do.
func (w *rpcWorker) releaseStale(now time.Time) []string {
	w.mu.Lock()
	defer w.mu.Unlock()
	if len(w.holders) == 0 || w.conns > 0 || now.Sub(w.lastConn) < rpcWorkerHoldGrace {
		return nil
	}
	var dropped []string
	for holder := range w.holders {
		dropped = append(dropped, holder)
		delete(w.holders, holder)
	}
	w.stopIfUnheldLocked()
	return dropped
}

// heldByOther names a member, other than the one asking, that already has a
// model split onto this node. One worker serves one set of weights: a second
// model split onto the same machine would ask it for memory the machine does
// not have, and llama.cpp's worker answers that by aborting, which would take
// the first model down with it.
func (w *rpcWorker) heldByOther(holder string) (string, bool) {
	w.mu.Lock()
	defer w.mu.Unlock()
	for other := range w.holders {
		if other != holder {
			return other, true
		}
	}
	return "", false
}

// users counts the spans currently held. The caller holds the lock.
func (w *rpcWorker) users() int {
	n := 0
	for _, c := range w.holders {
		n += c
	}
	return n
}

// release marks one of a member's spans as finished. The process stays up: a
// second split of the same model would otherwise pay the whole transfer again.
func (w *rpcWorker) release(holder string) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.holders[holder] > 1 {
		w.holders[holder]--
	} else {
		delete(w.holders, holder)
	}
	w.stopIfUnheldLocked()
}

// stopIfUnheldLocked stops the worker once no span is using it. The tensors it
// holds are the whole point of stopping promptly: they are this machine's
// memory, and the cache that makes a reload quick is on disk, so keeping the
// process alive would buy a second or two and cost gigabytes. It also means a
// worker that has wedged itself is never reused.
func (w *rpcWorker) stopIfUnheldLocked() {
	if w.cmd == nil || w.users() > 0 {
		return
	}
	w.logf("cluster: no model is split onto this node any more; stopping the RPC worker")
	w.stopLocked()
}

// releaseGone drops the holds of members that have left the cluster. A node
// removed while its split model is loaded never sends a release, and its hold
// would otherwise keep this machine out of the scheduler's reach for as long
// as it ran.
func (w *rpcWorker) releaseGone(stillPaired func(string) bool) []string {
	w.mu.Lock()
	defer w.mu.Unlock()
	var dropped []string
	for holder := range w.holders {
		if !stillPaired(holder) {
			delete(w.holders, holder)
			dropped = append(dropped, holder)
		}
	}
	if len(dropped) > 0 {
		w.stopIfUnheldLocked()
	}
	return dropped
}

// stop shuts the worker down unconditionally, for node shutdown.
func (w *rpcWorker) stop() {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.stopLocked()
}

// stopLocked kills the worker and forgets it. It does not wait for the
// process: the goroutine started with it owns that, and calling Wait from two
// places races over one exit status.
func (w *rpcWorker) stopLocked() {
	if w.cmd == nil {
		return
	}
	if w.cmd.Process != nil {
		_ = w.cmd.Process.Kill()
	}
	w.cmd = nil
	w.port = 0
	w.holders = map[string]int{}
	w.conns = 0
	w.forgetPID()
}

// inUse reports whether a span is currently holding this worker. It is what
// the scheduler is told about, rather than whether the process exists: the
// worker allocates its share of the weights per connection and frees them when
// the split model is unloaded, so a released worker is an idle process holding
// nothing and its machine can take ordinary work again at once.
func (w *rpcWorker) inUse() bool {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.cmd != nil && w.users() > 0
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

// recordPID and forgetPID maintain the note of which process is ours.
func (w *rpcWorker) recordPID(pid int, binary string) {
	if w.pidFile == "" {
		return
	}
	line := fmt.Sprintf("%d %s\n", pid, binary)
	if err := os.WriteFile(w.pidFile, []byte(line), 0o600); err != nil {
		w.logf("cluster: recording the RPC worker's process id: %v", err)
	}
}

func (w *rpcWorker) forgetPID() {
	if w.pidFile == "" {
		return
	}
	if err := os.Remove(w.pidFile); err != nil && !os.IsNotExist(err) {
		w.logf("cluster: clearing the RPC worker's process id: %v", err)
	}
}

// stopOrphan stops a worker left behind by a previous run of this node. A node
// that is killed outright, or a machine that loses power, never gets to stop
// its own child, and the child goes on holding its share of a model that no
// longer exists.
//
// The process is only stopped when it is still the same program: the recorded
// id alone would, after enough churn, eventually belong to something else
// entirely.
func (w *rpcWorker) stopOrphan() {
	if w.pidFile == "" {
		return
	}
	raw, err := os.ReadFile(w.pidFile)
	if err != nil {
		return
	}
	fields := strings.SplitN(strings.TrimSpace(string(raw)), " ", 2)
	pid, err := strconv.Atoi(fields[0])
	if err != nil || pid <= 1 {
		w.forgetPID()
		return
	}
	binary := ""
	if len(fields) > 1 {
		binary = fields[1]
	}
	if !processIsWorker(pid, binary) {
		w.forgetPID()
		return
	}
	proc, err := os.FindProcess(pid)
	if err != nil {
		w.forgetPID()
		return
	}
	if err := proc.Kill(); err != nil {
		w.logf("cluster: stopping the RPC worker left behind by a previous run (pid %d): %v", pid, err)
	} else {
		w.logf("cluster: stopped the RPC worker left behind by a previous run (pid %d); the memory it held is free again", pid)
	}
	w.forgetPID()
}

// processIsWorker reports whether pid is still running the worker binary this
// node started. It asks the operating system for the process's own command,
// which is the only thing that tells one reused process id from another.
func processIsWorker(pid int, binary string) bool {
	if runtime.GOOS == "windows" {
		// No cheap equivalent without more machinery than this is worth; a
		// Windows node leaves the orphan alone rather than risk the wrong one.
		return false
	}
	out, err := exec.Command("ps", "-p", strconv.Itoa(pid), "-o", "comm=").Output()
	if err != nil {
		return false
	}
	name := strings.TrimSpace(string(out))
	if name == "" {
		return false
	}
	if binary != "" && name == binary {
		return true
	}
	return filepath.Base(name) == rpcWorkerBinary
}
