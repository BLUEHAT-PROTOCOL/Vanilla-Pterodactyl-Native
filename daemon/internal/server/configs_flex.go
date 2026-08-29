package server

import (
	"encoding/json"
	"fmt"
)

// UnmarshalJSON makes ProcessConfiguration.Configs tolerant to both wire
// shapes seen in the wild:
//
//  1. Panel v1.15 (EggConfigurationService) sends an ARRAY of config objects:
//     "configs": [ {"file":"config.yml","parser":"yaml","replace":[...]}, ... ]
//
//  2. Legacy / raw egg config_files column sends an OBJECT keyed by filename
//     where entries carry a raw "find" map (and optionally "replace"):
//     "configs": { "config.yml": {"parser":"yaml","find":{...}} }
//
// Both are normalized into map[string]ConfigFile so the rest of the daemon
// (applyEggConfigs) keeps a single representation. Unknown shapes never
// hard-fail server registration: they degrade to an empty config set with a
// warning surfaced through the error return.
func (p *ProcessConfiguration) UnmarshalJSON(data []byte) error {
	type startupBlock struct {
		Startup StartupConfig   `json:"startup"`
		Stop    StopConfig      `json:"stop"`
		Configs json.RawMessage `json:"configs"`
		Logfile interface{}     `json:"logfile,omitempty"`
	}
	var a startupBlock
	if err := json.Unmarshal(data, &a); err != nil {
		return err
	}
	p.Startup = a.Startup
	p.Stop = a.Stop
	p.Logfile = a.Logfile
	p.Configs = map[string]ConfigFile{}

	raw := a.Configs
	if len(raw) == 0 || string(raw) == "null" {
		return nil
	}

	// Shape 1: array of ConfigFile objects.
	var arr []ConfigFile
	if err := json.Unmarshal(raw, &arr); err == nil {
		for _, cf := range arr {
			if cf.File == "" {
				continue
			}
			p.Configs[cf.File] = cf
		}
		return nil
	}

	// Shape 2: object keyed by filename.
	var obj map[string]ConfigFile
	if err := json.Unmarshal(raw, &obj); err != nil {
		return fmt.Errorf("configs: unsupported shape (neither array nor object)")
	}
	for k, cf := range obj {
		if cf.File == "" {
			cf.File = k
		}
		p.Configs[k] = cf
	}
	return nil
}
