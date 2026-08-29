package api

import (
	"net/http"
	"runtime"
	"strings"
	"os"

	"ptero-native/internal/util"
)

// handleSystem implements GET /api/system (panel dashboard compatibility).
func (a *App) handleSystem(w http.ResponseWriter, r *http.Request) {
	rel := kernelRelease()
	// Superset response: top-level keys satisfy the admin SystemInformationController,
	// nested blocks keep wings-shape consumers happy. docker is null (native runtime).
	util.WriteJSON(w, http.StatusOK, map[string]interface{}{
		"version":        a.Version,
		"os":             "Linux",
		"architecture":   runtime.GOARCH,
		"kernel_version": rel,
		"cpu_count":      runtime.NumCPU(),
		"docker": map[string]interface{}{
			"versions": nil,
			"driver":   nil,
			"info":     nil,
		},
		"system": map[string]interface{}{
			"type":           "linux",
			"release":        rel,
			"architecture":   runtime.GOARCH,
			"cpu_count":      runtime.NumCPU(),
			"kernel_version": rel,
		},
	})
}

// handleSystemConfig implements GET /api/system/config.
func (a *App) handleSystemConfig(w http.ResponseWriter, r *http.Request) {
	util.WriteJSON(w, http.StatusOK, map[string]interface{}{
		"websockets": map[string]interface{}{
			"enabled": true,
			"host":    "",
			"port":    8080,
		},
		"allocations": map[string]interface{}{
			"auto_create": false,
			"user_blocking": 0,
		},
		"detect_long_running": true,
	})
}

// handleUpdateGet implements GET /api/update (docker image check in wings).
func (a *App) handleUpdateGet(w http.ResponseWriter, r *http.Request) {
	util.WriteJSON(w, http.StatusOK, map[string]interface{}{
		"available": false,
		"images":    []interface{}{},
	})
}

// handleUpdatePost implements POST /api/update (no-op for native).
func (a *App) handleUpdatePost(w http.ResponseWriter, r *http.Request) {
	w.WriteHeader(http.StatusAccepted)
}

// handleLicense implements GET /api/verify-license.
func (a *App) handleLicense(w http.ResponseWriter, r *http.Request) {
	util.WriteJSON(w, http.StatusOK, map[string]interface{}{
		"valid": true,
	})
}

// kernelRelease reads uname -r.
func kernelRelease() string {
	b, err := os.ReadFile("/proc/sys/kernel/osrelease")
	if err != nil {
		return "unknown"
	}
	return strings.TrimSpace(string(b))
}
