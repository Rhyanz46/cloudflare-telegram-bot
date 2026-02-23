package main

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"strings"
	"time"

	"cf-dns-bot/external_resource/cloudflare"
	"cf-dns-bot/internal/domain"
	"cf-dns-bot/internal/repository"
	"cf-dns-bot/internal/usecase"
	"cf-dns-bot/pkg/config"
	"cf-dns-bot/pkg/storage"
)

// APIKeyStore manages API keys
type APIKeyStore struct {
	keys map[string]*APIKey // key -> metadata
}

// APIKey represents an API key with metadata
type APIKey struct {
	Key         string    `json:"key"`
	Name        string    `json:"name"`
	CreatedAt   time.Time `json:"created_at"`
	LastUsedAt  time.Time `json:"last_used_at,omitempty"`
	UsageCount  int       `json:"usage_count"`
	Enabled     bool      `json:"enabled"`
}

// NewAPIKeyStore creates a new API key store
func NewAPIKeyStore() *APIKeyStore {
	store := &APIKeyStore{
		keys: make(map[string]*APIKey),
	}
	store.loadFromEnv()
	return store
}

// loadFromEnv loads management key from environment
func (s *APIKeyStore) loadFromEnv() {
	// Load management key
	mgmtKey := os.Getenv("MCP_MANAGEMENT_KEY")
	if mgmtKey != "" {
		s.keys[mgmtKey] = &APIKey{
			Key:        mgmtKey,
			Name:       "management",
			CreatedAt:  time.Now(),
			Enabled:    true,
		}
		log.Println("[APIKeyStore] Management key loaded from environment")
	}

	// Load regular API keys (comma-separated)
	apiKeys := os.Getenv("MCP_API_KEYS")
	if apiKeys != "" {
		for i, key := range strings.Split(apiKeys, ",") {
			key = strings.TrimSpace(key)
			if key != "" {
				s.keys[key] = &APIKey{
					Key:       key,
					Name:      fmt.Sprintf("api-key-%d", i+1),
					CreatedAt: time.Now(),
					Enabled:   true,
				}
			}
		}
		log.Printf("[APIKeyStore] %d API key(s) loaded from environment", len(strings.Split(apiKeys, ",")))
	}
}

// Validate checks if a key is valid
func (s *APIKeyStore) Validate(key string) (*APIKey, bool) {
	k, exists := s.keys[key]
	if !exists || !k.Enabled {
		return nil, false
	}
	k.LastUsedAt = time.Now()
	k.UsageCount++
	return k, true
}

// IsManagement checks if a key is the management key
func (s *APIKeyStore) IsManagement(key string) bool {
	k, exists := s.keys[key]
	return exists && k.Name == "management"
}

// GenerateKey generates a new API key
func (s *APIKeyStore) GenerateKey(name string) string {
	bytes := make([]byte, 32)
	rand.Read(bytes)
	key := "mcp_" + hex.EncodeToString(bytes)
	s.keys[key] = &APIKey{
		Key:       key,
		Name:      name,
		CreatedAt: time.Now(),
		Enabled:   true,
	}
	return key
}

// ListKeys returns all keys (management only)
func (s *APIKeyStore) ListKeys() []*APIKey {
	result := make([]*APIKey, 0, len(s.keys))
	for _, k := range s.keys {
		result = append(result, k)
	}
	return result
}

// Server represents the HTTP MCP server
type Server struct {
	dnsUsecase usecase.DNSUsecase
	apiKeys    *APIKeyStore
	port       string
}

// NewServer creates a new HTTP MCP server
func NewServer(dnsUsecase usecase.DNSUsecase, apiKeys *APIKeyStore, port string) *Server {
	if port == "" {
		port = "8080"
	}
	return &Server{
		dnsUsecase: dnsUsecase,
		apiKeys:    apiKeys,
		port:       port,
	}
}

// authMiddleware validates API keys
func (s *Server) authMiddleware(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		authHeader := r.Header.Get("Authorization")
		if authHeader == "" {
			s.writeError(w, http.StatusUnauthorized, "Missing Authorization header")
			return
		}

		// Extract Bearer token
		parts := strings.SplitN(authHeader, " ", 2)
		if len(parts) != 2 || strings.ToLower(parts[0]) != "bearer" {
			s.writeError(w, http.StatusUnauthorized, "Invalid Authorization format. Use: Bearer <token>")
			return
		}

		apiKey := parts[1]
		keyInfo, valid := s.apiKeys.Validate(apiKey)
		if !valid {
			s.writeError(w, http.StatusUnauthorized, "Invalid API key")
			return
		}

		// Store key info in context
		ctx := context.WithValue(r.Context(), "api_key", keyInfo)
		next(w, r.WithContext(ctx))
	}
}

// writeError writes an error response
func (s *Server) writeError(w http.ResponseWriter, status int, message string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(map[string]interface{}{
		"success": false,
		"error":   message,
	})
}

// writeSuccess writes a success response
func (s *Server) writeSuccess(w http.ResponseWriter, data interface{}) {
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"success": true,
		"data":    data,
	})
}

// RegisterRoutes registers all HTTP routes
func (s *Server) RegisterRoutes() {
	// Health check (no auth required)
	http.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) {
		s.writeSuccess(w, map[string]string{
			"status":  "ok",
			"service": "cf-dns-mcp-http",
			"version": "1.0.0",
		})
	})

	// DNS API routes (require API key)
	http.HandleFunc("/api/zones", s.authMiddleware(s.handleListZones))
	http.HandleFunc("/api/records", s.authMiddleware(s.handleListRecords))
	http.HandleFunc("/api/record", s.authMiddleware(s.handleRecord))
	http.HandleFunc("/api/record/create", s.authMiddleware(s.handleCreateRecord))
	http.HandleFunc("/api/record/update", s.authMiddleware(s.handleUpdateRecord))
	http.HandleFunc("/api/record/delete", s.authMiddleware(s.handleDeleteRecord))
	http.HandleFunc("/api/record/upsert", s.authMiddleware(s.handleUpsertRecord))

	// Management routes (require management key)
	http.HandleFunc("/admin/keys", s.authMiddleware(s.handleManageKeys))
	http.HandleFunc("/admin/keys/generate", s.authMiddleware(s.handleGenerateKey))
}

// handleListZones handles GET /api/zones
func (s *Server) handleListZones(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		s.writeError(w, http.StatusMethodNotAllowed, "Method not allowed")
		return
	}

	ctx := context.Background()
	zones, err := s.dnsUsecase.ListZones(ctx)
	if err != nil {
		s.writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	s.writeSuccess(w, zones)
}

// handleListRecords handles GET /api/records?zone=example.com
func (s *Server) handleListRecords(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		s.writeError(w, http.StatusMethodNotAllowed, "Method not allowed")
		return
	}

	zoneName := r.URL.Query().Get("zone")
	if zoneName == "" {
		s.writeError(w, http.StatusBadRequest, "Missing 'zone' query parameter")
		return
	}

	ctx := context.Background()
	records, err := s.dnsUsecase.ListRecords(ctx, zoneName)
	if err != nil {
		s.writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	s.writeSuccess(w, records)
}

// handleRecord handles GET /api/record?zone=example.com&name=www.example.com
func (s *Server) handleRecord(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		s.writeError(w, http.StatusMethodNotAllowed, "Method not allowed")
		return
	}

	zoneName := r.URL.Query().Get("zone")
	recordName := r.URL.Query().Get("name")
	if zoneName == "" || recordName == "" {
		s.writeError(w, http.StatusBadRequest, "Missing 'zone' or 'name' query parameter")
		return
	}

	ctx := context.Background()
	record, err := s.dnsUsecase.GetRecord(ctx, zoneName, recordName)
	if err != nil {
		if err == domain.ErrRecordNotFound {
			s.writeError(w, http.StatusNotFound, "Record not found")
			return
		}
		s.writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	s.writeSuccess(w, record)
}

// handleCreateRecord handles POST /api/record/create
func (s *Server) handleCreateRecord(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		s.writeError(w, http.StatusMethodNotAllowed, "Method not allowed")
		return
	}

	var input usecase.CreateRecordInput
	if err := json.NewDecoder(r.Body).Decode(&input); err != nil {
		s.writeError(w, http.StatusBadRequest, "Invalid JSON: "+err.Error())
		return
	}

	ctx := context.Background()
	record, err := s.dnsUsecase.CreateRecord(ctx, input)
	if err != nil {
		if err == domain.ErrDuplicateRecord {
			s.writeError(w, http.StatusConflict, "Record already exists")
			return
		}
		s.writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	s.writeSuccess(w, record)
}

// handleUpdateRecord handles POST /api/record/update
func (s *Server) handleUpdateRecord(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		s.writeError(w, http.StatusMethodNotAllowed, "Method not allowed")
		return
	}

	var input usecase.UpdateRecordInput
	if err := json.NewDecoder(r.Body).Decode(&input); err != nil {
		s.writeError(w, http.StatusBadRequest, "Invalid JSON: "+err.Error())
		return
	}

	ctx := context.Background()
	record, err := s.dnsUsecase.UpdateRecord(ctx, input)
	if err != nil {
		s.writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	s.writeSuccess(w, record)
}

// handleDeleteRecord handles POST /api/record/delete
func (s *Server) handleDeleteRecord(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		s.writeError(w, http.StatusMethodNotAllowed, "Method not allowed")
		return
	}

	var req struct {
		ZoneName   string `json:"zone_name"`
		RecordName string `json:"record_name"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		s.writeError(w, http.StatusBadRequest, "Invalid JSON: "+err.Error())
		return
	}

	ctx := context.Background()
	err := s.dnsUsecase.DeleteRecord(ctx, req.ZoneName, req.RecordName)
	if err != nil {
		if err == domain.ErrRecordNotFound {
			s.writeError(w, http.StatusNotFound, "Record not found")
			return
		}
		s.writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	s.writeSuccess(w, map[string]string{"message": "Record deleted successfully"})
}

// handleUpsertRecord handles POST /api/record/upsert
func (s *Server) handleUpsertRecord(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		s.writeError(w, http.StatusMethodNotAllowed, "Method not allowed")
		return
	}

	var input usecase.CreateRecordInput
	if err := json.NewDecoder(r.Body).Decode(&input); err != nil {
		s.writeError(w, http.StatusBadRequest, "Invalid JSON: "+err.Error())
		return
	}

	ctx := context.Background()
	record, err := s.dnsUsecase.UpsertRecord(ctx, input)
	if err != nil {
		s.writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	s.writeSuccess(w, record)
}

// handleManageKeys handles GET /admin/keys (management only)
func (s *Server) handleManageKeys(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		s.writeError(w, http.StatusMethodNotAllowed, "Method not allowed")
		return
	}

	// Check if management key
	keyInfo := r.Context().Value("api_key").(*APIKey)
	if keyInfo.Name != "management" {
		s.writeError(w, http.StatusForbidden, "Management key required")
		return
	}

	keys := s.apiKeys.ListKeys()
	s.writeSuccess(w, keys)
}

// handleGenerateKey handles POST /admin/keys/generate (management only)
func (s *Server) handleGenerateKey(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		s.writeError(w, http.StatusMethodNotAllowed, "Method not allowed")
		return
	}

	// Check if management key
	keyInfo := r.Context().Value("api_key").(*APIKey)
	if keyInfo.Name != "management" {
		s.writeError(w, http.StatusForbidden, "Management key required")
		return
	}

	var req struct {
		Name string `json:"name"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		req.Name = "generated-key"
	}

	newKey := s.apiKeys.GenerateKey(req.Name)
	s.writeSuccess(w, map[string]string{
		"key":  newKey,
		"name": req.Name,
	})
}

// Start starts the HTTP server
func (s *Server) Start() error {
	s.RegisterRoutes()
	log.Printf("[HTTP MCP Server] Starting on port %s", s.port)
	log.Printf("[HTTP MCP Server] Health check: http://localhost:%s/health", s.port)
	return http.ListenAndServe(":"+s.port, nil)
}

func main() {
	// Load configuration
	cfg, err := config.Load()
	if err != nil {
		log.Fatalf("Failed to load config: %v", err)
	}

	// Initialize API key store
	apiKeys := NewAPIKeyStore()

	// Check if management key is configured
	hasMgmtKey := false
	for _, k := range apiKeys.keys {
		if k.Name == "management" {
			hasMgmtKey = true
			break
		}
	}
	if !hasMgmtKey {
		log.Println("[WARNING] No management key configured. Set MCP_MANAGEMENT_KEY environment variable.")
		log.Println("[WARNING] Generate a key with: openssl rand -hex 32")
	}

	// Initialize storage
	configStorage := storage.NewJSONStorage(cfg.DataDir)

	// Initialize Cloudflare client
	var cfClient cloudflare.Client
	if cfg.UseAPIToken() {
		cfClient, err = cloudflare.NewClient(cfg.CloudflareAPIToken)
	} else {
		cfClient, err = cloudflare.NewClientWithKey(cfg.CloudflareAPIKey, cfg.CloudflareEmail)
	}
	if err != nil {
		log.Fatalf("Failed to create Cloudflare client: %v", err)
	}

	// Initialize repositories
	zoneRepo := repository.NewZoneRepository(cfClient)
	dnsRepo := repository.NewDNSRepository(cfClient)

	// Initialize usecase
	dnsUsecase := usecase.NewDNSUsecase(zoneRepo, dnsRepo, configStorage)

	// Get port from environment
	port := os.Getenv("MCP_HTTP_PORT")
	if port == "" {
		port = "8080"
	}

	// Create and start HTTP server
	server := NewServer(dnsUsecase, apiKeys, port)
	if err := server.Start(); err != nil {
		log.Fatalf("Server error: %v", err)
	}
}
