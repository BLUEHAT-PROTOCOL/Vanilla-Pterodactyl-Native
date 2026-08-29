package api

import (
	"encoding/json"
	"io"
	"net/http"
	"time"

	"ptero-native/internal/server"
	"ptero-native/internal/util"
)

// serverSummary is the lightweight list entry for GET /api/servers.
type serverSummary struct {
	UUID        string `json:"uuid"`
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
	State       string `json:"state"`
}

// handleServersList implements GET /api/servers.
func (a *App) handleServersList(w http.ResponseWriter, r *http.Request) {
	all := a.Registry.All()
	data := make([]serverSummary, 0, len(all))
	for _, s := range all {
		st := s.Snapshot()
		data = append(data, serverSummary{
			UUID:  st.Config.UUID,
			Name:  st.Config.Name,
			State: st.State,
		})
	}
	util.WriteJSON(w, http.StatusOK, map[string]interface{}{
		"data": data,
		"meta": map[string]interface{}{
			"pagination": map[string]interface{}{
				"total":        len(data),
				"count":        len(data),
				"per_page":     len(data),
				"current_page": 1,
				"total_pages":  1,
			},
		},
	})
}

// handleServerCreate implements POST /api/servers.
func (a *App) handleServerCreate(w http.ResponseWriter, r *http.Request) {
	body, err := io.ReadAll(io.LimitReader(r.Body, 16<<20))
	if err != nil {
		util.WriteError(w, util.ErrBadRequest("read body: "+err.Error()))
		return
	}
	var cfg server.ServerConfig
	if err := json.Unmarshal(body, &cfg); err != nil {
		util.WriteError(w, util.ErrBadRequest("invalid server configuration: "+err.Error()))
		return
	}
	if cfg.UUID == "" {
		util.WriteError(w, util.ErrBadRequest("missing server uuid"))
		return
	}
	if cfg.Settings.UUID == "" {
		cfg.Settings.UUID = cfg.UUID
	}

	// resolve runtime mapping now and store it in the config
	a.resolveRuntime(&cfg)

	if cfg.Settings.Environment == nil {
		cfg.Settings.Environment = map[string]string{}
	}

	s := a.Registry.Put(&cfg)
	s.ChownVolume()

	a.Log.Info("server %s created (image=%q runtime=%s profile=%s)", cfg.UUID, cfg.Image(), cfg.Settings.Runtime, cfg.ResolvedProfile)
	util.WriteJSON(w, http.StatusNoContent, nil)
}

// resolveRuntime fills cfg.ResolvedProfile/Path/Env from settings.runtime or the image.
func (a *App) resolveRuntime(cfg *server.ServerConfig) {
	// explicit runtime key from panel patch wins
	if cfg.Settings.Runtime != "" {
		profile, path := splitRuntimeKey(cfg.Settings.Runtime)
		cfg.ResolvedProfile = profile
		cfg.ResolvedPath = path
		cfg.ResolvedEnv = map[string]string{}
		return
	}
	m, err := a.Resolver.Resolve(cfg.Image())
	if err != nil || m == nil {
		cfg.ResolvedProfile = "custom"
		return
	}
	cfg.ResolvedProfile = m.Profile
	cfg.ResolvedPath = m.Path
	cfg.ResolvedEnv = m.Env
	if cfg.Settings.Runtime == "" && m.Profile != "" {
		cfg.Settings.Runtime = m.Profile + "|" + m.Path
	}
}

func splitRuntimeKey(key string) (string, string) {
	for i := 0; i < len(key); i++ {
		if key[i] == '|' {
			return key[:i], key[i+1:]
		}
	}
	return key, ""
}

// handleServerGet implements GET /api/servers/{uuid}.
func (a *App) handleServerGet(w http.ResponseWriter, r *http.Request) {
	s, err := a.getServer(r)
	if err != nil {
		util.WriteError(w, err)
		return
	}
	st := s.Snapshot()
	c := st.Config
	util.WriteJSON(w, http.StatusOK, map[string]interface{}{
		"settings": c.Settings,
		"build":    c.Build,
		"allocations": c.Allocations,
	})
}

// handleServerUpdate implements PATCH /api/servers/{uuid}.
func (a *App) handleServerUpdate(w http.ResponseWriter, r *http.Request) {
	s, err := a.getServer(r)
	if err != nil {
		util.WriteError(w, err)
		return
	}
	body, err := io.ReadAll(io.LimitReader(r.Body, 16<<20))
	if err != nil {
		util.WriteError(w, util.ErrBadRequest("read body"))
		return
	}
	var patch map[string]json.RawMessage
	if err := json.Unmarshal(body, &patch); err != nil {
		util.WriteError(w, util.ErrBadRequest("invalid patch: "+err.Error()))
		return
	}
	a.patchServer(s, patch)
	util.WriteJSON(w, http.StatusNoContent, nil)
}

// patchServer applies partial config updates.
func (a *App) patchServer(s *server.Server, patch map[string]json.RawMessage) {
	s.LockConfig()
	defer s.UnlockConfig()
	c := s.Cfg
	if raw, ok := patch["build"]; ok {
		var bm map[string]interface{}
		if json.Unmarshal(raw, &bm) == nil {
			if v, ok := bm["memory_limit"].(float64); ok {
				c.Build.MemoryLimit = int64(v)
			}
			if v, ok := bm["swap"].(float64); ok {
				c.Build.Swap = int64(v)
			}
			if v, ok := bm["io_weight"].(float64); ok {
				c.Build.IoWeight = int64(v)
			}
			if v, ok := bm["cpu_limit"].(float64); ok {
				c.Build.CpuLimit = int64(v)
			}
			if v, ok := bm["disk_space"].(float64); ok {
				c.Build.DiskSpace = int64(v)
			}
			if v, ok := bm["oom_disabled"].(bool); ok {
				c.Build.OomDisabled = v
			}
		}
	}
	if raw, ok := patch["allocations"]; ok {
		var al server.Allocations
		if json.Unmarshal(raw, &al) == nil {
			c.Allocations = al
		}
	}
	if raw, ok := patch["settings"]; ok {
		var st server.Settings
		if json.Unmarshal(raw, &st) == nil {
			if st.Image != "" {
				c.Settings.Image = st.Image
			}
			if st.Runtime != "" {
				c.Settings.Runtime = st.Runtime
			}
			if st.Stopped != c.Settings.Stopped && st.Stopped {
				c.Settings.Stopped = st.Stopped
			}
		}
	}
	if raw, ok := patch["invocation"]; ok {
		var inv string
		if json.Unmarshal(raw, &inv) == nil && inv != "" {
			c.Invocation = inv
		}
	}
	if raw, ok := patch["container"]; ok {
		var ct server.Container
		if json.Unmarshal(raw, &ct) == nil {
			if ct.Image != "" {
				c.Container.Image = ct.Image
			}
			if ct.StartupCommand != "" {
				c.Container.StartupCommand = ct.StartupCommand
			}
			if ct.StopCommand != "" {
				c.Container.StopCommand = ct.StopCommand
			}
		}
	}
	a.resolveRuntime(c)
	_ = s.Persist()
}

// handleServerDelete implements DELETE /api/servers/{uuid}.
func (a *App) handleServerDelete(w http.ResponseWriter, r *http.Request) {
	s, err := a.getServer(r)
	if err != nil {
		util.WriteError(w, err)
		return
	}
	force := r.URL.Query().Get("force") == "1"
	st := s.Snapshot()
	if st.State == server.StateRunning || st.State == server.StateStarting || st.State == server.StateStopping {
		if !force {
			util.WriteError(w, util.NewErr(http.StatusConflict, "ServerIsRunningException", "server is running; use ?force=1"))
			return
		}
		_ = s.Kill()
	}
	uuid := s.UUID()
	// best-effort wait for exit
	for i := 0; i < 40; i++ {
		if s.State() == server.StateOffline || s.State() == server.StateCrashed {
			break
		}
		time.Sleep(150 * time.Millisecond)
	}
	a.Registry.Delete(uuid)
	a.Log.Info("server %s deleted", uuid)
	util.WriteJSON(w, http.StatusNoContent, nil)
}

// handleServerInstall implements PATCH /api/servers/{uuid}/install (panel-triggered install).
func (a *App) handleServerInstall(w http.ResponseWriter, r *http.Request) {
	s, err := a.getServer(r)
	if err != nil {
		util.WriteError(w, err)
		return
	}
	var body struct {
		Reinstall bool `json:"reinstall"`
	}
	_ = json.NewDecoder(io.LimitReader(r.Body, 1<<20)).Decode(&body)
	a.TriggerInstall(s.UUID(), body.Reinstall)
	util.WriteJSON(w, http.StatusNoContent, nil)
}

// handleServerReinstall implements POST /api/servers/{uuid}/reinstall.
func (a *App) handleServerReinstall(w http.ResponseWriter, r *http.Request) {
	s, err := a.getServer(r)
	if err != nil {
		util.WriteError(w, err)
		return
	}
	a.TriggerInstall(s.UUID(), true)
	util.WriteJSON(w, http.StatusAccepted, nil)
}

// handlePower implements POST /api/servers/{uuid}/power.
func (a *App) handlePower(w http.ResponseWriter, r *http.Request) {
	s, err := a.getServer(r)
	if err != nil {
		util.WriteError(w, err)
		return
	}
	var body struct {
		State string `json:"state"`
	}
	if err := json.NewDecoder(io.LimitReader(r.Body, 1<<20)).Decode(&body); err != nil || body.State == "" {
		util.WriteError(w, util.ErrBadRequest("missing power state"))
		return
	}
	if s.Cfg.Suspended {
		util.WriteError(w, util.ErrServerSuspended())
		return
	}
	if err := s.Power(body.State); err != nil {
		util.WriteError(w, err)
		return
	}
	a.Log.Info("server %s power action: %s", s.UUID(), body.State)
	w.WriteHeader(http.StatusAccepted)
}

// handleCommands implements POST /api/servers/{uuid}/commands.
func (a *App) handleCommands(w http.ResponseWriter, r *http.Request) {
	s, err := a.getServer(r)
	if err != nil {
		util.WriteError(w, err)
		return
	}
	var body struct {
		Commands []string `json:"commands"`
	}
	if err := json.NewDecoder(io.LimitReader(r.Body, 1<<20)).Decode(&body); err != nil || len(body.Commands) == 0 {
		util.WriteError(w, util.ErrBadRequest("missing commands"))
		return
	}
	for _, c := range body.Commands {
		if err := s.SendCommand(c); err != nil {
			util.WriteError(w, err)
			return
		}
	}
	w.WriteHeader(http.StatusNoContent)
}

// handleLogs implements GET /api/servers/{uuid}/logs?size=N.
func (a *App) handleLogs(w http.ResponseWriter, r *http.Request) {
	s, err := a.getServer(r)
	if err != nil {
		util.WriteError(w, err)
		return
	}
	size := 100
	if v := r.URL.Query().Get("size"); v != "" {
		n := 0
		for _, ch := range v {
			if ch < '0' || ch > '9' {
				n = -1
				break
			}
			n = n*10 + int(ch-'0')
		}
		if n > 0 {
			size = n
		}
	}
	util.WriteJSON(w, http.StatusOK, map[string]interface{}{
		"data": s.ConsoleLines(size),
		"meta": map[string]interface{}{},
	})
}
