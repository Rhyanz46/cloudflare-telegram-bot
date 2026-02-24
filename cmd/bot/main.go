package main

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"sync"
	"syscall"

	"cf-dns-bot/external_resource/cloudflare"
	"cf-dns-bot/internal/domain"
	"cf-dns-bot/internal/handler"
	"cf-dns-bot/internal/handler/telegram"
	"cf-dns-bot/internal/repository"
	"cf-dns-bot/internal/usecase"
	"cf-dns-bot/pkg/config"
	"cf-dns-bot/pkg/storage"
)

// MCPHTTPServer implements the MCPHTTPServerController interface
type MCPHTTPServer struct {
	dnsUsecase    usecase.DNSUsecase
	apiKeyStorage storage.APIKeyStorage
	configStorage telegram.ConfigStorage
	server        *http.Server
	port          string
	running       bool
	mu            sync.RWMutex
}

// NewMCPHTTPServer creates a new MCP HTTP server controller
func NewMCPHTTPServer(dnsUsecase usecase.DNSUsecase, apiKeyStorage storage.APIKeyStorage, configStorage telegram.ConfigStorage) *MCPHTTPServer {
	return &MCPHTTPServer{
		dnsUsecase:    dnsUsecase,
		apiKeyStorage: apiKeyStorage,
		configStorage: configStorage,
		port:          "8875",
	}
}

// Start starts the MCP HTTP server
func (s *MCPHTTPServer) Start() error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.running {
		return fmt.Errorf("server is already running")
	}

	// Get port from storage or use default
	if s.configStorage != nil {
		if port, err := s.configStorage.GetMCPHTTPPort(); err == nil && port != "" {
			s.port = port
		}
	}

	// Load API keys
	apiKeys, _ := s.apiKeyStorage.GetAPIKeys()
	envKeys := loadAPIKeys()
	apiKeys = append(apiKeys, envKeys...)

	if len(apiKeys) == 0 {
		log.Println("[MCP HTTP] WARNING: No API keys configured. Use Telegram bot to generate API keys.")
	} else {
		log.Printf("[MCP HTTP] Loaded %d API key(s)", len(apiKeys))
	}

	// Create new mux for this server instance
	mux := http.NewServeMux()
	mux.HandleFunc("/", s.handleRequest)

	s.server = &http.Server{
		Addr:    ":" + s.port,
		Handler: mux,
	}

	s.running = true

	// Start server in goroutine
	go func() {
		log.Printf("[MCP HTTP] Starting server on :%s", s.port)
		if err := s.server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Printf("[MCP HTTP] Server error: %v", err)
		}
	}()

	return nil
}

// Stop stops the MCP HTTP server
func (s *MCPHTTPServer) Stop() error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if !s.running {
		return fmt.Errorf("server is not running")
	}

	if s.server != nil {
		if err := s.server.Shutdown(context.Background()); err != nil {
			return fmt.Errorf("failed to shutdown server: %w", err)
		}
	}

	s.running = false
	log.Println("[MCP HTTP] Server stopped")
	return nil
}

// IsRunning returns whether the server is running
func (s *MCPHTTPServer) IsRunning() bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.running
}

// GetPort returns the current port
func (s *MCPHTTPServer) GetPort() string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.port
}

// SetPort sets the port (only when server is stopped)
func (s *MCPHTTPServer) SetPort(port string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.running {
		return fmt.Errorf("cannot change port while server is running")
	}

	s.port = port
	return nil
}

// handleRequest handles HTTP requests
func (s *MCPHTTPServer) handleRequest(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")

	// Load API keys
	apiKeys, _ := s.apiKeyStorage.GetAPIKeys()
	envKeys := loadAPIKeys()
	apiKeys = append(apiKeys, envKeys...)

	// API Key Authentication
	if len(apiKeys) > 0 {
		authHeader := r.Header.Get("Authorization")
		if authHeader == "" {
			w.WriteHeader(http.StatusUnauthorized)
			json.NewEncoder(w).Encode(map[string]interface{}{
				"jsonrpc": "2.0",
				"error": map[string]interface{}{
					"code":    -32001,
					"message": "Missing Authorization header",
				},
			})
			return
		}

		parts := strings.SplitN(authHeader, " ", 2)
		if len(parts) != 2 || strings.ToLower(parts[0]) != "bearer" {
			w.WriteHeader(http.StatusUnauthorized)
			json.NewEncoder(w).Encode(map[string]interface{}{
				"jsonrpc": "2.0",
				"error": map[string]interface{}{
					"code":    -32001,
					"message": "Invalid Authorization format. Use: Bearer <token>",
				},
			})
			return
		}

		apiKey := parts[1]
		if !s.apiKeyStorage.IsValidAPIKey(apiKey) && !isValidAPIKey(apiKeys, apiKey) {
			w.WriteHeader(http.StatusUnauthorized)
			json.NewEncoder(w).Encode(map[string]interface{}{
				"jsonrpc": "2.0",
				"error": map[string]interface{}{
					"code":    -32001,
					"message": "Invalid API key",
				},
			})
			return
		}
	}

	if r.Method != http.MethodPost {
		w.WriteHeader(http.StatusMethodNotAllowed)
		json.NewEncoder(w).Encode(map[string]interface{}{
			"jsonrpc": "2.0",
			"error": map[string]interface{}{
				"code":    -32600,
				"message": "Method not allowed",
			},
		})
		return
	}

	var req struct {
		JSONRPC string                 `json:"jsonrpc"`
		ID      interface{}            `json:"id"`
		Method  string                 `json:"method"`
		Params  map[string]interface{} `json:"params"`
	}

	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(map[string]interface{}{
			"jsonrpc": "2.0",
			"error": map[string]interface{}{
				"code":    -32700,
				"message": "Parse error: " + err.Error(),
			},
		})
		return
	}

	// Handle MCP methods
	var result interface{}
	var err error

	switch req.Method {
	case "initialize":
		result = map[string]interface{}{
			"protocolVersion": "2024-11-05",
			"capabilities": map[string]interface{}{
				"tools": map[string]interface{}{"listChanged": true},
			},
			"serverInfo": map[string]interface{}{
				"name":    "cf-dns",
				"version": "1.0.0",
			},
		}
	case "tools/list":
		result = getToolsList()
	case "tools/call":
		result, err = handleToolCall(s.dnsUsecase, req.Params)
	default:
		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode(map[string]interface{}{
			"jsonrpc": "2.0",
			"id":      req.ID,
			"error": map[string]interface{}{
				"code":    -32601,
				"message": "Method not found: " + req.Method,
			},
		})
		return
	}

	response := map[string]interface{}{
		"jsonrpc": "2.0",
		"id":      req.ID,
	}

	if err != nil {
		response["error"] = map[string]interface{}{
			"code":    -32603,
			"message": err.Error(),
		}
	} else {
		response["result"] = result
	}

	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(response)
}

func main() {
	// Load configuration
	cfg, err := config.Load()
	if err != nil {
		log.Fatalf("Failed to load config: %v", err)
	}

	// Initialize storage
	configStorage := storage.NewJSONStorageWithAPIKeys(cfg.DataDir)

	// Load or initialize config file
	storageConfig, err := configStorage.Load()
	if err != nil {
		log.Fatalf("Failed to load storage config: %v", err)
	}

	// Merge environment allowed users with storage config
	if len(cfg.AllowedUsers) > 0 {
		storageConfig.AllowedUsers = cfg.AllowedUsers
		if err := configStorage.Save(storageConfig); err != nil {
			log.Printf("Warning: failed to save config: %v", err)
		}
	}

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

	// Test Cloudflare connection
	ctx := context.Background()
	zones, err := cfClient.ListZones(ctx)
	if err != nil {
		log.Fatalf("Failed to connect to Cloudflare: %v", err)
	}
	log.Printf("Connected to Cloudflare. Found %d zones.", len(zones))

	// Initialize repositories
	zoneRepo := repository.NewZoneRepository(cfClient)
	dnsRepo := repository.NewDNSRepository(cfClient)

	// Initialize usecase
	dnsUsecase := usecase.NewDNSUsecase(zoneRepo, dnsRepo, configStorage)

	// Create MCP HTTP server controller
	mcpHTTPController := NewMCPHTTPServer(dnsUsecase, configStorage, configStorage)

	// Initialize Telegram bot handler with all dependencies
	botHandler := telegram.NewBot(dnsUsecase, cfg.TelegramBotToken, storageConfig.AllowedUsers, configStorage, configStorage, mcpHTTPController, configStorage)

	// Start bot in a goroutine
	go func() {
		log.Println("Starting Telegram bot...")
		if err := botHandler.Start(); err != nil {
			log.Fatalf("Bot error: %v", err)
		}
	}()

	// Start MCP HTTP server automatically on startup
	go func() {
		if err := mcpHTTPController.Start(); err != nil {
			log.Printf("[MCP HTTP] Failed to start: %v", err)
		}
	}()

	// Wait for interrupt signal
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)

	log.Println("Bot and MCP HTTP server are running. Press Ctrl+C to stop.")
	<-sigChan

	log.Println("Shutting down...")

	// Stop MCP HTTP server
	if err := mcpHTTPController.Stop(); err != nil {
		log.Printf("Error stopping MCP HTTP server: %v", err)
	}

	// Stop bot
	if err := botHandler.Stop(); err != nil {
		log.Printf("Error stopping bot: %v", err)
	}

	log.Println("Bot stopped.")
}

// loadAPIKeys loads API keys from environment
func loadAPIKeys() []string {
	keys := []string{}
	if key := os.Getenv("MCP_API_KEY"); key != "" {
		keys = append(keys, key)
	}
	if keysStr := os.Getenv("MCP_API_KEYS"); keysStr != "" {
		for _, key := range strings.Split(keysStr, ",") {
			key = strings.TrimSpace(key)
			if key != "" {
				keys = append(keys, key)
			}
		}
	}
	return keys
}

// isValidAPIKey checks if the provided key is valid
func isValidAPIKey(validKeys []string, key string) bool {
	for _, validKey := range validKeys {
		if subtle.ConstantTimeCompare([]byte(key), []byte(validKey)) == 1 {
			return true
		}
	}
	return false
}

// getToolsList returns list of available tools
func getToolsList() map[string]interface{} {
	return map[string]interface{}{
		"tools": []map[string]interface{}{
			{
				"name":        "list_zones",
				"description": "List all Cloudflare zones/domains",
				"inputSchema": map[string]interface{}{
					"type":       "object",
					"properties": map[string]interface{}{},
				},
			},
			{
				"name":        "list_records",
				"description": "List all DNS records for a specific zone",
				"inputSchema": map[string]interface{}{
					"type": "object",
					"properties": map[string]interface{}{
						"zone_name": map[string]interface{}{
							"type":        "string",
							"description": "The zone/domain name",
						},
					},
					"required": []string{"zone_name"},
				},
			},
			{
				"name":        "get_record",
				"description": "Get details of a specific DNS record",
				"inputSchema": map[string]interface{}{
					"type": "object",
					"properties": map[string]interface{}{
						"zone_name": map[string]interface{}{
							"type":        "string",
							"description": "The zone/domain name",
						},
						"record_name": map[string]interface{}{
							"type":        "string",
							"description": "The full record name",
						},
					},
					"required": []string{"zone_name", "record_name"},
				},
			},
			{
				"name":        "create_record",
				"description": "Create a new DNS record",
				"inputSchema": map[string]interface{}{
					"type": "object",
					"properties": map[string]interface{}{
						"zone_name": map[string]interface{}{"type": "string"},
						"name":      map[string]interface{}{"type": "string"},
						"type":      map[string]interface{}{"type": "string"},
						"content":   map[string]interface{}{"type": "string"},
						"ttl":       map[string]interface{}{"type": "number"},
						"proxied":   map[string]interface{}{"type": "boolean"},
					},
					"required": []string{"zone_name", "name", "type", "content"},
				},
			},
			{
				"name":        "update_record",
				"description": "Update an existing DNS record",
				"inputSchema": map[string]interface{}{
					"type": "object",
					"properties": map[string]interface{}{
						"zone_name": map[string]interface{}{"type": "string"},
						"record_id": map[string]interface{}{"type": "string"},
						"name":      map[string]interface{}{"type": "string"},
						"type":      map[string]interface{}{"type": "string"},
						"content":   map[string]interface{}{"type": "string"},
						"ttl":       map[string]interface{}{"type": "number"},
						"proxied":   map[string]interface{}{"type": "boolean"},
					},
					"required": []string{"zone_name", "record_id", "name", "type", "content"},
				},
			},
			{
				"name":        "delete_record",
				"description": "Delete a DNS record",
				"inputSchema": map[string]interface{}{
					"type": "object",
					"properties": map[string]interface{}{
						"zone_name":   map[string]interface{}{"type": "string"},
						"record_name": map[string]interface{}{"type": "string"},
					},
					"required": []string{"zone_name", "record_name"},
				},
			},
			{
				"name":        "upsert_record",
				"description": "Create or update a DNS record",
				"inputSchema": map[string]interface{}{
					"type": "object",
					"properties": map[string]interface{}{
						"zone_name": map[string]interface{}{"type": "string"},
						"name":      map[string]interface{}{"type": "string"},
						"type":      map[string]interface{}{"type": "string"},
						"content":   map[string]interface{}{"type": "string"},
						"ttl":       map[string]interface{}{"type": "number"},
						"proxied":   map[string]interface{}{"type": "boolean"},
					},
					"required": []string{"zone_name", "name", "type", "content"},
				},
			},
		},
	}
}

// handleToolCall handles tool execution
func handleToolCall(dnsUsecase usecase.DNSUsecase, params map[string]interface{}) (interface{}, error) {
	if params == nil {
		return nil, fmt.Errorf("params is required")
	}

	name, _ := params["name"].(string)
	arguments, _ := params["arguments"].(map[string]interface{})

	ctx := context.Background()

	switch name {
	case "list_zones":
		zones, err := dnsUsecase.ListZones(ctx)
		if err != nil {
			return nil, err
		}
		result := make([]map[string]string, len(zones))
		for i, z := range zones {
			result[i] = map[string]string{"id": z.ID, "name": z.Name}
		}
		return map[string]interface{}{"content": []map[string]interface{}{{"type": "text", "text": toJSON(result)}}}, nil

	case "list_records":
		zoneName, _ := arguments["zone_name"].(string)
		if zoneName == "" {
			return nil, fmt.Errorf("zone_name is required")
		}
		records, err := dnsUsecase.ListRecords(ctx, zoneName)
		if err != nil {
			return nil, err
		}
		return map[string]interface{}{"content": []map[string]interface{}{{"type": "text", "text": toJSON(records)}}}, nil

	case "get_record":
		zoneName, _ := arguments["zone_name"].(string)
		recordName, _ := arguments["record_name"].(string)
		if zoneName == "" || recordName == "" {
			return nil, fmt.Errorf("zone_name and record_name are required")
		}
		record, err := dnsUsecase.GetRecord(ctx, zoneName, recordName)
		if err != nil {
			if err == domain.ErrRecordNotFound {
				return map[string]interface{}{"content": []map[string]interface{}{{"type": "text", "text": "Record not found"}}}, nil
			}
			return nil, err
		}
		return map[string]interface{}{"content": []map[string]interface{}{{"type": "text", "text": toJSON(record)}}}, nil

	case "create_record":
		input := usecase.CreateRecordInput{
			ZoneName: getString(arguments, "zone_name"),
			Name:     getString(arguments, "name"),
			Type:     getString(arguments, "type"),
			Content:  getString(arguments, "content"),
			TTL:      getInt(arguments, "ttl"),
			Proxied:  getBool(arguments, "proxied"),
		}
		record, err := dnsUsecase.CreateRecord(ctx, input)
		if err != nil {
			return nil, err
		}
		return map[string]interface{}{"content": []map[string]interface{}{{"type": "text", "text": toJSON(record)}}}, nil

	case "update_record":
		input := usecase.UpdateRecordInput{
			ZoneName: getString(arguments, "zone_name"),
			RecordID: getString(arguments, "record_id"),
			Name:     getString(arguments, "name"),
			Type:     getString(arguments, "type"),
			Content:  getString(arguments, "content"),
			TTL:      getInt(arguments, "ttl"),
			Proxied:  getBool(arguments, "proxied"),
		}
		record, err := dnsUsecase.UpdateRecord(ctx, input)
		if err != nil {
			return nil, err
		}
		return map[string]interface{}{"content": []map[string]interface{}{{"type": "text", "text": toJSON(record)}}}, nil

	case "delete_record":
		zoneName := getString(arguments, "zone_name")
		recordName := getString(arguments, "record_name")
		if err := dnsUsecase.DeleteRecord(ctx, zoneName, recordName); err != nil {
			return nil, err
		}
		return map[string]interface{}{"content": []map[string]interface{}{{"type": "text", "text": "Record deleted successfully"}}}, nil

	case "upsert_record":
		input := usecase.CreateRecordInput{
			ZoneName: getString(arguments, "zone_name"),
			Name:     getString(arguments, "name"),
			Type:     getString(arguments, "type"),
			Content:  getString(arguments, "content"),
			TTL:      getInt(arguments, "ttl"),
			Proxied:  getBool(arguments, "proxied"),
		}
		record, err := dnsUsecase.UpsertRecord(ctx, input)
		if err != nil {
			return nil, err
		}
		return map[string]interface{}{"content": []map[string]interface{}{{"type": "text", "text": toJSON(record)}}}, nil

	default:
		return nil, fmt.Errorf("unknown tool: %s", name)
	}
}

// Helper functions
func toJSON(v interface{}) string {
	b, _ := json.MarshalIndent(v, "", "  ")
	return string(b)
}

func getString(m map[string]interface{}, key string) string {
	if v, ok := m[key].(string); ok {
		return v
	}
	return ""
}

func getInt(m map[string]interface{}, key string) int {
	if v, ok := m[key].(float64); ok {
		return int(v)
	}
	return 0
}

func getBool(m map[string]interface{}, key string) bool {
	if v, ok := m[key].(bool); ok {
		return v
	}
	return false
}

// ensure Bot implements handler.BotHandler
var _ handler.BotHandler = (*telegram.Bot)(nil)
