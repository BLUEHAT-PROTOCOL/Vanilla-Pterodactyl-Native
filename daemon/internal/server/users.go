package server

import (
	"os"
	"os/exec"
	"os/user"
	"path/filepath"
	"strconv"
	"strings"
)

type serverUser struct {
	uid, gid int
	username string
}

// lookupServerUser resolves (and creates when possible) the dedicated unix user
// for a server: <prefix><first 8 chars of uuid without dashes>.
func lookupServerUser(cfg interface {
	ServerVolume(uuid string) string
}, uuid string) *serverUser {
	if os.Geteuid() != 0 {
		return nil
	}
	short := shortUUID(uuid)
	username := "vrp_" + short
	if u, err := user.Lookup(username); err == nil {
		uid, _ := strconv.Atoi(u.Uid)
		gid, _ := strconv.Atoi(u.Gid)
		return &serverUser{uid: uid, gid: gid, username: username}
	}
	// try to create the user (requires root; ignore failure)
	if err := createServerUser(username); err == nil {
		if u, err := user.Lookup(username); err == nil {
			uid, _ := strconv.Atoi(u.Uid)
			gid, _ := strconv.Atoi(u.Gid)
			return &serverUser{uid: uid, gid: gid, username: username}
		}
	}
	return nil
}

func shortUUID(uuid string) string {
	clean := strings.ReplaceAll(uuid, "-", "")
	if len(clean) > 8 {
		clean = clean[:8]
	}
	return strings.ToLower(clean)
}

// createServerUser creates a nologin system user via useradd.
func createServerUser(username string) error {
	if os.Geteuid() != 0 {
		return os.ErrPermission
	}
	out, err := exec.Command("useradd", "--system", "--no-create-home", "--shell", "/usr/sbin/nologin", username).CombinedOutput()
	if err != nil {
		// user may already exist
		if strings.Contains(string(out), "already exists") {
			return nil
		}
		return err
	}
	return nil
}

// ChownVolume sets ownership of the server volume to the server user.
func (s *Server) ChownVolume() {
	if os.Geteuid() != 0 {
		return
	}
	u := lookupServerUser(s.cfg, s.Cfg.UUID)
	if u == nil {
		return
	}
	vol := s.DataDir()
	_ = os.MkdirAll(vol, 0o755)
	_ = chownRecursive(vol, u.uid, u.gid)
}

// chownRecursive chowns a directory tree.
func chownRecursive(root string, uid, gid int) error {
	return filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return nil // best effort
		}
		return os.Lchown(path, uid, gid)
	})
}
