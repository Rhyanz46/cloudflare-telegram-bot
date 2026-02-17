package storage

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
)

// jsonStorage implements ConfigStorage using JSON files
type jsonStorage struct {
	filePath string
}

// NewJSONStorage creates a new JSON storage
func NewJSONStorage(dataDir string) ConfigStorage {
	return &jsonStorage{
		filePath: filepath.Join(dataDir, "config.json"),
	}
}

// Load loads configuration from JSON file
func (s *jsonStorage) Load() (*Config, error) {
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
		AllowedUsers:   []int64{},
		DefaultTTL:     300,
		DefaultProxied: true,
	}
}
