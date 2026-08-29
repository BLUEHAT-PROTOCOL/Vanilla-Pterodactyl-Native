// Package api implements the wings-compatible HTTP API surface.
package api

import (
	"net/http"
	"sync"

	"ptero-native/internal/auth"
	"ptero-native/internal/backup"
	"ptero-native/internal/config"
	"ptero-native/internal/eggcompat"
	"ptero-native/internal/panel"
	"ptero-native/internal/server"
	"ptero-native/internal/util"
)

// App bundles daemon dependencies for the HTTP layer.
type App struct {
	Cfg      *config.Config
	Registry *server.Registry
	Panel    *panel.Client
	Resolver *eggcompat.Resolver
	Log      *util.Logger
	Version  string

	Hub        server.Hub // console hub (server.Hub interface)
	HubWS      WSHandler  // websocket route handler (console.Hub)
	BackupPath string

	// backup manager (lazy)
	bmOnce sync.Once
	BM     *backup.Manager
}

// WSHandler serves the websocket upgrade route.
type WSHandler interface {
	ServeWS(http.ResponseWriter, *http.Request)
}

// SetHub attaches the hub (both interfaces).
func (a *App) SetHub(h server.Hub, ws WSHandler) {
	a.Hub = h
	a.HubWS = ws
}

// InstallLogReplay replays buffered install output for ws clients.
func (a *App) InstallLogReplay(uuid string) []string { return installLogReplay(uuid) }

func (a *App) keys() map[string]string {
	return a.Cfg.Daemon.APIKeys
}

// apiKeys merges configured api_keys with the implicit daemon token pair.
func (a *App) apiKeys() map[string]string {
	k := map[string]string{}
	for id, key := range a.Cfg.Daemon.APIKeys {
		k[id] = key
	}
	if a.Cfg.Daemon.TokenID != "" && a.Cfg.Daemon.Token != "" {
		k[a.Cfg.Daemon.TokenID] = a.Cfg.Daemon.Token
	}
	return k
}

// requireAuth wraps a handler with wings bearer auth.
func (a *App) requireAuth(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !auth.CheckBearer(r, a.apiKeys()) {
			util.WriteJSON(w, http.StatusUnauthorized, map[string]interface{}{
				"errors": []interface{}{util.NewErr(http.StatusUnauthorized, "UnauthorizedAccessException", "invalid or missing daemon token")},
			})
			return
		}
		next(w, r)
	}
}

// getServer resolves {uuid} from the route.
func (a *App) getServer(r *http.Request) (*server.Server, error) {
	uuid := r.PathValue("uuid")
	s, ok := a.Registry.Get(uuid)
	if !ok {
		return nil, util.ErrNotFound("server")
	}
	return s, nil
}

// Router builds the full HTTP route table.
func (a *App) Router() http.Handler {
	mux := http.NewServeMux()

	// system
	mux.HandleFunc("GET /api/system", a.requireAuth(a.handleSystem))
	mux.HandleFunc("GET /api/system/config", a.requireAuth(a.handleSystemConfig))
	mux.HandleFunc("GET /api/update", a.requireAuth(a.handleUpdateGet))
	mux.HandleFunc("POST /api/update", a.requireAuth(a.handleUpdatePost))
	mux.HandleFunc("GET /api/verify-license", a.requireAuth(a.handleLicense))

	// servers CRUD
	mux.HandleFunc("GET /api/servers", a.requireAuth(a.handleServersList))
	mux.HandleFunc("POST /api/servers", a.requireAuth(a.handleServerCreate))
	mux.HandleFunc("GET /api/servers/{uuid}", a.requireAuth(a.handleServerGet))
	mux.HandleFunc("PATCH /api/servers/{uuid}", a.requireAuth(a.handleServerUpdate))
	mux.HandleFunc("DELETE /api/servers/{uuid}", a.requireAuth(a.handleServerDelete))
	mux.HandleFunc("PATCH /api/servers/{uuid}/install", a.requireAuth(a.handleServerInstall))
	mux.HandleFunc("POST /api/servers/{uuid}/reinstall", a.requireAuth(a.handleServerReinstall))

	// power & console
	mux.HandleFunc("POST /api/servers/{uuid}/power", a.requireAuth(a.handlePower))
	mux.HandleFunc("POST /api/servers/{uuid}/commands", a.requireAuth(a.handleCommands))
	mux.HandleFunc("GET /api/servers/{uuid}/logs", a.requireAuth(a.handleLogs))
	mux.HandleFunc("GET /api/servers/{uuid}/ws", a.handleWSRoute)

	// files
	mux.HandleFunc("GET /api/servers/{uuid}/files/list", a.requireAuth(a.handleFilesList))
	mux.HandleFunc("GET /api/servers/{uuid}/files/contents", a.requireAuth(a.handleFilesContents))
	mux.HandleFunc("POST /api/servers/{uuid}/files/write", a.requireAuth(a.handleFilesWrite))
	mux.HandleFunc("PUT /api/servers/{uuid}/files/rename", a.requireAuth(a.handleFilesRename))
	mux.HandleFunc("POST /api/servers/{uuid}/files/copy", a.requireAuth(a.handleFilesCopy))
	mux.HandleFunc("POST /api/servers/{uuid}/files/create-directory", a.requireAuth(a.handleFilesMkdir))
	mux.HandleFunc("POST /api/servers/{uuid}/files/delete", a.requireAuth(a.handleFilesDelete))
	mux.HandleFunc("POST /api/servers/{uuid}/files/compress", a.requireAuth(a.handleFilesCompress))
	mux.HandleFunc("POST /api/servers/{uuid}/files/uncompress", a.requireAuth(a.handleFilesUncompress))
	mux.HandleFunc("POST /api/servers/{uuid}/files/chmod", a.requireAuth(a.handleFilesChmod))
	mux.HandleFunc("POST /api/servers/{uuid}/files/pull", a.requireAuth(a.handleFilesPull))
	mux.HandleFunc("GET /api/servers/{uuid}/files/pull/{identifier}", a.requireAuth(a.handleFilesPullStatus))
	mux.HandleFunc("DELETE /api/servers/{uuid}/files/pull/{identifier}", a.requireAuth(a.handleFilesPullCancel))
	mux.HandleFunc("GET /api/upload", a.requireAuth(a.handleUploadTicket))
	mux.HandleFunc("POST /upload", a.handleUpload)

	// backups
	mux.HandleFunc("GET /api/servers/{uuid}/backups", a.requireAuth(a.handleBackupsList))
	mux.HandleFunc("POST /api/servers/{uuid}/backups", a.requireAuth(a.handleBackupCreate))
	mux.HandleFunc("GET /api/servers/{uuid}/backups/{backup}", a.requireAuth(a.handleBackupGet))
	mux.HandleFunc("GET /api/servers/{uuid}/backups/{backup}/download", a.requireAuth(a.handleBackupDownload))
	mux.HandleFunc("DELETE /api/servers/{uuid}/backups/{backup}", a.requireAuth(a.handleBackupDelete))
	mux.HandleFunc("POST /api/servers/{uuid}/backups/{backup}/restore", a.requireAuth(a.handleBackupRestore))

	// signed downloads (file + backup)
	mux.HandleFunc("GET /download", a.handleSignedDownload)
	mux.HandleFunc("GET /download/backup", a.handleSignedBackupDownload)

	// transfer stubs (respond politely; unsupported in native)
	mux.HandleFunc("POST /api/transfer", a.requireAuth(func(w http.ResponseWriter, r *http.Request) {
		util.WriteError(w, util.NewErr(http.StatusBadRequest, "TransferNotSupportedException", "transfers are not supported by the native runtime"))
	}))

	return a.recoverMiddleware(mux)
}

// handleWSRoute delegates to the console hub if attached.
func (a *App) handleWSRoute(w http.ResponseWriter, r *http.Request) {
	if a.HubWS == nil {
		util.WriteError(w, util.NewErr(http.StatusServiceUnavailable, "BackupsDisabledException", "websocket hub not ready"))
		return
	}
	a.HubWS.ServeWS(w, r)
}

// recoverMiddleware converts panics into 500 error envelopes.
func (a *App) recoverMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer func() {
			if rec := recover(); rec != nil {
				a.Log.Error("panic serving %s %s: %v", r.Method, r.URL.Path, rec)
				util.WriteError(w, util.ErrInternal("internal daemon error"))
			}
		}()
		next.ServeHTTP(w, r)
	})
}
