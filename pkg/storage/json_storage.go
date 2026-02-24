package storage

import (
	"crypto/subtle"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
)

// jsonStorage implements ConfigStorage using JSON files
type jsonStorage struct {
	filePath string
	mu       sync.RWMutex
}

// NewJSONStorage creates a new JSON storage
func NewJSONStorage(dataDir string) ConfigStorage {
	return &jsonStorage{
		filePath: filepath.Join(dataDir, "config.json"),
	}
}

// NewJSONStorageWithAPIKeys creates a new JSON storage that implements ConfigStorage, APIKeyStorage, and MCPHTTPConfigStorage
type CombinedStorage interface {
	ConfigStorage
	APIKeyStorage
	MCPHTTPConfigStorage
	PendingRequestStorage
}

// NewJSONStorageWithAPIKeys creates a new JSON storage that implements all storage interfaces
func NewJSONStorageWithAPIKeys(dataDir string) CombinedStorage {
	return &jsonStorage{
		filePath: filepath.Join(dataDir, "config.json"),
	}
}

// Load loads configuration from JSON file
func (s *jsonStorage) Load() (*Config, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	// Check if file exists
	if _, err := os.Stat(s.filePath); os.IsNotExist(err) {
		// Return default config if file doesn't exist
		return s.defaultConfig(), nil
	}

	data, err := os.ReadFile(s.filePath)
	if err != nil {
		return nil, fmt.Errorf("failed to read config file: %w", err)
	}

	var config Config
	if err := json.Unmarshal(data, &config); err != nil {
		return nil, fmt.Errorf("failed to parse config file: %w", err)
	}

	return &config, nil
}

// Save saves configuration to JSON file
func (s *jsonStorage) Save(cfg *Config) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	// Ensure directory exists
	dir := filepath.Dir(s.filePath)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return fmt.Errorf("failed to create data directory: %w", err)
	}

	data, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return fmt.Errorf("failed to marshal config: %w", err)
	}

	if err := os.WriteFile(s.filePath, data, 0644); err != nil {
		return fmt.Errorf("failed to write config file: %w", err)
	}

	return nil
}

// defaultConfig returns default configuration
func (s *jsonStorage) defaultConfig() *Config {
	return &Config{
		AllowedUsers:    []int64{},
		PendingRequests: []PendingRequest{},
		DefaultTTL:      300,
		DefaultProxied:  true,
		MCPAPIKeys:      []string{},
		MCPHTTPPort:     "8875",
		MCPHTTPEnabled:  true,
	}
}

// GetAPIKeys returns all stored API keys
func (s *jsonStorage) GetAPIKeys() ([]string, error) {
	cfg, err := s.Load()
	if err != nil {
		return nil, err
	}
	return cfg.MCPAPIKeys, nil
}

// AddAPIKey adds a new API key
func (s *jsonStorage) AddAPIKey(key string) error {
	cfg, err := s.Load()
	if err != nil {
		return err
	}

	// Check if key already exists
	for _, k := range cfg.MCPAPIKeys {
		if subtle.ConstantTimeCompare([]byte(k), []byte(key)) == 1 {
			return fmt.Errorf("API key already exists")
		}
	}

	cfg.MCPAPIKeys = append(cfg.MCPAPIKeys, key)
	return s.Save(cfg)
}

// RemoveAPIKey removes an API key
func (s *jsonStorage) RemoveAPIKey(key string) error {
	cfg, err := s.Load()
	if err != nil {
		return err
	}

	found := false
	newKeys := make([]string, 0, len(cfg.MCPAPIKeys))
	for _, k := range cfg.MCPAPIKeys {
		if subtle.ConstantTimeCompare([]byte(k), []byte(key)) == 1 {
			found = true
			continue
		}
		newKeys = append(newKeys, k)
	}

	if !found {
		return fmt.Errorf("API key not found")
	}

	cfg.MCPAPIKeys = newKeys
	return s.Save(cfg)
}

// IsValidAPIKey checks if the provided key is valid
func (s *jsonStorage) IsValidAPIKey(key string) bool {
	cfg, err := s.Load()
	if err != nil {
		return false
	}

	for _, k := range cfg.MCPAPIKeys {
		if subtle.ConstantTimeCompare([]byte(k), []byte(key)) == 1 {
			return true
		}
	}
	return false
}

// GetMCPHTTPPort returns the configured MCP HTTP port
func (s *jsonStorage) GetMCPHTTPPort() (string, error) {
	cfg, err := s.Load()
	if err != nil {
		return "", err
	}
	if cfg.MCPHTTPPort == "" {
		return "8875", nil // Default port
	}
	return cfg.MCPHTTPPort, nil
}

// SetMCPHTTPPort sets the MCP HTTP port
func (s *jsonStorage) SetMCPHTTPPort(port string) error {
	cfg, err := s.Load()
	if err != nil {
		return err
	}
	cfg.MCPHTTPPort = port
	return s.Save(cfg)
}

// GetMCPHTTPEnabled returns whether MCP HTTP server is enabled
func (s *jsonStorage) GetMCPHTTPEnabled() (bool, error) {
	cfg, err := s.Load()
	if err != nil {
		return false, err
	}
	return cfg.MCPHTTPEnabled, nil
}

// SetMCPHTTPEnabled sets whether MCP HTTP server is enabled
func (s *jsonStorage) SetMCPHTTPEnabled(enabled bool) error {
	cfg, err := s.Load()
	if err != nil {
		return err
	}
	cfg.MCPHTTPEnabled = enabled
	return s.Save(cfg)
}

// GetPendingRequests returns all pending access requests
func (s *jsonStorage) GetPendingRequests() ([]PendingRequest, error) {
	cfg, err := s.Load()
	if err != nil {
		return nil, err
	}
	return cfg.PendingRequests, nil
}

// AddPendingRequest adds a new pending access request
func (s *jsonStorage) AddPendingRequest(req PendingRequest) error {
	cfg, err := s.Load()
	if err != nil {
		return err
	}

	// Check if already pending
	for _, r := range cfg.PendingRequests {
		if r.UserID == req.UserID {
			return fmt.Errorf("request already pending")
		}
	}

	cfg.PendingRequests = append(cfg.PendingRequests, req)
	return s.Save(cfg)
}

// RemovePendingRequest removes a pending access request
func (s *jsonStorage) RemovePendingRequest(userID int64) error {
	cfg, err := s.Load()
	if err != nil {
		return err
	}

	found := false
	newRequests := make([]PendingRequest, 0, len(cfg.PendingRequests))
	for _, r := range cfg.PendingRequests {
		if r.UserID == userID {
			found = true
			continue
		}
		newRequests = append(newRequests, r)
	}

	if !found {
		return fmt.Errorf("pending request not found")
	}

	cfg.PendingRequests = newRequests
	return s.Save(cfg)
}

// IsPendingRequest checks if a user has a pending access request
func (s *jsonStorage) IsPendingRequest(userID int64) (bool, error) {
	cfg, err := s.Load()
	if err != nil {
		return false, err
	}

	for _, r := range cfg.PendingRequests {
		if r.UserID == userID {
			return true, nil
		}
	}
	return false, nil
}
