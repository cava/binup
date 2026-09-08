package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"time"
)

type ToolRecord struct {
	Repo        string    `json:"repo"`
	Source      string    `json:"source"`
	Tag         string    `json:"tag"`
	Asset       string    `json:"asset"`
	Files       []string  `json:"files"`
	Target      string    `json:"target"`
	FileGlob    string    `json:"file_glob"`
	Filters     []string  `json:"asset_filters"`
	SHA256      string    `json:"sha256"`
	InstalledAt time.Time `json:"installed_at"`
}

type Manifest struct {
	path  string
	Tools map[string]*ToolRecord `json:"tools"`
}

func manifestPath() string {
	if p := os.Getenv("BINUP_MANIFEST"); p != "" {
		return p
	}
	dataHome := os.Getenv("XDG_DATA_HOME")
	if dataHome == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			home = "."
		}
		dataHome = filepath.Join(home, ".local", "share")
	}
	return filepath.Join(dataHome, "binup", "manifest.json")
}

func loadManifest() (*Manifest, error) {
	p := manifestPath()
	m := &Manifest{path: p, Tools: map[string]*ToolRecord{}}
	data, err := os.ReadFile(p)
	if err != nil {
		if os.IsNotExist(err) {
			return m, nil
		}
		return m, err
	}
	if len(data) > 0 {
		if err := json.Unmarshal(data, &m.Tools); err != nil {
			return m, err
		}
	}
	return m, nil
}

func (m *Manifest) save() error {
	if err := os.MkdirAll(filepath.Dir(m.path), 0755); err != nil {
		return err
	}
	data, err := json.MarshalIndent(m.Tools, "", "  ")
	if err != nil {
		return err
	}
	tmp := m.path + ".tmp"
	if err := os.WriteFile(tmp, data, 0644); err != nil {
		return err
	}
	return os.Rename(tmp, m.path)
}

func (m *Manifest) keys() []string {
	out := make([]string, 0, len(m.Tools))
	for k := range m.Tools {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
