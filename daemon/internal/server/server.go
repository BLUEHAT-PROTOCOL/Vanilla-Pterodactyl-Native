// Package server implements the server model, registry and process supervision.
package server

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"time"

	"ptero-native/internal/config"
	"ptero-native/internal/util"
)

// Server power states (wings-compatible).
const (
	StateOffline    = "offline"
	StateStarting   = "starting"
	StateRunning    = "running"
	StateStopping   = "stopping"
	StateInstalling = "installing"
	StateCrashed    = "crashed"
)

// Build mirrors panel's build block.
type Build struct {
	MemoryLimit int64  `json:"memory_limit"`
	Swap        int64  `json:"swap"`
	IoWeight    int64  `json:"io_weight"`
	CpuLimit    int64  `json:"cpu_limit"`
	Threads     string `json:"threads"`
	DiskSpace   int64  `json:"disk_space"`
	OomDisabled bool   `json:"oom_disabled"`
}

// Allocation is a single allocation entry.
type Allocation struct {
	IP      string  `json:"ip"`
	Port    int     `json:"port"`
	IPAlias *string `json:"ip_alias"`
	Notes   *string `json:"notes"`
}

// Allocations mirrors panel's allocations block.
type Allocations struct {
	ForceOutgoingIP bool             `json:"force_outgoing_ip"`
	Default         Allocation       `json:"default"`
	Mappings        map[string][]int `json:"mappings"`
}

// Container mirrors panel's container block.
type Container struct {
	Image             string `json:"image"`
	OomDisabled       bool   `json:"oom_disabled"`
	RequiresRebuild   bool   `json:"requires_rebuild"`
	RequiresContainer bool   `json:"requires_container,omitempty"`
}

// EggRef identifies the egg (v1.15: id = egg uuid).
type EggRef struct {
	ID           string   `json:"id"`
	FileDenylist []string `json:"file_denylist"`
}

// Meta carries display info.
type Meta struct {
	Name        string `json:"name"`
	Description string `json:"description"`
}

// Settings is the panel's "settings" block (ServerConfigurationStructureService format).
type Settings struct {
	UUID           string                 `json:"uuid"`
	Meta           Meta                   `json:"meta"`
	Suspended      bool                   `json:"suspended"`
	Environment    map[string]interface{} `json:"environment"`
	Invocation     string                 `json:"invocation"`
	SkipEggScripts bool                   `json:"skip_egg_scripts"`
	Build          Build                  `json:"build"`
	Container      Container              `json:"container"`
	Allocations    Allocations            `json:"allocations"`
	Egg            EggRef                 `json:"egg"`
	Runtime        string                 `json:"runtime,omitempty"` // native addition (resolved by panel patch)
	OwnerID        int                    `json:"owner_id,omitempty"`
}

// StopConfig describes how to stop the process.
type StopConfig struct {
	Type  string `json:"type"`  // "command" | "signal"
	Value string `json:"value"` // command or signal name
}

// StartupConfig describes state detection.
type StartupConfig struct {
	Done            []string `json:"done"`
	UserInteraction []string `json:"user_interaction"`
	StripAnsi       bool     `json:"strip_ansi"`
}

// ConfigReplace is one replacement rule.
type ConfigReplace struct {
	Match       string `json:"match"`
	IfValue     string `json:"if_value"`
	ReplaceWith string `json:"replace_with"`
}

// ConfigFile is one egg config file entry.
type ConfigFile struct {
	File    string                 `json:"file"` // relative path
	Parser  string                 `json:"parser,omitempty"`
	Find    map[string]interface{} `json:"find"`
	Replace []ConfigReplace        `json:"replace"`
}

// ProcessConfiguration is the egg behavior config (v1.15).
type ProcessConfiguration struct {
	Startup StartupConfig         `json:"startup"`
	Stop    StopConfig            `json:"stop"`
	Configs map[string]ConfigFile `json:"configs"`
	Logfile interface{}           `json:"logfile,omitempty"`
}

// ServerConfig is the full daemon-side server configuration (panel remote detail shape).
type ServerConfig struct {
	Settings             Settings             `json:"settings"`
	ProcessConfiguration ProcessConfiguration `json:"process_configuration,omitempty"`

	// Native-resolved runtime details (not from panel).
	ResolvedProfile string            `json:"resolved_profile,omitempty"`
	ResolvedPath    string            `json:"resolved_path,omitempty"`
	ResolvedEnv     map[string]string `json:"resolved_env,omitempty"`
}

// UUID returns the server uuid.
func (c *ServerConfig) UUID() string {
	if c.Settings.UUID != "" {
		return c.Settings.UUID
	}
	return ""
}

// Name returns the display name.
func (c *ServerConfig) Name() string { return c.Settings.Meta.Name }

// Image returns the docker image string used for runtime mapping.
func (c *ServerConfig) Image() string { return c.Settings.Container.Image }

// InvocationLine returns the effective startup command.
func (c *ServerConfig) InvocationLine() string {
	return c.Settings.Invocation
}

// EnvStringMap merges all environment sources into one string map.
func (c *ServerConfig) EnvStringMap() map[string]string {
	out := map[string]string{}
	for k, v := range c.Settings.Environment {
		out[k] = toString(v)
	}
	if c.ResolvedEnv != nil {
		for k, v := range c.ResolvedEnv {
			out[k] = v
		}
	}
	return out
}

func toString(v interface{}) string {
	switch t := v.(type) {
	case string:
		return t
	case float64:
		if t == float64(int64(t)) {
			b, _ := json.Marshal(int64(t))
			return string(b)
		}
		b, _ := json.Marshal(t)
		return string(b)
	case bool:
		if t {
			return "true"
		}
		return "false"
	case nil:
		return ""
	default:
		b, _ := json.Marshal(t)
		return string(b)
	}
}

// State is the runtime state of a server managed by the daemon.
type State struct {
	Config     *ServerConfig `json:"config"`
	Installed  int           `json:"installed"` // 0 never, 1 installed, 2 failed
	PID        int           `json:"pid,omitempty"`
	State      string        `json:"state"`
	StartedAt  *time.Time    `json:"started_at,omitempty"`
	CrashCount int           `json:"crash_count,omitempty"`
}

// Server couples config + live process handle.
type Server struct {
	mu sync.RWMutex

	Cfg *ServerConfig

	// live process info
	cmd          *exec.Cmd
	stdinPipe    io.WriteCloser
	pid          int
	state        string
	startedAt    time.Time
	stopFlag     bool // user-initiated stop in progress
	killFlag     bool
	crashCount   int
	installedVal int

	logs      *Ring
	hub       Hub // console hub (may be nil in core-only builds/tests)
	quota     Quota
	cpuSample *procCPUSample

	diskCacheBytes int64
	diskCacheAt    time.Time

	log *util.Logger
	cfg *config.Config
}

// Hub is the console hub interface (implemented by console package).
type Hub interface {
	ConsoleLine(uuid, line string)
	StatusChange(uuid, state string)
	InstallOutput(uuid, line string)
	InstallStatus(uuid, status string)
	Stats(uuid string, data interface{})
}

// Quota enforces per-server disk limits.
type Quota interface {
	UsedBytes(s *Server) int64
	LimitBytes(s *Server) int64
	HasSpace(s *Server, n int64) bool
}

// Registry holds all servers known to the daemon.
type Registry struct {
	mu      sync.RWMutex
	servers map[string]*Server
	cfg     *config.Config
	log     *util.Logger
	hub     Hub
}

// NewRegistry creates an empty registry.
func NewRegistry(cfg *config.Config, log *util.Logger) *Registry {
	return &Registry{servers: map[string]*Server{}, cfg: cfg, log: log}
}

// SetHub attaches the console hub.
func (r *Registry) SetHub(h Hub) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.hub = h
	for _, s := range r.servers {
		s.mu.Lock()
		s.hub = h
		s.mu.Unlock()
	}
}

// Get returns a server by uuid.
func (r *Registry) Get(uuid string) (*Server, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	s, ok := r.servers[uuid]
	return s, ok
}

// Len returns server count.
func (r *Registry) Len() int {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return len(r.servers)
}

// All returns a snapshot list of servers.
func (r *Registry) All() []*Server {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]*Server, 0, len(r.servers))
	for _, s := range r.servers {
		out = append(out, s)
	}
	return out
}

// Put inserts or replaces a server (config merge, preserves runtime fields).
func (r *Registry) Put(cfg *ServerConfig) *Server {
	r.mu.Lock()
	defer r.mu.Unlock()
	if cfg.Settings.UUID == "" {
		return nil
	}
	uuid := cfg.Settings.UUID
	existing, ok := r.servers[uuid]
	if ok {
		existing.mu.Lock()
		existing.Cfg = cfg
		existing.mu.Unlock()
		_ = existing.persistState()
		return existing
	}
	s := &Server{
		Cfg:   cfg,
		state: StateOffline,
		log:   r.log,
		cfg:   r.cfg,
		hub:   r.hub,
	}
	s.logs = NewRing(r.cfg.Limits.LogMaxLines)
	r.servers[uuid] = s
	_ = s.persistState()
	return s
}

// Delete removes a server from the registry.
func (r *Registry) Delete(uuid string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	delete(r.servers, uuid)
	_ = os.Remove(r.cfg.ServerStatePath(uuid))
}

// LoadState reads daemon-local state files from disk at boot.
func (r *Registry) LoadState() {
	dir := filepath.Join(r.cfg.Daemon.DataPath, "state")
	entries, err := os.ReadDir(dir)
	if err != nil {
		return
	}
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".json") {
			continue
		}
		b, err := os.ReadFile(filepath.Join(dir, e.Name()))
		if err != nil {
			continue
		}
		var st State
		if err := json.Unmarshal(b, &st); err != nil || st.Config == nil {
			continue
		}
		if st.Config.Settings.UUID == "" {
			continue
		}
		if _, ok := r.servers[st.Config.Settings.UUID]; ok {
			continue
		}
		s := &Server{
			Cfg:          st.Config,
			state:        StateOffline,
			log:          r.log,
			cfg:          r.cfg,
			hub:          r.hub,
			crashCount:   st.CrashCount,
			installedVal: st.Installed,
		}
		s.logs = NewRing(r.cfg.Limits.LogMaxLines)
		r.servers[st.Config.Settings.UUID] = s
	}
}

// UUID returns the server uuid.
func (s *Server) UUID() string { return s.Cfg.UUID() }

// Name returns the display name.
func (s *Server) Name() string { return s.Cfg.Name() }

// Snapshot returns a consistent state snapshot.
func (s *Server) Snapshot() State {
	s.mu.RLock()
	defer s.mu.RUnlock()
	st := State{
		Config:     s.Cfg,
		Installed:  s.installedVal,
		PID:        s.pid,
		State:      s.state,
		CrashCount: s.crashCount,
	}
	if !s.startedAt.IsZero() {
		t := s.startedAt
		st.StartedAt = &t
	}
	return st
}

// setStateLocked emits state change while holding the lock.
func (s *Server) setStateLocked(state string) {
	s.state = state
	if s.hub != nil {
		go s.hub.StatusChange(s.Cfg.UUID(), state)
	}
}

// State returns the current power state.
func (s *Server) State() string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.state
}

// SetInstalled updates the installed marker and persists.
func (s *Server) SetInstalled(v int) {
	s.mu.Lock()
	s.installedVal = v
	err := s.persistState()
	s.mu.Unlock()
	if err != nil {
		s.log.Warn("persist installed state: %v", err)
	}
}

// Installed returns the installed marker.
func (s *Server) Installed() int {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.installedVal
}

// persistState writes the daemon-local state file.
func (s *Server) persistState() error {
	path := s.cfg.ServerStatePath(s.Cfg.UUID())
	_ = os.MkdirAll(filepath.Dir(path), 0o755)
	st := State{Config: s.Cfg, Installed: s.installedVal, PID: s.pid, State: s.state, CrashCount: s.crashCount}
	if !s.startedAt.IsZero() {
		st.StartedAt = &s.startedAt
	}
	b, err := json.MarshalIndent(st, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, b, 0o600)
}

// LockConfig locks the server config for direct mutation.
func (s *Server) LockConfig() { s.mu.Lock() }

// UnlockConfig unlocks the server config.
func (s *Server) UnlockConfig() { s.mu.Unlock() }

// Persist writes the daemon-local state file.
func (s *Server) Persist() error { return s.persistState() }

// AttachHub sets the console hub reference.
func (s *Server) AttachHub(h Hub) {
	s.mu.Lock()
	s.hub = h
	s.mu.Unlock()
}

// Quota returns the quota interface.
func (s *Server) Quota() Quota { return s.quota }

// SetQuota attaches a quota tracker.
func (s *Server) SetQuota(q Quota) {
	s.mu.Lock()
	s.quota = q
	s.mu.Unlock()
}

// SetState updates the power state and emits a hub event.
func (s *Server) SetState(state string) {
	s.mu.Lock()
	s.setStateLocked(state)
	s.mu.Unlock()
	_ = s.persistState()
}

// Adopt re-attaches a running process after a daemon restart (pid reuse is
// checked against the stored start time heuristically via /proc existence).
func (s *Server) Adopt(pid int, startedAt time.Time) error {
	if pid <= 0 {
		return fmt.Errorf("no pid")
	}
	p, err := os.FindProcess(pid)
	if err != nil {
		return err
	}
	if p.Signal(syscall.Signal(0)) != nil {
		return fmt.Errorf("process %d no longer exists", pid)
	}
	s.mu.Lock()
	s.pid = pid
	s.startedAt = startedAt
	s.state = StateRunning
	s.mu.Unlock()
	_ = s.persistState()
	return nil
}
