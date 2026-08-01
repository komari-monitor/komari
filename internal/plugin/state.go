package plugin

import (
	"encoding/json"
	"os"
	"sync"
	"time"
)

// State persists the enabled flag, the approved permissions hash and the
// last error for each installed plugin in DataDir/state.json. Keeping it
// next to the plugin directories keeps plugin management self-contained.
type State struct {
	mu      sync.Mutex
	path    string
	current map[string]PluginState
}

// PluginState is the persisted state of one plugin.
type PluginState struct {
	Enabled                 bool      `json:"enabled"`
	ApprovedPermissionsHash string    `json:"approved_permissions_hash"`
	LastError               string    `json:"last_error"`
	UpdatedAt               time.Time `json:"updated_at"`
}

func openState(path string) *State {
	return &State{path: path}
}

func (s *State) get(short string) PluginState {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.ensure()
	return s.current[short]
}

func (s *State) set(short string, st PluginState) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.ensure()
	st.UpdatedAt = time.Now().UTC()
	s.current[short] = st
	s.saveLocked()
}

func (s *State) delete(short string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.ensure()
	if _, ok := s.current[short]; ok {
		delete(s.current, short)
		s.saveLocked()
	}
}

func (s *State) ensure() {
	if s.current != nil {
		return
	}
	s.current = map[string]PluginState{}
	data, err := os.ReadFile(s.path)
	if err != nil {
		return
	}
	var file struct {
		Plugins map[string]PluginState `json:"plugins"`
	}
	if json.Unmarshal(data, &file) == nil && file.Plugins != nil {
		s.current = file.Plugins
	}
}

func (s *State) saveLocked() {
	file := struct {
		Plugins map[string]PluginState `json:"plugins"`
	}{Plugins: s.current}
	data, err := json.MarshalIndent(file, "", "  ")
	if err != nil {
		return
	}
	_ = os.WriteFile(s.path, data, 0644)
}
