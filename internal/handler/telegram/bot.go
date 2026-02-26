package telegram

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"log"
	"strconv"
	"strings"
	"time"

	"cf-dns-bot/internal/domain"
	"cf-dns-bot/internal/usecase"
	"cf-dns-bot/pkg/storage"

	tele "gopkg.in/telebot.v3"
)

// APIKeyStorage defines the interface for API key management
type APIKeyStorage interface {
	GetAPIKeys() ([]string, error)
	AddAPIKey(key string) error
	RemoveAPIKey(key string) error
	IsValidAPIKey(key string) bool
}

// ConfigStorage defines the interface for configuration storage
type ConfigStorage interface {
	GetMCPHTTPPort() (string, error)
	SetMCPHTTPPort(port string) error
	GetMCPHTTPEnabled() (bool, error)
	SetMCPHTTPEnabled(enabled bool) error
}

// Ensure our interfaces match the storage package interfaces
var _ APIKeyStorage = (storage.APIKeyStorage)(nil)
var _ ConfigStorage = (storage.MCPHTTPConfigStorage)(nil)

// MCPHTTPServerController defines the interface for controlling MCP HTTP server
type MCPHTTPServerController interface {
	Start() error
	Stop() error
	IsRunning() bool
	GetPort() string
}

// PendingRequestStorage defines the interface for pending request storage
type PendingRequestStorage interface {
	GetPendingRequests() ([]storage.PendingRequest, error)
	AddPendingRequest(req storage.PendingRequest) error
	RemovePendingRequest(userID int64) error
	IsPendingRequest(userID int64) (bool, error)
}

// Bot implements handler.BotHandler for Telegram with button-based UI
type Bot struct {
	dnsUsecase        usecase.DNSUsecase
	bot               *tele.Bot
	token             string
	allowedIDs        map[int64]bool
	stateManager      *StateManager
	apiKeyStorage     APIKeyStorage
	configStorage     ConfigStorage
	mcpHTTPController MCPHTTPServerController
	pendingReqStorage PendingRequestStorage
}

// NewBot creates a new Telegram bot handler
func NewBot(dnsUsecase usecase.DNSUsecase, token string, allowedUsers []int64, apiKeyStorage APIKeyStorage, configStorage ConfigStorage, mcpHTTPController MCPHTTPServerController, pendingReqStorage PendingRequestStorage) *Bot {
	allowedIDs := make(map[int64]bool)
	for _, id := range allowedUsers {
		allowedIDs[id] = true
	}

	return &Bot{
		dnsUsecase:        dnsUsecase,
		token:             token,
		allowedIDs:        allowedIDs,
		stateManager:      NewStateManager(),
		apiKeyStorage:     apiKeyStorage,
		configStorage:     configStorage,
		mcpHTTPController: mcpHTTPController,
		pendingReqStorage: pendingReqStorage,
	}
}

// Start starts the bot
func (b *Bot) Start() error {
	pref := tele.Settings{
		Token:  b.token,
		Poller: &tele.LongPoller{Timeout: 10 * time.Second},
	}

	bot, err := tele.NewBot(pref)
	if err != nil {
		return fmt.Errorf("failed to create bot: %w", err)
	}

	b.bot = bot
	log.Printf("Authorized on account %s", bot.Me.Username)

	// Send startup notification to all admin users
	b.notifyAdminOnStartup()

	// Setup handlers
	b.setupHandlers()

	bot.Start()
	return nil
}

// Stop stops the bot
func (b *Bot) Stop() error {
	if b.bot != nil {
		b.bot.Stop()
	}
	return nil
}

// isAuthorized checks if a user is authorized
func (b *Bot) isAuthorized(userID int64) bool {
	if len(b.allowedIDs) == 0 {
		return true
	}
	return b.allowedIDs[userID]
}

// setupHandlers sets up all bot handlers
func (b *Bot) setupHandlers() {
	// Middleware for authorization
	b.bot.Use(func(next tele.HandlerFunc) tele.HandlerFunc {
		return func(c tele.Context) error {
			if !b.isAuthorized(c.Sender().ID) {
				b.handleUnauthorizedUser(c)
				return nil
			}
			return next(c)
		}
	})

	// Thread ID middleware - ensures all replies go to the same thread
	b.bot.Use(func(next tele.HandlerFunc) tele.HandlerFunc {
		return func(c tele.Context) error {
			// Get thread ID from incoming message
			threadID := 0
			if c.Message() != nil && c.Message().ThreadID != 0 {
				threadID = c.Message().ThreadID
				b.stateManager.SetData(c.Sender().ID, "thread_id", threadID)
				log.Printf("[Middleware] Stored thread_id %d from message for user %d", threadID, c.Sender().ID)
			} else if c.Callback() != nil && c.Callback().Message != nil && c.Callback().Message.ThreadID != 0 {
				threadID = c.Callback().Message.ThreadID
				b.stateManager.SetData(c.Sender().ID, "thread_id", threadID)
				log.Printf("[Middleware] Stored thread_id %d from callback for user %d", threadID, c.Sender().ID)
			} else {
				// Try to get from state
				if storedID, exists := b.stateManager.GetData(c.Sender().ID, "thread_id"); exists {
					threadID = storedID.(int)
					log.Printf("[Middleware] Retrieved thread_id %d from state for user %d", threadID, c.Sender().ID)
				}
			}
			return next(c)
		}
	})

	// Command handlers
	b.bot.Handle("/start", func(c tele.Context) error {
		return b.showMainMenu(c)
	})

	b.bot.Handle("/requests", func(c tele.Context) error {
		// Only admins can use this command
		if !b.isAuthorized(c.Sender().ID) {
			return c.Send("⛔ You are not authorized to use this command.", tele.ModeMarkdown)
		}
		return b.showPendingRequests(c)
	})

	// Callback handlers
	b.bot.Handle(&tele.Btn{Unique: "menu"}, func(c tele.Context) error {
		return b.showMainMenu(c)
	})

	b.bot.Handle(&tele.Btn{Unique: "zones"}, func(c tele.Context) error {
		return b.showZones(c)
	})

	b.bot.Handle(&tele.Btn{Unique: "create"}, func(c tele.Context) error {
		return b.startCreateRecord(c)
	})

	b.bot.Handle(&tele.Btn{Unique: "manage"}, func(c tele.Context) error {
		return b.startManageRecords(c)
	})

	b.bot.Handle(&tele.Btn{Unique: "mcphttp"}, func(c tele.Context) error {
		return b.showMCPHTTPMenu(c)
	})

	b.bot.Handle(&tele.Btn{Unique: "apikeys"}, func(c tele.Context) error {
		return b.showAPIKeysMenu(c)
	})

	// Handle text messages for state-based input
	b.bot.Handle(tele.OnText, func(c tele.Context) error {
		return b.handleTextMessage(c)
	})

	// Handle callback data with pattern matching
	b.bot.Handle(tele.OnCallback, func(c tele.Context) error {
		data := c.Data()
		log.Printf("[OnCallback] Raw callback data: %q, Unique: %q", data, c.Callback().Unique)
		return b.handleCallback(c)
	})
}

// notifyAdminOnStartup sends a startup notification to all admin users
func (b *Bot) notifyAdminOnStartup() {
	if len(b.allowedIDs) == 0 {
		return
	}

	message := "🤖 *Bot Started*\n\nCF DNS Bot is now online and ready to use."

	for userID := range b.allowedIDs {
		b.sendMessage(userID, message)
	}
}

// handleUnauthorizedUser handles unauthorized users
func (b *Bot) handleUnauthorizedUser(c tele.Context) {
	userID := c.Sender().ID
	chatID := c.Chat().ID
	threadID := 0
	if c.Message() != nil {
		threadID = c.Message().ThreadID
	}

	// Check if already pending
	if b.pendingReqStorage != nil {
		isPending, _ := b.pendingReqStorage.IsPendingRequest(userID)
		if isPending {
			b.sendMessage(chatID, "⏳ Your access request is pending approval. Please wait for an admin to review your request.", threadID)
			return
		}
	}

	// Show request access button
	menu := &tele.ReplyMarkup{ResizeKeyboard: true}
	btnRequest := menu.Data("📝 Request Access", "request_access")
	menu.Inline(menu.Row(btnRequest))

	b.sendMessageWithMarkup(chatID, "⛔ *Access Denied*\n\nYou are not authorized to use this bot. Would you like to request access?", menu, threadID)
}

// handleTextMessage handles incoming text messages
func (b *Bot) handleTextMessage(c tele.Context) error {
	userID := c.Sender().ID
	chatID := c.Chat().ID
	threadID := c.Message().ThreadID

	// Debug logging
	log.Printf("[handleTextMessage] UserID: %d, ChatID: %d, ThreadID: %d, Text: %s", userID, chatID, threadID, c.Text())

	// Store thread ID in state
	if threadID != 0 {
		b.stateManager.SetData(userID, "thread_id", threadID)
		log.Printf("[handleTextMessage] Stored thread_id %d for user %d", threadID, userID)
	}

	step := b.stateManager.GetCurrentStep(userID)

	switch step {
	case StepInputRecordName:
		if msgID, exists := b.stateManager.GetData(userID, "create_message_id"); exists {
			return b.handleInputRecordName(c, chatID, userID, msgID.(int), c.Text())
		}
	case StepInputRecordContent:
		if msgID, exists := b.stateManager.GetData(userID, "create_message_id"); exists {
			return b.handleInputRecordContent(c, chatID, userID, msgID.(int), c.Text())
		}
	case StepInputRecordTTL:
		return b.handleInputRecordTTL(c, chatID, userID, c.Text())
	case StepEditRecordContent:
		if msgID, exists := b.stateManager.GetData(userID, "edit_message_id"); exists {
			return b.handleEditRecordContent(c, chatID, userID, msgID.(int), c.Text())
		}
	case StepEditRecordTTL:
		if msgID, exists := b.stateManager.GetData(userID, "edit_message_id"); exists {
			return b.handleEditRecordTTL(c, chatID, userID, msgID.(int), c.Text())
		}
	case StepInputMCPHTTPPort:
		return b.handleMCPHTTPPortChange(c, chatID, userID, c.Text())
	default:
		return b.showMainMenu(c)
	}

	return nil
}

// handleCallback handles callback queries
func (b *Bot) handleCallback(c tele.Context) error {
	data := c.Data()
	chatID := c.Chat().ID
	userID := c.Sender().ID
	messageID := c.Message().ID
	threadID := c.Message().ThreadID

	log.Printf("[Callback] UserID: %d, Raw Data: %q, ThreadID: %d", userID, data, threadID)

	// Answer callback
	c.Respond()

	// Strip the \f prefix that telebot.v3 adds to inline button callbacks
	data = strings.TrimPrefix(data, "\f")
	log.Printf("[Callback] After TrimPrefix: %q", data)

	// Parse callback data - telebot.v3 uses | as separator
	parts := strings.Split(data, "|")
	action := parts[0]
	log.Printf("[Callback] Action: %q, Parts: %v", action, parts)

	switch action {
	case "menu":
		return b.showMainMenu(c)
	case "zones":
		return b.showZones(c)
	case "create":
		return b.startCreateRecord(c)
	case "manage":
		return b.startManageRecords(c)
	case "select_zone_create":
		if len(parts) > 1 {
			return b.handleZoneSelectedForCreate(c, chatID, userID, messageID, parts[1])
		}
	case "select_zone_manage":
		if len(parts) > 1 {
			zoneName := parts[1]
			return b.handleZoneSelectedForManage(c, chatID, userID, messageID, zoneName)
		}
	case "select_type":
		if len(parts) > 1 {
			return b.handleRecordTypeSelected(c, chatID, userID, messageID, parts[1])
		}
	case "select_ttl":
		if len(parts) > 1 {
			return b.handleTTLSelected(c, chatID, userID, messageID, parts[1])
		}
	case "proxied":
		if len(parts) > 1 {
			return b.handleProxiedSelected(c, chatID, userID, messageID, parts[1] == "true")
		}
	case "confirm_create":
		return b.handleConfirmCreate(c, chatID, userID, messageID)
	case "cancel_create":
		b.stateManager.ClearState(userID)
		return b.showMainMenu(c)
	case "mcphttp":
		return b.showMCPHTTPMenu(c)
	case "apikeys":
		return b.showAPIKeysMenu(c)
	case "view_rec":
		if len(parts) >= 4 {
			return b.handleViewRecord(c, chatID, userID, messageID, parts[1], parts[2], parts[3])
		}
	case "page":
		if len(parts) >= 3 {
			return b.handlePageChange(c, chatID, userID, messageID, parts[1], parts[2])
		}
	case "refresh":
		if len(parts) >= 3 && parts[1] == "zone" {
			return b.refreshZoneRecords(c, chatID, userID, messageID, parts[2], 0)
		}
	case "create_in_zone":
		if len(parts) >= 2 {
			zoneName := parts[1]
			return b.handleCreateInZone(c, chatID, userID, zoneName)
		}
	case "mcphttp_start":
		return b.handleMCPHTTPStart(c)
	case "mcphttp_stop":
		return b.handleMCPHTTPStop(c)
	case "mcphttp_port":
		return b.handleMCPHTTPPortInput(c, userID)
	case "mcphttp_status":
		return b.handleMCPHTTPStatus(c)
	case "apikey_generate":
		return b.handleAPIKeyGenerate(c)
	case "apikey_list":
		return b.handleAPIKeyList(c)
	case "apikey_delete":
		return b.handleAPIKeyDeleteMenu(c)
	case "delete_key":
		if len(parts) >= 2 {
			return b.handleAPIKeyDelete(c, parts[1])
		}
	case "request_access":
		return b.handleRequestAccess(c, userID)
	case "approve_request":
		if len(parts) >= 2 {
			return b.handleApproveRequest(c, parts[1])
		}
	case "reject_request":
		if len(parts) >= 2 {
			return b.handleRejectRequest(c, parts[1])
		}
	case "noop":
		// Do nothing for pagination display button
		return nil
	case "edit_rec":
		if len(parts) >= 4 {
			return b.handleEditRecord(c, chatID, userID, messageID, parts[1], parts[2], parts[3])
		}
	case "delete_rec":
		if len(parts) >= 4 {
			return b.handleDeleteRecord(c, chatID, userID, messageID, parts[1], parts[2], parts[3])
		}
	case "back":
		if len(parts) > 1 {
			return b.handleBackNavigation(c, chatID, userID, messageID, parts[1])
		}
	case "cancel_edit":
		b.stateManager.ClearState(userID)
		return b.showMainMenu(c)
	case "edit_ttl":
		if len(parts) > 1 {
			return b.handleEditRecordTTL(c, chatID, userID, messageID, parts[1])
		}
	case "edit_proxied":
		if len(parts) > 1 {
			return b.handleEditProxiedSelected(c, chatID, userID, messageID, parts[1] == "true")
		}
	}

	return nil
}

// Helper methods for sending messages

func (b *Bot) sendMessage(chatID int64, text string, threadID ...int) error {
	opts := []interface{}{tele.ModeMarkdown}
	if len(threadID) > 0 && threadID[0] != 0 {
		opts = append(opts, tele.Silent, &tele.SendOptions{ThreadID: threadID[0]})
	}
	_, err := b.bot.Send(&tele.Chat{ID: chatID}, text, opts...)
	if err != nil {
		log.Printf("Failed to send message: %v", err)
	}
	return err
}

func (b *Bot) sendMessageWithMarkup(chatID int64, text string, markup *tele.ReplyMarkup, threadID ...int) error {
	opts := []interface{}{tele.ModeMarkdown, markup}
	if len(threadID) > 0 && threadID[0] != 0 {
		opts = append(opts, &tele.SendOptions{ThreadID: threadID[0]})
	}
	_, err := b.bot.Send(&tele.Chat{ID: chatID}, text, opts...)
	if err != nil {
		log.Printf("Failed to send message with markup: %v", err)
	}
	return err
}

func (b *Bot) sendMessageToThread(chatID int64, threadID int, text string) error {
	if threadID == 0 {
		return b.sendMessage(chatID, text)
	}
	_, err := b.bot.Send(&tele.Chat{ID: chatID}, text, tele.ModeMarkdown, &tele.SendOptions{ThreadID: threadID})
	if err != nil {
		log.Printf("Failed to send message to thread: %v", err)
	}
	return err
}

// getThreadIDFromContext extracts thread ID from context (message or callback)
func (b *Bot) getThreadIDFromContext(c tele.Context) int {
	if c.Message() != nil && c.Message().ThreadID != 0 {
		return c.Message().ThreadID
	}
	if c.Callback() != nil && c.Callback().Message != nil && c.Callback().Message.ThreadID != 0 {
		return c.Callback().Message.ThreadID
	}
	return 0
}

// sendWithThread sends a message with proper thread ID from context
func (b *Bot) sendWithThread(c tele.Context, text string, opts ...interface{}) error {
	threadID := b.getThreadIDFromContext(c)
	if threadID != 0 {
		// Prepend SendOptions to ensure it's processed correctly
		opts = append([]interface{}{&tele.SendOptions{ThreadID: threadID}}, opts...)
	}
	return c.Send(text, opts...)
}

// editWithThread edits a message with proper thread ID from context
func (b *Bot) editWithThread(c tele.Context, text string, opts ...interface{}) error {
	threadID := b.getThreadIDFromContext(c)
	if threadID != 0 {
		// Prepend SendOptions to ensure it's processed correctly
		opts = append([]interface{}{&tele.SendOptions{ThreadID: threadID}}, opts...)
	}
	return c.Edit(text, opts...)
}

// showMainMenu shows the main menu
func (b *Bot) showMainMenu(c tele.Context) error {
	menu := &tele.ReplyMarkup{ResizeKeyboard: true}
	btnManage := menu.Data("🔍 Manage Records", "manage")
	btnMCPHTTP := menu.Data("🌐 MCP HTTP Server", "mcphttp")
	menu.Inline(menu.Row(btnManage), menu.Row(btnMCPHTTP))

	return b.sendWithThread(c, "*🏠 Main Menu*\n\nWhat would you like to do?", menu, tele.ModeMarkdown)
}

// showZones shows all zones
func (b *Bot) showZones(c tele.Context) error {
	ctx := context.Background()
	zones, err := b.dnsUsecase.ListZones(ctx)
	if err != nil {
		return b.sendWithThread(c, fmt.Sprintf("❌ Error: %v", err), tele.ModeMarkdown)
	}

	if len(zones) == 0 {
		return b.sendWithThread(c, "📭 No zones found.", tele.ModeMarkdown)
	}

	var text strings.Builder
	text.WriteString("*📋 Your Zones:*\n\n")
	for i, zone := range zones {
		text.WriteString(fmt.Sprintf("%d. `%s`\n", i+1, zone.Name))
	}

	menu := &tele.ReplyMarkup{ResizeKeyboard: true}
	btnBack := menu.Data("◀️ Back to Menu", "menu")
	menu.Inline(menu.Row(btnBack))

	return b.sendWithThread(c, text.String(), menu, tele.ModeMarkdown)
}

// startCreateRecord starts the create record flow
func (b *Bot) startCreateRecord(c tele.Context) error {
	ctx := context.Background()
	zones, err := b.dnsUsecase.ListZones(ctx)
	if err != nil {
		return b.sendWithThread(c, fmt.Sprintf("❌ Error: %v", err), tele.ModeMarkdown)
	}

	if len(zones) == 0 {
		return b.sendWithThread(c, "📭 No zones found.", tele.ModeMarkdown)
	}

	userID := c.Sender().ID
	b.stateManager.SetStep(userID, StepSelectZoneForCreate)

	menu := &tele.ReplyMarkup{ResizeKeyboard: true}
	var rows []tele.Row
	for i := 0; i < len(zones); i += 2 {
		var row tele.Row
		btn1 := menu.Data(zones[i].Name, "select_zone_create", zones[i].Name)
		row = append(row, btn1)
		if i+1 < len(zones) {
			btn2 := menu.Data(zones[i+1].Name, "select_zone_create", zones[i+1].Name)
			row = append(row, btn2)
		}
		rows = append(rows, row)
	}
	rows = append(rows, menu.Row(menu.Data("◀️ Cancel", "menu")))
	menu.Inline(rows...)

	return b.sendWithThread(c, "*➕ Create DNS Record*\n\nStep 1/6: Select a zone:", menu, tele.ModeMarkdown)
}

// handleZoneSelectedForCreate handles zone selection for create
func (b *Bot) handleZoneSelectedForCreate(c tele.Context, chatID int64, userID int64, messageID int, zoneName string) error {
	b.stateManager.SetData(userID, "zone", zoneName)
	b.stateManager.SetStep(userID, StepSelectRecordType)

	types := []string{"A", "AAAA", "CNAME", "MX", "TXT", "NS", "SRV", "CAA"}
	menu := &tele.ReplyMarkup{ResizeKeyboard: true}
	var rows []tele.Row
	for i := 0; i < len(types); i += 4 {
		var row tele.Row
		for j := i; j < i+4 && j < len(types); j++ {
			row = append(row, menu.Data(types[j], "select_type", types[j]))
		}
		rows = append(rows, row)
	}
	rows = append(rows, menu.Row(menu.Data("◀️ Back", "back", "create")))
	rows = append(rows, menu.Row(menu.Data("❌ Cancel", "cancel_create")))
	menu.Inline(rows...)

	return b.editWithThread(c, fmt.Sprintf("*➕ Create DNS Record*\n\nZone: `%s`\n\nStep 2/6: Select record type:", zoneName), menu, tele.ModeMarkdown)
}

// handleRecordTypeSelected handles record type selection
func (b *Bot) handleRecordTypeSelected(c tele.Context, chatID int64, userID int64, messageID int, recordType string) error {
	b.stateManager.SetData(userID, "type", recordType)
	b.stateManager.SetStep(userID, StepInputRecordName)

	menu := &tele.ReplyMarkup{ResizeKeyboard: true}
	menu.Inline(
		menu.Row(menu.Data("◀️ Back", "back", "type"), menu.Data("❌ Cancel", "cancel_create")),
	)

	zone, _ := b.stateManager.GetData(userID, "zone")
	b.stateManager.SetData(userID, "create_message_id", messageID)

	return b.editWithThread(c, fmt.Sprintf(
		"*➕ Create DNS Record*\n\nZone: `%s`\nType: `%s`\n\nStep 3/6: Enter the record name (e.g., `www`, `api`, `@` for root):",
		zone, recordType,
	), menu, tele.ModeMarkdown)
}

// handleInputRecordName handles record name input
func (b *Bot) handleInputRecordName(c tele.Context, chatID int64, userID int64, messageID int, name string) error {
	b.stateManager.SetData(userID, "name", name)
	b.stateManager.SetStep(userID, StepInputRecordContent)

	menu := &tele.ReplyMarkup{ResizeKeyboard: true}
	menu.Inline(
		menu.Row(menu.Data("◀️ Back", "back", "name"), menu.Data("❌ Cancel", "cancel_create")),
	)

	zone, _ := b.stateManager.GetData(userID, "zone")
	recordType, _ := b.stateManager.GetData(userID, "type")

	return b.editWithThread(c, fmt.Sprintf(
		"*➕ Create DNS Record*\n\nZone: `%s`\nType: `%s`\nName: `%s`\n\nStep 4/6: Enter the content (IP for A/AAAA, domain for CNAME, etc.):",
		zone, recordType, name,
	), menu, tele.ModeMarkdown)
}

// handleInputRecordContent handles record content input
func (b *Bot) handleInputRecordContent(c tele.Context, chatID int64, userID int64, messageID int, content string) error {
	b.stateManager.SetData(userID, "content", content)
	b.stateManager.SetStep(userID, StepInputRecordTTL)

	menu := &tele.ReplyMarkup{ResizeKeyboard: true}
	menu.Inline(
		menu.Row(
			menu.Data("Auto (1)", "select_ttl", "1"),
			menu.Data("300", "select_ttl", "300"),
			menu.Data("600", "select_ttl", "600"),
		),
		menu.Row(
			menu.Data("1800", "select_ttl", "1800"),
			menu.Data("3600", "select_ttl", "3600"),
			menu.Data("86400", "select_ttl", "86400"),
		),
		menu.Row(menu.Data("◀️ Back", "back", "content"), menu.Data("❌ Cancel", "cancel_create")),
	)

	zone, _ := b.stateManager.GetData(userID, "zone")
	recordType, _ := b.stateManager.GetData(userID, "type")
	name, _ := b.stateManager.GetData(userID, "name")

	return b.editWithThread(c, fmt.Sprintf(
		"*➕ Create DNS Record*\n\nZone: `%s`\nType: `%s`\nName: `%s`\nContent: `%s`\n\nStep 5/6: Select TTL:",
		zone, recordType, name, content,
	), menu, tele.ModeMarkdown)
}

// handleTTLSelected handles TTL selection
func (b *Bot) handleTTLSelected(c tele.Context, chatID int64, userID int64, messageID int, ttlStr string) error {
	ttl, _ := strconv.Atoi(ttlStr)
	b.stateManager.SetData(userID, "ttl", ttl)
	b.stateManager.SetStep(userID, StepInputRecordProxied)

	menu := &tele.ReplyMarkup{ResizeKeyboard: true}
	menu.Inline(
		menu.Row(
			menu.Data("✅ Yes (Proxied)", "proxied", "true"),
			menu.Data("❌ No (DNS Only)", "proxied", "false"),
		),
		menu.Row(menu.Data("◀️ Back", "back", "ttl"), menu.Data("❌ Cancel", "cancel_create")),
	)

	zone, _ := b.stateManager.GetData(userID, "zone")
	recordType, _ := b.stateManager.GetData(userID, "type")
	name, _ := b.stateManager.GetData(userID, "name")
	content, _ := b.stateManager.GetData(userID, "content")

	return b.editWithThread(c, fmt.Sprintf(
		"*➕ Create DNS Record*\n\nZone: `%s`\nType: `%s`\nName: `%s`\nContent: `%s`\nTTL: `%d`\n\nStep 6/6: Enable Cloudflare proxy?",
		zone, recordType, name, content, ttl,
	), menu, tele.ModeMarkdown)
}

// handleProxiedSelected handles proxied selection
func (b *Bot) handleProxiedSelected(c tele.Context, chatID int64, userID int64, messageID int, proxied bool) error {
	b.stateManager.SetData(userID, "proxied", proxied)
	b.stateManager.SetStep(userID, StepConfirmCreate)

	zone, _ := b.stateManager.GetData(userID, "zone")
	recordType, _ := b.stateManager.GetData(userID, "type")
	name, _ := b.stateManager.GetData(userID, "name")
	content, _ := b.stateManager.GetData(userID, "content")
	ttl, _ := b.stateManager.GetData(userID, "ttl")

	proxiedStr := "No"
	if proxied {
		proxiedStr = "Yes"
	}

	menu := &tele.ReplyMarkup{ResizeKeyboard: true}
	menu.Inline(
		menu.Row(menu.Data("✅ Confirm Create", "confirm_create")),
		menu.Row(menu.Data("❌ Cancel", "cancel_create")),
	)

	return b.editWithThread(c, fmt.Sprintf(
		"*➕ Create DNS Record - Confirm*\n\nZone: `%s`\nType: `%s`\nName: `%s`\nContent: `%s`\nTTL: `%v`\nProxied: `%s`\n\nConfirm creation?",
		zone, recordType, name, content, ttl, proxiedStr,
	), menu, tele.ModeMarkdown)
}

// handleConfirmCreate confirms and creates the record
func (b *Bot) handleConfirmCreate(c tele.Context, chatID int64, userID int64, messageID int) error {
	ctx := context.Background()

	zone, _ := b.stateManager.GetData(userID, "zone")
	recordType, _ := b.stateManager.GetData(userID, "type")
	name, _ := b.stateManager.GetData(userID, "name")
	content, _ := b.stateManager.GetData(userID, "content")
	ttl, _ := b.stateManager.GetData(userID, "ttl")
	proxied, _ := b.stateManager.GetData(userID, "proxied")

	input := usecase.CreateRecordInput{
		ZoneName: zone.(string),
		Name:     name.(string),
		Type:     recordType.(string),
		Content:  content.(string),
		TTL:      ttl.(int),
		Proxied:  proxied.(bool),
	}

	record, err := b.dnsUsecase.CreateRecord(ctx, input)
	if err != nil {
		if err == domain.ErrDuplicateRecord {
			return b.editWithThread(c, fmt.Sprintf("❌ Record `%s` already exists. Use *Manage Records* to update it.", name.(string)), tele.ModeMarkdown)
		}
		return b.editWithThread(c, fmt.Sprintf("❌ Error creating record: %v", err), tele.ModeMarkdown)
	}

	menu := &tele.ReplyMarkup{ResizeKeyboard: true}
	menu.Inline(
		menu.Row(menu.Data("➕ Create Another", "create"), menu.Data("🏠 Main Menu", "menu")),
	)

	b.stateManager.ClearState(userID)

	return b.editWithThread(c, fmt.Sprintf(
		"✅ *Record Created Successfully!*\n\nName: `%s`\nType: `%s`\nContent: `%s`\nTTL: `%d`\nProxied: `%v`",
		record.Name, record.Type, record.Content, record.TTL, record.Proxied,
	), menu, tele.ModeMarkdown)
}

// startManageRecords starts the manage records flow
func (b *Bot) startManageRecords(c tele.Context) error {
	ctx := context.Background()
	zones, err := b.dnsUsecase.ListZones(ctx)
	if err != nil {
		return b.sendWithThread(c, fmt.Sprintf("❌ Error: %v", err), tele.ModeMarkdown)
	}

	if len(zones) == 0 {
		return b.sendWithThread(c, "📭 No zones found.", tele.ModeMarkdown)
	}

	userID := c.Sender().ID
	b.stateManager.SetStep(userID, StepSelectZoneForManage)

	menu := &tele.ReplyMarkup{ResizeKeyboard: true}
	var rows []tele.Row
	for i := 0; i < len(zones); i += 2 {
		var row tele.Row
		btn1 := menu.Data(zones[i].Name, "select_zone_manage", zones[i].Name)
		row = append(row, btn1)
		if i+1 < len(zones) {
			btn2 := menu.Data(zones[i+1].Name, "select_zone_manage", zones[i+1].Name)
			row = append(row, btn2)
		}
		rows = append(rows, row)
	}
	rows = append(rows, menu.Row(menu.Data("◀️ Back to Menu", "menu")))
	menu.Inline(rows...)

	return b.sendWithThread(c, "*🔍 Manage Records*\n\nSelect a zone:", menu, tele.ModeMarkdown)
}

// handleZoneSelectedForManage handles zone selection for manage
func (b *Bot) handleZoneSelectedForManage(c tele.Context, chatID int64, userID int64, messageID int, zoneName string) error {
	return b.refreshZoneRecords(c, chatID, userID, messageID, zoneName, 0)
}

// refreshZoneRecords refreshes the zone records display with pagination
func (b *Bot) refreshZoneRecords(c tele.Context, chatID int64, userID int64, messageID int, zoneName string, page int) error {
	ctx := context.Background()
	records, err := b.dnsUsecase.ListRecords(ctx, zoneName)
	if err != nil {
		return b.editWithThread(c, fmt.Sprintf("❌ Error loading records: %v", err), tele.ModeMarkdown)
	}

	if len(records) == 0 {
		menu := &tele.ReplyMarkup{ResizeKeyboard: true}
		menu.Inline(
			menu.Row(menu.Data("➕ Create Record", "create_in_zone", zoneName), menu.Data("◀️ Back", "manage")),
		)
		return b.editWithThread(c, fmt.Sprintf("📭 No records found in `%s`.", zoneName), menu, tele.ModeMarkdown)
	}

	// Pagination settings
	recordsPerPage := 10
	totalRecords := len(records)
	totalPages := (totalRecords + recordsPerPage - 1) / recordsPerPage
	if totalPages < 1 {
		totalPages = 1
	}
	if page < 0 {
		page = 0
	}
	if page >= totalPages {
		page = totalPages - 1
	}

	startIdx := page * recordsPerPage
	endIdx := startIdx + recordsPerPage
	if endIdx > totalRecords {
		endIdx = totalRecords
	}

	var text strings.Builder
	text.WriteString(fmt.Sprintf("*🔍 Records in %s*\n", zoneName))
	text.WriteString(fmt.Sprintf("Page %d/%d (%d records)\n\n", page+1, totalPages, totalRecords))
	text.WriteString("Click a record to view details:\n")

	menu := &tele.ReplyMarkup{ResizeKeyboard: true}
	var rows []tele.Row
	for i := startIdx; i < endIdx; i++ {
		r := records[i]
		callbackData := fmt.Sprintf("view_rec:%s:%d:%d", zoneName, page, i-startIdx)
		if len(callbackData) > 64 {
			maxZoneLen := 64 - len(fmt.Sprintf("view_rec::%d:%d", page, i-startIdx)) - 1
			if maxZoneLen > 0 && len(zoneName) > maxZoneLen {
				callbackData = fmt.Sprintf("view_rec:%s:%d:%d", zoneName[:maxZoneLen], page, i-startIdx)
			}
		}
		rows = append(rows, menu.Row(menu.Data(fmt.Sprintf("📄 %s (%s)", r.Name, r.Type), "view_rec", zoneName, strconv.Itoa(page), strconv.Itoa(i-startIdx))))
	}

	// Pagination buttons
	var paginationRow tele.Row
	if page > 0 {
		paginationRow = append(paginationRow, menu.Data("⬅️ Prev", "page", zoneName, strconv.Itoa(page-1)))
	}
	paginationRow = append(paginationRow, menu.Data(fmt.Sprintf("📄 %d/%d", page+1, totalPages), "noop"))
	if page < totalPages-1 {
		paginationRow = append(paginationRow, menu.Data("Next ➡️", "page", zoneName, strconv.Itoa(page+1)))
	}
	rows = append(rows, paginationRow)

	rows = append(rows, menu.Row(menu.Data("🔄 Refresh", "refresh", "zone", zoneName), menu.Data("➕ Create", "create_in_zone", zoneName)))
	rows = append(rows, menu.Row(menu.Data("◀️ Back", "manage"), menu.Data("🏠 Menu", "menu")))

	menu.Inline(rows...)
	return b.editWithThread(c, text.String(), menu, tele.ModeMarkdown)
}

// handleInputRecordTTL handles custom TTL input
func (b *Bot) handleInputRecordTTL(c tele.Context, chatID int64, userID int64, ttlStr string) error {
	ttl, err := strconv.Atoi(ttlStr)
	if err != nil {
		return b.sendWithThread(c, "❌ Invalid TTL. Please enter a number.", tele.ModeMarkdown)
	}

	b.stateManager.SetData(userID, "ttl", ttl)
	b.stateManager.SetStep(userID, StepInputRecordProxied)

	menu := &tele.ReplyMarkup{ResizeKeyboard: true}
	menu.Inline(
		menu.Row(
			menu.Data("✅ Yes (Proxied)", "proxied", "true"),
			menu.Data("❌ No (DNS Only)", "proxied", "false"),
		),
		menu.Row(menu.Data("❌ Cancel", "cancel_create")),
	)

	zone, _ := b.stateManager.GetData(userID, "zone")
	recordType, _ := b.stateManager.GetData(userID, "type")
	name, _ := b.stateManager.GetData(userID, "name")
	content, _ := b.stateManager.GetData(userID, "content")

	return b.sendWithThread(c, fmt.Sprintf(
		"*➕ Create DNS Record*\n\nZone: `%s`\nType: `%s`\nName: `%s`\nContent: `%s`\nTTL: `%d`\n\nStep 6/6: Enable Cloudflare proxy?",
		zone, recordType, name, content, ttl,
	), menu, tele.ModeMarkdown)
}

// showMCPHTTPMenu shows the MCP HTTP server management menu
func (b *Bot) showMCPHTTPMenu(c tele.Context) error {
	if b.mcpHTTPController == nil {
		return b.sendWithThread(c, "❌ MCP HTTP server controller not configured.", tele.ModeMarkdown)
	}

	status := "🔴 Stopped"
	if b.mcpHTTPController.IsRunning() {
		status = "🟢 Running"
	}
	port := b.mcpHTTPController.GetPort()

	menu := &tele.ReplyMarkup{ResizeKeyboard: true}
	if b.mcpHTTPController.IsRunning() {
		menu.Inline(
			menu.Row(menu.Data("🛑 Stop Server", "mcphttp_stop")),
			menu.Row(menu.Data("🔢 Change Port", "mcphttp_port"), menu.Data("📊 Status", "mcphttp_status")),
			menu.Row(menu.Data("🔑 MCP API Keys", "apikeys")),
			menu.Row(menu.Data("◀️ Back to Menu", "menu")),
		)
	} else {
		menu.Inline(
			menu.Row(menu.Data("▶️ Start Server", "mcphttp_start")),
			menu.Row(menu.Data("🔢 Change Port", "mcphttp_port"), menu.Data("📊 Status", "mcphttp_status")),
			menu.Row(menu.Data("🔑 MCP API Keys", "apikeys")),
			menu.Row(menu.Data("◀️ Back to Menu", "menu")),
		)
	}

	return b.sendWithThread(c, fmt.Sprintf(
		"*🌐 MCP HTTP Server Management*\n\nStatus: %s\nPort: `%s`\n\nWhat would you like to do?",
		status, port,
	), menu, tele.ModeMarkdown)
}

// showAPIKeysMenu shows the API key management menu
func (b *Bot) showAPIKeysMenu(c tele.Context) error {
	menu := &tele.ReplyMarkup{ResizeKeyboard: true}
	menu.Inline(
		menu.Row(menu.Data("➕ Generate New Key", "apikey_generate")),
		menu.Row(menu.Data("📋 List Keys", "apikey_list")),
		menu.Row(menu.Data("🗑️ Delete Key", "apikey_delete")),
		menu.Row(menu.Data("◀️ Back to MCP HTTP Server", "mcphttp")),
	)

	return b.sendWithThread(c, "*🔑 MCP API Key Management*\n\nManage API keys for MCP server access:", menu, tele.ModeMarkdown)
}

// handleMCPHTTPPortChange handles the port change input
func (b *Bot) handleMCPHTTPPortChange(c tele.Context, chatID int64, userID int64, portStr string) error {
	if b.configStorage == nil || b.mcpHTTPController == nil {
		return b.sendWithThread(c, "❌ Configuration not available.", tele.ModeMarkdown)
	}

	port, err := strconv.Atoi(portStr)
	if err != nil || port < 1 || port > 65535 {
		return b.sendWithThread(c, "❌ Invalid port number. Please enter a number between 1 and 65535.", tele.ModeMarkdown)
	}

	b.stateManager.SetData(userID, "new_port", portStr)

	wasRunning := b.mcpHTTPController.IsRunning()

	if wasRunning {
		if err := b.mcpHTTPController.Stop(); err != nil {
			return b.sendWithThread(c, fmt.Sprintf("❌ Error stopping server: %v", err), tele.ModeMarkdown)
		}
	}

	if err := b.configStorage.SetMCPHTTPPort(portStr); err != nil {
		return b.sendWithThread(c, fmt.Sprintf("❌ Error saving port: %v", err), tele.ModeMarkdown)
	}

	if wasRunning {
		if err := b.mcpHTTPController.Start(); err != nil {
			return b.sendWithThread(c, fmt.Sprintf("❌ Error restarting server with new port: %v", err), tele.ModeMarkdown)
		}
	}

	menu := &tele.ReplyMarkup{ResizeKeyboard: true}
	menu.Inline(
		menu.Row(menu.Data("🔄 Change Again", "mcphttp_port"), menu.Data("📊 Status", "mcphttp_status")),
		menu.Row(menu.Data("🏠 Main Menu", "menu")),
	)

	b.stateManager.ClearState(userID)

	statusMsg := "Port saved. Start the server to use the new port."
	if wasRunning {
		statusMsg = "Server restarted with new port."
	}

	return b.sendWithThread(c, fmt.Sprintf(
		"✅ *Port Changed!*\n\nNew port: `%s`\n%s",
		portStr, statusMsg,
	), menu, tele.ModeMarkdown)
}

// Helper functions
func (b *Bot) generateRandomKey() (string, error) {
	bytes := make([]byte, 32)
	if _, err := rand.Read(bytes); err != nil {
		return "", err
	}
	return hex.EncodeToString(bytes), nil
}

func (b *Bot) maskAPIKey(key string) string {
	if len(key) <= 16 {
		return "****"
	}
	return key[:8] + "..." + key[len(key)-8:]
}

// handleViewRecord handles viewing a specific record
func (b *Bot) handleViewRecord(c tele.Context, chatID int64, userID int64, messageID int, zoneName, pageStr, idxStr string) error {
	page, _ := strconv.Atoi(pageStr)
	idx, _ := strconv.Atoi(idxStr)

	ctx := context.Background()
	records, err := b.dnsUsecase.ListRecords(ctx, zoneName)
	if err != nil {
		return b.editWithThread(c, fmt.Sprintf("❌ Error loading records: %v", err), tele.ModeMarkdown)
	}

	recordsPerPage := 10
	startIdx := page * recordsPerPage

	if startIdx+idx >= len(records) {
		return b.editWithThread(c, "❌ Record not found.", tele.ModeMarkdown)
	}

	r := records[startIdx+idx]

	menu := &tele.ReplyMarkup{ResizeKeyboard: true}
	menu.Inline(
		menu.Row(menu.Data("✏️ Edit", "edit_rec", zoneName, pageStr, idxStr), menu.Data("🗑️ Delete", "delete_rec", zoneName, pageStr, idxStr)),
		menu.Row(menu.Data("◀️ Back to List", "page", zoneName, pageStr)),
		menu.Row(menu.Data("🏠 Main Menu", "menu")),
	)

	proxiedStr := "❌ No"
	if r.Proxied {
		proxiedStr = "✅ Yes"
	}

	return b.editWithThread(c, fmt.Sprintf(
		"*📄 Record Details*\n\nZone: `%s`\nName: `%s`\nType: `%s`\nContent: `%s`\nTTL: `%d`\nProxied: `%s`\nRecord ID: `%s`",
		zoneName, r.Name, r.Type, r.Content, r.TTL, proxiedStr, r.ID,
	), menu, tele.ModeMarkdown)
}

// handlePageChange handles pagination
func (b *Bot) handlePageChange(c tele.Context, chatID int64, userID int64, messageID int, zoneName, pageStr string) error {
	page, _ := strconv.Atoi(pageStr)
	return b.refreshZoneRecords(c, chatID, userID, messageID, zoneName, page)
}

// handleCreateInZone starts creating a record in a specific zone
func (b *Bot) handleCreateInZone(c tele.Context, chatID int64, userID int64, zoneName string) error {
	b.stateManager.SetData(userID, "zone", zoneName)
	b.stateManager.SetStep(userID, StepSelectRecordType)

	types := []string{"A", "AAAA", "CNAME", "MX", "TXT", "NS", "SRV", "CAA"}
	menu := &tele.ReplyMarkup{ResizeKeyboard: true}
	var rows []tele.Row
	for i := 0; i < len(types); i += 4 {
		var row tele.Row
		for j := i; j < i+4 && j < len(types); j++ {
			row = append(row, menu.Data(types[j], "select_type", types[j]))
		}
		rows = append(rows, row)
	}
	rows = append(rows, menu.Row(menu.Data("◀️ Back", "back", "create")))
	rows = append(rows, menu.Row(menu.Data("❌ Cancel", "cancel_create")))
	menu.Inline(rows...)

	return b.sendWithThread(c, fmt.Sprintf("*➕ Create DNS Record*\n\nZone: `%s`\n\nStep 2/6: Select record type:", zoneName), menu, tele.ModeMarkdown)
}

// handleMCPHTTPStart starts the MCP HTTP server
func (b *Bot) handleMCPHTTPStart(c tele.Context) error {
	if b.mcpHTTPController == nil {
		return b.sendWithThread(c, "❌ MCP HTTP server controller not configured.", tele.ModeMarkdown)
	}

	if err := b.mcpHTTPController.Start(); err != nil {
		return b.sendWithThread(c, fmt.Sprintf("❌ Error starting server: %v", err), tele.ModeMarkdown)
	}

	return b.showMCPHTTPMenu(c)
}

// handleMCPHTTPStop stops the MCP HTTP server
func (b *Bot) handleMCPHTTPStop(c tele.Context) error {
	if b.mcpHTTPController == nil {
		return b.sendWithThread(c, "❌ MCP HTTP server controller not configured.", tele.ModeMarkdown)
	}

	if err := b.mcpHTTPController.Stop(); err != nil {
		return b.sendWithThread(c, fmt.Sprintf("❌ Error stopping server: %v", err), tele.ModeMarkdown)
	}

	return b.showMCPHTTPMenu(c)
}

// handleMCPHTTPPortInput prompts for port input
func (b *Bot) handleMCPHTTPPortInput(c tele.Context, userID int64) error {
	b.stateManager.SetStep(userID, StepInputMCPHTTPPort)

	menu := &tele.ReplyMarkup{ResizeKeyboard: true}
	menu.Inline(menu.Row(menu.Data("❌ Cancel", "mcphttp")))

	return b.sendWithThread(c, "🔢 *Change Port*\n\nEnter the new port number (1-65535):", menu, tele.ModeMarkdown)
}

// handleMCPHTTPStatus shows MCP HTTP server status
func (b *Bot) handleMCPHTTPStatus(c tele.Context) error {
	if b.mcpHTTPController == nil {
		return b.sendWithThread(c, "❌ MCP HTTP server controller not configured.", tele.ModeMarkdown)
	}

	status := "🔴 Stopped"
	if b.mcpHTTPController.IsRunning() {
		status = "🟢 Running"
	}
	port := b.mcpHTTPController.GetPort()

	menu := &tele.ReplyMarkup{ResizeKeyboard: true}
	menu.Inline(menu.Row(menu.Data("◀️ Back", "mcphttp")))

	return b.sendWithThread(c, fmt.Sprintf(
		"*📊 MCP HTTP Server Status*\n\nStatus: %s\nPort: `%s`",
		status, port,
	), menu, tele.ModeMarkdown)
}

// handleAPIKeyGenerate generates a new API key
func (b *Bot) handleAPIKeyGenerate(c tele.Context) error {
	if b.apiKeyStorage == nil {
		return b.sendWithThread(c, "❌ API key storage not configured.", tele.ModeMarkdown)
	}

	key, err := b.generateRandomKey()
	if err != nil {
		return b.sendWithThread(c, fmt.Sprintf("❌ Error generating key: %v", err), tele.ModeMarkdown)
	}

	if err := b.apiKeyStorage.AddAPIKey(key); err != nil {
		return b.sendWithThread(c, fmt.Sprintf("❌ Error saving key: %v", err), tele.ModeMarkdown)
	}

	menu := &tele.ReplyMarkup{ResizeKeyboard: true}
	menu.Inline(
		menu.Row(menu.Data("➕ Generate Another", "apikey_generate")),
		menu.Row(menu.Data("📋 List Keys", "apikey_list")),
		menu.Row(menu.Data("◀️ Back", "apikeys")),
	)

	return b.sendWithThread(c, fmt.Sprintf(
		"✅ *API Key Generated!*\n\nKey: `%s`\n\n⚠️ *Important:* Copy this key now. It will not be shown again.",
		key,
	), menu, tele.ModeMarkdown)
}

// handleAPIKeyList lists all API keys
func (b *Bot) handleAPIKeyList(c tele.Context) error {
	if b.apiKeyStorage == nil {
		return b.sendWithThread(c, "❌ API key storage not configured.", tele.ModeMarkdown)
	}

	keys, err := b.apiKeyStorage.GetAPIKeys()
	if err != nil {
		return b.sendWithThread(c, fmt.Sprintf("❌ Error getting keys: %v", err), tele.ModeMarkdown)
	}

	if len(keys) == 0 {
		menu := &tele.ReplyMarkup{ResizeKeyboard: true}
		menu.Inline(
			menu.Row(menu.Data("➕ Generate Key", "apikey_generate")),
			menu.Row(menu.Data("◀️ Back", "apikeys")),
		)
		return b.sendWithThread(c, "📭 No API keys found.", menu, tele.ModeMarkdown)
	}

	var text strings.Builder
	text.WriteString("*🔑 API Keys:*\n\n")
	for i, key := range keys {
		text.WriteString(fmt.Sprintf("%d. `%s`\n", i+1, b.maskAPIKey(key)))
	}

	menu := &tele.ReplyMarkup{ResizeKeyboard: true}
	menu.Inline(
		menu.Row(menu.Data("➕ Generate New", "apikey_generate"), menu.Data("🗑️ Delete Key", "apikey_delete")),
		menu.Row(menu.Data("◀️ Back", "apikeys")),
	)

	return b.sendWithThread(c, text.String(), menu, tele.ModeMarkdown)
}

// handleAPIKeyDeleteMenu shows the delete key menu
func (b *Bot) handleAPIKeyDeleteMenu(c tele.Context) error {
	if b.apiKeyStorage == nil {
		return b.sendWithThread(c, "❌ API key storage not configured.", tele.ModeMarkdown)
	}

	keys, err := b.apiKeyStorage.GetAPIKeys()
	if err != nil {
		return b.sendWithThread(c, fmt.Sprintf("❌ Error getting keys: %v", err), tele.ModeMarkdown)
	}

	if len(keys) == 0 {
		menu := &tele.ReplyMarkup{ResizeKeyboard: true}
		menu.Inline(
			menu.Row(menu.Data("➕ Generate Key", "apikey_generate")),
			menu.Row(menu.Data("◀️ Back", "apikeys")),
		)
		return b.sendWithThread(c, "📭 No API keys to delete.", menu, tele.ModeMarkdown)
	}

	menu := &tele.ReplyMarkup{ResizeKeyboard: true}
	var rows []tele.Row
	for i, key := range keys {
		rows = append(rows, menu.Row(menu.Data(fmt.Sprintf("🗑️ %s", b.maskAPIKey(key)), "delete_key", strconv.Itoa(i))))
	}
	rows = append(rows, menu.Row(menu.Data("◀️ Back", "apikeys")))
	menu.Inline(rows...)

	return b.sendWithThread(c, "*🗑️ Delete API Key*\n\nSelect a key to delete:", menu, tele.ModeMarkdown)
}

// handleAPIKeyDelete deletes a specific API key
func (b *Bot) handleAPIKeyDelete(c tele.Context, keyIdxStr string) error {
	if b.apiKeyStorage == nil {
		return b.sendWithThread(c, "❌ API key storage not configured.", tele.ModeMarkdown)
	}

	keyIdx, err := strconv.Atoi(keyIdxStr)
	if err != nil {
		return b.sendWithThread(c, "❌ Invalid key index.", tele.ModeMarkdown)
	}

	keys, err := b.apiKeyStorage.GetAPIKeys()
	if err != nil {
		return b.sendWithThread(c, fmt.Sprintf("❌ Error getting keys: %v", err), tele.ModeMarkdown)
	}

	if keyIdx < 0 || keyIdx >= len(keys) {
		return b.sendWithThread(c, "❌ Invalid key index.", tele.ModeMarkdown)
	}

	key := keys[keyIdx]
	if err := b.apiKeyStorage.RemoveAPIKey(key); err != nil {
		return b.sendWithThread(c, fmt.Sprintf("❌ Error deleting key: %v", err), tele.ModeMarkdown)
	}

	menu := &tele.ReplyMarkup{ResizeKeyboard: true}
	menu.Inline(
		menu.Row(menu.Data("📋 List Keys", "apikey_list")),
		menu.Row(menu.Data("◀️ Back", "apikeys")),
	)

	return b.sendWithThread(c, fmt.Sprintf("✅ Key `%s` deleted.", b.maskAPIKey(key)), menu, tele.ModeMarkdown)
}

// handleRequestAccess handles access requests from unauthorized users
func (b *Bot) handleRequestAccess(c tele.Context, userID int64) error {
	if b.pendingReqStorage == nil {
		return b.sendWithThread(c, "❌ Request system not configured.", tele.ModeMarkdown)
	}

	// Check if already pending
	isPending, _ := b.pendingReqStorage.IsPendingRequest(userID)
	if isPending {
		return b.sendWithThread(c, "⏳ Your request is already pending approval.", tele.ModeMarkdown)
	}

	// Add to pending requests
	req := storage.PendingRequest{
		UserID:    userID,
		Username:  c.Sender().Username,
		FirstName: c.Sender().FirstName,
		LastName:  c.Sender().LastName,
	}

	if err := b.pendingReqStorage.AddPendingRequest(req); err != nil {
		return b.sendWithThread(c, fmt.Sprintf("❌ Error submitting request: %v", err), tele.ModeMarkdown)
	}

	// Notify admins
	b.notifyAdminsOfRequest(req)

	return b.sendWithThread(c, "✅ Your access request has been submitted. You will be notified when it's reviewed.", tele.ModeMarkdown)
}

// notifyAdminsOfRequest notifies all admins of a new access request with approve/reject buttons
func (b *Bot) notifyAdminsOfRequest(req storage.PendingRequest) {
	userDesc := fmt.Sprintf("User ID: `%d`", req.UserID)
	if req.Username != "" {
		userDesc += fmt.Sprintf("\nUsername: @%s", req.Username)
	}
	if req.FirstName != "" || req.LastName != "" {
		userDesc += fmt.Sprintf("\nName: %s %s", req.FirstName, req.LastName)
	}

	message := fmt.Sprintf(
		"📝 *New Access Request*\n\n%s\n\nPlease review this request:",
		userDesc,
	)

	// Create approve/reject buttons
	menu := &tele.ReplyMarkup{ResizeKeyboard: true}
	btnApprove := menu.Data("✅ Approve", "approve_request", strconv.FormatInt(req.UserID, 10))
	btnReject := menu.Data("❌ Reject", "reject_request", strconv.FormatInt(req.UserID, 10))
	menu.Inline(menu.Row(btnApprove, btnReject))

	for adminID := range b.allowedIDs {
		b.sendMessageWithMarkup(adminID, message, menu)
	}
}

// handleApproveRequest approves an access request
func (b *Bot) handleApproveRequest(c tele.Context, userIDStr string) error {
	userID, err := strconv.ParseInt(userIDStr, 10, 64)
	if err != nil {
		return b.sendWithThread(c, "❌ Invalid user ID.", tele.ModeMarkdown)
	}

	if b.pendingReqStorage == nil {
		return b.sendWithThread(c, "❌ Request system not configured.", tele.ModeMarkdown)
	}

	// Remove from pending
	if err := b.pendingReqStorage.RemovePendingRequest(userID); err != nil {
		return b.sendWithThread(c, fmt.Sprintf("❌ Error removing request: %v", err), tele.ModeMarkdown)
	}

	// Add to allowed IDs
	b.allowedIDs[userID] = true

	// Notify user
	b.sendMessage(userID, "✅ *Access Approved*\n\nYour access request has been approved. You can now use the bot.")

	return b.sendWithThread(c, fmt.Sprintf("✅ User `%d` has been approved.", userID), tele.ModeMarkdown)
}

// showPendingRequests shows all pending access requests to admin
func (b *Bot) showPendingRequests(c tele.Context) error {
	if b.pendingReqStorage == nil {
		return b.sendWithThread(c, "❌ Request system not configured.", tele.ModeMarkdown)
	}

	requests, err := b.pendingReqStorage.GetPendingRequests()
	if err != nil {
		return b.sendWithThread(c, fmt.Sprintf("❌ Error getting pending requests: %v", err), tele.ModeMarkdown)
	}

	if len(requests) == 0 {
		return b.sendWithThread(c, "📭 No pending access requests.", tele.ModeMarkdown)
	}

	for _, req := range requests {
		userDesc := fmt.Sprintf("User ID: `%d`", req.UserID)
		if req.Username != "" {
			userDesc += fmt.Sprintf("\nUsername: @%s", req.Username)
		}
		if req.FirstName != "" || req.LastName != "" {
			userDesc += fmt.Sprintf("\nName: %s %s", req.FirstName, req.LastName)
		}

		message := fmt.Sprintf(
			"📝 *Pending Access Request*\n\n%s\n\nPlease review this request:",
			userDesc,
		)

		// Create approve/reject buttons
		menu := &tele.ReplyMarkup{ResizeKeyboard: true}
		btnApprove := menu.Data("✅ Approve", "approve_request", strconv.FormatInt(req.UserID, 10))
		btnReject := menu.Data("❌ Reject", "reject_request", strconv.FormatInt(req.UserID, 10))
		menu.Inline(menu.Row(btnApprove, btnReject))

		b.sendWithThread(c, message, menu, tele.ModeMarkdown)
	}

	return nil
}

// handleRejectRequest rejects an access request
func (b *Bot) handleRejectRequest(c tele.Context, userIDStr string) error {
	userID, err := strconv.ParseInt(userIDStr, 10, 64)
	if err != nil {
		return b.sendWithThread(c, "❌ Invalid user ID.", tele.ModeMarkdown)
	}

	if b.pendingReqStorage == nil {
		return b.sendWithThread(c, "❌ Request system not configured.", tele.ModeMarkdown)
	}

	// Remove from pending
	if err := b.pendingReqStorage.RemovePendingRequest(userID); err != nil {
		return b.sendWithThread(c, fmt.Sprintf("❌ Error removing request: %v", err), tele.ModeMarkdown)
	}

	// Notify user
	b.sendMessage(userID, "❌ *Access Denied*\n\nYour access request has been rejected.")

	return b.sendWithThread(c, fmt.Sprintf("❌ User `%d` has been rejected.", userID), tele.ModeMarkdown)
}

// handleEditRecordContent handles editing record content
func (b *Bot) handleEditRecordContent(c tele.Context, chatID int64, userID int64, messageID int, content string) error {
	b.stateManager.SetData(userID, "edit_content", content)
	b.stateManager.SetStep(userID, StepEditRecordTTL)

	menu := &tele.ReplyMarkup{ResizeKeyboard: true}
	menu.Inline(
		menu.Row(
			menu.Data("Auto (1)", "edit_ttl", "1"),
			menu.Data("300", "edit_ttl", "300"),
			menu.Data("600", "edit_ttl", "600"),
		),
		menu.Row(
			menu.Data("1800", "edit_ttl", "1800"),
			menu.Data("3600", "edit_ttl", "3600"),
			menu.Data("86400", "edit_ttl", "86400"),
		),
		menu.Row(menu.Data("◀️ Back", "back", "edit_content"), menu.Data("❌ Cancel", "cancel_edit")),
	)

	zone, _ := b.stateManager.GetData(userID, "edit_zone")
	recordType, _ := b.stateManager.GetData(userID, "edit_type")
	name, _ := b.stateManager.GetData(userID, "edit_name")

	return b.editWithThread(c, fmt.Sprintf(
		"*✏️ Edit DNS Record - TTL*\n\nZone: `%s`\nType: `%s`\nName: `%s`\nNew Content: `%s`\n\nSelect new TTL:",
		zone, recordType, name, content,
	), menu, tele.ModeMarkdown)
}

// handleEditRecordTTL handles editing record TTL
func (b *Bot) handleEditRecordTTL(c tele.Context, chatID int64, userID int64, messageID int, ttlStr string) error {
	ttl, err := strconv.Atoi(ttlStr)
	if err != nil {
		return b.sendWithThread(c, "❌ Invalid TTL. Please enter a number.", tele.ModeMarkdown)
	}

	b.stateManager.SetData(userID, "edit_ttl", ttl)
	b.stateManager.SetStep(userID, StepEditRecordProxied)

	menu := &tele.ReplyMarkup{ResizeKeyboard: true}
	menu.Inline(
		menu.Row(
			menu.Data("✅ Yes (Proxied)", "edit_proxied", "true"),
			menu.Data("❌ No (DNS Only)", "edit_proxied", "false"),
		),
		menu.Row(menu.Data("❌ Cancel", "cancel_edit")),
	)

	zone, _ := b.stateManager.GetData(userID, "edit_zone")
	recordType, _ := b.stateManager.GetData(userID, "edit_type")
	name, _ := b.stateManager.GetData(userID, "edit_name")
	content, _ := b.stateManager.GetData(userID, "edit_content")

	return b.sendWithThread(c, fmt.Sprintf(
		"*✏️ Edit DNS Record - Proxy*\n\nZone: `%s`\nType: `%s`\nName: `%s`\nNew Content: `%s`\nNew TTL: `%d`\n\nEnable Cloudflare proxy?",
		zone, recordType, name, content, ttl,
	), menu, tele.ModeMarkdown)
}

// handleEditRecord starts the edit record flow
func (b *Bot) handleEditRecord(c tele.Context, chatID int64, userID int64, messageID int, zoneName, pageStr, idxStr string) error {
	page, _ := strconv.Atoi(pageStr)
	idx, _ := strconv.Atoi(idxStr)

	ctx := context.Background()
	records, err := b.dnsUsecase.ListRecords(ctx, zoneName)
	if err != nil {
		return b.editWithThread(c, fmt.Sprintf("❌ Error loading records: %v", err), tele.ModeMarkdown)
	}

	recordsPerPage := 10
	startIdx := page * recordsPerPage

	if startIdx+idx >= len(records) {
		return b.editWithThread(c, "❌ Record not found.", tele.ModeMarkdown)
	}

	r := records[startIdx+idx]

	// Store record data in state
	b.stateManager.SetData(userID, "edit_zone", zoneName)
	b.stateManager.SetData(userID, "edit_type", r.Type)
	b.stateManager.SetData(userID, "edit_name", r.Name)
	b.stateManager.SetData(userID, "edit_record_id", r.ID)
	b.stateManager.SetData(userID, "edit_page", page)
	b.stateManager.SetData(userID, "edit_idx", idx)
	b.stateManager.SetStep(userID, StepEditRecordContent)
	b.stateManager.SetData(userID, "edit_message_id", messageID)

	menu := &tele.ReplyMarkup{ResizeKeyboard: true}
	menu.Inline(
		menu.Row(menu.Data("❌ Cancel", "cancel_edit")),
	)

	return b.editWithThread(c, fmt.Sprintf(
		"*✏️ Edit DNS Record*\n\nZone: `%s`\nType: `%s`\nName: `%s`\nCurrent Content: `%s`\n\nEnter the new content:",
		zoneName, r.Type, r.Name, r.Content,
	), menu, tele.ModeMarkdown)
}

// handleDeleteRecord deletes a record
func (b *Bot) handleDeleteRecord(c tele.Context, chatID int64, userID int64, messageID int, zoneName, pageStr, idxStr string) error {
	page, _ := strconv.Atoi(pageStr)
	idx, _ := strconv.Atoi(idxStr)

	ctx := context.Background()
	records, err := b.dnsUsecase.ListRecords(ctx, zoneName)
	if err != nil {
		return b.editWithThread(c, fmt.Sprintf("❌ Error loading records: %v", err), tele.ModeMarkdown)
	}

	recordsPerPage := 10
	startIdx := page * recordsPerPage

	if startIdx+idx >= len(records) {
		return b.editWithThread(c, "❌ Record not found.", tele.ModeMarkdown)
	}

	r := records[startIdx+idx]

	// Delete the record
	err = b.dnsUsecase.DeleteRecord(ctx, zoneName, r.ID)
	if err != nil {
		return b.editWithThread(c, fmt.Sprintf("❌ Error deleting record: %v", err), tele.ModeMarkdown)
	}

	menu := &tele.ReplyMarkup{ResizeKeyboard: true}
	menu.Inline(
		menu.Row(menu.Data("◀️ Back to List", "page", zoneName, pageStr)),
		menu.Row(menu.Data("🏠 Main Menu", "menu")),
	)

	return b.editWithThread(c, fmt.Sprintf(
		"✅ *Record Deleted*\n\nName: `%s`\nType: `%s`\nContent: `%s`",
		r.Name, r.Type, r.Content,
	), menu, tele.ModeMarkdown)
}

// handleBackNavigation handles back button navigation
func (b *Bot) handleBackNavigation(c tele.Context, chatID int64, userID int64, messageID int, backTo string) error {
	switch backTo {
	case "create":
		return b.startCreateRecord(c)
	case "type":
		zone, exists := b.stateManager.GetData(userID, "zone")
		if exists {
			return b.handleZoneSelectedForCreate(c, chatID, userID, messageID, zone.(string))
		}
		return b.startCreateRecord(c)
	case "name":
		return b.handleRecordTypeSelected(c, chatID, userID, messageID, b.getStateData(userID, "type"))
	case "content":
		return b.handleInputRecordName(c, chatID, userID, messageID, b.getStateData(userID, "name"))
	case "ttl":
		return b.handleInputRecordContent(c, chatID, userID, messageID, b.getStateData(userID, "content"))
	case "edit_content":
		// Go back to record details
		zone := b.getStateData(userID, "edit_zone")
		page := b.getStateData(userID, "edit_page")
		idx := b.getStateData(userID, "edit_idx")
		if zone != "" {
			pageInt, _ := strconv.Atoi(page)
			idxInt, _ := strconv.Atoi(idx)
			return b.handleViewRecord(c, chatID, userID, messageID, zone, strconv.Itoa(pageInt), strconv.Itoa(idxInt))
		}
	}
	return b.showMainMenu(c)
}

// getStateData safely gets state data
func (b *Bot) getStateData(userID int64, key string) string {
	val, exists := b.stateManager.GetData(userID, key)
	if !exists {
		return ""
	}
	switch v := val.(type) {
	case string:
		return v
	case int:
		return strconv.Itoa(v)
	case bool:
		return strconv.FormatBool(v)
	default:
		return fmt.Sprintf("%v", v)
	}
}

// handleEditProxiedSelected handles proxied selection for edit
func (b *Bot) handleEditProxiedSelected(c tele.Context, chatID int64, userID int64, messageID int, proxied bool) error {
	zone := b.getStateData(userID, "edit_zone")
	recordType := b.getStateData(userID, "edit_type")
	name := b.getStateData(userID, "edit_name")
	content := b.getStateData(userID, "edit_content")
	ttlStr := b.getStateData(userID, "edit_ttl")
	recordID := b.getStateData(userID, "edit_record_id")

	ttl, _ := strconv.Atoi(ttlStr)

	ctx := context.Background()
	input := usecase.UpdateRecordInput{
		ZoneName: zone,
		RecordID: recordID,
		Content:  content,
		TTL:      ttl,
		Proxied:  proxied,
	}

	_, err := b.dnsUsecase.UpdateRecord(ctx, input)
	if err != nil {
		return b.sendWithThread(c, fmt.Sprintf("❌ Error updating record: %v", err), tele.ModeMarkdown)
	}

	menu := &tele.ReplyMarkup{ResizeKeyboard: true}
	menu.Inline(
		menu.Row(menu.Data("◀️ Back to List", "page", zone, b.getStateData(userID, "edit_page"))),
		menu.Row(menu.Data("🏠 Main Menu", "menu")),
	)

	b.stateManager.ClearState(userID)

	return b.sendWithThread(c, fmt.Sprintf(
		"✅ *Record Updated Successfully!*\n\nZone: `%s`\nType: `%s`\nName: `%s`\nContent: `%s`\nTTL: `%d`\nProxied: `%v`",
		zone, recordType, name, content, ttl, proxied,
	), menu, tele.ModeMarkdown)
}
