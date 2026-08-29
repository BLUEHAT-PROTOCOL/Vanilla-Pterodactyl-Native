// Package files implements the wings-compatible file manager with path safety.
package files

import (
	"archive/tar"
	"strconv"
	"archive/zip"
	"compress/gzip"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// Entry is the wings FileEntry JSON (created/modified REQUIRED by panel frontend).
type Entry struct {
	Name     string `json:"name"`
	Size     int64  `json:"size"`
	Mode     uint32 `json:"mode"`
	ModeBits string `json:"mode_bits"`
	Created  string `json:"created"`
	Modified string `json:"modified"`
}

func rfc3339(t time.Time) string {
	if t.IsZero() {
		t = time.Unix(0, 0)
	}
	return t.UTC().Format("2006-01-02T15:04:05.000000Z07:00")
}

// modeBitsString renders 4-digit octal like "0644"/"0755" (setuid bits stripped).
func modeBitsString(mode fs.FileMode) string {
	bits := uint32(mode.Perm())
	if mode.IsDir() {
		bits |= 0o400 // keep dir bit pattern readable
	}
	return fmt.Sprintf("%04o", bits)
}

// EntryFromInfo converts an os.FileInfo into a wings FileEntry.
func EntryFromInfo(info os.FileInfo) Entry {
	return Entry{
		Name:     info.Name(),
		Size:     info.Size(),
		Mode:     uint32(info.Mode()),
		ModeBits: modeBitsString(info.Mode()),
		Created:  rfc3339(cTime(info)),
		Modified: rfc3339(info.ModTime()),
	}
}

// cTime best-effort creation/birth time (falls back to mod time).
func cTime(info os.FileInfo) time.Time {
	type statT interface {
		Sys() interface{}
	}
	if st, ok := info.Sys().(interface{ Birthtime() time.Time }); ok {
		if !st.Birthtime().IsZero() {
			return st.Birthtime()
		}
	}
	return info.ModTime()
}

// ErrOutsideRoot is returned when a path escapes the server volume.
type ErrOutsideRoot struct{ Path string }

func (e *ErrOutsideRoot) Error() string { return "path escapes server root: " + e.Path }

// SafePath resolves a server-relative path and guarantees it stays inside root.
// Symlink escapes are rejected.
func SafePath(root, sub string) (string, error) {
	if sub == "" || sub == "." || sub == "/" || sub == "~" {
		return root, nil
	}
	clean := path.Clean("/" + strings.TrimPrefix(sub, "/"))
	full := filepath.Join(root, clean)
	absRoot, err := filepath.Abs(root)
	if err != nil {
		return "", err
	}
	absFull, err := filepath.Abs(full)
	if err != nil {
		return "", err
	}
	if absFull != absRoot && !strings.HasPrefix(absFull, absRoot+string(filepath.Separator)) {
		return "", &ErrOutsideRoot{Path: sub}
	}
	// symlink escape check: walk each component using Lstat
	rel, _ := filepath.Rel(absRoot, absFull)
	cur := absRoot
	if rel != "." {
		for _, part := range strings.Split(rel, string(filepath.Separator)) {
			if part == "" || part == "." {
				continue
			}
			cur = filepath.Join(cur, part)
			fi, err := os.Lstat(cur)
			if err != nil {
				// not found yet — fine (creation target)
				continue
			}
			if fi.Mode()&os.ModeSymlink != 0 {
				real, err := filepath.EvalSymlinks(cur)
				if err == nil {
					if real != absRoot && !strings.HasPrefix(real, absRoot+string(filepath.Separator)) {
						return "", &ErrOutsideRoot{Path: sub}
					}
				}
			}
		}
	}
	return full, nil
}

// List returns entries of a directory sorted (dirs first, then files).
func List(root, dir string) ([]Entry, error) {
	full, err := SafePath(root, dir)
	if err != nil {
		return nil, err
	}
	entries, err := os.ReadDir(full)
	if err != nil {
		return nil, err
	}
	out := make([]Entry, 0, len(entries))
	for _, e := range entries {
		info, err := e.Info()
		if err != nil {
			continue
		}
		if e.Type()&os.ModeSymlink != 0 {
			// follow symlink for display; broken symlinks still listed
			if real, rerr := filepath.EvalSymlinks(filepath.Join(full, e.Name())); rerr == nil {
				if fi2, serr := os.Stat(real); serr == nil {
					info = fi2
				}
			}
		}
		out = append(out, EntryFromInfo(info))
	}
	sort.Slice(out, func(i, j int) bool {
		// directories first, then alphabetical
		iDir, _ := isDir(full, out[i].Name)
		jDir, _ := isDir(full, out[j].Name)
		if iDir != jDir {
			return iDir
		}
		return out[i].Name < out[j].Name
	})
	return out, nil
}

func isDir(parent, name string) (bool, error) {
	fi, err := os.Stat(filepath.Join(parent, name))
	if err != nil {
		return false, err
	}
	return fi.IsDir(), nil
}

// Read returns the contents of a file (capped).
func Read(root, file string, limit int64) ([]byte, error) {
	full, err := SafePath(root, file)
	if err != nil {
		return nil, err
	}
	fi, err := os.Stat(full)
	if err != nil {
		return nil, err
	}
	if fi.IsDir() {
		return nil, fmt.Errorf("%s is a directory", file)
	}
	f, err := os.Open(full)
	if err != nil {
		return nil, err
	}
	defer func() { _ = f.Close() }()
	if limit <= 0 {
		limit = 2 << 20 // 2 MiB default cap for editor
	}
	return io.ReadAll(io.LimitReader(f, limit))
}

// Write writes file contents atomically.
func Write(root, file string, data []byte, allowAppend bool) error {
	full, err := SafePath(root, file)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
		return err
	}
	if allowAppend {
		f, ferr := os.OpenFile(full, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
		if ferr != nil {
			return ferr
		}
		_, werr := f.Write(data)
		_ = f.Close()
		return werr
	}
	tmp := full + ".ptero-tmp"
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, full)
}

// Rename renames/moves entries within root.
func Rename(root string, pairs [][2]string) error {
	for _, p := range pairs {
		from, err := SafePath(root, p[0])
		if err != nil {
			return err
		}
		to, err := SafePath(root, p[1])
		if err != nil {
			return err
		}
		if err := os.MkdirAll(filepath.Dir(to), 0o755); err != nil {
			return err
		}
		if err := os.Rename(from, to); err != nil {
			return err
		}
	}
	return nil
}

// Copy copies a file (or directory) within root.
func Copy(root, src string) error {
	from, err := SafePath(root, src)
	if err != nil {
		return err
	}
	fi, err := os.Stat(from)
	if err != nil {
		return err
	}
	dst := from
	ext := filepath.Ext(from)
	if ext != "" {
		dst = strings.TrimSuffix(from, ext) + ".copy" + ext
	} else {
		dst = from + ".copy"
	}
	if fi.IsDir() {
		return copyDir(from, dst)
	}
	return copyFile(from, dst)
}

func copyFile(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer func() { _ = in.Close() }()
	out, err := os.OpenFile(dst, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o644)
	if err != nil {
		return err
	}
	defer func() { _ = out.Close() }()
	_, err = io.Copy(out, in)
	return err
}

func copyDir(src, dst string) error {
	return filepath.WalkDir(src, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		rel, _ := filepath.Rel(src, p)
		target := filepath.Join(dst, rel)
		if d.IsDir() {
			return os.MkdirAll(target, 0o755)
		}
		if d.Type()&os.ModeSymlink != 0 {
			return nil // skip symlinks on copy (security)
		}
		if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
			return err
		}
		return copyFile(p, target)
	})
}

// CreateDirectory creates nested directories.
func CreateDirectory(root, dir, name string) error {
	full, err := SafePath(root, filepath.Join(dir, name))
	if err != nil {
		return err
	}
	return os.MkdirAll(full, 0o755)
}

// Delete removes files/directories (refuses to remove the root itself).
func Delete(root string, files []string) error {
	for _, f := range files {
		full, err := SafePath(root, f)
		if err != nil {
			return err
		}
		if strings.TrimSpace(full) == strings.TrimSpace(root) {
			return fmt.Errorf("refusing to delete the data root")
		}
		if err := os.RemoveAll(full); err != nil {
			return err
		}
	}
	return nil
}

// Compress creates a .tar.gz from the given entries (like wings).
func Compress(root, dir string, names []string) (Entry, error) {
	base, err := SafePath(root, dir)
	if err != nil {
		return Entry{}, err
	}
	archiveName := "archive-" + time.Now().UTC().Format("20060102-150405") + ".tar.gz"
	if len(names) == 1 {
		base_ := strings.TrimSuffix(names[0], filepath.Ext(names[0]))
		if base_ != "" {
			archiveName = base_ + ".tar.gz"
		}
	}
	outPath, err := SafePath(root, filepath.Join(dir, archiveName))
	if err != nil {
		return Entry{}, err
	}
	out, err := os.Create(outPath)
	if err != nil {
		return Entry{}, err
	}
	defer func() { _ = out.Close() }()

	gz := gzip.NewWriter(out)
	tw := tar.NewWriter(gz)

	for _, name := range names {
		full, err := SafePath(base, name)
		if err != nil {
			return Entry{}, err
		}
		if err := addToTar(tw, base, full); err != nil {
			return Entry{}, err
		}
	}
	if err := tw.Close(); err != nil {
		return Entry{}, err
	}
	if err := gz.Close(); err != nil {
		return Entry{}, err
	}
	_ = out.Sync()

	info, err := os.Stat(outPath)
	if err != nil {
		return Entry{}, err
	}
	e := EntryFromInfo(info)
	e.Name = archiveName
	return e, nil
}

// addToTar recursively adds a path to a tar archive (symlinks skipped).
func addToTar(tw *tar.Writer, base, full string) error {
	return filepath.Walk(full, func(p string, fi os.FileInfo, err error) error {
		if err != nil {
			return nil
		}
		if fi.Mode()&os.ModeSymlink != 0 {
			return nil
		}
		rel, err := filepath.Rel(base, p)
		if err != nil {
			return nil
		}
		hdr, err := tar.FileInfoHeader(fi, "")
		if err != nil {
			return nil
		}
		hdr.Name = filepath.ToSlash(rel)
		if fi.IsDir() {
			hdr.Name += "/"
		}
		if err := tw.WriteHeader(hdr); err != nil {
			return err
		}
		if fi.IsDir() {
			return nil
		}
		f, err := os.Open(p)
		if err != nil {
			return nil
		}
		defer func() { _ = f.Close() }()
		_, err = io.Copy(tw, f)
		return err
	})
}

// maxUncompressed caps extraction size (zip-bomb guard).
const maxUncompressed = 20 << 30 // 20 GiB hard cap

// Uncompress extracts .tar.gz/.tar/.zip safely (traversal + bomb guards).
func Uncompress(root, file string) error {
	full, err := SafePath(root, file)
	if err != nil {
		return err
	}
	base := filepath.Dir(full)

	lower := strings.ToLower(full)
	switch {
	case strings.HasSuffix(lower, ".zip"):
		return extractZip(full, base)
	default: // tar, tar.gz, tgz
		return extractTar(full, base)
	}
}

func extractTar(archive, dest string) error {
	f, err := os.Open(archive)
	if err != nil {
		return err
	}
	defer func() { _ = f.Close() }()

	var tr *tar.Reader
	if strings.HasSuffix(strings.ToLower(archive), ".gz") || strings.HasSuffix(strings.ToLower(archive), ".tgz") {
		gz, err := gzip.NewReader(f)
		if err != nil {
			return err
		}
		defer func() { _ = gz.Close() }()
		tr = tar.NewReader(gz)
	} else {
		tr = tar.NewReader(f)
	}

	var total int64
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return err
		}
		name := hdr.Name
		if strings.Contains(name, "..") || strings.HasPrefix(name, "/") || strings.Contains(name, "\x00") {
			return fmt.Errorf("unsafe tar member: %s", name)
		}
		target, err := SafePath(dest, name)
		if err != nil {
			return err
		}
		switch hdr.Typeflag {
		case tar.TypeDir:
			if err := os.MkdirAll(target, 0o755); err != nil {
				return err
			}
		case tar.TypeReg:
			if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
				return err
			}
			total += hdr.Size
			if total > maxUncompressed {
				return fmt.Errorf("uncompressed size exceeds safety cap")
			}
			out, err := os.OpenFile(target, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, os.FileMode(hdr.Mode)&0o777)
			if err != nil {
				return err
			}
			n, err := io.Copy(out, io.LimitReader(tr, maxUncompressed-total))
			_ = out.Close()
			if err != nil {
				return err
			}
			total += n - hdr.Size // correct: actual copied
		default:
			// skip links/devices (security)
		}
	}
	return nil
}

func extractZip(archive, dest string) error {
	zr, err := zip.OpenReader(archive)
	if err != nil {
		return err
	}
	defer func() { _ = zr.Close() }()

	var total int64
	for _, zf := range zr.File {
		name := zf.Name
		if strings.Contains(name, "..") || strings.HasPrefix(name, "/") {
			return fmt.Errorf("unsafe zip member: %s", name)
		}
		target, err := SafePath(dest, name)
		if err != nil {
			return err
		}
		if zf.FileInfo().IsDir() {
			if err := os.MkdirAll(target, 0o755); err != nil {
				return err
			}
			continue
		}
		if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
			return err
		}
		rc, err := zf.Open()
		if err != nil {
			return err
		}
		total += int64(zf.UncompressedSize64)
		if total > maxUncompressed {
			_ = rc.Close()
			return fmt.Errorf("uncompressed size exceeds safety cap")
		}
		out, err := os.OpenFile(target, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, zf.Mode().Perm()&0o777)
		if err != nil {
			_ = rc.Close()
			return err
		}
		_, err = io.Copy(out, rc)
		_ = out.Close()
		_ = rc.Close()
		if err != nil {
			return err
		}
	}
	return nil
}

// ChmodEntry is one chmod operation.
type ChmodEntry struct {
	File string
	Mode string
}

// Chmod applies mode changes within root (setuid bits stripped).
func Chmod(root string, entries []ChmodEntry) error {
	for _, e := range entries {
		full, err := SafePath(root, e.File)
		if err != nil {
			return err
		}
		mode, err := parseMode(e.Mode)
		if err != nil {
			return err
		}
		if err := os.Chmod(full, mode); err != nil {
			return err
		}
	}
	return nil
}

// parseMode parses octal ("0755") or symbolic ("rwxr-xr-x") modes.
func parseMode(s string) (os.FileMode, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return 0, fmt.Errorf("empty mode")
	}
	if strings.ContainsAny(s, "rwx-") {
		// symbolic: 9 chars
		if len(s) != 9 {
			return 0, fmt.Errorf("invalid symbolic mode: %s", s)
		}
		var m os.FileMode
		perms := []struct {
			chars string
			bit   os.FileMode
		}{
			{"r", 0o400}, {"w", 0o200}, {"x", 0o100},
			{"r", 0o040}, {"w", 0o020}, {"x", 0o010},
			{"r", 0o004}, {"w", 0o002}, {"x", 0o001},
		}
		for i, p := range perms {
			if s[i] == p.chars[0] {
				m |= p.bit
			}
		}
		return m, nil
	}
	v, err := strconv.ParseUint(s, 8, 32)
	if err != nil {
		return 0, err
	}
	return os.FileMode(v) & 0o777, nil
}

// Stat returns a single entry.
func Stat(root, file string) (Entry, error) {
	full, err := SafePath(root, file)
	if err != nil {
		return Entry{}, err
	}
	info, err := os.Stat(full)
	if err != nil {
		return Entry{}, err
	}
	return EntryFromInfo(info), nil
}
