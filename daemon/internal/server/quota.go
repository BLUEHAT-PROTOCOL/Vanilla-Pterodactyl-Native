package server

import (
	"io/fs"
	"path/filepath"
	"time"
)

// QuotaTracker implements the Quota interface with cached directory walks.
type QuotaTracker struct{}

// UsedBytes returns the data-dir usage for a server (cached, refreshed every 30s).
func (q *QuotaTracker) UsedBytes(s *Server) int64 {
	s.mu.RLock()
	if time.Since(s.diskCacheAt) < 30*time.Second {
		v := s.diskCacheBytes
		s.mu.RUnlock()
		return v
	}
	s.mu.RUnlock()

	var total int64
	_ = walkSize(s.DataDir(), &total)
	_ = walkSize(filepath.Join(s.cfg.Daemon.BackupPath, s.Cfg.UUID()), &total)

	s.mu.Lock()
	s.diskCacheBytes = total
	s.diskCacheAt = time.Now()
	s.mu.Unlock()
	return total
}

// LimitBytes returns the disk limit for a server in bytes.
func (q *QuotaTracker) LimitBytes(s *Server) int64 {
	lim := s.Cfg.Settings.Build.DiskSpace
	if lim <= 0 {
		return 0 // unlimited
	}
	return lim * 1024 * 1024
}

// HasSpace returns whether writing n bytes is within quota.
func (q *QuotaTracker) HasSpace(s *Server, n int64) bool {
	limit := q.LimitBytes(s)
	if limit <= 0 {
		return true
	}
	return q.UsedBytes(s)+n <= limit
}

// walkSize sums file sizes under root.
func walkSize(root string, total *int64) error {
	return filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if !d.IsDir() {
			if fi, ferr := d.Info(); ferr == nil {
				*total += fi.Size()
			}
		}
		return nil
	})
}
