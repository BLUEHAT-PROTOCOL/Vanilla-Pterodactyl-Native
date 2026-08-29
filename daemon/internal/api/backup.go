package api

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"time"

	"ptero-native/internal/backup"
	"ptero-native/internal/util"
)

// appBackupManager lazily builds/holds the backup manager.
func (a *App) backups() *backup.Manager {
	a.bmOnce.Do(func() {
		a.BM = backup.NewManager()
		a.BM.LoadFromDisk(a.BackupPath)
	})
	return a.BM
}

// handleBackupsList implements GET /backups.
func (a *App) handleBackupsList(w http.ResponseWriter, r *http.Request) {
	s, err := a.getServer(r)
	if err != nil {
		util.WriteError(w, err)
		return
	}
	list := a.backups().List(s.UUID())
	data := make([]map[string]interface{}, 0, len(list))
	for _, b := range list {
		data = append(data, map[string]interface{}{
			"uuid":         b.UUID,
			"name":         b.Name,
			"successful":   b.Successful,
			"is_locked":    b.Locked,
			"checksum":     b.Checksum,
			"bytes":        b.Size,
			"created_at":   b.CreatedAt,
			"completed_at": b.CompletedAt,
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

// handleBackupGet implements GET /backups/{backup}.
func (a *App) handleBackupGet(w http.ResponseWriter, r *http.Request) {
	s, err := a.getServer(r)
	if err != nil {
		util.WriteError(w, err)
		return
	}
	bid := r.PathValue("backup")
	b, ok := a.backups().Get(s.UUID(), bid)
	if !ok {
		util.WriteError(w, util.ErrNotFound("backup"))
		return
	}
	util.WriteJSON(w, http.StatusOK, map[string]interface{}{
		"uuid":         b.UUID,
		"name":         b.Name,
		"successful":   b.Successful,
		"is_locked":    b.Locked,
		"checksum":     b.Checksum,
		"bytes":        b.Size,
		"created_at":   b.CreatedAt,
		"completed_at": b.CompletedAt,
	})
}

// handleBackupCreate implements POST /backups.
func (a *App) handleBackupCreate(w http.ResponseWriter, r *http.Request) {
	s, err := a.getServer(r)
	if err != nil {
		util.WriteError(w, err)
		return
	}
	var body struct {
		Adapter string `json:"adapter"`
		UUID    string `json:"uuid"`
		Name    string `json:"name"`
		Ignore  string `json:"ignore"`
	}
	_ = json.NewDecoder(io.LimitReader(r.Body, 1<<20)).Decode(&body)
	if body.UUID == "" {
		body.UUID = newID()
	}
	if body.Name == "" {
		body.Name = "Backup at " + time.Now().UTC().Format(time.RFC3339)
	}

	serverUUID := s.UUID()
	go func() {
		info, err := a.backups().Create(a.BackupPath, serverUUID, body.UUID, body.Name, s.DataDir(), body.Ignore)
		if err != nil {
			a.Log.Error("backup %s failed: %v", body.UUID, err)
			_ = a.Panel.ReportBackup(body.UUID, false, "", "sha256", 0)
			return
		}
		a.Log.Info("backup %s created (%d bytes, sha256=%s)", body.UUID, info.Size, info.Checksum[:12])
		if err := a.Panel.ReportBackup(body.UUID, true, info.Checksum, "sha256", info.Size); err != nil {
			a.Log.Warn("backup completion callback failed: %v", err)
		}
	}()

	w.Header().Set("Location", "/api/servers/"+serverUUID+"/backups/"+body.UUID)
	w.WriteHeader(http.StatusAccepted)
}

// handleBackupDownload implements GET /backups/{backup}/download.
func (a *App) handleBackupDownload(w http.ResponseWriter, r *http.Request) {
	s, err := a.getServer(r)
	if err != nil {
		util.WriteError(w, err)
		return
	}
	bid := r.PathValue("backup")
	if _, ok := a.backups().Get(s.UUID(), bid); !ok {
		util.WriteError(w, util.ErrNotFound("backup"))
		return
	}
	secret := a.Cfg.Daemon.Token
	tok, err := signToken(secret, map[string]interface{}{
		"sub":       "backup-download",
		"unique_id": newID(),
		"server":    s.UUID(),
		"backup":    bid,
		"exp":       time.Now().Add(15 * time.Minute).Unix(),
	})
	if err != nil {
		util.WriteError(w, util.ErrInternal("sign token"))
		return
	}
	// relative URL — panel resolves it against the node base
	util.WriteJSON(w, http.StatusOK, map[string]interface{}{
		"path": "/download/backup?token=" + tok,
	})
}

// handleBackupDelete implements DELETE /backups/{backup}.
func (a *App) handleBackupDelete(w http.ResponseWriter, r *http.Request) {
	s, err := a.getServer(r)
	if err != nil {
		util.WriteError(w, err)
		return
	}
	bid := r.PathValue("backup")
	if err := a.backups().Delete(a.BackupPath, s.UUID(), bid); err != nil {
		if os.IsNotExist(err) {
			util.WriteError(w, util.ErrNotFound("backup"))
			return
		}
		util.WriteError(w, util.ErrInternal(err.Error()))
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// handleBackupRestore implements POST /backups/{backup}/restore.
func (a *App) handleBackupRestore(w http.ResponseWriter, r *http.Request) {
	s, err := a.getServer(r)
	if err != nil {
		util.WriteError(w, err)
		return
	}
	bid := r.PathValue("backup")
	var body struct {
		Adapter            string `json:"adapter"`
		TruncateDirectory bool `json:"truncate_directory"`
		DownloadURL        string `json:"download_url"`
	}
	_ = json.NewDecoder(io.LimitReader(r.Body, 1<<20)).Decode(&body)

	b, ok := a.backups().Get(s.UUID(), bid)
	if !ok {
		util.WriteError(w, util.ErrNotFound("backup"))
		return
	}
	if backup.IsRunning(s.UUID()) {
		util.WriteError(w, util.NewErr(http.StatusConflict, "BackupInProgressException", "a backup is in progress"))
		return
	}

	serverUUID := s.UUID()
	go func() {
		// stop server before restore (wings does the same when truncating)
		st := s.Snapshot()
		if st.State != "offline" {
			_ = s.Stop()
			for i := 0; i < 60; i++ {
				if s.State() == "offline" || s.State() == "crashed" {
					break
				}
				time.Sleep(250 * time.Millisecond)
			}
		}
		if err := a.backups().Restore(a.BackupPath, serverUUID, bid, s.DataDir(), body.TruncateDirectory); err != nil {
			a.Log.Error("restore %s failed: %v", bid, err)
			_ = a.Panel.ReportRestore(bid, false, err.Error())
			return
		}
		_ = b
		a.Log.Info("restore %s completed", bid)
		_ = a.Panel.ReportRestore(bid, true, "")
		s.PushConsole("[ptero-native] backup restored successfully")
	}()

	w.WriteHeader(http.StatusAccepted)
}

// verifyBackupChecksum is a helper used by E2E tooling (exported via API? kept internal).
func verifyBackupChecksum(path, want string) (bool, error) {
	f, err := os.Open(path)
	if err != nil {
		return false, err
	}
	defer func() { _ = f.Close() }()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return false, err
	}
	got := hex.EncodeToString(h.Sum(nil))
	return got == want, nil
}

var _ = fmt.Sprintf
