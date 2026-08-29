package api

import (
	"encoding/json"
	"fmt"
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
			UUID:        st.Config.UUID(),
			Name:        st.Config.Name(),
			Description: st.Config.Settings.Meta.Description,
			State:       st.State,
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

// handleServerCreate implements POST /api/servers (v1.15 pull model:
// body {uuid, start_on_completion}; the daemon fetches config from the panel).
func (a *App) handleServerCreate(w http.ResponseWriter, r *http.Request) {
	var body struct {
		UUID              string `json:"uuid"`
		StartOnCompletion bool   `json:"start_on_completion"`
	}
	if err := json.NewDecoder(io.LimitReader(r.Body, 1<<20)).Decode(&body); err != nil || body.UUID == "" {
		util.WriteError(w, util.ErrBadRequest("missing server uuid"))
		return
	}
	cfg, err := a.fetchAndBuildConfig(body.UUID)
	if err != nil {
		util.WriteError(w, util.ErrInternal("fetch server config: "+err.Error()))
		return
	}
	s := a.Registry.Put(cfg)
	if s == nil {
		util.WriteError(w, util.ErrInternal("register server"))
		return
	}
	s.ChownVolume()
	a.Log.Info("server %s created (image=%q profile=%s start_on_completion=%v)", cfg.UUID(), cfg.Image(), cfg.ResolvedProfile, body.StartOnCompletion)

	// v1.15 parity: run install automatically when the server was never installed,
	// then optionally start on completion.
	go a.TriggerInstall(body.UUID, false, body.StartOnCompletion)
	util.WriteJSON(w, http.StatusNoContent, nil)
}

// fetchAndBuildConfig pulls the server detail from the panel and resolves runtime.
func (a *App) fetchAndBuildConfig(uuid string) (*server.ServerConfig, error) {
	raw, err := a.Panel.GetServer(uuid)
	if err != nil {
		return nil, err
	}
	b, err := json.Marshal(raw)
	if err != nil {
		return nil, err
	}
	var cfg server.ServerConfig
	if err := json.Unmarshal(b, &cfg); err != nil {
		return nil, err
	}
	if cfg.Settings.UUID == "" {
		cfg.Settings.UUID = uuid
	}
	// resolve runtime mapping now and store it
	a.resolveRuntime(&cfg)
	return &cfg, nil
}

// resolveRuntime fills cfg.ResolvedProfile/Path/Env from settings.runtime or the image.
func (a *App) resolveRuntime(cfg *server.ServerConfig) {
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

// handleServerSync implements POST /api/servers/{uuid}/sync (panel config refresh).
func (a *App) handleServerSync(w http.ResponseWriter, r *http.Request) {
	s, err := a.getServer(r)
	if err != nil {
		util.WriteError(w, err)
		return
	}
	cfg, err := a.fetchAndBuildConfig(s.UUID())
	if err != nil {
		util.WriteError(w, util.ErrInternal("sync: "+err.Error()))
		return
	}
	a.Registry.Put(cfg)
	a.Log.Info("server %s synced from panel", s.UUID())
	util.WriteJSON(w, http.StatusNoContent, nil)
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
		"settings":              c.Settings,
		"process_configuration": c.ProcessConfiguration,
	})
}

// handleServerUpdate implements PATCH /api/servers/{uuid} (config refresh from panel).
func (a *App) handleServerUpdate(w http.ResponseWriter, r *http.Request) {
	s, err := a.getServer(r)
	if err != nil {
		util.WriteError(w, err)
		return
	}
	cfg, err := a.fetchAndBuildConfig(s.UUID())
	if err != nil {
		util.WriteError(w, util.ErrInternal("refresh config: "+err.Error()))
		return
	}
	a.Registry.Put(cfg)
	util.WriteJSON(w, http.StatusNoContent, nil)
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

// handleServerInstall implements PATCH /api/servers/{uuid}/install (explicit install trigger).
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
	go a.TriggerInstall(s.UUID(), body.Reinstall, false)
	util.WriteJSON(w, http.StatusNoContent, nil)
}

// handleServerReinstall implements POST /api/servers/{uuid}/reinstall.
func (a *App) handleServerReinstall(w http.ResponseWriter, r *http.Request) {
	s, err := a.getServer(r)
	if err != nil {
		util.WriteError(w, err)
		return
	}
	go a.TriggerInstall(s.UUID(), true, false)
	util.WriteJSON(w, http.StatusAccepted, nil)
}

// handleServerArchive implements POST /api/servers/{uuid}/archive (native: no-op 202,
// transfers/archives are not supported — documented limitation).
func (a *App) handleServerArchive(w http.ResponseWriter, r *http.Request) {
	if _, err := a.getServer(r); err != nil {
		util.WriteError(w, err)
		return
	}
	util.WriteJSON(w, http.StatusAccepted, map[string]interface{}{"archived": false})
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
		Wait  bool   `json:"wait"`
	}
	if err := json.NewDecoder(io.LimitReader(r.Body, 1<<20)).Decode(&body); err != nil || body.State == "" {
		util.WriteError(w, util.ErrBadRequest("missing power state"))
		return
	}
	if s.Cfg.Settings.Suspended {
		util.WriteError(w, util.ErrServerSuspended())
		return
	}
	if err := s.Power(body.State); err != nil {
		util.WriteError(w, err)
		return
	}
	a.Log.Info("server %s power action: %s", s.UUID(), body.State)

	if body.Wait {
		// wait up to 20s for the action to take effect
		target := map[string]string{
			"start": server.StateRunning, "stop": server.StateOffline,
			"kill": server.StateOffline,
		}[body.State]
		if target != "" {
			deadline := time.Now().Add(20 * time.Second)
			for time.Now().Before(deadline) {
				if s.State() == target {
					break
				}
				time.Sleep(250 * time.Millisecond)
			}
		}
	}
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

var _ = fmt.Sprintf
