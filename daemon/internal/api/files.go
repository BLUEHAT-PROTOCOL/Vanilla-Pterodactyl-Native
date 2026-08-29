package api

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"ptero-native/internal/files"
	"ptero-native/internal/server"
	"ptero-native/internal/util"
)

// handleFilesList implements GET /files/list?directory=.
func (a *App) handleFilesList(w http.ResponseWriter, r *http.Request) {
	s, err := a.getServer(r)
	if err != nil {
		util.WriteError(w, err)
		return
	}
	dir := r.URL.Query().Get("directory")
	if dir == "" {
		dir = "/"
	}
	entries, err := files.List(s.DataDir(), dir)
	if err != nil {
		util.WriteError(w, filesErr(err))
		return
	}
	util.WriteJSON(w, http.StatusOK, map[string]interface{}{
		"data": entries,
		"meta": map[string]interface{}{
			"pagination": map[string]interface{}{
				"total":        len(entries),
				"count":        len(entries),
				"per_page":     len(entries),
				"current_page": 1,
				"total_pages":  1,
			},
		},
	})
}

// filesErr maps fs errors to API errors.
func filesErr(err error) error {
	if _, ok := err.(*files.ErrOutsideRoot); ok {
		return util.NewErr(http.StatusForbidden, "PathResolutionException", err.Error())
	}
	if os.IsNotExist(err) {
		return util.ErrNotFound("file")
	}
	return util.NewErr(http.StatusInternalServerError, "FilesystemException", err.Error())
}

// handleFilesContents implements GET /files/contents?file=.
func (a *App) handleFilesContents(w http.ResponseWriter, r *http.Request) {
	s, err := a.getServer(r)
	if err != nil {
		util.WriteError(w, err)
		return
	}
	file := r.URL.Query().Get("file")
	data, err := files.Read(s.DataDir(), file, 0)
	if err != nil {
		util.WriteError(w, filesErr(err))
		return
	}
	w.Header().Set("Content-Type", "application/octet-stream")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(data)
}

// handleFilesWrite implements POST /files/write?file=.
func (a *App) handleFilesWrite(w http.ResponseWriter, r *http.Request) {
	s, err := a.getServer(r)
	if err != nil {
		util.WriteError(w, err)
		return
	}
	file := r.URL.Query().Get("file")
	body, err := io.ReadAll(io.LimitReader(r.Body, 256<<20))
	if err != nil {
		util.WriteError(w, util.ErrBadRequest("read body"))
		return
	}
	// quota check
	if q := s.Quota(); q != nil {
		if !q.HasSpace(s, int64(len(body))) {
			util.WriteError(w, util.NewErr(http.StatusInsufficientStorage, "InsufficientQuotaException", "disk quota exceeded"))
			return
		}
	}
	if err := files.Write(s.DataDir(), file, body, false); err != nil {
		util.WriteError(w, filesErr(err))
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// handleFilesRename implements PUT /files/rename.
func (a *App) handleFilesRename(w http.ResponseWriter, r *http.Request) {
	s, err := a.getServer(r)
	if err != nil {
		util.WriteError(w, err)
		return
	}
	var body struct {
		Root  string `json:"root"`
		Files []struct {
			From string `json:"from"`
			To   string `json:"to"`
		} `json:"files"`
	}
	if err := json.NewDecoder(io.LimitReader(r.Body, 4<<20)).Decode(&body); err != nil {
		util.WriteError(w, util.ErrBadRequest("invalid body"))
		return
	}
	pairs := make([][2]string, 0, len(body.Files))
	for _, f := range body.Files {
		pairs = append(pairs, [2]string{
			filepath.Join(body.Root, f.From),
			filepath.Join(body.Root, f.To),
		})
	}
	if err := files.Rename(s.DataDir(), pairs); err != nil {
		util.WriteError(w, filesErr(err))
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// handleFilesCopy implements POST /files/copy.
func (a *App) handleFilesCopy(w http.ResponseWriter, r *http.Request) {
	s, err := a.getServer(r)
	if err != nil {
		util.WriteError(w, err)
		return
	}
	var body struct {
		Location string `json:"location"`
	}
	if err := json.NewDecoder(io.LimitReader(r.Body, 1<<20)).Decode(&body); err != nil || body.Location == "" {
		util.WriteError(w, util.ErrBadRequest("missing location"))
		return
	}
	if err := files.Copy(s.DataDir(), body.Location); err != nil {
		util.WriteError(w, filesErr(err))
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// handleFilesMkdir implements POST /files/create-directory.
func (a *App) handleFilesMkdir(w http.ResponseWriter, r *http.Request) {
	s, err := a.getServer(r)
	if err != nil {
		util.WriteError(w, err)
		return
	}
	var body struct {
		Root string `json:"root"`
		Name string `json:"name"`
	}
	if err := json.NewDecoder(io.LimitReader(r.Body, 1<<20)).Decode(&body); err != nil || body.Name == "" {
		util.WriteError(w, util.ErrBadRequest("missing name"))
		return
	}
	if err := files.CreateDirectory(s.DataDir(), body.Root, body.Name); err != nil {
		util.WriteError(w, filesErr(err))
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// handleFilesDelete implements POST /files/delete.
func (a *App) handleFilesDelete(w http.ResponseWriter, r *http.Request) {
	s, err := a.getServer(r)
	if err != nil {
		util.WriteError(w, err)
		return
	}
	var body struct {
		Root  string   `json:"root"`
		Files []string `json:"files"`
	}
	if err := json.NewDecoder(io.LimitReader(r.Body, 4<<20)).Decode(&body); err != nil || len(body.Files) == 0 {
		util.WriteError(w, util.ErrBadRequest("missing files"))
		return
	}
	targets := make([]string, 0, len(body.Files))
	for _, f := range body.Files {
		targets = append(targets, filepath.Join(body.Root, f))
	}
	if err := files.Delete(s.DataDir(), targets); err != nil {
		util.WriteError(w, filesErr(err))
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// handleFilesCompress implements POST /files/compress.
func (a *App) handleFilesCompress(w http.ResponseWriter, r *http.Request) {
	s, err := a.getServer(r)
	if err != nil {
		util.WriteError(w, err)
		return
	}
	var body struct {
		Root  string   `json:"root"`
		Files []string `json:"files"`
	}
	if err := json.NewDecoder(io.LimitReader(r.Body, 4<<20)).Decode(&body); err != nil || len(body.Files) == 0 {
		util.WriteError(w, util.ErrBadRequest("missing files"))
		return
	}
	entry, err := files.Compress(s.DataDir(), body.Root, body.Files)
	if err != nil {
		util.WriteError(w, filesErr(err))
		return
	}
	util.WriteJSON(w, http.StatusOK, entry)
}

// handleFilesUncompress implements POST /files/uncompress.
func (a *App) handleFilesUncompress(w http.ResponseWriter, r *http.Request) {
	s, err := a.getServer(r)
	if err != nil {
		util.WriteError(w, err)
		return
	}
	var body struct {
		Root string `json:"root"`
		File string `json:"file"`
	}
	if err := json.NewDecoder(io.LimitReader(r.Body, 1<<20)).Decode(&body); err != nil || body.File == "" {
		util.WriteError(w, util.ErrBadRequest("missing file"))
		return
	}
	if err := files.Uncompress(s.DataDir(), filepath.Join(body.Root, body.File)); err != nil {
		util.WriteError(w, filesErr(err))
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// handleFilesChmod implements POST /files/chmod.
func (a *App) handleFilesChmod(w http.ResponseWriter, r *http.Request) {
	s, err := a.getServer(r)
	if err != nil {
		util.WriteError(w, err)
		return
	}
	var body struct {
		Root  string `json:"root"`
		Files []struct {
			File string `json:"file"`
			Mode string `json:"mode"`
		} `json:"files"`
	}
	if err := json.NewDecoder(io.LimitReader(r.Body, 4<<20)).Decode(&body); err != nil || len(body.Files) == 0 {
		util.WriteError(w, util.ErrBadRequest("missing files"))
		return
	}
	entries := make([]files.ChmodEntry, 0, len(body.Files))
	for _, f := range body.Files {
		entries = append(entries, files.ChmodEntry{File: filepath.Join(body.Root, f.File), Mode: f.Mode})
	}
	if err := files.Chmod(s.DataDir(), entries); err != nil {
		util.WriteError(w, filesErr(err))
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// ---------- remote file pull ----------

type pullJob struct {
	id     string
	uuid   string
	url    string
	target string
	total  int64
	done   int64
	state  string // pending|downloading|completed|failed|cancelled
	cancel chan struct{}
	mu     sync.Mutex
}

var pullJobs = struct {
	mu   sync.Mutex
	jobs map[string]*pullJob
}{jobs: map[string]*pullJob{}}

// handleFilesPull implements POST /files/pull.
func (a *App) handleFilesPull(w http.ResponseWriter, r *http.Request) {
	s, err := a.getServer(r)
	if err != nil {
		util.WriteError(w, err)
		return
	}
	var body struct {
		URL       string `json:"url"`
		Root      string `json:"root"`
		FileName  string `json:"file_name"`
		UseHeader bool   `json:"use_header"`
	}
	if err := json.NewDecoder(io.LimitReader(r.Body, 1<<20)).Decode(&body); err != nil || body.URL == "" {
		util.WriteError(w, util.ErrBadRequest("missing url"))
		return
	}
	if !strings.HasPrefix(body.URL, "http://") && !strings.HasPrefix(body.URL, "https://") {
		util.WriteError(w, util.ErrBadRequest("url must be http(s)"))
		return
	}
	name := body.FileName
	if name == "" {
		name = filenameFromURL(body.URL)
	}
	target := filepath.Join(body.Root, name)

	id := newID()
	job := &pullJob{id: id, uuid: s.UUID(), url: body.URL, target: target, state: "pending", cancel: make(chan struct{})}
	pullJobs.mu.Lock()
	pullJobs.jobs[id] = job
	pullJobs.mu.Unlock()

	go a.runPull(s, job)
	util.WriteJSON(w, http.StatusOK, map[string]interface{}{"identifier": id})
}

func (a *App) runPull(s *server.Server, job *pullJob) {
	job.mu.Lock()
	job.state = "downloading"
	job.mu.Unlock()

	if err := os.MkdirAll(s.DataDir(), 0o755); err != nil {
		a.finishPull(job, "failed")
		return
	}
	tmpPath := filepath.Join(a.Cfg.Daemon.TmpPath, "pull-"+job.id)
	_ = os.MkdirAll(a.Cfg.Daemon.TmpPath, 0o755)

	out, err := os.Create(tmpPath)
	if err != nil {
		a.finishPull(job, "failed")
		return
	}
	done := false
	defer func() {
		_ = out.Close()
		if !done {
			_ = os.Remove(tmpPath)
		}
	}()

	req, err := http.NewRequest(http.MethodGet, job.url, nil)
	if err != nil {
		a.finishPull(job, "failed")
		return
	}
	req.Header.Set("User-Agent", "ptero-native/1.0")
	client := &http.Client{Timeout: 30 * time.Minute}
	resp, err := client.Do(req)
	if err != nil {
		a.finishPull(job, "failed")
		return
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode >= 400 {
		a.finishPull(job, "failed")
		return
	}
	job.mu.Lock()
	job.total = resp.ContentLength
	job.mu.Unlock()

	// copy with progress + cancel + quota
	buf := make([]byte, 128*1024)
	for {
		select {
		case <-job.cancel:
			a.finishPull(job, "cancelled")
			return
		default:
		}
		n, rerr := resp.Body.Read(buf)
		if n > 0 {
			if _, werr := out.Write(buf[:n]); werr != nil {
				a.finishPull(job, "failed")
				return
			}
			job.mu.Lock()
			job.done += int64(n)
			job.mu.Unlock()
		}
		if rerr == io.EOF {
			break
		}
		if rerr != nil {
			a.finishPull(job, "failed")
			return
		}
	}
	_ = out.Close()
	done = true

	// move into server volume
	if err := os.MkdirAll(filepath.Dir(filepath.Join(s.DataDir(), job.target)), 0o755); err != nil {
		a.finishPull(job, "failed")
		return
	}
	if err := os.Rename(tmpPath, filepath.Join(s.DataDir(), job.target)); err != nil {
		a.finishPull(job, "failed")
		return
	}
	a.finishPull(job, "completed")
	s.PushConsole("[ptero-native] downloaded " + job.target)
}

func (a *App) finishPull(job *pullJob, state string) {
	job.mu.Lock()
	job.state = state
	job.mu.Unlock()
}

func filenameFromURL(u string) string {
	p := u
	for _, c := range []string{"?", "#"} {
		if i := strings.Index(p, c); i >= 0 {
			p = p[:i]
		}
	}
	parts := strings.Split(p, "/")
	name := parts[len(parts)-1]
	if name == "" {
		name = "download-" + newID()
	}
	return name
}

func newID() string {
	b := make([]byte, 16)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

// handleFilesPullStatus implements GET /files/pull/{identifier}.
func (a *App) handleFilesPullStatus(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("identifier")
	pullJobs.mu.Lock()
	job, ok := pullJobs.jobs[id]
	pullJobs.mu.Unlock()
	if !ok {
		util.WriteError(w, util.ErrNotFound("pull job"))
		return
	}
	job.mu.Lock()
	defer job.mu.Unlock()
	resp := map[string]interface{}{
		"state": job.state,
		"progress": map[string]interface{}{
			"total": job.total,
			"done":  job.done,
			"overall": func() float64 {
				if job.total <= 0 {
					return 0
				}
				return float64(job.done) / float64(job.total) * 100
			}(),
		},
	}
	util.WriteJSON(w, http.StatusOK, resp)
}

// handleFilesPullCancel implements DELETE /files/pull/{identifier}.
func (a *App) handleFilesPullCancel(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("identifier")
	pullJobs.mu.Lock()
	job, ok := pullJobs.jobs[id]
	pullJobs.mu.Unlock()
	if !ok {
		util.WriteError(w, util.ErrNotFound("pull job"))
		return
	}
	select {
	case <-job.cancel:
	default:
		close(job.cancel)
	}
	w.WriteHeader(http.StatusNoContent)
}

// handleUploadTicket implements GET /api/upload.
func (a *App) handleUploadTicket(w http.ResponseWriter, r *http.Request) {
	// sign a one-shot upload token valid 10 minutes
	uuid := r.URL.Query().Get("uuid")
	if uuid == "" {
		util.WriteError(w, util.ErrBadRequest("missing server uuid"))
		return
	}
	claims := map[string]interface{}{
		"sub":         "upload",
		"unique_id":   newID(),
		"server_uuid": uuid,
		"server":      uuid,
		"exp":         time.Now().Add(15 * time.Minute).Unix(),
	}
	// verify the server exists (upload target)
	if _, ok := a.Registry.Get(uuid); !ok {
		util.WriteError(w, util.ErrNotFound("server"))
		return
	}
	tok, err := signToken(a.Cfg.Daemon.Token, claims)
	if err != nil {
		util.WriteError(w, util.ErrInternal("sign token"))
		return
	}
	util.WriteJSON(w, http.StatusOK, map[string]interface{}{
		"upload_url": "/upload?token=" + tok,
	})
}

// handleUpload implements POST /upload?token=.
func (a *App) handleUpload(w http.ResponseWriter, r *http.Request) {
	token := r.URL.Query().Get("token")
	if token == "" {
		util.WriteError(w, util.ErrBadRequest("missing token"))
		return
	}
	// parse without verifying first to find the server (then verify with its secret)
	claims, err := unverifiedClaims(token)
	if err != nil {
		util.WriteError(w, util.ErrBadRequest("bad token"))
		return
	}
	uuid, _ := claims["server"].(string)
	s, ok := a.Registry.Get(uuid)
	if !ok {
		util.WriteError(w, util.ErrNotFound("server"))
		return
	}
	secret := a.Cfg.Daemon.Token
	if _, err := verifyToken(token, secret); err != nil {
		util.WriteJSON(w, http.StatusUnauthorized, map[string]interface{}{
			"errors": []interface{}{util.NewErr(http.StatusUnauthorized, "UnauthorizedAccessException", "invalid upload token")},
		})
		return
	}

	dataDir := r.Header.Get("X-Data-Dir")
	if dataDir == "" {
		dataDir = "/"
	}
	if err := r.ParseMultipartForm(a.Cfg.Daemon.UploadLimitMB << 20); err != nil {
		util.WriteError(w, util.ErrBadRequest("multipart: "+err.Error()))
		return
	}
	form := r.MultipartForm
	if form == nil || form.File == nil {
		util.WriteError(w, util.ErrBadRequest("no files"))
		return
	}
	var saved int
	for _, fhs := range form.File {
		for _, fh := range fhs {
			// quota check
			if q := s.Quota(); q != nil {
				if !q.HasSpace(s, fh.Size) {
					util.WriteError(w, util.NewErr(http.StatusInsufficientStorage, "InsufficientQuotaException", "disk quota exceeded"))
					return
				}
			}
			src, err := fh.Open()
			if err != nil {
				util.WriteError(w, util.ErrBadRequest("open upload"))
				return
			}
			dst, err := files.SafePath(s.DataDir(), filepath.Join(dataDir, filepath.Base(fh.Filename)))
			if err != nil {
				_ = src.Close()
				util.WriteError(w, filesErr(err))
				return
			}
			if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
				_ = src.Close()
				util.WriteError(w, filesErr(err))
				return
			}
			out, err := os.Create(dst)
			if err != nil {
				_ = src.Close()
				util.WriteError(w, filesErr(err))
				return
			}
			_, err = io.Copy(out, src)
			_ = out.Close()
			_ = src.Close()
			if err != nil {
				util.WriteError(w, filesErr(err))
				return
			}
			saved++
		}
	}
	a.Log.Info("server %s: uploaded %d file(s)", s.UUID(), saved)
	w.WriteHeader(http.StatusNoContent)
}

// handleSignedDownload implements GET /download?token= (user file download).
func (a *App) handleSignedDownload(w http.ResponseWriter, r *http.Request) {
	token := r.URL.Query().Get("token")
	if token == "" {
		util.WriteError(w, util.ErrBadRequest("missing token"))
		return
	}
	claims, err := unverifiedClaims(token)
	if err != nil {
		util.WriteError(w, util.ErrBadRequest("bad token"))
		return
	}
	uuid, _ := claims["server_uuid"].(string)
	if uuid == "" {
		uuid, _ = claims["server"].(string)
	}
	filePath, _ := claims["file_path"].(string)
	srv, ok := a.Registry.Get(uuid)
	if !ok {
		util.WriteError(w, util.ErrNotFound("server"))
		return
	}
	if _, err := verifyToken(token, a.Cfg.Daemon.Token); err != nil {
		util.WriteJSON(w, http.StatusUnauthorized, map[string]interface{}{
			"errors": []interface{}{util.NewErr(http.StatusUnauthorized, "UnauthorizedAccessException", "invalid token")},
		})
		return
	}
	full, err := files.SafePath(srv.DataDir(), filePath)
	if err != nil {
		util.WriteError(w, filesErr(err))
		return
	}
	f, err := os.Open(full)
	if err != nil {
		util.WriteError(w, filesErr(err))
		return
	}
	defer func() { _ = f.Close() }()
	fi, _ := f.Stat()
	w.Header().Set("Content-Type", "application/octet-stream")
	if fi != nil {
		w.Header().Set("Content-Length", fmt.Sprintf("%d", fi.Size()))
	}
	_, _ = io.Copy(w, f)
}

// handleSignedBackupDownload implements GET /download/backup?token=.
func (a *App) handleSignedBackupDownload(w http.ResponseWriter, r *http.Request) {
	token := r.URL.Query().Get("token")
	if token == "" {
		util.WriteError(w, util.ErrBadRequest("missing token"))
		return
	}
	claims, err := unverifiedClaims(token)
	if err != nil {
		util.WriteError(w, util.ErrBadRequest("bad token"))
		return
	}
	uuid, _ := claims["server"].(string)
	if uuid == "" {
		uuid, _ = claims["server_uuid"].(string)
	}
	backup, _ := claims["backup"].(string)
	if backup == "" {
		backup, _ = claims["backup_uuid"].(string)
	}
	if _, ok := a.Registry.Get(uuid); !ok {
		util.WriteError(w, util.ErrNotFound("server"))
		return
	}
	secret := a.Cfg.Daemon.Token
	if _, err := verifyToken(token, secret); err != nil {
		util.WriteJSON(w, http.StatusUnauthorized, map[string]interface{}{
			"errors": []interface{}{util.NewErr(http.StatusUnauthorized, "UnauthorizedAccessException", "invalid token")},
		})
		return
	}
	_ = backup
	path := filepath.Join(a.Cfg.Daemon.BackupPath, uuid, backup+".tar.gz")
	f, err := os.Open(path)
	if err != nil {
		util.WriteError(w, util.ErrNotFound("backup"))
		return
	}
	defer func() { _ = f.Close() }()
	fi, _ := f.Stat()
	w.Header().Set("Content-Type", "application/gzip")
	if fi != nil {
		w.Header().Set("Content-Length", fmt.Sprintf("%d", fi.Size()))
	}
	_, _ = io.Copy(w, f)
}
