// ptero-native — the native (Docker-free) wings-compatible daemon.
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"ptero-native/internal/api"
	"ptero-native/internal/config"
	"ptero-native/internal/console"
	"ptero-native/internal/eggcompat"
	"ptero-native/internal/panel"
	"ptero-native/internal/server"
	"ptero-native/internal/util"
)

const version = "1.0.0"

func main() {
	cfgPath := flag.String("config", "/etc/ptero-native/config.yml", "path to config file")
	flag.Parse()

	log := util.NewLogger("/var/log/ptero-native/daemon.log", false)

	cfg, err := config.Load(*cfgPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "ptero-native: %v\n", err)
		os.Exit(1)
	}
	if cfg.Debug {
		log = util.NewLogger("/var/log/ptero-native/daemon.log", true)
	}
	_ = os.MkdirAll(cfg.VolumesPath(), 0o755)
	_ = os.MkdirAll(cfg.Daemon.BackupPath, 0o755)
	_ = os.MkdirAll(cfg.Daemon.TmpPath, 0o755)

	log.Info("ptero-native %s starting (data=%s listen=%s panel=%s)", version, cfg.Daemon.DataPath, cfg.Daemon.Listen, cfg.Panel.URL)

	panelClient := panel.New(cfg.Panel.URL, cfg.Panel.Token, cfg.Panel.AllowInsecure)
	registry := server.NewRegistry(cfg, log)

	resolver := eggcompat.NewResolver(nil)
	syncMappingsFromPanel(panelClient, resolver, log)

	quota := &server.QuotaTracker{}

	// load persisted local state, then re-sync from panel (source of truth)
	registry.LoadState()
	syncServersFromPanel(panelClient, registry, log)
	adopted := 0
	for _, s := range registry.All() {
		s.SetQuota(quota)
		s.ChownVolume()
		if st := s.Snapshot(); st.PID > 0 {
			if err := s.Adopt(st.PID, *st.StartedAt); err == nil {
				adopted++
			}
		}
	}
	if adopted > 0 {
		log.Info("re-adopted %d running process(es) after restart", adopted)
	}

	app := &api.App{
		Cfg:        cfg,
		Registry:   registry,
		Panel:      panelClient,
		Resolver:   resolver,
		Log:        log,
		Version:    version,
		BackupPath: cfg.Daemon.BackupPath,
	}

	hub := console.BuildHub(app)
	app.SetHub(hub, hub)
	registry.SetHub(hub)

	srv := &http.Server{
		Addr:    cfg.Daemon.Listen,
		Handler: app.Router(),
	}

	go func() {
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Error("listen: %v", err)
			os.Exit(1)
		}
	}()
	log.Info("daemon listening on %s", cfg.Daemon.Listen)

	sig := make(chan os.Signal, 2)
	signal.Notify(sig, syscall.SIGINT, syscall.SIGTERM)
	<-sig
	log.Info("shutting down")
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	_ = srv.Shutdown(ctx)
}

// syncMappingsFromPanel pulls runtime mappings from the panel (best effort).
func syncMappingsFromPanel(pc *panel.Client, r *eggcompat.Resolver, log *util.Logger) {
	var out struct {
		Data []struct {
			Attributes struct {
				DockerImage    string            `json:"docker_image"`
				RuntimeVersion string            `json:"runtime_version"`
				EnvPath        string            `json:"env_path"`
				ExtraEnv       map[string]string `json:"extra_env"`
				Profile        struct {
					Slug string `json:"slug"`
				} `json:"profile"`
			} `json:"attributes"`
		} `json:"data"`
	}
	resp, err := pc.RawGet("/api/remote/runtime/mappings")
	if err != nil {
		log.Debug("panel runtime mappings sync skipped: %v", err)
		return
	}
	defer func() { _ = resp.Body.Close() }()
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return
	}
	for _, m := range out.Data {
		if m.Attributes.DockerImage == "" {
			continue
		}
		r.SetMapping(m.Attributes.DockerImage, eggcompat.Mapping{
			Profile: slugToProfile(m.Attributes.Profile.Slug),
			Version: m.Attributes.RuntimeVersion,
			Path:    m.Attributes.EnvPath,
			Env:     m.Attributes.ExtraEnv,
		})
	}
	if len(out.Data) > 0 {
		log.Info("synced %d runtime mappings from panel", len(out.Data))
	}
}

func slugToProfile(slug string) string {
	switch {
	case len(slug) >= 4 && slug[:4] == "node":
		return "node"
	case len(slug) >= 6 && slug[:6] == "python":
		return "python"
	case len(slug) >= 4 && slug[:4] == "java":
		return "java"
	case slug == "static":
		return "static"
	default:
		return "custom"
	}
}

// syncServersFromPanel fetches all servers assigned to this node and registers them.
func syncServersFromPanel(pc *panel.Client, registry *server.Registry, log *util.Logger) {
	servers, err := pc.AllServers()
	if err != nil {
		log.Warn("panel server sync failed: %v (continuing with local state)", err)
		return
	}
	for _, raw := range servers {
		b, err := json.Marshal(raw)
		if err != nil {
			continue
		}
		var cfg server.ServerConfig
		if err := json.Unmarshal(b, &cfg); err != nil {
			log.Warn("server config decode failed: %v", err)
			continue
		}
		if cfg.UUID() == "" {
			continue
		}
		if _, exists := registry.Get(cfg.UUID()); !exists {
			registry.Put(&cfg)
		}
	}
	if n := registry.Len(); n > 0 {
		log.Info("registry: %d servers loaded", n)
	}
}
