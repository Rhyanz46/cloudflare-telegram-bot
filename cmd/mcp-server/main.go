package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log"

	"cf-dns-bot/external_resource/cloudflare"
	"cf-dns-bot/internal/domain"
	"cf-dns-bot/internal/repository"
	"cf-dns-bot/internal/usecase"
	"cf-dns-bot/pkg/config"
	"cf-dns-bot/pkg/storage"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
)

func main() {
	// Load configuration
	cfg, err := config.Load()
	if err != nil {
		log.Fatalf("Failed to load config: %v", err)
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

	// Create MCP server with tool capabilities enabled
	s := server.NewMCPServer(
		"cf-dns",
		"1.0.0",
		server.WithLogging(),
		server.WithToolCapabilities(true),
	)

	// Register tool: list_zones
	listZonesTool := mcp.NewTool("list_zones",
		"List all Cloudflare zones/domains",
		map[string]interface{}{
			"type":       "object",
			"properties": map[string]interface{}{},
		},
	)
	s.AddTool(listZonesTool, func(arguments map[string]interface{}) (*mcp.CallToolResult, error) {
		ctx := context.Background()
		zones, err := dnsUsecase.ListZones(ctx)
		if err != nil {
			return &mcp.CallToolResult{
				IsError: true,
				Content: []interface{}{mcp.NewTextContent(fmt.Sprintf("Error: %v", err))},
			}, nil
		}

		result := make([]map[string]string, len(zones))
		for i, z := range zones {
			result[i] = map[string]string{
				"id":   z.ID,
				"name": z.Name,
			}
		}

		jsonData, err := json.MarshalIndent(result, "", "  ")
		if err != nil {
			return &mcp.CallToolResult{
				IsError: true,
				Content: []interface{}{mcp.NewTextContent(fmt.Sprintf("Error: %v", err))},
			}, nil
		}

		return &mcp.CallToolResult{
			Content: []interface{}{mcp.NewTextContent(string(jsonData))},
		}, nil
	})

	// Register tool: list_records
	listRecordsTool := mcp.NewTool("list_records",
		"List all DNS records for a specific zone. IMPORTANT: Use params.arguments format. Example: {\"jsonrpc\":\"2.0\",\"id\":1,\"method\":\"tools/call\",\"params\":{\"name\":\"list_records\",\"arguments\":{\"zone_name\":\"example.com\"}}}",
		map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"zone_name": map[string]interface{}{
					"type":        "string",
					"description": "The zone/domain name (e.g., example.com)",
				},
			},
			"required": []string{"zone_name"},
		},
	)
	s.AddTool(listRecordsTool, func(arguments map[string]interface{}) (*mcp.CallToolResult, error) {
		ctx := context.Background()

		zoneName, ok := arguments["zone_name"].(string)
		if !ok || zoneName == "" {
			return &mcp.CallToolResult{
				IsError: true,
				Content: []interface{}{mcp.NewTextContent("Error: zone_name is required")},
			}, nil
		}

		records, err := dnsUsecase.ListRecords(ctx, zoneName)
		if err != nil {
			return &mcp.CallToolResult{
				IsError: true,
				Content: []interface{}{mcp.NewTextContent(fmt.Sprintf("Error: %v", err))},
			}, nil
		}

		result := make([]map[string]interface{}, len(records))
		for i, r := range records {
			result[i] = map[string]interface{}{
				"id":       r.ID,
				"name":     r.Name,
				"type":     r.Type,
				"content":  r.Content,
				"ttl":      r.TTL,
				"proxied":  r.Proxied,
				"priority": r.Priority,
			}
		}

		jsonData, err := json.MarshalIndent(result, "", "  ")
		if err != nil {
			return &mcp.CallToolResult{
				IsError: true,
				Content: []interface{}{mcp.NewTextContent(fmt.Sprintf("Error: %v", err))},
			}, nil
		}

		return &mcp.CallToolResult{
			Content: []interface{}{mcp.NewTextContent(string(jsonData))},
		}, nil
	})

	// Register tool: get_record
	getRecordTool := mcp.NewTool("get_record",
		"Get details of a specific DNS record. IMPORTANT: Use params.arguments format. Example: {\"jsonrpc\":\"2.0\",\"id\":1,\"method\":\"tools/call\",\"params\":{\"name\":\"get_record\",\"arguments\":{\"zone_name\":\"example.com\",\"record_name\":\"www.example.com\"}}}",
		map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"zone_name": map[string]interface{}{
					"type":        "string",
					"description": "The zone/domain name (e.g., example.com)",
				},
				"record_name": map[string]interface{}{
					"type":        "string",
					"description": "The full record name (e.g., www.example.com)",
				},
			},
			"required": []string{"zone_name", "record_name"},
		},
	)
	s.AddTool(getRecordTool, func(arguments map[string]interface{}) (*mcp.CallToolResult, error) {
		ctx := context.Background()

		zoneName, ok := arguments["zone_name"].(string)
		if !ok || zoneName == "" {
			return &mcp.CallToolResult{
				IsError: true,
				Content: []interface{}{mcp.NewTextContent("Error: zone_name is required")},
			}, nil
		}

		recordName, ok := arguments["record_name"].(string)
		if !ok || recordName == "" {
			return &mcp.CallToolResult{
				IsError: true,
				Content: []interface{}{mcp.NewTextContent("Error: record_name is required")},
			}, nil
		}

		record, err := dnsUsecase.GetRecord(ctx, zoneName, recordName)
		if err != nil {
			if err == domain.ErrRecordNotFound {
				return &mcp.CallToolResult{
					Content: []interface{}{mcp.NewTextContent("Record not found")},
				}, nil
			}
			return &mcp.CallToolResult{
				IsError: true,
				Content: []interface{}{mcp.NewTextContent(fmt.Sprintf("Error: %v", err))},
			}, nil
		}

		result := map[string]interface{}{
			"id":       record.ID,
			"name":     record.Name,
			"type":     record.Type,
			"content":  record.Content,
			"ttl":      record.TTL,
			"proxied":  record.Proxied,
			"priority": record.Priority,
		}

		jsonData, err := json.MarshalIndent(result, "", "  ")
		if err != nil {
			return &mcp.CallToolResult{
				IsError: true,
				Content: []interface{}{mcp.NewTextContent(fmt.Sprintf("Error: %v", err))},
			}, nil
		}

		return &mcp.CallToolResult{
			Content: []interface{}{mcp.NewTextContent(string(jsonData))},
		}, nil
	})

	// Register tool: create_record
	createRecordTool := mcp.NewTool("create_record",
		"Create a new DNS record",
		map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"zone_name": map[string]interface{}{
					"type":        "string",
					"description": "The zone/domain name",
				},
				"name": map[string]interface{}{
					"type":        "string",
					"description": "The record name (e.g., www, api, or @ for root)",
				},
				"type": map[string]interface{}{
					"type":        "string",
					"description": "Record type: A, AAAA, CNAME, MX, TXT, NS, SRV, CAA",
				},
				"content": map[string]interface{}{
					"type":        "string",
					"description": "The record content (IP for A/AAAA, domain for CNAME, etc.)",
				},
				"ttl": map[string]interface{}{
					"type":        "number",
					"description": "TTL in seconds (default: 300)",
				},
				"proxied": map[string]interface{}{
					"type":        "boolean",
					"description": "Enable Cloudflare proxy (default: false)",
				},
			},
			"required": []string{"zone_name", "name", "type", "content"},
		},
	)
	s.AddTool(createRecordTool, func(arguments map[string]interface{}) (*mcp.CallToolResult, error) {
		ctx := context.Background()

		input := usecase.CreateRecordInput{}

		if v, ok := arguments["zone_name"].(string); ok {
			input.ZoneName = v
		}
		if v, ok := arguments["name"].(string); ok {
			input.Name = v
		}
		if v, ok := arguments["type"].(string); ok {
			input.Type = v
		}
		if v, ok := arguments["content"].(string); ok {
			input.Content = v
		}
		if v, ok := arguments["ttl"].(float64); ok {
			input.TTL = int(v)
		}
		if v, ok := arguments["proxied"].(bool); ok {
			input.Proxied = v
		}

		record, err := dnsUsecase.CreateRecord(ctx, input)
		if err != nil {
			if err == domain.ErrDuplicateRecord {
				return &mcp.CallToolResult{
					Content: []interface{}{mcp.NewTextContent("Record already exists. Use upsert_record to update or update_record to modify.")},
				}, nil
			}
			return &mcp.CallToolResult{
				IsError: true,
				Content: []interface{}{mcp.NewTextContent(fmt.Sprintf("Error: %v", err))},
			}, nil
		}

		result := map[string]interface{}{
			"id":      record.ID,
			"name":    record.Name,
			"type":    record.Type,
			"content": record.Content,
			"ttl":     record.TTL,
			"proxied": record.Proxied,
		}

		jsonData, err := json.MarshalIndent(result, "", "  ")
		if err != nil {
			return &mcp.CallToolResult{
				IsError: true,
				Content: []interface{}{mcp.NewTextContent(fmt.Sprintf("Error: %v", err))},
			}, nil
		}

		return &mcp.CallToolResult{
			Content: []interface{}{mcp.NewTextContent(string(jsonData))},
		}, nil
	})

	// Register tool: update_record
	updateRecordTool := mcp.NewTool("update_record",
		"Update an existing DNS record",
		map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"zone_name": map[string]interface{}{
					"type":        "string",
					"description": "The zone/domain name",
				},
				"record_id": map[string]interface{}{
					"type":        "string",
					"description": "The record ID",
				},
				"name": map[string]interface{}{
					"type":        "string",
					"description": "The record name",
				},
				"type": map[string]interface{}{
					"type":        "string",
					"description": "Record type",
				},
				"content": map[string]interface{}{
					"type":        "string",
					"description": "The record content",
				},
				"ttl": map[string]interface{}{
					"type":        "number",
					"description": "TTL in seconds",
				},
				"proxied": map[string]interface{}{
					"type":        "boolean",
					"description": "Enable Cloudflare proxy",
				},
			},
			"required": []string{"zone_name", "record_id", "name", "type", "content"},
		},
	)
	s.AddTool(updateRecordTool, func(arguments map[string]interface{}) (*mcp.CallToolResult, error) {
		ctx := context.Background()

		input := usecase.UpdateRecordInput{}

		if v, ok := arguments["zone_name"].(string); ok {
			input.ZoneName = v
		}
		if v, ok := arguments["record_id"].(string); ok {
			input.RecordID = v
		}
		if v, ok := arguments["name"].(string); ok {
			input.Name = v
		}
		if v, ok := arguments["type"].(string); ok {
			input.Type = v
		}
		if v, ok := arguments["content"].(string); ok {
			input.Content = v
		}
		if v, ok := arguments["ttl"].(float64); ok {
			input.TTL = int(v)
		}
		if v, ok := arguments["proxied"].(bool); ok {
			input.Proxied = v
		}

		record, err := dnsUsecase.UpdateRecord(ctx, input)
		if err != nil {
			return &mcp.CallToolResult{
				IsError: true,
				Content: []interface{}{mcp.NewTextContent(fmt.Sprintf("Error: %v", err))},
			}, nil
		}

		result := map[string]interface{}{
			"id":      record.ID,
			"name":    record.Name,
			"type":    record.Type,
			"content": record.Content,
			"ttl":     record.TTL,
			"proxied": record.Proxied,
		}

		jsonData, err := json.MarshalIndent(result, "", "  ")
		if err != nil {
			return &mcp.CallToolResult{
				IsError: true,
				Content: []interface{}{mcp.NewTextContent(fmt.Sprintf("Error: %v", err))},
			}, nil
		}

		return &mcp.CallToolResult{
			Content: []interface{}{mcp.NewTextContent(string(jsonData))},
		}, nil
	})

	// Register tool: delete_record
	deleteRecordSchema := map[string]interface{}{
		"type": "object",
		"properties": map[string]interface{}{
			"zone_name": map[string]interface{}{
				"type":        "string",
				"description": "The zone/domain name (e.g., example.com)",
			},
			"record_name": map[string]interface{}{
				"type":        "string",
				"description": "The full record name to delete (e.g., www.example.com or cache.example.com)",
			},
		},
		"required": []string{"zone_name", "record_name"},
	}
	deleteRecordTool := mcp.NewTool("delete_record",
		"Delete a DNS record. IMPORTANT: Use params.arguments format with zone_name and record_name. Example request: {\"jsonrpc\":\"2.0\",\"id\":1,\"method\":\"tools/call\",\"params\":{\"name\":\"delete_record\",\"arguments\":{\"zone_name\":\"example.com\",\"record_name\":\"www.example.com\"}}}",
		deleteRecordSchema,
	)
	s.AddTool(deleteRecordTool, func(arguments map[string]interface{}) (*mcp.CallToolResult, error) {
		ctx := context.Background()

		// Debug: log all arguments received
		argsJSON, _ := json.Marshal(arguments)
		log.Printf("[delete_record] Received arguments: %s", string(argsJSON))

		zoneName, ok := arguments["zone_name"].(string)
		if !ok || zoneName == "" {
			log.Printf("[delete_record] ERROR: zone_name missing or invalid. Value: %v", arguments["zone_name"])
			return &mcp.CallToolResult{
				IsError: true,
				Content: []interface{}{mcp.NewTextContent("Error: zone_name is required. Received: " + string(argsJSON))},
			}, nil
		}

		recordName, ok := arguments["record_name"].(string)
		if !ok || recordName == "" {
			log.Printf("[delete_record] ERROR: record_name missing or invalid. Value: %v", arguments["record_name"])
			return &mcp.CallToolResult{
				IsError: true,
				Content: []interface{}{mcp.NewTextContent("Error: record_name is required. Received: " + string(argsJSON))},
			}, nil
		}

		err := dnsUsecase.DeleteRecord(ctx, zoneName, recordName)
		if err != nil {
			if err == domain.ErrRecordNotFound {
				return &mcp.CallToolResult{
					Content: []interface{}{mcp.NewTextContent("Record not found")},
				}, nil
			}
			return &mcp.CallToolResult{
				IsError: true,
				Content: []interface{}{mcp.NewTextContent(fmt.Sprintf("Error: %v", err))},
			}, nil
		}

		return &mcp.CallToolResult{
			Content: []interface{}{mcp.NewTextContent(fmt.Sprintf("Record '%s' deleted successfully", recordName))},
		}, nil
	})

	// Register tool: upsert_record
	upsertRecordTool := mcp.NewTool("upsert_record",
		"Create or update a DNS record (creates if not exists, updates if exists)",
		map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"zone_name": map[string]interface{}{
					"type":        "string",
					"description": "The zone/domain name",
				},
				"name": map[string]interface{}{
					"type":        "string",
					"description": "The record name (e.g., www, api, or @ for root)",
				},
				"type": map[string]interface{}{
					"type":        "string",
					"description": "Record type: A, AAAA, CNAME, MX, TXT, NS, SRV, CAA",
				},
				"content": map[string]interface{}{
					"type":        "string",
					"description": "The record content",
				},
				"ttl": map[string]interface{}{
					"type":        "number",
					"description": "TTL in seconds (default: 300)",
				},
				"proxied": map[string]interface{}{
					"type":        "boolean",
					"description": "Enable Cloudflare proxy (default: false)",
				},
			},
			"required": []string{"zone_name", "name", "type", "content"},
		},
	)
	s.AddTool(upsertRecordTool, func(arguments map[string]interface{}) (*mcp.CallToolResult, error) {
		ctx := context.Background()

		input := usecase.CreateRecordInput{}

		if v, ok := arguments["zone_name"].(string); ok {
			input.ZoneName = v
		}
		if v, ok := arguments["name"].(string); ok {
			input.Name = v
		}
		if v, ok := arguments["type"].(string); ok {
			input.Type = v
		}
		if v, ok := arguments["content"].(string); ok {
			input.Content = v
		}
		if v, ok := arguments["ttl"].(float64); ok {
			input.TTL = int(v)
		}
		if v, ok := arguments["proxied"].(bool); ok {
			input.Proxied = v
		}

		record, err := dnsUsecase.UpsertRecord(ctx, input)
		if err != nil {
			return &mcp.CallToolResult{
				IsError: true,
				Content: []interface{}{mcp.NewTextContent(fmt.Sprintf("Error: %v", err))},
			}, nil
		}

		result := map[string]interface{}{
			"id":      record.ID,
			"name":    record.Name,
			"type":    record.Type,
			"content": record.Content,
			"ttl":     record.TTL,
			"proxied": record.Proxied,
		}

		jsonData, err := json.MarshalIndent(result, "", "  ")
		if err != nil {
			return &mcp.CallToolResult{
				IsError: true,
				Content: []interface{}{mcp.NewTextContent(fmt.Sprintf("Error: %v", err))},
			}, nil
		}

		return &mcp.CallToolResult{
			Content: []interface{}{mcp.NewTextContent(string(jsonData))},
		}, nil
	})

	// Start server (stdio only)
	log.Println("Starting MCP stdio server...")
	if err := server.ServeStdio(s); err != nil {
		log.Fatalf("Server error: %v", err)
	}
}
