// Package server implements the server model, registry and process supervision.
package server

import (
        "encoding/json"
        "io"
        "os"
        "os/exec"
        "path/filepath"
        "strings"
        "sync"
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
        StateOnline     = "online"
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
        IP     string  `json:"ip"`
        Port   int     `json:"port"`
        IPAlias *string `json:"ip_alias"`
        Notes  *string `json:"notes"`
}

// Allocations mirrors panel's allocations block.
type Allocations struct {
        ForceOutgoingIP bool                `json:"force_outgoing_ip"`
        Default         Allocation          `json:"default"`
        Mappings        map[string][]int    `json:"mappings"`
}

// Settings mirrors panel's settings block.
type Settings struct {
        UUID        string            `json:"uuid"`
        User        int               `json:"user"`
        Egg         EggRef            `json:"egg"`
        Image       string            `json:"image"`
        Stopped     bool              `json:"stopped"`
        Runtime     string            `json:"runtime,omitempty"` // native addition (resolved by panel patch)
        Environment map[string]string `json:"environment"`
}

// EggRef identifies the egg.
type EggRef struct {
        ID   int    `json:"id"`
        Name string `json:"name"`
        UUID string `json:"uuid,omitempty"`
}

// Container mirrors panel's container block.
type Container struct {
        Image              string            `json:"image"`
        StartupCommand     string            `json:"startup_command,omitempty"`
        StopCommand        string            `json:"stop_command,omitempty"`
        Entrypoint         string            `json:"entrypoint,omitempty"`
        Environment        map[string]string `json:"environment,omitempty"`
        Installed          int               `json:"installed,omitempty"`
        RequiresContainer  bool              `json:"requires_container,omitempty"`
}

// ServerConfig is the full configuration panel sends / the daemon persists.
type ServerConfig struct {
        UUID        string                 `json:"uuid"`
        Name        string                 `json:"name,omitempty"`
        Suspended   bool                   `json:"suspended,omitempty"`
        JWTSecret   string                 `json:"jwt_secret,omitempty"`
        Settings    Settings               `json:"settings"`
        Build       Build                  `json:"build"`
        Allocations Allocations            `json:"allocations"`
        Environment map[string]interface{} `json:"environment,omitempty"`
        Invocation  string                 `json:"invocation,omitempty"`
        Container   Container              `json:"container,omitempty"`

        // Native-resolved runtime details (not from panel).
        ResolvedProfile string            `json:"resolved_profile,omitempty"`
        ResolvedPath    string            `json:"resolved_path,omitempty"`
        ResolvedEnv     map[string]string `json:"resolved_env,omitempty"`
}

// EnvStringMap merges all environment sources into one string map.
func (c *ServerConfig) EnvStringMap() map[string]string {
        out := map[string]string{}
        for k, v := range c.Settings.Environment {
                out[k] = toString(v)
        }
        for k, v := range c.Environment {
                out[k] = toString(v)
        }
        for k, v := range c.Container.Environment {
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
                        return fmtInt(int64(t))
                }
                return jsonNumber(t)
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

func fmtInt(i int64) string {
        return strings.TrimSpace(strings.ReplaceAll(strings.ReplaceAll(jsonNumber(float64(i)), ".0", ""), "+", ""))
}

func jsonNumber(f float64) string {
        b, _ := json.Marshal(f)
        return string(b)
}

// InvocationLine returns the effective startup command.
func (c *ServerConfig) InvocationLine() string {
        if c.Invocation != "" {
                return c.Invocation
        }
        if c.Container.StartupCommand != "" {
                return c.Container.StartupCommand
        }
        if s, ok := c.EnvStringMap()["STARTUP"]; ok && s != "" {
                return s
        }
        return ""
}

// Image returns the docker image string used for runtime mapping.
func (c *ServerConfig) Image() string {
        if c.Settings.Image != "" {
                return c.Settings.Image
        }
        return c.Container.Image
}

// State is the runtime state of a server managed by the daemon.
type State struct {
        Config      *ServerConfig `json:"config"`
        Installed   int           `json:"installed"` // 0 never, 1 installed, 2 failed
        PID         int           `json:"pid,omitempty"`
        State       string        `json:"state"`
        StartedAt   *time.Time    `json:"started_at,omitempty"`
        CrashCount  int           `json:"crash_count,omitempty"`
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

        logs *Ring
        hub  Hub // console hub (may be nil in core-only builds/tests)
        quota Quota
	cpuSample    *procCPUSample

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

// Put inserts or replaces a server (config merge).
func (r *Registry) Put(cfg *ServerConfig) *Server {
        r.mu.Lock()
        defer r.mu.Unlock()
        existing, ok := r.servers[cfg.UUID]
        if ok {
                existing.mu.Lock()
                // preserve jwt secret if the new config omits it
                if cfg.JWTSecret == "" {
                        cfg.JWTSecret = existing.Cfg.JWTSecret
                }
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
        r.servers[cfg.UUID] = s
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
                if _, ok := r.servers[st.Config.UUID]; ok {
                        continue
                }
                s := &Server{
                        Cfg:        st.Config,
                        state:      StateOffline,
                        log:        r.log,
                        cfg:        r.cfg,
                        hub:        r.hub,
                        crashCount: st.CrashCount,
                }
                s.logs = NewRing(r.cfg.Limits.LogMaxLines)
                r.servers[st.Config.UUID] = s
        }
}

// UUID returns the server uuid.
func (s *Server) UUID() string { return s.Cfg.UUID }

// Snapshot returns a consistent state snapshot.
func (s *Server) Snapshot() State {
        s.mu.RLock()
        defer s.mu.RUnlock()
        st := State{
                Config:     s.Cfg,
                Installed:  s.installed(),
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

// installed reads the installed marker without extra locking (caller holds lock).
func (s *Server) installed() int { return s.installedVal }

func (s *Server) setStateLocked(state string) {
        s.state = state
        if s.hub != nil {
                go s.hub.StatusChange(s.Cfg.UUID, state)
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

// persistState writes the daemon-local state file (caller holds s.mu or during init).
func (s *Server) persistState() error {
        path := s.cfg.ServerStatePath(s.Cfg.UUID)
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

// SetState updates the power state and emits a hub event.
func (s *Server) SetState(state string) {
	s.mu.Lock()
	s.setStateLocked(state)
	s.mu.Unlock()
	_ = s.persistState()
}

// Quota returns the quota interface.
func (s *Server) Quota() Quota { return s.quota }

// SetQuota attaches a quota tracker.
func (s *Server) SetQuota(q Quota) {
	s.mu.Lock()
	s.quota = q
	s.mu.Unlock()
}
