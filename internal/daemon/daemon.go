// Package daemon keeps one `rclone serve` process running per enabled
// protocol and applies changes from the state file as they happen.
package daemon

import (
	"bufio"
	"crypto/sha256"
	"errors"
	"fmt"
	"io"
	"log"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/mattn/go-isatty"

	"github.com/nosini/rclone-proxy-tui/internal/rclone"
	"github.com/nosini/rclone-proxy-tui/internal/state"
)

const (
	pollInterval   = time.Second
	statusInterval = 5 * time.Second
	stopTimeout    = 5 * time.Second
	settleTime     = 2 * time.Second
	minBackoff     = 2 * time.Second
	maxBackoff     = time.Minute
	maxLogSize     = 10 << 20
	drainTimeout   = 2 * time.Minute
	shutdownDrain  = 60 * time.Second
)

type proc struct {
	proto     state.Protocol
	key       string // command line, to detect config changes
	cmd       *exec.Cmd
	done      chan struct{}
	started   time.Time
	stopping  bool
	tail      *tailBuffer
	backoff   time.Duration
	nextStart time.Time
	status    state.ProtoStatus

	rcSock     string // rclone rc socket, for pending upload counts
	draining   bool   // waiting for uploads before a restart/stop
	drainUntil time.Time
}

type exitEvent struct {
	p   *proc
	cmd *exec.Cmd
	err error
}

// Daemon supervises the rclone serve processes.
type Daemon struct {
	paths        state.Paths
	exe          string
	logger       *log.Logger
	logOut       io.Writer
	procs        map[state.Protocol]*proc
	st           *state.State
	stMod        time.Time
	configHash   [sha256.Size]byte
	configLoaded bool
	clients      map[string]string // username -> fingerprint
	bin          string            // rclone binary the version was read from
	exits        chan exitEvent
	status       state.Status
}

// Run starts the daemon and blocks until it receives SIGINT or SIGTERM.
func Run(paths state.Paths) error {
	if err := os.MkdirAll(paths.Dir, 0o700); err != nil {
		return err
	}
	lock, err := os.OpenFile(paths.DaemonLock(), os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return err
	}
	defer lock.Close()
	if err := syscall.Flock(int(lock.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		return fmt.Errorf("another daemon is already running for %s", paths.Dir)
	}

	logOut, err := openLog(paths.Log())
	if err != nil {
		return err
	}
	exe, err := os.Executable()
	if err != nil {
		return err
	}
	if resolved, err := filepath.EvalSymlinks(exe); err == nil {
		exe = resolved
	}

	d := &Daemon{
		paths:   paths,
		exe:     exe,
		logOut:  logOut,
		logger:  log.New(logOut, "", log.LstdFlags),
		procs:   map[state.Protocol]*proc{},
		clients: map[string]string{},
		exits:   make(chan exitEvent, 16),
		status: state.Status{
			PID:       os.Getpid(),
			Started:   time.Now(),
			Protocols: map[state.Protocol]*state.ProtoStatus{},
		},
	}
	if err := os.WriteFile(paths.PID(), []byte(strconv.Itoa(os.Getpid())+"\n"), 0o600); err != nil {
		return err
	}
	defer os.Remove(paths.PID())
	defer os.Remove(paths.Status())

	d.logger.Printf("daemon started (pid %d, state %s)", os.Getpid(), paths.Dir)
	return d.loop()
}

func openLog(path string) (io.Writer, error) {
	if st, err := os.Stat(path); err == nil && st.Size() > maxLogSize {
		_ = os.Rename(path, path+".1")
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		return nil, err
	}
	// Also log to stderr when someone is watching: a terminal, or journald
	// and other pipes. Not when detached with stderr on /dev/null.
	fi, err := os.Stderr.Stat()
	if err == nil && (isatty.IsTerminal(os.Stderr.Fd()) || fi.Mode()&os.ModeCharDevice == 0) {
		return io.MultiWriter(f, os.Stderr), nil
	}
	return f, nil
}

func (d *Daemon) loop() error {
	sigs := make(chan os.Signal, 4)
	signal.Notify(sigs, syscall.SIGINT, syscall.SIGTERM, syscall.SIGHUP)
	poll := time.NewTicker(pollInterval)
	defer poll.Stop()
	lastStatus := time.Time{}

	d.reconcile(true)
	for {
		select {
		case sig := <-sigs:
			if sig == syscall.SIGHUP {
				d.logger.Printf("SIGHUP: reloading")
				d.reconcile(true)
				continue
			}
			d.logger.Printf("%v: stopping", sig)
			d.stopAll()
			d.logger.Printf("daemon stopped")
			return nil
		case ev := <-d.exits:
			d.handleExit(ev)
			d.writeStatus()
		case <-poll.C:
			changed := d.reconcile(false)
			if changed || time.Since(lastStatus) > statusInterval {
				d.writeStatus()
				lastStatus = time.Now()
			}
		}
	}
}

// loadState re-reads the state file if it changed. It returns whether any
// client lost access or had its credentials or shares changed, in which case
// running servers must be restarted to drop rclone's cached logins.
func (d *Daemon) loadState(force bool) (changed, revoked bool) {
	fi, err := os.Stat(d.paths.State())
	var mod time.Time
	if err == nil {
		mod = fi.ModTime()
	}
	if !force && d.st != nil && mod.Equal(d.stMod) {
		return false, false
	}
	st, err := d.paths.Load()
	if err != nil {
		d.logger.Printf("failed to load state, keeping previous: %v", err)
		d.status.Message = err.Error()
		d.stMod = mod
		return false, false
	}
	d.status.Message = ""
	if st.RcloneBinary == "" {
		if bin, err := rclone.DetectBinary(); err == nil {
			st.RcloneBinary = bin
		}
	}
	newClients := map[string]string{}
	for _, c := range st.Clients {
		newClients[c.Username] = c.Fingerprint()
	}
	if d.st != nil {
		for user, fp := range d.clients {
			if newClients[user] != fp {
				revoked = true
				break
			}
		}
	}
	first := d.st == nil
	d.st, d.stMod, d.clients = st, mod, newClients
	if first || st.RcloneBinary != d.bin {
		d.bin = st.RcloneBinary
		d.updateVersion()
	}
	return true, revoked
}

func (d *Daemon) updateVersion() {
	r := &rclone.Rclone{Bin: d.st.RcloneBinary}
	v, err := r.Version()
	if err != nil {
		d.status.RcloneVersion = ""
		return
	}
	d.status.RcloneVersion = v
}

func (d *Daemon) reconcile(force bool) bool {
	changed, revoked := d.loadState(force)
	if d.st == nil {
		return false
	}
	if revoked {
		d.logger.Printf("client access changed: restarting servers to drop cached logins")
	}
	// rclone reads named remote definitions at startup, so changes to the
	// config contents need a restart even when state.json is unchanged.
	if config, err := os.ReadFile(d.st.RcloneConfig); err == nil || errors.Is(err, os.ErrNotExist) {
		hash := sha256.Sum256(config)
		if d.configLoaded && hash != d.configHash {
			revoked = true
			d.logger.Printf("rclone config changed: restarting servers")
		}
		d.configHash, d.configLoaded = hash, true
	}
	dirty := changed
	for _, proto := range state.AllProtocols {
		if d.reconcileProto(proto, revoked) {
			dirty = true
		}
	}
	return dirty
}

func (d *Daemon) setStatus(proto state.Protocol, s state.ProtoStatus) {
	cp := s
	d.status.Protocols[proto] = &cp
}

// reconcileProto starts, stops or restarts one protocol. It returns whether
// anything changed.
//
// A running server is never killed with uploads still queued in its VFS
// cache: rclone gives proxy backends a per-process cache name, so uploads
// interrupted by a restart would never resume. Instead the server drains
// (up to drainTimeout) and is then restarted.
func (d *Daemon) reconcileProto(proto state.Protocol, revoked bool) bool {
	pc := d.st.Protocols[proto]
	p := d.procs[proto]
	addr := ""
	if pc != nil {
		addr = fmt.Sprintf("%s:%d", d.st.BindAddress, pc.Port)
	}
	desired := pc != nil && pc.Enabled

	var (
		args []string
		err  error
		key  string
	)
	if desired {
		args, err = ServeArgs(d.st, proto, d.exe)
		if err == nil && d.st.RcloneBinary == "" {
			err = rclone.ErrNotFound
		}
		if err == nil && proto == state.S3 {
			r := &rclone.Rclone{Bin: d.st.RcloneBinary}
			if !r.SupportsAuthProxy(string(proto)) {
				err = errors.New("this rclone is too old for S3 with per-client logins (needs --auth-proxy on serve s3); upgrade rclone")
			}
		}
		if err == nil {
			key = d.st.RcloneBinary + "\x00" + strings.Join(args, "\x00")
		}
	}

	// Does the running server have to go?
	if p != nil && p.cmd != nil && !p.draining && (!desired || err != nil || p.key != key || revoked) {
		p.draining = true
		p.drainUntil = time.Now().Add(drainTimeout)
		d.logger.Printf("[%s] configuration changed, restarting once pending uploads finish", proto)
	}
	if p != nil && p.cmd != nil && p.draining {
		n := pendingUploads(p.rcSock)
		if n > 0 && time.Now().Before(p.drainUntil) {
			note := fmt.Sprintf("applying changes after %d pending upload(s) finish", n)
			if p.status.Note != note {
				p.status.Note = note
				d.setStatus(proto, p.status)
				return true
			}
			return false
		}
		if n > 0 {
			d.logger.Printf("[%s] gave up waiting after %s: %d upload(s) still pending and may need re-uploading", proto, drainTimeout, n)
		}
		d.stop(p)
		p.draining = false
		p.status.Note = ""
		p.backoff = 0
		p.nextStart = time.Time{}
		p.status.Restarts = 0
	}

	if !desired {
		delete(d.procs, proto)
		prev := d.status.Protocols[proto]
		if prev == nil || prev.State != state.ProcDisabled {
			d.setStatus(proto, state.ProtoStatus{State: state.ProcDisabled, Since: time.Now()})
			return true
		}
		return false
	}
	if err != nil {
		delete(d.procs, proto)
		prev := d.status.Protocols[proto]
		if prev == nil || prev.Error != err.Error() {
			d.logger.Printf("[%s] cannot start: %v", proto, err)
			d.setStatus(proto, state.ProtoStatus{State: state.ProcError, Addr: addr, Error: err.Error(), Since: time.Now()})
			return true
		}
		return false
	}
	if p == nil {
		p = &proc{proto: proto}
		d.procs[proto] = p
	}

	if p.cmd != nil {
		// Promote to running once it survived the settle time.
		if p.status.State == state.ProcStarting && time.Since(p.started) > settleTime {
			p.status.State = state.ProcRunning
			p.status.Error = ""
			p.status.Since = time.Now()
			d.setStatus(proto, p.status)
			d.logger.Printf("[%s] serving on %s", proto, addr)
			return true
		}
		return false
	}
	if p.key != key {
		// A config change resets the crash backoff.
		p.key = key
		p.backoff = 0
		p.nextStart = time.Time{}
	}
	if time.Now().Before(p.nextStart) {
		return false
	}
	d.start(p, d.st.RcloneBinary, args, addr)
	return true
}

func (d *Daemon) start(p *proc, bin string, args []string, addr string) {
	// A private rc socket lets us see pending uploads before restarting.
	p.rcSock = filepath.Join(d.paths.Dir, "rc-"+string(p.proto)+".sock")
	_ = os.Remove(p.rcSock)
	if len(p.rcSock) < 100 {
		args = append(append([]string(nil), args...), "--rc", "--rc-addr", "unix://"+p.rcSock, "--rc-no-auth")
	} else {
		p.rcSock = ""
	}
	cmd := exec.Command(bin, args...)
	cmd.Env = append(os.Environ(), state.EnvDir+"="+d.paths.Dir)
	cmd.SysProcAttr = sysProcAttr()
	p.tail = newTailBuffer(6)
	out := &prefixWriter{prefix: "[" + string(p.proto) + "] ", logger: d.logger, tail: p.tail}
	cmd.Stdout = out
	cmd.Stderr = out
	if err := cmd.Start(); err != nil {
		d.logger.Printf("[%s] failed to start: %v", p.proto, err)
		p.status = state.ProtoStatus{State: state.ProcError, Addr: addr, Error: err.Error(), Since: time.Now(), Restarts: p.status.Restarts}
		d.setStatus(p.proto, p.status)
		d.scheduleRetry(p)
		return
	}
	p.cmd = cmd
	p.started = time.Now()
	p.stopping = false
	p.done = make(chan struct{})
	p.status = state.ProtoStatus{State: state.ProcStarting, Addr: addr, Since: time.Now(), Restarts: p.status.Restarts, PID: cmd.Process.Pid}
	d.setStatus(p.proto, p.status)
	d.logger.Printf("[%s] starting: %s %s", p.proto, bin, strings.Join(args, " "))
	go func(cmd *exec.Cmd, done chan struct{}) {
		err := cmd.Wait()
		out.Flush()
		close(done)
		d.exits <- exitEvent{p: p, cmd: cmd, err: err}
	}(cmd, p.done)
}

func (d *Daemon) scheduleRetry(p *proc) {
	if p.backoff == 0 {
		p.backoff = minBackoff
	} else {
		p.backoff *= 2
		if p.backoff > maxBackoff {
			p.backoff = maxBackoff
		}
	}
	p.nextStart = time.Now().Add(p.backoff)
}

func (d *Daemon) handleExit(ev exitEvent) {
	p := ev.p
	if p.stopping || d.procs[p.proto] != p || p.cmd == nil || p.cmd != ev.cmd {
		return
	}
	ran := time.Since(p.started)
	p.cmd = nil
	p.draining = false
	p.status.Note = ""
	msg := p.tail.String()
	if msg == "" && ev.err != nil {
		msg = ev.err.Error()
	}
	if ran > 30*time.Second {
		p.backoff = 0
	}
	d.scheduleRetry(p)
	p.status.Restarts++
	p.status.State = state.ProcError
	p.status.Error = msg
	p.status.PID = 0
	p.status.Since = time.Now()
	d.setStatus(p.proto, p.status)
	d.logger.Printf("[%s] exited after %s (%v), retrying in %s", p.proto, ran.Round(time.Second), ev.err, p.backoff)
}

func (d *Daemon) stop(p *proc) {
	if p.cmd == nil || p.cmd.Process == nil {
		return
	}
	p.stopping = true
	_ = p.cmd.Process.Signal(syscall.SIGTERM)
	select {
	case <-p.done:
	case <-time.After(stopTimeout):
		_ = p.cmd.Process.Kill()
		<-p.done
	}
	d.logger.Printf("[%s] stopped", p.proto)
	if p.rcSock != "" {
		_ = os.Remove(p.rcSock)
	}
	p.cmd = nil
	p.status.State = state.ProcStopped
	p.status.PID = 0
}

func (d *Daemon) stopAll() {
	deadline := time.Now().Add(shutdownDrain)
	for {
		n := 0
		for _, p := range d.procs {
			if p.cmd != nil {
				n += pendingUploads(p.rcSock)
			}
		}
		if n == 0 {
			break
		}
		if time.Now().After(deadline) {
			d.logger.Printf("gave up waiting: %d upload(s) still pending and may need re-uploading", n)
			break
		}
		d.logger.Printf("waiting for %d pending upload(s) before stopping", n)
		d.writeStatus()
		time.Sleep(2 * time.Second)
	}
	var wg sync.WaitGroup
	for _, p := range d.procs {
		wg.Add(1)
		go func(p *proc) {
			defer wg.Done()
			d.stop(p)
		}(p)
	}
	wg.Wait()
}

func (d *Daemon) writeStatus() {
	d.status.Updated = time.Now()
	if err := d.paths.SaveStatus(&d.status); err != nil {
		d.logger.Printf("failed to write status: %v", err)
	}
}

// prefixWriter sends child output to the log line by line and keeps the
// last few lines for error reporting.
type prefixWriter struct {
	mu     sync.Mutex
	prefix string
	logger *log.Logger
	tail   *tailBuffer
	buf    []byte
}

func (w *prefixWriter) Write(b []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.buf = append(w.buf, b...)
	for {
		i := strings.IndexByte(string(w.buf), '\n')
		if i < 0 {
			break
		}
		w.line(string(w.buf[:i]))
		w.buf = w.buf[i+1:]
	}
	return len(b), nil
}

// rcloneTimestamp matches the date rclone puts in front of its log lines;
// our own logger already adds one.
var rcloneTimestamp = regexp.MustCompile(`^\d{4}/\d\d/\d\d \d\d:\d\d:\d\d `)

func (w *prefixWriter) line(l string) {
	l = rcloneTimestamp.ReplaceAllString(strings.TrimRight(l, "\r"), "")
	if l == "" {
		return
	}
	w.logger.Print(w.prefix + l)
	w.tail.Add(l)
}

func (w *prefixWriter) Flush() {
	w.mu.Lock()
	defer w.mu.Unlock()
	if len(w.buf) > 0 {
		w.line(string(w.buf))
		w.buf = nil
	}
}

type tailBuffer struct {
	mu    sync.Mutex
	n     int
	lines []string
}

func newTailBuffer(n int) *tailBuffer { return &tailBuffer{n: n} }

func (t *tailBuffer) Add(l string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.lines = append(t.lines, l)
	if len(t.lines) > t.n {
		t.lines = t.lines[len(t.lines)-t.n:]
	}
}

// String returns the most relevant recent lines: errors if there are any.
func (t *tailBuffer) String() string {
	t.mu.Lock()
	defer t.mu.Unlock()
	var errs []string
	for _, l := range t.lines {
		if strings.Contains(l, "ERROR") || strings.Contains(l, "CRITICAL") || strings.Contains(l, "Fatal") || strings.Contains(l, "Failed") {
			errs = append(errs, l)
		}
	}
	if len(errs) == 0 {
		errs = t.lines
	}
	if len(errs) > 3 {
		errs = errs[len(errs)-3:]
	}
	return strings.Join(errs, "\n")
}

// ScanLog returns the last n lines of the daemon log.
func ScanLog(path string, n int) ([]string, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	if st, err := f.Stat(); err == nil && st.Size() > 512<<10 {
		_, _ = f.Seek(st.Size()-512<<10, io.SeekStart)
	}
	var lines []string
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 64<<10), 1<<20)
	for sc.Scan() {
		lines = append(lines, sc.Text())
		if len(lines) > n*2 {
			lines = lines[len(lines)-n:]
		}
	}
	if len(lines) > n {
		lines = lines[len(lines)-n:]
	}
	return lines, sc.Err()
}
