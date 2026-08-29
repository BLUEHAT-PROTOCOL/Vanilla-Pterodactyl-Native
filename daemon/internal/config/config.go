// Package config loads and validates the ptero-native daemon configuration.
package config

import (
	"fmt"
	"os"
	"path/filepath"

	"gopkg.in/yaml.v3"
)

// PanelConfig holds Panel-facing settings used by the remote API client.
type PanelConfig struct {
	URL           string `yaml:"url"`
	Token         string `yaml:"token"` // full daemon token: "<token_id>.<token>"
	AllowInsecure bool   `yaml:"allow_insecure"`
}

// DaemonConfig holds daemon-facing settings.
type DaemonConfig struct {
	Listen         string            `yaml:"listen"`
	TokenID        string            `yaml:"token_id"`
	Token          string            `yaml:"token"`
	APIKeys        map[string]string `yaml:"api_keys"` // token_id -> key the Panel uses against us
	DataPath       string            `yaml:"data_path"`
	BackupPath     string            `yaml:"backup_path"`
	TmpPath        string            `yaml:"tmp_path"`
	UsernamePrefix string            `yaml:"username_prefix"`
	UploadLimitMB  int64             `yaml:"upload_size_limit"`
}

// LimitsConfig holds crash/log behavior.
type LimitsConfig struct {
	CrashRestarts int `yaml:"crash_restarts"`
	CrashWindow   int `yaml:"crash_window"`
	LogMaxLines   int `yaml:"log_max_lines"`
}

// RuntimeMapping maps a docker image name to a native runtime.
type RuntimeMapping struct {
	Profile string            `yaml:"profile"`
	Version string            `yaml:"version"`
	Path    string            `yaml:"path"` // bin directory added to PATH
	Env     map[string]string `yaml:"env"`
}

// Config is the daemon root configuration.
type Config struct {
	Panel    PanelConfig               `yaml:"panel"`
	Daemon   DaemonConfig              `yaml:"daemon"`
	Limits   LimitsConfig              `yaml:"limits"`
	Runtimes map[string]RuntimeMapping `yaml:"runtimes"`
	Debug    bool                      `yaml:"debug"`
}

// Defaults fills unset fields with production-safe defaults.
func (c *Config) Defaults() {
	if c.Panel.URL == "" {
		c.Panel.URL = "http://127.0.0.1:8000"
	}
	if c.Daemon.Listen == "" {
		c.Daemon.Listen = "0.0.0.0:8080"
	}
	if c.Daemon.DataPath == "" {
		c.Daemon.DataPath = "/var/lib/ptero-native"
	}
	if c.Daemon.BackupPath == "" {
		c.Daemon.BackupPath = filepath.Join(c.Daemon.DataPath, "backups")
	}
	if c.Daemon.TmpPath == "" {
		c.Daemon.TmpPath = "/tmp/ptero-native"
	}
	if c.Daemon.UsernamePrefix == "" {
		c.Daemon.UsernamePrefix = "vrp_"
	}
	if c.Daemon.UploadLimitMB == 0 {
		c.Daemon.UploadLimitMB = 100
	}
	if c.Limits.CrashRestarts == 0 {
		c.Limits.CrashRestarts = 3
	}
	if c.Limits.CrashWindow == 0 {
		c.Limits.CrashWindow = 60
	}
	if c.Limits.LogMaxLines == 0 {
		c.Limits.LogMaxLines = 5000
	}
}

// Load reads the YAML config at path and applies defaults.
func Load(path string) (*Config, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read config: %w", err)
	}
	c := &Config{}
	if err := yaml.Unmarshal(b, c); err != nil {
		return nil, fmt.Errorf("parse config: %w", err)
	}
	c.Defaults()
	if c.Panel.Token == "" {
		return nil, fmt.Errorf("panel.token is required")
	}
	if len(c.Daemon.APIKeys) == 0 && (c.Daemon.TokenID == "" || c.Daemon.Token == "") {
		return nil, fmt.Errorf("daemon.api_keys or daemon.token_id/token is required")
	}
	return c, nil
}

// VolumesPath returns the base path for server data volumes.
func (c *Config) VolumesPath() string {
	return filepath.Join(c.Daemon.DataPath, "volumes")
}

// ServerVolume returns the data directory for a server uuid.
func (c *Config) ServerVolume(uuid string) string {
	return filepath.Join(c.VolumesPath(), uuid)
}

// ServerStatePath returns the daemon-local state file path for a server.
func (c *Config) ServerStatePath(uuid string) string {
	return filepath.Join(c.Daemon.DataPath, "state", uuid+".json")
}
