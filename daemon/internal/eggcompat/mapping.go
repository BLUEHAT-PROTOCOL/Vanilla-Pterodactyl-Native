// Package eggcompat resolves docker images to native runtimes and translates
// egg startup/config conventions for the native daemon.
package eggcompat

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

// Mapping is the resolved runtime for an image.
type Mapping struct {
	Profile string            `json:"profile"` // node|python|java|static|custom
	Version string            `json:"version"`
	Path    string            `json:"path"` // bin dir prepended to PATH
	Env     map[string]string `json:"env,omitempty"`
}

// Resolver maps docker image names to native runtimes.
type Resolver struct {
	// mappings sourced from daemon config + panel runtime_mappings sync
	mappings map[string]Mapping
}

// NewResolver builds a resolver with the given static mappings.
func NewResolver(m map[string]Mapping) *Resolver {
	r := &Resolver{mappings: map[string]Mapping{}}
	for k, v := range m {
		r.mappings[strings.ToLower(k)] = v
	}
	// built-in defaults for official yolks
	defaults := map[string]string{
		"ghcr.io/pterodactyl/yolks:nodejs_18": "node20",
		"ghcr.io/pterodactyl/yolks:nodejs_20": "node20",
		"ghcr.io/pterodactyl/yolks:nodejs_22": "node22",
		"ghcr.io/pterodactyl/yolks:python_3.11":  "python311",
		"ghcr.io/pterodactyl/yolks:python_3.12":  "python312",
		"ghcr.io/pterodactyl/yolks:java_17":      "java17",
		"ghcr.io/pterodactyl/yolks:java_21":      "java21",
		"ghcr.io/pterodactyl/yolks:java_22":      "java21",
		"ghcr.io/pterodactyl/yolks:debian":       "static",
		"ghcr.io/pterodactyl/yolks:ubuntu":       "static",
		"ghcr.io/pterodactyl/yolks:alpine":       "static",
	}
	binFor := map[string]string{
		"node20":    "/opt/runtimes/node20/bin",
		"node22":    "/opt/runtimes/node22/bin",
		"python311": "/opt/runtimes/python311/bin",
		"python312": "/opt/runtimes/python312/bin",
		"java17":    "/opt/runtimes/java17/bin",
		"java21":    "/opt/runtimes/java21/bin",
		"static":    "",
		"custom":    "",
	}
	profileOf := map[string]string{
		"node20": "node", "node22": "node",
		"python311": "python", "python312": "python",
		"java17": "java", "java21": "java",
		"static": "static", "custom": "custom",
	}
	versionOf := map[string]string{
		"node20": "20", "node22": "22",
		"python311": "3.11", "python312": "3.12",
		"java17": "17", "java21": "21",
		"static": "", "custom": "",
	}
	for image, profile := range defaults {
		key := strings.ToLower(image)
		if _, ok := r.mappings[key]; !ok {
			r.mappings[key] = Mapping{
				Profile: profileOf[profile],
				Version: versionOf[profile],
				Path:    binFor[profile],
			}
		}
	}
	return r
}

// Resolve maps an image to a runtime mapping (image matched case-insensitively,
// tag-normalized: docker.io/library prefixes stripped).
func (r *Resolver) Resolve(image string) (*Mapping, error) {
	key := NormalizeImage(image)
	if m, ok := r.mappings[key]; ok {
		cp := m
		return &cp, nil
	}
	// graceful degradation: custom profile runs with system PATH
	return &Mapping{Profile: "custom", Version: ""}, nil
}

// NormalizeImage normalizes a docker image reference for lookup.
func NormalizeImage(image string) string {
	s := strings.ToLower(strings.TrimSpace(image))
	s = strings.TrimPrefix(s, "https://")
	s = strings.TrimPrefix(s, "docker.io/")
	s = strings.TrimPrefix(s, "library/")
	return s
}

// SetMapping inserts/overrides a mapping at runtime (panel sync).
func (r *Resolver) SetMapping(image string, m Mapping) {
	r.mappings[NormalizeImage(image)] = m
}

// varNameRe validates {{VAR}} placeholders.
var varNameRe = regexp.MustCompile(`^\{\{\s*([A-Za-z_][A-Za-z0-9_]*)\s*\}\}`)

// TranslateStartup converts a docker-style startup into a native bash command:
// {{VAR}} -> ${VAR}, /home/container and /mnt/server -> data dir.
func TranslateStartup(startup, dataDir string) string {
	s := startup
	s = TranslateDockerPaths(s, dataDir)
	var b strings.Builder
	rest := s
	for {
		loc := varNameRe.FindStringSubmatchIndex(rest)
		if loc == nil {
			b.WriteString(rest)
			break
		}
		b.WriteString(rest[:loc[0]])
		b.WriteString("${" + rest[loc[2]:loc[3]] + "}")
		rest = rest[loc[1]:]
	}
	return b.String()
}

// TranslateDockerPaths rewrites known docker container paths to the data dir.
func TranslateDockerPaths(s, dataDir string) string {
	repl := func(p string) string {
		return p
	}
	_ = repl
	out := strings.ReplaceAll(s, "/home/container", dataDir)
	out = strings.ReplaceAll(out, "/mnt/server", dataDir)
	out = strings.ReplaceAll(out, "~/", dataDir+"/")
	return out
}

// SanitizeDataDir ensures the directory exists.
func SanitizeDataDir(dir string) error {
	if dir == "" {
		return fmt.Errorf("empty data dir")
	}
	return os.MkdirAll(dir, 0o755)
}

// DetectProfileFromInvocation guesses a runtime profile from the startup line
// (used when no image mapping exists).
func DetectProfileFromInvocation(startup string) string {
	fields := strings.Fields(strings.TrimSpace(startup))
	if len(fields) == 0 {
		return "custom"
	}
	switch filepath.Base(fields[0]) {
	case "node", "nodejs", "npm", "npx", "pnpm", "yarn", "bun", "ts-node", "tsx":
		return "node"
	case "python", "python3", "python3.11", "python3.12", "pip", "uvicorn", "gunicorn":
		return "python"
	case "java", "java17", "java21":
		return "java"
	case "busybox", "httpd", "nginx", "caddy", "python-server":
		return "static"
	default:
		return "custom"
	}
}
