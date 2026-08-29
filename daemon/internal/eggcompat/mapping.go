// Package eggcompat resolves docker images to native runtimes and translates
// egg startup/config conventions for the native daemon.
package eggcompat

import (
	"encoding/json"
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
	mappings map[string]Mapping
}

// NewResolver builds a resolver with the given static mappings.
func NewResolver(m map[string]Mapping) *Resolver {
	r := &Resolver{mappings: map[string]Mapping{}}
	for k, v := range m {
		r.mappings[strings.ToLower(k)] = v
	}
	r.loadDefaults()
	return r
}

// loadDefaults seeds official yolks mappings.
func (r *Resolver) loadDefaults() {
	defaults := map[string]string{
		"ghcr.io/pterodactyl/yolks:nodejs_18":   "node20",
		"ghcr.io/pterodactyl/yolks:nodejs_20":   "node20",
		"ghcr.io/pterodactyl/yolks:nodejs_22":   "node22",
		"ghcr.io/pterodactyl/yolks:python_3.11": "python311",
		"ghcr.io/pterodactyl/yolks:python_3.12": "python312",
		"ghcr.io/pterodactyl/yolks:java_17":     "java17",
		"ghcr.io/pterodactyl/yolks:java_21":     "java21",
		"ghcr.io/pterodactyl/yolks:java_22":     "java21",
		"ghcr.io/pterodactyl/yolks:debian":      "static",
		"ghcr.io/pterodactyl/yolks:ubuntu":      "static",
		"ghcr.io/pterodactyl/yolks:alpine":      "static",
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
	for image, slug := range defaults {
		key := strings.ToLower(image)
		if _, ok := r.mappings[key]; !ok {
			r.mappings[key] = Mapping{
				Profile: profileOf[slug],
				Version: versionOf[slug],
				Path:    binFor[slug],
			}
		}
	}
}

// Resolve maps an image to a runtime mapping (case-insensitive, tag-normalized).
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

// TranslateStartup converts a docker-style startup into a native bash command.
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

// DetectProfileFromInvocation guesses a runtime profile from the startup line.
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
	case "java":
		return "java"
	case "busybox", "httpd", "nginx", "caddy":
		return "static"
	default:
		return "custom"
	}
}

// --- egg config file processing (find/replace) ---

// ApplyConfigFile processes one egg config entry against the data dir.
// It supports the same parser semantics as Wings: properties, yaml, file (plain), json.
func ApplyConfigFile(dataDir string, file string, replaces []interface{}, find map[string]interface{}) error {
	if file == "" {
		return fmt.Errorf("config file path empty")
	}
	// translate docker path prefixes in the file path itself
	f := strings.ReplaceAll(file, "/home/container", dataDir)
	f = strings.ReplaceAll(f, "/mnt/server", dataDir)
	f = strings.TrimPrefix(f, dataDir+"/")
	target := filepath.Join(dataDir, filepath.FromSlash(f))

	if _, err := os.Stat(target); err != nil {
		return nil // nothing to process
	}
	data, err := os.ReadFile(target)
	if err != nil {
		return err
	}
	content := string(data)

	for _, rep := range replaces {
		m, ok := rep.(map[string]interface{})
		if !ok {
			continue
		}
		match, _ := m["match"].(string)
		replaceWith, _ := m["replace_with"].(string)
		ifValue, _ := m["if_value"].(string)
		if match == "" {
			continue
		}
		match = strings.ReplaceAll(match, "/home/container", dataDir)
		replaceWith = strings.ReplaceAll(replaceWith, "/home/container", dataDir)
		if ifValue != "" && !strings.Contains(content, ifValue) {
			continue
		}
		content = strings.ReplaceAll(content, match, replaceWith)
	}

	// legacy "find" map: key -> value(s) literal replacement
	for k, v := range find {
		val := literal(v)
		if k == "" {
			continue
		}
		content = strings.ReplaceAll(content, k, val)
	}

	return os.WriteFile(target, []byte(content), 0o644)
}

func literal(v interface{}) string {
	switch t := v.(type) {
	case string:
		return t
	case bool:
		if t {
			return "true"
		}
		return "false"
	case float64:
		if t == float64(int64(t)) {
			return fmt.Sprintf("%d", int64(t))
		}
		return fmt.Sprintf("%g", t)
	case nil:
		return ""
	default:
		b, _ := json.Marshal(v)
		return string(b)
	}
}
