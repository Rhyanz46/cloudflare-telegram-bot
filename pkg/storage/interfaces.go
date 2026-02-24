package storage

// PendingRequest represents a pending access request
type PendingRequest struct {
	UserID    int64  `json:"user_id"`
	Username  string `json:"username"`
	FirstName string `json:"first_name"`
	LastName  string `json:"last_name"`
}

// Config represents the application configuration stored in JSON
type Config struct {
	AllowedUsers    []int64          `json:"allowed_users"`
	PendingRequests []PendingRequest `json:"pending_requests"`
	DefaultTTL      int              `json:"default_ttl"`
	DefaultProxied  bool             `json:"default_proxied"`
	MCPAPIKeys      []string         `json:"mcp_api_keys"`
	MCPHTTPPort     string           `json:"mcp_http_port"`
	MCPHTTPEnabled  bool             `json:"mcp_http_enabled"`
}

// ConfigStorage defines the interface for configuration storage
type ConfigStorage interface {
	Load() (*Config, error)
	Save(cfg *Config) error
}

// APIKeyStorage defines the interface for API key storage
type APIKeyStorage interface {
	GetAPIKeys() ([]string, error)
	AddAPIKey(key string) error
	RemoveAPIKey(key string) error
	IsValidAPIKey(key string) bool
}

// MCPHTTPConfigStorage defines the interface for MCP HTTP server configuration
type MCPHTTPConfigStorage interface {
	GetMCPHTTPPort() (string, error)
	SetMCPHTTPPort(port string) error
	GetMCPHTTPEnabled() (bool, error)
	SetMCPHTTPEnabled(enabled bool) error
}

// PendingRequestStorage defines the interface for pending access request storage
type PendingRequestStorage interface {
	GetPendingRequests() ([]PendingRequest, error)
	AddPendingRequest(req PendingRequest) error
	RemovePendingRequest(userID int64) error
	IsPendingRequest(userID int64) (bool, error)
}
