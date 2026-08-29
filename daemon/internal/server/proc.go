package server

import (
	"bufio"
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"syscall"
	"time"

	"ptero-native/internal/eggcompat"
	"ptero-native/internal/util"
)

// Start spawns the server process (native bash -c invocation).
func (s *Server) Start() error {
	s.mu.Lock()
	defer s.mu.Unlock()

	switch s.state {
	case StateRunning, StateStarting:
		return util.ErrPowerConflict("server is already running")
	case StateInstalling:
		return util.NewErr(409, "ServerIsInstallingException", "cannot start server while installing")
	}

	if s.Cfg.Settings.Suspended {
		return util.ErrServerSuspended()
	}

	vol := s.cfg.ServerVolume(s.Cfg.UUID())
	if err := os.MkdirAll(vol, 0o755); err != nil {
		return util.ErrInternal("prepare volume: " + err.Error())
	}

	raw := s.Cfg.InvocationLine()
	if raw == "" {
		return util.NewErr(400, "InvalidInvocationException", "no startup command configured for server")
	}

	// egg compat: apply process_configuration.configs (find/replace) before start
	s.applyEggConfigs(vol)

	// egg compat: translate docker paths + {{VAR}} -> ${VAR}
	interpolated := translatePlaceholders(eggPathTranslate(raw, vol))

	env := s.buildEnvLocked()

	uid, gid := s.resolveRunUser()
	if s.log != nil {
		s.log.Info("server %s start: uid=%d gid=%d cmd=%q", s.Cfg.UUID(), uid, gid, interpolated)
	}

	cmd := exec.Command("bash", "-c", interpolated)
	cmd.Dir = vol
	cmd.Env = env
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	if uid >= 0 && gid >= 0 {
		cmd.SysProcAttr.Credential = &syscall.Credential{Uid: uint32(uid), Gid: uint32(gid)}
	}

	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return util.ErrInternal("stdout pipe: " + err.Error())
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		return util.ErrInternal("stderr pipe: " + err.Error())
	}
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return util.ErrInternal("stdin pipe: " + err.Error())
	}

	if err := cmd.Start(); err != nil {
		s.setStateLocked(StateCrashed)
		return util.NewErr(502, "ProcessStartException", "failed to start process: "+err.Error())
	}

	s.cmd = cmd
	s.stdinPipe = stdin
	s.pid = cmd.Process.Pid
	s.startedAt = time.Now()
	s.stopFlag = false
	s.killFlag = false
	s.setStateLocked(StateStarting)

	go s.pumpOutput(stdout)
	go s.pumpOutput(stderr)
	go s.supervise(cmd)

	_ = s.persistState()
	return nil
}

// applyEggConfigs processes egg config find/replace entries (wings parity).
func (s *Server) applyEggConfigs(vol string) {
	if s.Cfg.Settings.SkipEggScripts || len(s.Cfg.ProcessConfiguration.Configs) == 0 {
		return
	}
	for file, cf := range s.Cfg.ProcessConfiguration.Configs {
		if cf.File == "" {
			cf.File = file
		}
		replaces := make([]map[string]string, 0, len(cf.Replace))
		for _, r := range cf.Replace {
			replaces = append(replaces, map[string]string{
				"match":        r.Match,
				"if_value":     r.IfValue,
				"replace_with": r.ReplaceWith,
			})
		}
		if err := eggcompat.ApplyEggConfig(vol, cf.File, cf.Parser, replaces, cf.Find); err != nil {
			if s.log != nil {
				s.log.Warn("egg config %s: %v", cf.File, err)
			}
		}
	}
}

// eggPathTranslate rewrites docker paths to the native data dir.
func eggPathTranslate(s, dataDir string) string {
	out := strings.ReplaceAll(s, "/home/container", dataDir)
	out = strings.ReplaceAll(out, "/mnt/server", dataDir)
	return out
}

// translatePlaceholders converts {{VAR}} placeholders to ${VAR}.
func translatePlaceholders(s string) string {
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		if s[i] == '{' && i+1 < len(s) && s[i+1] == '{' {
			end := strings.Index(s[i+2:], "}}")
			if end >= 0 {
				name := strings.TrimSpace(s[i+2 : i+2+end])
				if isValidVarName(name) {
					b.WriteString("${" + name + "}")
					i = i + 2 + end + 1
					continue
				}
			}
		}
		b.WriteByte(s[i])
	}
	return b.String()
}

func isValidVarName(s string) bool {
	if s == "" {
		return false
	}
	for _, c := range s {
		if !(c == '_' || (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9')) {
			return false
		}
	}
	return true
}

// BuildEnv assembles the process environment (lock-safe public wrapper).
func (s *Server) BuildEnv() []string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.buildEnvLocked()
}

// buildEnvLocked assembles the environment; caller must already hold s.mu
// (reading or writing). Do NOT acquire the lock here — Start() invokes this
// while holding the write lock (double-RLock inside Lock self-deadlocks).
func (s *Server) buildEnvLocked() []string {
	envMap := s.Cfg.EnvStringMap()

	def := s.Cfg.Settings.Allocations.Default
	envMap["SERVER_IP"] = def.IP
	if def.IP == "" {
		envMap["SERVER_IP"] = "0.0.0.0"
	}
	envMap["SERVER_PORT"] = strconv.Itoa(def.Port)
	envMap["SERVER_MEMORY"] = strconv.FormatInt(s.Cfg.Settings.Build.MemoryLimit, 10)
	envMap["SERVER_UUID"] = s.Cfg.UUID()
	if n := s.Cfg.Name(); n != "" {
		envMap["SERVER_NAME"] = n
	}
	envMap["P_SERVER_UUID"] = s.Cfg.UUID()
	envMap["P_SERVER_LOCATION"] = "native"
	envMap["P_SERVER_ALLOCATION_LIMIT"] = strconv.Itoa(allocCount(s.Cfg))
	envMap["HOME"] = s.cfg.ServerVolume(s.Cfg.UUID())
	envMap["PWD"] = s.cfg.ServerVolume(s.Cfg.UUID())
	envMap["TMPDIR"] = s.cfg.Daemon.TmpPath

	if s.Cfg.ResolvedPath != "" {
		envMap["PATH"] = s.Cfg.ResolvedPath + ":" + envMap["PATH"]
	}

	envMap["P_SERVER_HOST_IP"] = envMap["SERVER_IP"]
	envMap["P_SERVER_HOST_PORT"] = envMap["SERVER_PORT"]

	// uppercase aliases (egg contract)
	upper := map[string]string{}
	for k, v := range envMap {
		upper[strings.ToUpper(k)] = v
	}
	for k, v := range upper {
		if _, ok := envMap[k]; !ok {
			envMap[k] = v
		}
	}

	out := make([]string, 0, len(envMap)+2)
	for k, v := range envMap {
		out = append(out, k+"="+v)
	}
	out = append(out, "TERM=xterm-256color")
	return out
}

func allocCount(c *ServerConfig) int {
	n := 0
	for _, ports := range c.Settings.Allocations.Mappings {
		n += len(ports)
	}
	if n == 0 {
		n = 1
	}
	return n
}

// resolveRunUser determines uid/gid for the process (per-server user when root).
func (s *Server) resolveRunUser() (int, int) {
	if os.Geteuid() != 0 {
		return -1, -1
	}
	u := lookupServerUser(s.cfg, s.Cfg.UUID())
	if u == nil {
		return -1, -1
	}
	return u.uid, u.gid
}

// pumpOutput reads a pipe into the ring buffer + hub, detecting done lines.
func (s *Server) pumpOutput(r interface{ Read([]byte) (int, error) }) {
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 64*1024), 512*1024)
	for sc.Scan() {
		line := sc.Text()
		line = util.TruncateLine(line, 4096)
		s.mu.RLock()
		ring := s.logs
		hub := s.hub
		state := s.state
		doneLines := s.Cfg.ProcessConfiguration.Startup.Done
		s.mu.RUnlock()
		if ring != nil {
			ring.Push(line)
		}
		if hub != nil {
			hub.ConsoleLine(s.Cfg.UUID(), line)
		}
		// done-line detection: starting -> running
		if state == StateStarting && len(doneLines) > 0 {
			for _, done := range doneLines {
				if done != "" && strings.Contains(line, done) {
					s.mu.Lock()
					if s.state == StateStarting {
						s.state = StateRunning
						if s.hub != nil {
							go s.hub.StatusChange(s.Cfg.UUID(), StateRunning)
						}
					}
					s.mu.Unlock()
					break
				}
			}
		}
	}
}

// supervise waits for the process to exit and handles stop/kill/crash logic.
func (s *Server) supervise(cmd *exec.Cmd) {
	err := cmd.Wait()

	s.mu.Lock()
	s.stdinPipe = nil
	wasStopped := s.stopFlag || s.killFlag
	s.stopFlag = false
	s.killFlag = false
	exitErr := err
	startTime := s.startedAt
	s.pid = 0
	s.cmd = nil
	s.mu.Unlock()

	_ = s.persistState()

	if wasStopped || exitErr == nil {
		s.mu.Lock()
		if s.state != StateInstalling {
			s.setStateLocked(StateOffline)
		}
		s.mu.Unlock()
		return
	}

	if ee, ok := exitErr.(*exec.ExitError); ok {
		if code := ee.ExitCode(); code != 0 {
			s.handleCrash(code, time.Since(startTime))
			return
		}
		s.mu.Lock()
		s.setStateLocked(StateOffline)
		s.mu.Unlock()
		return
	}
	s.handleCrash(-1, time.Since(startTime))
}

// handleCrash implements crash detection + restart budget.
func (s *Server) handleCrash(exitCode int, uptime time.Duration) {
	s.mu.Lock()
	if s.state == StateStopping || s.state == StateInstalling {
		s.mu.Unlock()
		return
	}
	window := time.Duration(s.cfg.Limits.CrashWindow) * time.Second
	if uptime > window {
		s.crashCount = 1
	} else {
		s.crashCount++
	}
	count := s.crashCount
	budget := s.cfg.Limits.CrashRestarts
	s.setStateLocked(StateCrashed)
	s.mu.Unlock()

	if s.log != nil {
		s.log.Warn("server %s crashed (exit=%d uptime=%s crashCount=%d/%d)", s.Cfg.UUID(), exitCode, uptime, count, budget)
	}

	if count > budget {
		if s.log != nil {
			s.log.Error("server %s entered crash-loop; giving up", s.Cfg.UUID())
		}
		return
	}

	go func() {
		time.Sleep(5 * time.Second)
		s.mu.RLock()
		cur := s.state
		s.mu.RUnlock()
		if cur != StateCrashed {
			return
		}
		if err := s.Start(); err != nil && s.log != nil {
			s.log.Error("auto-restart failed for %s: %v", s.Cfg.UUID(), err)
		}
	}()
}

// Stop gracefully stops the server honoring the egg stop configuration.
func (s *Server) Stop() error {
	return s.stop(false)
}

// Kill force-kills the server.
func (s *Server) Kill() error {
	return s.stop(true)
}

func (s *Server) stop(kill bool) error {
	s.mu.RLock()
	pid := s.pid
	state := s.state
	stopCfg := s.Cfg.ProcessConfiguration.Stop
	s.mu.RUnlock()

	if pid == 0 {
		if state == StateOffline || state == StateCrashed {
			return util.NewErr(409, "PowerActionConflict", "server is not running")
		}
		return util.NewErr(409, "PowerActionConflict", "server is not running")
	}

	s.mu.Lock()
	s.killFlag = kill
	s.stopFlag = true
	s.setStateLocked(StateStopping)
	s.mu.Unlock()

	if kill {
		_ = signalGroup(pid, syscall.SIGKILL)
		return nil
	}

	// egg-defined stop: command -> stdin, signal -> signal
	if stopCfg.Type == "command" && stopCfg.Value != "" {
		s.mu.RLock()
		pipe := s.stdinPipe
		s.mu.RUnlock()
		if pipe != nil {
			_, _ = fmt.Fprintln(pipe, stopCfg.Value)
		}
	} else if stopCfg.Type == "signal" && stopCfg.Value != "" {
		sig := parseSignal(stopCfg.Value)
		if sig != 0 {
			_ = signalGroup(pid, sig)
		}
	} else {
		// default: wings-style SIGSTOP then SIGTERM
		_ = signalGroup(pid, syscall.SIGSTOP)
		_ = signalGroup(pid, syscall.SIGTERM)
	}

	go func() {
		deadline := time.Now().Add(30 * time.Second)
		for time.Now().Before(deadline) {
			s.mu.RLock()
			gone := s.pid == 0
			s.mu.RUnlock()
			if gone {
				return
			}
			if !processAlive(pid) {
				return
			}
			time.Sleep(300 * time.Millisecond)
		}
		_ = signalGroup(pid, syscall.SIGKILL)
	}()
	return nil
}

// parseSignal maps a signal name to a syscall signal.
func parseSignal(name string) syscall.Signal {
	signals := map[string]syscall.Signal{
		"SIGKILL": syscall.SIGKILL, "KILL": syscall.SIGKILL,
		"SIGTERM": syscall.SIGTERM, "TERM": syscall.SIGTERM,
		"SIGINT": syscall.SIGINT, "INT": syscall.SIGINT,
		"SIGHUP": syscall.SIGHUP, "HUP": syscall.SIGHUP,
		"SIGSTOP": syscall.SIGSTOP, "STOP": syscall.SIGSTOP,
		"SIGQUIT": syscall.SIGQUIT, "QUIT": syscall.SIGQUIT,
	}
	if sig, ok := signals[strings.ToUpper(strings.TrimSpace(name))]; ok {
		return sig
	}
	return 0
}

// signalGroup signals a whole process group.
func signalGroup(pid int, sig syscall.Signal) error {
	if err := syscall.Kill(-pid, sig); err != nil {
		if err2 := syscall.Kill(pid, sig); err2 != nil {
			return err
		}
	}
	return nil
}

// processAlive checks pid existence.
func processAlive(pid int) bool {
	p, err := os.FindProcess(pid)
	if err != nil {
		return false
	}
	return p.Signal(syscall.Signal(0)) == nil
}

// SendCommand writes a command line to the process stdin.
func (s *Server) SendCommand(cmd string) error {
	s.mu.RLock()
	pipe := s.stdinPipe
	state := s.state
	s.mu.RUnlock()
	if pipe == nil || (state != StateRunning && state != StateStarting) {
		return util.NewErr(409, "PowerActionConflict", "server is not running")
	}
	_, err := fmt.Fprintln(pipe, cmd)
	return err
}

// ConsoleLines returns the last n lines.
func (s *Server) ConsoleLines(n int) []string {
	s.mu.RLock()
	r := s.logs
	s.mu.RUnlock()
	if r == nil {
		return nil
	}
	return r.Replay(n, true)
}

// PushConsole injects a daemon-side console line.
func (s *Server) PushConsole(line string) {
	s.mu.RLock()
	r := s.logs
	hub := s.hub
	s.mu.RUnlock()
	if r != nil {
		r.Push(line)
	}
	if hub != nil {
		hub.ConsoleLine(s.Cfg.UUID(), line)
	}
}

// Power performs a power action.
func (s *Server) Power(state string) error {
	switch strings.ToLower(state) {
	case "start":
		return s.Start()
	case "stop":
		return s.Stop()
	case "restart":
		st := s.State()
		if st == StateOffline || st == StateCrashed {
			return s.Start()
		}
		if err := s.Stop(); err != nil {
			return err
		}
		go func() {
			time.Sleep(1500 * time.Millisecond)
			_ = s.Start()
		}()
		return nil
	case "kill":
		return s.Kill()
	default:
		return util.ErrBadRequest("invalid power state " + state)
	}
}

// Uptime returns seconds since start (0 if not running).
func (s *Server) Uptime() int64 {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.pid == 0 || s.startedAt.IsZero() {
		return 0
	}
	return int64(time.Since(s.startedAt).Seconds())
}

// DataDir returns the server volume path.
func (s *Server) DataDir() string { return s.cfg.ServerVolume(s.Cfg.UUID()) }
