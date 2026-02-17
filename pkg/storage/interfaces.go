package storage

// Config represents the application configuration stored in JSON
type Config struct {
	AllowedUsers   []int64 `json:"allowed_users"`
	DefaultTTL     int     `json:"default_ttl"`
	DefaultProxied bool    `json:"default_proxied"`
}

// ConfigStorage defines the interface for configuration storage
type ConfigStorage interface {
	Load() (*Config, error)
	Save(cfg *Config) error
}
