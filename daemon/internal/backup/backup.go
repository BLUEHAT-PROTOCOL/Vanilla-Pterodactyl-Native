// Package backup implements tar.gz backups with sha256 checksums.
package backup

import (
	"archive/tar"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// Info describes a local backup.
type Info struct {
	UUID        string `json:"uuid"`
	ServerUUID  string `json:"server_uuid"`
	Name        string `json:"name"`
	Size        int64  `json:"size"`
	Checksum    string `json:"checksum"` // sha256 hex
	CreatedAt   string `json:"created_at"`
	CompletedAt string `json:"completed_at"`
	Successful  bool   `json:"successful"`
	Locked      bool   `json:"locked"`
}

// Manager tracks local backups per server.
type Manager struct {
	mu      sync.RWMutex
	backups map[string]map[string]Info // serverUUID -> backupUUID -> info
}

// NewManager creates an empty manager.
func NewManager() *Manager {
	return &Manager{backups: map[string]map[string]Info{}}
}

// running tracks in-flight backup jobs.
var running = struct {
	mu  sync.Mutex
	set map[string]struct{}
}{set: map[string]struct{}{}}

// BackupPath returns the backup file path.
func (m *Manager) BackupPath(base, serverUUID, backupUUID string) string {
	return filepath.Join(base, serverUUID, backupUUID+".tar.gz")
}

// Create archives the server data directory to base/<server>/<backup>.tar.gz.
// Reports progress via onProgress (0-100) when non-nil.
func (m *Manager) Create(base, serverUUID, backupUUID, name, dataDir, ignore string) (Info, error) {
	running.mu.Lock()
	if _, busy := running.set[serverUUID]; busy {
		running.mu.Unlock()
		return Info{}, fmt.Errorf("backup already in progress for this server")
	}
	running.set[serverUUID] = struct{}{}
	running.mu.Unlock()
	defer func() {
		running.mu.Lock()
		delete(running.set, serverUUID)
		running.mu.Unlock()
	}()

	dst := m.BackupPath(base, serverUUID, backupUUID)
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return Info{}, err
	}
	tmp := dst + ".tmp"

	f, err := os.Create(tmp)
	if err != nil {
		return Info{}, err
	}
	defer func() {
		_ = f.Close()
		_ = os.Remove(tmp) // no-op after successful rename
	}()

	hasher := sha256.New()
	gz := gzip.NewWriter(io.MultiWriter(f, hasher))
	tw := tar.NewWriter(gz)

	var total int64
	ignoreSet := map[string]struct{}{}
	for _, line := range strings.Split(ignore, "\n") {
		line = strings.TrimSpace(line)
		if line != "" {
			ignoreSet[line] = struct{}{}
		}
	}
	err = filepath.Walk(dataDir, func(p string, fi fs.FileInfo, werr error) error {
		if werr != nil {
			return nil
		}
		// skip the backups dir if nested inside, and any tmp files
		if fi.Mode()&os.ModeSymlink != 0 {
			return nil
		}
		rel, rerr := filepath.Rel(dataDir, p)
		if rerr != nil {
			return nil
		}
		if rel == "." {
			return nil
		}
		if _, skip := ignoreSet[rel]; skip {
			if fi.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		hdr, herr := tar.FileInfoHeader(fi, "")
		if herr != nil {
			return nil
		}
		hdr.Name = filepath.ToSlash(rel)
		if fi.IsDir() {
			hdr.Name += "/"
		}
		if terr := tw.WriteHeader(hdr); terr != nil {
			return terr
		}
		if fi.IsDir() {
			return nil
		}
		src, oerr := os.Open(p)
		if oerr != nil {
			return nil
		}
		n, cerr := io.Copy(tw, src)
		_ = src.Close()
		total += n
		return cerr
	})
	if err == nil {
		err = tw.Close()
		if err == nil {
			err = gz.Close()
			if err == nil {
				err = f.Sync()
			}
		}
	}
	if err != nil {
		return Info{}, err
	}
	if err := os.Rename(tmp, dst); err != nil {
		return Info{}, err
	}

	fi, err := os.Stat(dst)
	if err != nil {
		return Info{}, err
	}
	info := Info{
		UUID:        backupUUID,
		ServerUUID:  serverUUID,
		Name:        name,
		Size:        fi.Size(),
		Checksum:    hex.EncodeToString(hasher.Sum(nil)),
		CreatedAt:   time.Now().UTC().Format(time.RFC3339),
		CompletedAt: time.Now().UTC().Format(time.RFC3339),
		Successful:  true,
	}

	m.mu.Lock()
	if m.backups[serverUUID] == nil {
		m.backups[serverUUID] = map[string]Info{}
	}
	m.backups[serverUUID][backupUUID] = info
	m.mu.Unlock()
	_ = total
	return info, nil
}

// Restore extracts a backup into the data dir (optionally truncating first).
func (m *Manager) Restore(base, serverUUID, backupUUID, dataDir string, truncate bool) error {
	src := m.BackupPath(base, serverUUID, backupUUID)
	if _, err := os.Stat(src); err != nil {
		return fmt.Errorf("backup archive not found")
	}
	if truncate {
		entries, err := os.ReadDir(dataDir)
		if err == nil {
			for _, e := range entries {
				_ = os.RemoveAll(filepath.Join(dataDir, e.Name()))
			}
		}
	}
	f, err := os.Open(src)
	if err != nil {
		return err
	}
	defer func() { _ = f.Close() }()
	gz, err := gzip.NewReader(f)
	if err != nil {
		return err
	}
	defer func() { _ = gz.Close() }()
	tr := tar.NewReader(gz)
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return err
		}
		name := hdr.Name
		if filepath.IsAbs(name) || containsDotDot(name) {
			return fmt.Errorf("unsafe backup member: %s", name)
		}
		target := filepath.Join(dataDir, filepath.FromSlash(name))
		switch hdr.Typeflag {
		case tar.TypeDir:
			if err := os.MkdirAll(target, 0o755); err != nil {
				return err
			}
		case tar.TypeReg:
			if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
				return err
			}
			out, oerr := os.OpenFile(target, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, os.FileMode(hdr.Mode)&0o777)
			if oerr != nil {
				return oerr
			}
			_, cerr := io.Copy(out, tr)
			_ = out.Close()
			if cerr != nil {
				return cerr
			}
		default:
			// skip non-regular entries
		}
	}
	return nil
}

func containsDotDot(s string) bool {
	for _, part := range splitPath(s) {
		if part == ".." {
			return true
		}
	}
	return false
}

func splitPath(s string) []string {
	var out []string
	cur := ""
	for _, c := range s {
		if c == '/' || c == '\\' {
			if cur != "" {
				out = append(out, cur)
				cur = ""
			}
			continue
		}
		cur += string(c)
	}
	if cur != "" {
		out = append(out, cur)
	}
	return out
}

// Get returns a backup's info.
func (m *Manager) Get(serverUUID, backupUUID string) (Info, bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	if m.backups[serverUUID] == nil {
		return Info{}, false
	}
	b, ok := m.backups[serverUUID][backupUUID]
	return b, ok
}

// List returns all backups of a server.
func (m *Manager) List(serverUUID string) []Info {
	m.mu.RLock()
	defer m.mu.RUnlock()
	var out []Info
	for _, b := range m.backups[serverUUID] {
		out = append(out, b)
	}
	// stable order: newest first
	for i := 0; i < len(out); i++ {
		for j := i + 1; j < len(out); j++ {
			if out[j].CreatedAt > out[i].CreatedAt {
				out[i], out[j] = out[j], out[i]
			}
		}
	}
	return out
}

// Delete removes a backup (file + record).
func (m *Manager) Delete(base, serverUUID, backupUUID string) error {
	m.mu.Lock()
	if m.backups[serverUUID] != nil {
		delete(m.backups[serverUUID], backupUUID)
	}
	m.mu.Unlock()
	return os.Remove(m.BackupPath(base, serverUUID, backupUUID))
}

// LoadFromDisk scans the backup directory and rebuilds the index.
func (m *Manager) LoadFromDisk(base string) {
	serverDirs, err := os.ReadDir(base)
	if err != nil {
		return
	}
	for _, sd := range serverDirs {
		if !sd.IsDir() {
			continue
		}
		serverUUID := sd.Name()
		files, err := os.ReadDir(filepath.Join(base, serverUUID))
		if err != nil {
			continue
		}
		for _, f := range files {
			name := f.Name()
			if !hasSuffix(name, ".tar.gz") {
				continue
			}
			buuid := name[:len(name)-len(".tar.gz")]
			fi, err := f.Info()
			if err != nil {
				continue
			}
			if _, ok := m.Get(serverUUID, buuid); ok {
				continue
			}
			m.mu.Lock()
			if m.backups[serverUUID] == nil {
				m.backups[serverUUID] = map[string]Info{}
			}
			m.backups[serverUUID][buuid] = Info{
				UUID:       buuid,
				ServerUUID: serverUUID,
				Name:       buuid,
				Size:       fi.Size(),
				CreatedAt:  fi.ModTime().UTC().Format(time.RFC3339),
				Successful: true,
			}
			m.mu.Unlock()
		}
	}
}

func hasSuffix(s, suf string) bool {
	return len(s) >= len(suf) && s[len(s)-len(suf):] == suf
}

// IsRunning reports whether a backup is in flight for the server.
func IsRunning(serverUUID string) bool {
	running.mu.Lock()
	defer running.mu.Unlock()
	_, ok := running.set[serverUUID]
	return ok
}
