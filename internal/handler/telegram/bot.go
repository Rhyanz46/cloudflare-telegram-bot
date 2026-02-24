package telegram

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"log"
	"strconv"
	"strings"

	"cf-dns-bot/internal/domain"
	"cf-dns-bot/internal/usecase"
	"cf-dns-bot/pkg/storage"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
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
	dnsUsecase         usecase.DNSUsecase
	bot                *tgbotapi.BotAPI
	token              string
	allowedIDs         map[int64]bool
	stateManager       *StateManager
	apiKeyStorage      APIKeyStorage
	configStorage      ConfigStorage
	mcpHTTPController  MCPHTTPServerController
	pendingReqStorage  PendingRequestStorage
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
	bot, err := tgbotapi.NewBotAPI(b.token)
	if err != nil {
		return fmt.Errorf("failed to create bot: %w", err)
	}

	b.bot = bot
	log.Printf("Authorized on account %s", bot.Self.UserName)

	// Send startup notification to all admin users
	b.notifyAdminOnStartup()

	u := tgbotapi.NewUpdate(0)
	u.Timeout = 60

	updates := bot.GetUpdatesChan(u)

	for update := range updates {
		if update.Message != nil {
			if !b.isAuthorized(update.Message.From.ID) {
				b.handleUnauthorizedUser(update.Message)
				continue
			}
			go func(msg *tgbotapi.Message) {
				defer func() {
					if r := recover(); r != nil {
						log.Printf("[Panic] handleMessage: %v", r)
					}
				}()
				b.handleMessage(msg)
			}(update.Message)
		} else if update.CallbackQuery != nil {
			if !b.isAuthorized(update.CallbackQuery.From.ID) {
				b.answerCallback(update.CallbackQuery.ID, "⛔ Not authorized")
				continue
			}
			go func(cb *tgbotapi.CallbackQuery) {
				defer func() {
					if r := recover(); r != nil {
						log.Printf("[Panic] handleCallback: %v", r)
					}
				}()
				b.handleCallback(cb)
			}(update.CallbackQuery)
		}
	}

	return nil
}

// Stop stops the bot
func (b *Bot) Stop() error {
	if b.bot != nil {
		b.bot.StopReceivingUpdates()
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

// handleUnauthorizedUser handles unauthorized users by offering to send access request
func (b *Bot) handleUnauthorizedUser(msg *tgbotapi.Message) {
	userID := msg.From.ID
	chatID := msg.Chat.ID

	// Check if already pending
	if b.pendingReqStorage != nil {
		isPending, _ := b.pendingReqStorage.IsPendingRequest(userID)
		if isPending {
			b.sendMessage(chatID, "⏳ Your access request is pending approval. Please wait for an admin to review your request.")
			return
		}
	}

	// Show request access button
	keyboard := tgbotapi.NewInlineKeyboardMarkup(
		tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData("📝 Request Access", "request_access"),
		),
	)

	b.sendMessageWithKeyboard(chatID, "⛔ *Access Denied*\n\nYou are not authorized to use this bot. Would you like to request access?", keyboard)
}

// handleRequestAccess handles access request from unauthorized user
func (b *Bot) handleRequestAccess(chatID int64, userID int64, user *tgbotapi.User) {
	if b.pendingReqStorage == nil {
		b.sendMessage(chatID, "❌ Request system is not available. Please contact the admin directly.")
		return
	}

	// Check if already pending
	isPending, _ := b.pendingReqStorage.IsPendingRequest(userID)
	if isPending {
		b.sendMessage(chatID, "⏳ Your access request is already pending approval.")
		return
	}

	// Create pending request
	req := storage.PendingRequest{
		UserID:    userID,
		Username:  user.UserName,
		FirstName: user.FirstName,
		LastName:  user.LastName,
	}

	if err := b.pendingReqStorage.AddPendingRequest(req); err != nil {
		b.sendMessage(chatID, "❌ Failed to submit request. Please try again later.")
		return
	}

	// Notify user
	b.sendMessage(chatID, "✅ *Access Request Submitted*\n\nYour request has been sent to the admin. You will be notified once it's approved.")

	// Notify all admins
	b.notifyAdminsOfNewRequest(req)
}

// notifyAdminsOfNewRequest sends notification to all admins about a new access request
func (b *Bot) notifyAdminsOfNewRequest(req storage.PendingRequest) {
	if len(b.allowedIDs) == 0 {
		return
	}

	userInfo := fmt.Sprintf("User ID: `%d`", req.UserID)
	if req.Username != "" {
		userInfo += fmt.Sprintf("\nUsername: @%s", req.Username)
	}
	if req.FirstName != "" {
		userInfo += fmt.Sprintf("\nName: %s", req.FirstName)
		if req.LastName != "" {
			userInfo += fmt.Sprintf(" %s", req.LastName)
		}
	}

	message := fmt.Sprintf(
		"🔔 *New Access Request*\n\n%s\n\nWould you like to approve this request?",
		userInfo,
	)

	keyboard := tgbotapi.NewInlineKeyboardMarkup(
		tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData("✅ Approve", fmt.Sprintf("approve_request:%d", req.UserID)),
			tgbotapi.NewInlineKeyboardButtonData("❌ Reject", fmt.Sprintf("reject_request:%d", req.UserID)),
		),
	)

	for adminID := range b.allowedIDs {
		b.sendMessageWithKeyboard(adminID, message, keyboard)
	}
}

// handleApproveRequest handles admin approving an access request
func (b *Bot) handleApproveRequest(chatID int64, adminID int64, messageID int, userID int64) {
	if b.pendingReqStorage == nil {
		b.sendMessage(chatID, "❌ Request system is not available.")
		return
	}

	// Get pending requests to find the user info
	requests, _ := b.pendingReqStorage.GetPendingRequests()
	var foundReq *storage.PendingRequest
	for _, r := range requests {
		if r.UserID == userID {
			foundReq = &r
			break
		}
	}

	if foundReq == nil {
		b.editMessage(chatID, messageID, "❌ Request not found or already processed.", nil)
		return
	}

	// Remove from pending
	if err := b.pendingReqStorage.RemovePendingRequest(userID); err != nil {
		b.editMessage(chatID, messageID, "❌ Failed to process request.", nil)
		return
	}

	// Add to allowed users
	b.allowedIDs[userID] = true

	// Notify admin
	userInfo := fmt.Sprintf("User ID: `%d`", foundReq.UserID)
	if foundReq.Username != "" {
		userInfo += fmt.Sprintf(" (@%s)", foundReq.Username)
	}
	b.editMessage(chatID, messageID, fmt.Sprintf("✅ *Request Approved*\n\n%s has been granted access.", userInfo), nil)

	// Notify user
	b.sendMessage(userID, "🎉 *Access Granted!*\n\nYour request has been approved. You can now use the bot. Send /start to begin.")
}

// handleRejectRequest handles admin rejecting an access request
func (b *Bot) handleRejectRequest(chatID int64, adminID int64, messageID int, userID int64) {
	if b.pendingReqStorage == nil {
		b.sendMessage(chatID, "❌ Request system is not available.")
		return
	}

	// Get pending requests to find the user info
	requests, _ := b.pendingReqStorage.GetPendingRequests()
	var foundReq *storage.PendingRequest
	for _, r := range requests {
		if r.UserID == userID {
			foundReq = &r
			break
		}
	}

	if foundReq == nil {
		b.editMessage(chatID, messageID, "❌ Request not found or already processed.", nil)
		return
	}

	// Remove from pending
	if err := b.pendingReqStorage.RemovePendingRequest(userID); err != nil {
		b.editMessage(chatID, messageID, "❌ Failed to process request.", nil)
		return
	}

	// Notify admin
	userInfo := fmt.Sprintf("User ID: `%d`", foundReq.UserID)
	if foundReq.Username != "" {
		userInfo += fmt.Sprintf(" (@%s)", foundReq.Username)
	}
	b.editMessage(chatID, messageID, fmt.Sprintf("❌ *Request Rejected*\n\n%s has been denied access.", userInfo), nil)

	// Notify user
	b.sendMessage(userID, "❌ *Access Denied*\n\nYour request has been rejected. You cannot use this bot.")
}

// sendMessage sends a message to a chat
func (b *Bot) sendMessage(chatID int64, text string) {
	msg := tgbotapi.NewMessage(chatID, text)
	msg.ParseMode = "Markdown"
	if _, err := b.bot.Send(msg); err != nil {
		log.Printf("Failed to send message: %v", err)
	}
}

// sendMessageWithKeyboard sends a message with inline keyboard
func (b *Bot) sendMessageWithKeyboard(chatID int64, text string, keyboard tgbotapi.InlineKeyboardMarkup) {
	msg := tgbotapi.NewMessage(chatID, text)
	msg.ParseMode = "Markdown"
	msg.ReplyMarkup = keyboard
	if _, err := b.bot.Send(msg); err != nil {
		log.Printf("Failed to send message with keyboard: %v", err)
	}
}

// editMessage edits a message
func (b *Bot) editMessage(chatID int64, messageID int, text string, keyboard *tgbotapi.InlineKeyboardMarkup) {
	log.Printf("[editMessage] chatID: %d, messageID: %d, textLength: %d", chatID, messageID, len(text))
	edit := tgbotapi.NewEditMessageText(chatID, messageID, text)
	edit.ParseMode = "Markdown"
	if keyboard != nil {
		edit.ReplyMarkup = keyboard
	}
	if _, err := b.bot.Send(edit); err != nil {
		log.Printf("[editMessage] Failed to edit message: %v", err)
	} else {
		log.Printf("[editMessage] Message edited successfully")
	}
}

// answerCallback answers a callback query
func (b *Bot) answerCallback(callbackID string, text string) {
	callback := tgbotapi.NewCallback(callbackID, text)
	if _, err := b.bot.Send(callback); err != nil {
		log.Printf("Failed to answer callback: %v", err)
	}
}

// handleMessage handles incoming text messages
func (b *Bot) handleMessage(msg *tgbotapi.Message) {
	// Handle /start command
	if msg.IsCommand() && msg.Command() == "start" {
		b.showMainMenu(msg.Chat.ID)
		return
	}

	// Handle state-based input
	userID := msg.From.ID
	chatID := msg.Chat.ID
	step := b.stateManager.GetCurrentStep(userID)

	switch step {
	case StepInputRecordName:
		if msgID, exists := b.stateManager.GetData(userID, "create_message_id"); exists {
			b.handleInputRecordName(chatID, userID, msgID.(int), msg.Text)
		}
	case StepInputRecordContent:
		if msgID, exists := b.stateManager.GetData(userID, "create_message_id"); exists {
			b.handleInputRecordContent(chatID, userID, msgID.(int), msg.Text)
		}
	case StepInputRecordTTL:
		b.handleInputRecordTTL(chatID, userID, msg.Text)
	case StepEditRecordContent:
		if msgID, exists := b.stateManager.GetData(userID, "edit_message_id"); exists {
			b.handleEditRecordContent(chatID, userID, msgID.(int), msg.Text)
		}
	case StepEditRecordTTL:
		if msgID, exists := b.stateManager.GetData(userID, "edit_message_id"); exists {
			b.handleEditRecordTTL(chatID, userID, msgID.(int), msg.Text)
		}
	case StepInputMCPHTTPPort:
		b.handleMCPHTTPPortChange(chatID, userID, msg.Text)
	default:
		b.showMainMenu(msg.Chat.ID)
	}
}

// handleCallback handles inline keyboard callbacks
func (b *Bot) handleCallback(callback *tgbotapi.CallbackQuery) {
	data := callback.Data
	chatID := callback.Message.Chat.ID
	userID := callback.From.ID
	messageID := callback.Message.MessageID

	log.Printf("[Callback] UserID: %d, Data: %s", userID, data)

	// Answer callback to remove loading state
	b.answerCallback(callback.ID, "")

	// Parse callback data
	parts := strings.Split(data, ":")
	action := parts[0]
	log.Printf("[Callback] Action: %s, Parts: %v", action, parts)

	switch action {
	case "menu":
		b.showMainMenu(chatID)
	case "zones":
		b.showZones(chatID, userID)
	case "create":
		b.startCreateRecord(chatID, userID)
	case "create_in_zone":
		if len(parts) > 1 {
			zoneName := strings.Join(parts[1:], ":")
			b.startCreateRecordInZone(chatID, userID, messageID, zoneName)
		}
	case "manage":
		b.startManageRecords(chatID, userID)
	case "select_zone_create":
		if len(parts) > 1 {
			b.handleZoneSelectedForCreate(chatID, userID, messageID, parts[1])
		}
	case "select_zone_manage":
		if len(parts) > 1 {
			// Join remaining parts in case zone name contains ":"
			zoneName := strings.Join(parts[1:], ":")
			log.Printf("[Callback] Managing zone: %s", zoneName)
			b.handleZoneSelectedForManage(chatID, userID, messageID, zoneName)
		}
	case "select_type":
		if len(parts) > 1 {
			b.handleRecordTypeSelected(chatID, userID, messageID, parts[1])
		}
	case "select_ttl":
		if len(parts) > 1 {
			b.handleTTLSelected(chatID, userID, messageID, parts[1])
		}
	case "proxied":
		if len(parts) > 1 {
			b.handleProxiedSelected(chatID, userID, messageID, parts[1] == "true")
		}
	case "confirm_create":
		b.handleConfirmCreate(chatID, userID, messageID)
	case "cancel_create":
		b.stateManager.ClearState(userID)
		b.showMainMenu(chatID)
	case "view_rec":
		// Format: view_rec:zoneName:page:index
		if len(parts) >= 4 {
			// Join parts except first 3 for zone name (in case zone contains ":")
			zoneName := strings.Join(parts[1:len(parts)-2], ":")
			page, _ := strconv.Atoi(parts[len(parts)-2])
			index, _ := strconv.Atoi(parts[len(parts)-1])
			log.Printf("[Callback] View record: zoneName=%s, page=%d, index=%d", zoneName, page, index)
			b.showRecordDetailByIndex(chatID, userID, messageID, zoneName, page, index)
		}
	case "edit_record":
		if len(parts) > 2 {
			b.startEditRecord(chatID, userID, messageID, parts[1], parts[2])
		}
	case "delete_record":
		if len(parts) > 2 {
			b.startDeleteRecord(chatID, userID, messageID, parts[1], parts[2])
		}
	case "confirm_delete":
		if len(parts) > 2 {
			b.handleConfirmDelete(chatID, userID, messageID, parts[1], parts[2])
		}
	case "cancel_delete":
		if len(parts) > 1 {
			b.refreshZoneRecords(chatID, userID, messageID, parts[1], 0)
		}
	case "noop":
		// Do nothing, just a placeholder button
	case "back":
		b.handleBack(chatID, userID, messageID, parts)
	case "refresh":
		if len(parts) > 2 && parts[1] == "zone" {
			b.refreshZoneRecords(chatID, userID, messageID, parts[2], 0)
		}
	case "page":
		if len(parts) >= 3 {
			// Join all parts except first and last for zone name (in case zone contains ":")
			zoneName := strings.Join(parts[1:len(parts)-1], ":")
			page, _ := strconv.Atoi(parts[len(parts)-1])
			log.Printf("[Callback] Page navigation: zoneName=%s, page=%d, parts=%v", zoneName, page, parts)
			b.refreshZoneRecords(chatID, userID, messageID, zoneName, page)
		}
	case "edit_content":
		b.startEditContent(chatID, userID, messageID)
	case "edit_ttl":
		b.startEditTTL(chatID, userID, messageID)
	case "edit_proxied":
		b.handleToggleProxied(chatID, userID, messageID)
	case "back_to_edit":
		b.handleBackToEdit(chatID, userID, messageID)
	case "apikeys":
		b.showAPIKeysMenu(chatID, userID)
	case "apikey_generate":
		b.handleGenerateAPIKey(chatID, userID)
	case "apikey_list":
		b.handleListAPIKeys(chatID, userID)
	case "apikey_delete":
		b.startDeleteAPIKey(chatID, userID)
	case "confirm_delete_apikey":
		if len(parts) > 1 {
			key := strings.Join(parts[1:], ":")
			b.handleConfirmDeleteAPIKey(chatID, userID, key)
		}
	case "mcphttp":
		b.showMCPHTTPMenu(chatID, userID)
	case "mcphttp_start":
		b.handleMCPHTTPStart(chatID, userID)
	case "mcphttp_stop":
		b.handleMCPHTTPStop(chatID, userID)
	case "mcphttp_port":
		b.startMCPHTTPPortChange(chatID, userID, messageID)
	case "confirm_port_change":
		if len(parts) > 1 {
			b.handleConfirmPortChange(chatID, userID, messageID, parts[1])
		}
	case "mcphttp_status":
		b.handleMCPHTTPStatus(chatID, userID)
	case "request_access":
		b.handleRequestAccess(chatID, userID, callback.From)
	case "approve_request":
		if len(parts) > 1 {
			targetUserID, _ := strconv.ParseInt(parts[1], 10, 64)
			b.handleApproveRequest(chatID, userID, messageID, targetUserID)
		}
	case "reject_request":
		if len(parts) > 1 {
			targetUserID, _ := strconv.ParseInt(parts[1], 10, 64)
			b.handleRejectRequest(chatID, userID, messageID, targetUserID)
		}
	}
}

// showMainMenu shows the main menu
func (b *Bot) showMainMenu(chatID int64) {
	keyboard := tgbotapi.NewInlineKeyboardMarkup(
		tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData("🔍 Manage Records", "manage"),
		),
		tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData("🌐 MCP HTTP Server", "mcphttp"),
		),
	)

	b.sendMessageWithKeyboard(chatID, "*🏠 Main Menu*\n\nWhat would you like to do?", keyboard)
}

// showZones shows all zones
func (b *Bot) showZones(chatID int64, userID int64) {
	ctx := context.Background()
	zones, err := b.dnsUsecase.ListZones(ctx)
	if err != nil {
		b.sendMessage(chatID, fmt.Sprintf("❌ Error: %v", err))
		return
	}

	if len(zones) == 0 {
		b.sendMessage(chatID, "📭 No zones found.")
		return
	}

	var text strings.Builder
	text.WriteString("*📋 Your Zones:*\n\n")
	for i, zone := range zones {
		text.WriteString(fmt.Sprintf("%d. `%s`\n", i+1, zone.Name))
	}

	keyboard := tgbotapi.NewInlineKeyboardMarkup(
		tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData("◀️ Back to Menu", "menu"),
		),
	)

	b.sendMessageWithKeyboard(chatID, text.String(), keyboard)
}

// startCreateRecord starts the create record flow
func (b *Bot) startCreateRecord(chatID int64, userID int64) {
	ctx := context.Background()
	zones, err := b.dnsUsecase.ListZones(ctx)
	if err != nil {
		b.sendMessage(chatID, fmt.Sprintf("❌ Error: %v", err))
		return
	}

	if len(zones) == 0 {
		b.sendMessage(chatID, "📭 No zones found.")
		return
	}

	// Create zone selection buttons (2 per row)
	var rows [][]tgbotapi.InlineKeyboardButton
	for i := 0; i < len(zones); i += 2 {
		var row []tgbotapi.InlineKeyboardButton
		row = append(row, tgbotapi.NewInlineKeyboardButtonData(zones[i].Name, fmt.Sprintf("select_zone_create:%s", zones[i].Name)))
		if i+1 < len(zones) {
			row = append(row, tgbotapi.NewInlineKeyboardButtonData(zones[i+1].Name, fmt.Sprintf("select_zone_create:%s", zones[i+1].Name)))
		}
		rows = append(rows, row)
	}
	rows = append(rows, tgbotapi.NewInlineKeyboardRow(
		tgbotapi.NewInlineKeyboardButtonData("◀️ Cancel", "menu"),
	))

	keyboard := tgbotapi.NewInlineKeyboardMarkup(rows...)
	b.stateManager.SetStep(userID, StepSelectZoneForCreate)
	b.sendMessageWithKeyboard(chatID, "*➕ Create DNS Record*\n\nStep 1/6: Select a zone:", keyboard)
}

// handleZoneSelectedForCreate handles zone selection for create
func (b *Bot) handleZoneSelectedForCreate(chatID int64, userID int64, messageID int, zoneName string) {
	b.stateManager.SetData(userID, "zone", zoneName)
	b.stateManager.SetStep(userID, StepSelectRecordType)

	// Show record type selection
	types := []string{"A", "AAAA", "CNAME", "MX", "TXT", "NS", "SRV", "CAA"}
	var rows [][]tgbotapi.InlineKeyboardButton
	for i := 0; i < len(types); i += 4 {
		var row []tgbotapi.InlineKeyboardButton
		for j := i; j < i+4 && j < len(types); j++ {
			row = append(row, tgbotapi.NewInlineKeyboardButtonData(types[j], fmt.Sprintf("select_type:%s", types[j])))
		}
		rows = append(rows, row)
	}
	rows = append(rows, tgbotapi.NewInlineKeyboardRow(
		tgbotapi.NewInlineKeyboardButtonData("◀️ Back", "back:create"),
		tgbotapi.NewInlineKeyboardButtonData("❌ Cancel", "cancel_create"),
	))

	keyboard := tgbotapi.NewInlineKeyboardMarkup(rows...)
	b.editMessage(chatID, messageID, fmt.Sprintf("*➕ Create DNS Record*\n\nZone: `%s`\n\nStep 2/6: Select record type:", zoneName), &keyboard)
}

// startCreateRecordInZone starts create record flow with zone already selected
func (b *Bot) startCreateRecordInZone(chatID int64, userID int64, messageID int, zoneName string) {
	b.stateManager.SetData(userID, "zone", zoneName)
	b.stateManager.SetStep(userID, StepSelectRecordType)

	// Show record type selection (skip zone selection)
	types := []string{"A", "AAAA", "CNAME", "MX", "TXT", "NS", "SRV", "CAA"}
	var rows [][]tgbotapi.InlineKeyboardButton
	for i := 0; i < len(types); i += 4 {
		var row []tgbotapi.InlineKeyboardButton
		for j := i; j < i+4 && j < len(types); j++ {
			row = append(row, tgbotapi.NewInlineKeyboardButtonData(types[j], fmt.Sprintf("select_type:%s", types[j])))
		}
		rows = append(rows, row)
	}
	rows = append(rows, tgbotapi.NewInlineKeyboardRow(
		tgbotapi.NewInlineKeyboardButtonData("◀️ Back", fmt.Sprintf("refresh:zone:%s", zoneName)),
		tgbotapi.NewInlineKeyboardButtonData("❌ Cancel", "cancel_create"),
	))

	keyboard := tgbotapi.NewInlineKeyboardMarkup(rows...)
	b.editMessage(chatID, messageID, fmt.Sprintf("*➕ Create DNS Record*\n\nZone: `%s`\n\nStep 2/5: Select record type:", zoneName), &keyboard)
}

// handleRecordTypeSelected handles record type selection
func (b *Bot) handleRecordTypeSelected(chatID int64, userID int64, messageID int, recordType string) {
	b.stateManager.SetData(userID, "type", recordType)
	b.stateManager.SetStep(userID, StepInputRecordName)

	keyboard := tgbotapi.NewInlineKeyboardMarkup(
		tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData("◀️ Back", "back:type"),
			tgbotapi.NewInlineKeyboardButtonData("❌ Cancel", "cancel_create"),
		),
	)

	zone, _ := b.stateManager.GetData(userID, "zone")
	b.editMessage(chatID, messageID, fmt.Sprintf(
		"*➕ Create DNS Record*\n\nZone: `%s`\nType: `%s`\n\nStep 3/6: Enter the record name (e.g., `www`, `api`, `@` for root):",
		zone, recordType,
	), &keyboard)

	// Store message ID for later use
	b.stateManager.SetData(userID, "create_message_id", messageID)
}

// handleInputRecordName handles record name input
func (b *Bot) handleInputRecordName(chatID int64, userID int64, messageID int, name string) {
	b.stateManager.SetData(userID, "name", name)
	b.stateManager.SetStep(userID, StepInputRecordContent)

	keyboard := tgbotapi.NewInlineKeyboardMarkup(
		tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData("◀️ Back", "back:name"),
			tgbotapi.NewInlineKeyboardButtonData("❌ Cancel", "cancel_create"),
		),
	)

	zone, _ := b.stateManager.GetData(userID, "zone")
	recordType, _ := b.stateManager.GetData(userID, "type")
	b.editMessage(chatID, messageID, fmt.Sprintf(
		"*➕ Create DNS Record*\n\nZone: `%s`\nType: `%s`\nName: `%s`\n\nStep 4/6: Enter the content (IP for A/AAAA, domain for CNAME, etc.):",
		zone, recordType, name,
	), &keyboard)
}

// handleInputRecordContent handles record content input
func (b *Bot) handleInputRecordContent(chatID int64, userID int64, messageID int, content string) {
	b.stateManager.SetData(userID, "content", content)
	b.stateManager.SetStep(userID, StepInputRecordTTL)

	// Show TTL options
	keyboard := tgbotapi.NewInlineKeyboardMarkup(
		tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData("Auto (1)", "select_ttl:1"),
			tgbotapi.NewInlineKeyboardButtonData("300", "select_ttl:300"),
			tgbotapi.NewInlineKeyboardButtonData("600", "select_ttl:600"),
		),
		tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData("1800", "select_ttl:1800"),
			tgbotapi.NewInlineKeyboardButtonData("3600", "select_ttl:3600"),
			tgbotapi.NewInlineKeyboardButtonData("86400", "select_ttl:86400"),
		),
		tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData("◀️ Back", "back:content"),
			tgbotapi.NewInlineKeyboardButtonData("❌ Cancel", "cancel_create"),
		),
	)

	zone, _ := b.stateManager.GetData(userID, "zone")
	recordType, _ := b.stateManager.GetData(userID, "type")
	name, _ := b.stateManager.GetData(userID, "name")
	b.editMessage(chatID, messageID, fmt.Sprintf(
		"*➕ Create DNS Record*\n\nZone: `%s`\nType: `%s`\nName: `%s`\nContent: `%s`\n\nStep 5/6: Select TTL:",
		zone, recordType, name, content,
	), &keyboard)
}

// handleTTLSelected handles TTL selection
func (b *Bot) handleTTLSelected(chatID int64, userID int64, messageID int, ttlStr string) {
	ttl, _ := strconv.Atoi(ttlStr)
	b.stateManager.SetData(userID, "ttl", ttl)
	b.stateManager.SetStep(userID, StepInputRecordProxied)

	keyboard := tgbotapi.NewInlineKeyboardMarkup(
		tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData("✅ Yes (Proxied)", "proxied:true"),
			tgbotapi.NewInlineKeyboardButtonData("❌ No (DNS Only)", "proxied:false"),
		),
		tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData("◀️ Back", "back:ttl"),
			tgbotapi.NewInlineKeyboardButtonData("❌ Cancel", "cancel_create"),
		),
	)

	zone, _ := b.stateManager.GetData(userID, "zone")
	recordType, _ := b.stateManager.GetData(userID, "type")
	name, _ := b.stateManager.GetData(userID, "name")
	content, _ := b.stateManager.GetData(userID, "content")
	b.editMessage(chatID, messageID, fmt.Sprintf(
		"*➕ Create DNS Record*\n\nZone: `%s`\nType: `%s`\nName: `%s`\nContent: `%s`\nTTL: `%d`\n\nStep 6/6: Enable Cloudflare proxy?",
		zone, recordType, name, content, ttl,
	), &keyboard)
}

// handleProxiedSelected handles proxied selection
func (b *Bot) handleProxiedSelected(chatID int64, userID int64, messageID int, proxied bool) {
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

	keyboard := tgbotapi.NewInlineKeyboardMarkup(
		tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData("✅ Confirm Create", "confirm_create"),
		),
		tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData("❌ Cancel", "cancel_create"),
		),
	)

	b.editMessage(chatID, messageID, fmt.Sprintf(
		"*➕ Create DNS Record - Confirm*\n\nZone: `%s`\nType: `%s`\nName: `%s`\nContent: `%s`\nTTL: `%v`\nProxied: `%s`\n\nConfirm creation?",
		zone, recordType, name, content, ttl, proxiedStr,
	), &keyboard)
}

// handleConfirmCreate confirms and creates the record
func (b *Bot) handleConfirmCreate(chatID int64, userID int64, messageID int) {
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
			b.editMessage(chatID, messageID, fmt.Sprintf("❌ Record `%s` already exists. Use *Manage Records* to update it.", name.(string)), nil)
		} else {
			b.editMessage(chatID, messageID, fmt.Sprintf("❌ Error creating record: %v", err), nil)
		}
		b.stateManager.ClearState(userID)
		return
	}

	keyboard := tgbotapi.NewInlineKeyboardMarkup(
		tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData("➕ Create Another", "create"),
			tgbotapi.NewInlineKeyboardButtonData("🏠 Main Menu", "menu"),
		),
	)

	b.editMessage(chatID, messageID, fmt.Sprintf(
		"✅ *Record Created Successfully!*\n\nName: `%s`\nType: `%s`\nContent: `%s`\nTTL: `%d`\nProxied: `%v`",
		record.Name, record.Type, record.Content, record.TTL, record.Proxied,
	), &keyboard)

	b.stateManager.ClearState(userID)
}

// startManageRecords starts the manage records flow
func (b *Bot) startManageRecords(chatID int64, userID int64) {
	ctx := context.Background()
	zones, err := b.dnsUsecase.ListZones(ctx)
	if err != nil {
		b.sendMessage(chatID, fmt.Sprintf("❌ Error: %v", err))
		return
	}

	if len(zones) == 0 {
		b.sendMessage(chatID, "📭 No zones found.")
		return
	}

	var rows [][]tgbotapi.InlineKeyboardButton
	for i := 0; i < len(zones); i += 2 {
		var row []tgbotapi.InlineKeyboardButton
		row = append(row, tgbotapi.NewInlineKeyboardButtonData(zones[i].Name, fmt.Sprintf("select_zone_manage:%s", zones[i].Name)))
		if i+1 < len(zones) {
			row = append(row, tgbotapi.NewInlineKeyboardButtonData(zones[i+1].Name, fmt.Sprintf("select_zone_manage:%s", zones[i+1].Name)))
		}
		rows = append(rows, row)
	}
	rows = append(rows, tgbotapi.NewInlineKeyboardRow(
		tgbotapi.NewInlineKeyboardButtonData("◀️ Back to Menu", "menu"),
	))

	keyboard := tgbotapi.NewInlineKeyboardMarkup(rows...)
	b.stateManager.SetStep(userID, StepSelectZoneForManage)
	b.sendMessageWithKeyboard(chatID, "*🔍 Manage Records*\n\nSelect a zone:", keyboard)
}

// handleZoneSelectedForManage handles zone selection for manage
func (b *Bot) handleZoneSelectedForManage(chatID int64, userID int64, messageID int, zoneName string) {
	log.Printf("[handleZoneSelectedForManage] START chatID=%d, userID=%d, messageID=%d, zoneName=%s", chatID, userID, messageID, zoneName)
	b.refreshZoneRecords(chatID, userID, messageID, zoneName, 0)
	log.Printf("[handleZoneSelectedForManage] END")
}

// refreshZoneRecords refreshes the zone records display with pagination
func (b *Bot) refreshZoneRecords(chatID int64, userID int64, messageID int, zoneName string, page int) {
	log.Printf("[refreshZoneRecords] START chatID=%d, messageID=%d, zoneName=%s, page=%d", chatID, messageID, zoneName, page)
	ctx := context.Background()
	log.Printf("[refreshZoneRecords] Calling ListRecords for zone: %s", zoneName)
	records, err := b.dnsUsecase.ListRecords(ctx, zoneName)
	if err != nil {
		log.Printf("[refreshZoneRecords] ERROR: %v", err)
		b.sendMessage(chatID, fmt.Sprintf("❌ Error loading records: %v", err))
		return
	}
	log.Printf("[refreshZoneRecords] SUCCESS: Found %d records", len(records))

	if len(records) == 0 {
		keyboard := tgbotapi.NewInlineKeyboardMarkup(
			tgbotapi.NewInlineKeyboardRow(
				tgbotapi.NewInlineKeyboardButtonData("➕ Create Record", fmt.Sprintf("create_in_zone:%s", zoneName)),
				tgbotapi.NewInlineKeyboardButtonData("◀️ Back", "manage"),
			),
		)
		msg := fmt.Sprintf("📭 No records found in `%s`.", zoneName)
		b.editMessage(chatID, messageID, msg, &keyboard)
		return
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
	log.Printf("[refreshZoneRecords] Pagination: totalRecords=%d, totalPages=%d, currentPage=%d", totalRecords, totalPages, page)

	startIdx := page * recordsPerPage
	endIdx := startIdx + recordsPerPage
	if endIdx > totalRecords {
		endIdx = totalRecords
	}

	var text strings.Builder
	text.WriteString(fmt.Sprintf("*🔍 Records in %s*\n", zoneName))
	text.WriteString(fmt.Sprintf("Page %d/%d (%d records)\n\n", page+1, totalPages, totalRecords))
	text.WriteString("Click a record to view details:\n")

	var rows [][]tgbotapi.InlineKeyboardButton
	for i := startIdx; i < endIdx; i++ {
		r := records[i]

		// Add button for each record (click to view detail)
		// Use record index instead of ID to avoid callback data length limit (64 bytes max)
		// Index format: zoneName:page:index
		callbackData := fmt.Sprintf("view_rec:%s:%d:%d", zoneName, page, i-startIdx)
		if len(callbackData) > 64 {
			// Truncate zone name if too long
			maxZoneLen := 64 - len(fmt.Sprintf("view_rec::%d:%d", page, i-startIdx)) - 1
			if maxZoneLen > 0 && len(zoneName) > maxZoneLen {
				callbackData = fmt.Sprintf("view_rec:%s:%d:%d", zoneName[:maxZoneLen], page, i-startIdx)
			}
		}
		rows = append(rows, tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData(fmt.Sprintf("📄 %s (%s)", r.Name, r.Type), callbackData),
		))
	}

	// Pagination buttons
	var paginationRow []tgbotapi.InlineKeyboardButton
	if page > 0 {
		prevCallback := fmt.Sprintf("page:%s:%d", zoneName, page-1)
		if len(prevCallback) > 64 {
			// Truncate zone name if too long
			maxZoneLen := 64 - len(fmt.Sprintf("page::%d", page-1)) - 1
			if maxZoneLen > 0 && len(zoneName) > maxZoneLen {
				prevCallback = fmt.Sprintf("page:%s:%d", zoneName[:maxZoneLen], page-1)
			}
		}
		paginationRow = append(paginationRow, tgbotapi.NewInlineKeyboardButtonData("⬅️ Prev", prevCallback))
	}
	paginationRow = append(paginationRow, tgbotapi.NewInlineKeyboardButtonData(fmt.Sprintf("📄 %d/%d", page+1, totalPages), "noop"))
	if page < totalPages-1 {
		nextCallback := fmt.Sprintf("page:%s:%d", zoneName, page+1)
		log.Printf("[refreshZoneRecords] Next button callback: %s (length: %d)", nextCallback, len(nextCallback))
		if len(nextCallback) > 64 {
			// Truncate zone name if too long
			maxZoneLen := 64 - len(fmt.Sprintf("page::%d", page+1)) - 1
			if maxZoneLen > 0 && len(zoneName) > maxZoneLen {
				nextCallback = fmt.Sprintf("page:%s:%d", zoneName[:maxZoneLen], page+1)
				log.Printf("[refreshZoneRecords] Truncated callback: %s", nextCallback)
			}
		}
		paginationRow = append(paginationRow, tgbotapi.NewInlineKeyboardButtonData("Next ➡️", nextCallback))
	}
	rows = append(rows, paginationRow)

	rows = append(rows, tgbotapi.NewInlineKeyboardRow(
		tgbotapi.NewInlineKeyboardButtonData("🔄 Refresh", fmt.Sprintf("refresh:zone:%s", zoneName)),
		tgbotapi.NewInlineKeyboardButtonData("➕ Create", fmt.Sprintf("create_in_zone:%s", zoneName)),
	))
	rows = append(rows, tgbotapi.NewInlineKeyboardRow(
		tgbotapi.NewInlineKeyboardButtonData("◀️ Back", "manage"),
		tgbotapi.NewInlineKeyboardButtonData("🏠 Menu", "menu"),
	))

	keyboard := tgbotapi.NewInlineKeyboardMarkup(rows...)
	msgText := text.String()
	log.Printf("[refreshZoneRecords] Sending message with length=%d", len(msgText))
	b.editMessage(chatID, messageID, msgText, &keyboard)
}

// showRecordDetail shows the detail of a record with edit/delete buttons
func (b *Bot) showRecordDetail(chatID int64, userID int64, messageID int, zoneName, recordName string) {
	ctx := context.Background()

	record, err := b.dnsUsecase.GetRecord(ctx, zoneName, recordName)
	if err != nil {
		b.editMessage(chatID, messageID, fmt.Sprintf("❌ Error: %v", err), nil)
		return
	}

	proxiedStr := "❌ No"
	if record.Proxied {
		proxiedStr = "✅ Yes"
	}

	var text strings.Builder
	text.WriteString(fmt.Sprintf("*📋 Record Details*\n\n"))
	text.WriteString(fmt.Sprintf("*Zone:* `%s`\n", zoneName))
	text.WriteString(fmt.Sprintf("*Name:* `%s`\n", record.Name))
	text.WriteString(fmt.Sprintf("*Type:* `%s`\n", record.Type))
	text.WriteString(fmt.Sprintf("*Content:* `%s`\n", record.Content))
	text.WriteString(fmt.Sprintf("*TTL:* `%d`\n", record.TTL))
	text.WriteString(fmt.Sprintf("*Proxied:* %s\n", proxiedStr))

	keyboard := tgbotapi.NewInlineKeyboardMarkup(
		tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData("✏️ Edit", fmt.Sprintf("edit_record:%s:%s", zoneName, recordName)),
			tgbotapi.NewInlineKeyboardButtonData("🗑️ Delete", fmt.Sprintf("delete_record:%s:%s", zoneName, recordName)),
		),
		tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData("◀️ Back to List", fmt.Sprintf("refresh:zone:%s", zoneName)),
			tgbotapi.NewInlineKeyboardButtonData("🏠 Menu", "menu"),
		),
	)

	b.editMessage(chatID, messageID, text.String(), &keyboard)
}

// showRecordDetailByIndex shows the detail of a record by its page and index
func (b *Bot) showRecordDetailByIndex(chatID int64, userID int64, messageID int, zoneName string, page, index int) {
	ctx := context.Background()
	log.Printf("[showRecordDetailByIndex] zoneName=%s, page=%d, index=%d", zoneName, page, index)

	// List all records
	records, err := b.dnsUsecase.ListRecords(ctx, zoneName)
	if err != nil {
		log.Printf("[showRecordDetailByIndex] ERROR listing records: %v", err)
		b.editMessage(chatID, messageID, fmt.Sprintf("❌ Error loading records: %v", err), nil)
		return
	}

	// Calculate the actual index in the records array
	recordsPerPage := 10
	actualIndex := page*recordsPerPage + index

	if actualIndex < 0 || actualIndex >= len(records) {
		log.Printf("[showRecordDetailByIndex] Index out of range: %d (total: %d)", actualIndex, len(records))
		b.editMessage(chatID, messageID, "❌ Record not found.", nil)
		return
	}

	record := &records[actualIndex]

	log.Printf("[showRecordDetailByIndex] Found record: %s (type: %s)", record.Name, record.Type)

	proxiedStr := "❌ No"
	if record.Proxied {
		proxiedStr = "✅ Yes"
	}

	var text strings.Builder
	text.WriteString(fmt.Sprintf("*📋 Record Details*\n\n"))
	text.WriteString(fmt.Sprintf("*Zone:* `%s`\n", zoneName))
	text.WriteString(fmt.Sprintf("*Name:* `%s`\n", record.Name))
	text.WriteString(fmt.Sprintf("*Type:* `%s`\n", record.Type))
	text.WriteString(fmt.Sprintf("*Content:* `%s`\n", record.Content))
	text.WriteString(fmt.Sprintf("*TTL:* `%d`\n", record.TTL))
	text.WriteString(fmt.Sprintf("*Proxied:* %s\n", proxiedStr))

	keyboard := tgbotapi.NewInlineKeyboardMarkup(
		tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData("✏️ Edit", fmt.Sprintf("edit_record:%s:%s", zoneName, record.Name)),
			tgbotapi.NewInlineKeyboardButtonData("🗑️ Delete", fmt.Sprintf("delete_record:%s:%s", zoneName, record.Name)),
		),
		tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData("◀️ Back to List", fmt.Sprintf("refresh:zone:%s", zoneName)),
			tgbotapi.NewInlineKeyboardButtonData("🏠 Menu", "menu"),
		),
	)

	b.editMessage(chatID, messageID, text.String(), &keyboard)
}

// startDeleteRecord starts the delete record flow
func (b *Bot) startDeleteRecord(chatID int64, userID int64, messageID int, zoneName, recordName string) {
	keyboard := tgbotapi.NewInlineKeyboardMarkup(
		tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData("✅ Yes, Delete", fmt.Sprintf("confirm_delete:%s:%s", zoneName, recordName)),
		),
		tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData("❌ Cancel", fmt.Sprintf("cancel_delete:%s", zoneName)),
		),
	)

	b.editMessage(chatID, messageID, fmt.Sprintf(
		"*🗑️ Delete Record*\n\nAre you sure you want to delete:\n`%s` in zone `%s`?\n\n⚠️ This action cannot be undone!",
		recordName, zoneName,
	), &keyboard)
}

// handleConfirmDelete confirms and deletes the record
func (b *Bot) handleConfirmDelete(chatID int64, userID int64, messageID int, zoneName, recordName string) {
	ctx := context.Background()

	err := b.dnsUsecase.DeleteRecord(ctx, zoneName, recordName)
	if err != nil {
		if err == domain.ErrRecordNotFound {
			b.editMessage(chatID, messageID, fmt.Sprintf("❌ Record `%s` not found.", recordName), nil)
		} else {
			b.editMessage(chatID, messageID, fmt.Sprintf("❌ Error deleting record: %v", err), nil)
		}
		return
	}

	keyboard := tgbotapi.NewInlineKeyboardMarkup(
		tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData("🔍 Back to Records", fmt.Sprintf("select_zone_manage:%s", zoneName)),
			tgbotapi.NewInlineKeyboardButtonData("🏠 Main Menu", "menu"),
		),
	)

	b.editMessage(chatID, messageID, fmt.Sprintf("✅ Record `%s` deleted successfully!", recordName), &keyboard)
}

// startEditRecord starts the edit record flow
func (b *Bot) startEditRecord(chatID int64, userID int64, messageID int, zoneName, recordName string) {
	ctx := context.Background()

	record, err := b.dnsUsecase.GetRecord(ctx, zoneName, recordName)
	if err != nil {
		b.editMessage(chatID, messageID, fmt.Sprintf("❌ Error: %v", err), nil)
		return
	}

	b.stateManager.SetData(userID, "edit_zone", zoneName)
	b.stateManager.SetData(userID, "edit_record_name", recordName)
	b.stateManager.SetData(userID, "edit_record_id", record.ID)
	b.stateManager.SetData(userID, "edit_type", record.Type)
	b.stateManager.SetData(userID, "edit_current_content", record.Content)
	b.stateManager.SetData(userID, "edit_current_ttl", record.TTL)
	b.stateManager.SetData(userID, "edit_current_proxied", record.Proxied)

	keyboard := tgbotapi.NewInlineKeyboardMarkup(
		tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData("📝 Edit Content", "edit_content"),
			tgbotapi.NewInlineKeyboardButtonData("⏱️ Edit TTL", "edit_ttl"),
		),
		tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData("🔀 Toggle Proxy", "edit_proxied"),
			tgbotapi.NewInlineKeyboardButtonData("◀️ Back", fmt.Sprintf("refresh:zone:%s", zoneName)),
		),
	)

	proxied := "No"
	if record.Proxied {
		proxied = "Yes"
	}

	b.editMessage(chatID, messageID, fmt.Sprintf(
		"*✏️ Edit Record*\n\nName: `%s`\nType: `%s`\nContent: `%s`\nTTL: `%d`\nProxied: `%s`\n\nWhat would you like to edit?",
		record.Name, record.Type, record.Content, record.TTL, proxied,
	), &keyboard)
}

// handleBack handles back navigation
func (b *Bot) handleBack(chatID int64, userID int64, messageID int, parts []string) {
	if len(parts) < 2 {
		b.showMainMenu(chatID)
		return
	}

	switch parts[1] {
	case "create":
		b.startCreateRecord(chatID, userID)
	case "type":
		// Go back to zone selection (Step 1)
		b.startCreateRecord(chatID, userID)
	case "name":
		// Go back to type selection (Step 2)
		zone, _ := b.stateManager.GetData(userID, "zone")
		b.stateManager.SetStep(userID, StepSelectRecordType)
		types := []string{"A", "AAAA", "CNAME", "MX", "TXT", "NS", "SRV", "CAA"}
		var rows [][]tgbotapi.InlineKeyboardButton
		for i := 0; i < len(types); i += 4 {
			var row []tgbotapi.InlineKeyboardButton
			for j := i; j < i+4 && j < len(types); j++ {
				row = append(row, tgbotapi.NewInlineKeyboardButtonData(types[j], fmt.Sprintf("select_type:%s", types[j])))
			}
			rows = append(rows, row)
		}
		rows = append(rows, tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData("◀️ Back", "back:create"),
			tgbotapi.NewInlineKeyboardButtonData("❌ Cancel", "cancel_create"),
		))
		keyboard := tgbotapi.NewInlineKeyboardMarkup(rows...)
		b.editMessage(chatID, messageID, fmt.Sprintf("*➕ Create DNS Record*\n\nZone: `%s`\n\nStep 2/6: Select record type:", zone), &keyboard)
	case "content":
		// Go back to name input (Step 3)
		zone, _ := b.stateManager.GetData(userID, "zone")
		recordType, _ := b.stateManager.GetData(userID, "type")
		b.stateManager.SetStep(userID, StepInputRecordName)
		keyboard := tgbotapi.NewInlineKeyboardMarkup(
			tgbotapi.NewInlineKeyboardRow(
				tgbotapi.NewInlineKeyboardButtonData("◀️ Back", "back:type"),
				tgbotapi.NewInlineKeyboardButtonData("❌ Cancel", "cancel_create"),
			),
		)
		b.editMessage(chatID, messageID, fmt.Sprintf(
			"*➕ Create DNS Record*\n\nZone: `%s`\nType: `%s`\n\nStep 3/6: Enter the record name (e.g., `www`, `api`, `@` for root):",
			zone, recordType,
		), &keyboard)
	case "ttl":
		// Go back to content input (Step 4)
		zone, _ := b.stateManager.GetData(userID, "zone")
		recordType, _ := b.stateManager.GetData(userID, "type")
		name, _ := b.stateManager.GetData(userID, "name")
		b.stateManager.SetStep(userID, StepInputRecordContent)
		keyboard := tgbotapi.NewInlineKeyboardMarkup(
			tgbotapi.NewInlineKeyboardRow(
				tgbotapi.NewInlineKeyboardButtonData("◀️ Back", "back:name"),
				tgbotapi.NewInlineKeyboardButtonData("❌ Cancel", "cancel_create"),
			),
		)
		b.editMessage(chatID, messageID, fmt.Sprintf(
			"*➕ Create DNS Record*\n\nZone: `%s`\nType: `%s`\nName: `%s`\n\nStep 4/6: Enter the content (IP for A/AAAA, domain for CNAME, etc.):",
			zone, recordType, name,
		), &keyboard)
	default:
		b.showMainMenu(chatID)
	}
}

// handleBackToEdit returns to the edit menu from content/TTL editing
func (b *Bot) handleBackToEdit(chatID int64, userID int64, messageID int) {
	zoneName, _ := b.stateManager.GetData(userID, "edit_zone")
	recordName, _ := b.stateManager.GetData(userID, "edit_record_name")
	recordType, _ := b.stateManager.GetData(userID, "edit_type")
	currentContent, _ := b.stateManager.GetData(userID, "edit_current_content")
	currentTTL, _ := b.stateManager.GetData(userID, "edit_current_ttl")
	currentProxied, _ := b.stateManager.GetData(userID, "edit_current_proxied")

	keyboard := tgbotapi.NewInlineKeyboardMarkup(
		tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData("📝 Edit Content", "edit_content"),
			tgbotapi.NewInlineKeyboardButtonData("⏱️ Edit TTL", "edit_ttl"),
		),
		tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData("🔀 Toggle Proxy", "edit_proxied"),
			tgbotapi.NewInlineKeyboardButtonData("◀️ Back", fmt.Sprintf("refresh:zone:%s", zoneName)),
		),
	)

	proxiedStr := "❌ No"
	if currentProxied.(bool) {
		proxiedStr = "✅ Yes"
	}

	b.editMessage(chatID, messageID, fmt.Sprintf(
		"*✏️ Edit Record*\n\nName: `%s`\nType: `%s`\nContent: `%s`\nTTL: `%d`\nProxied: `%s`\n\nWhat would you like to edit?",
		recordName, recordType, currentContent, currentTTL, proxiedStr,
	), &keyboard)
}

// startEditContent starts the edit content flow
func (b *Bot) startEditContent(chatID int64, userID int64, messageID int) {
	b.stateManager.SetStep(userID, StepEditRecordContent)
	b.stateManager.SetData(userID, "edit_message_id", messageID)

	zoneName, _ := b.stateManager.GetData(userID, "edit_zone")
	recordName, _ := b.stateManager.GetData(userID, "edit_record_name")
	currentContent, _ := b.stateManager.GetData(userID, "edit_current_content")

	keyboard := tgbotapi.NewInlineKeyboardMarkup(
		tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData("◀️ Back", "back_to_edit"),
			tgbotapi.NewInlineKeyboardButtonData("❌ Cancel", "menu"),
		),
	)

	b.editMessage(chatID, messageID, fmt.Sprintf(
		"*✏️ Edit Content*\n\nZone: `%s`\nRecord: `%s`\nCurrent content: `%s`\n\nEnter the new content:",
		zoneName, recordName, currentContent,
	), &keyboard)
}

// startEditTTL starts the edit TTL flow
func (b *Bot) startEditTTL(chatID int64, userID int64, messageID int) {
	b.stateManager.SetStep(userID, StepEditRecordTTL)
	b.stateManager.SetData(userID, "edit_message_id", messageID)

	zoneName, _ := b.stateManager.GetData(userID, "edit_zone")
	recordName, _ := b.stateManager.GetData(userID, "edit_record_name")
	currentTTL, _ := b.stateManager.GetData(userID, "edit_current_ttl")

	keyboard := tgbotapi.NewInlineKeyboardMarkup(
		tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData("Auto (1)", "select_ttl:1"),
			tgbotapi.NewInlineKeyboardButtonData("300", "select_ttl:300"),
			tgbotapi.NewInlineKeyboardButtonData("600", "select_ttl:600"),
		),
		tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData("1800", "select_ttl:1800"),
			tgbotapi.NewInlineKeyboardButtonData("3600", "select_ttl:3600"),
			tgbotapi.NewInlineKeyboardButtonData("86400", "select_ttl:86400"),
		),
		tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData("◀️ Back", "back_to_edit"),
			tgbotapi.NewInlineKeyboardButtonData("❌ Cancel", "menu"),
		),
	)

	b.editMessage(chatID, messageID, fmt.Sprintf(
		"*✏️ Edit TTL*\n\nZone: `%s`\nRecord: `%s`\nCurrent TTL: `%v`\n\nSelect new TTL:",
		zoneName, recordName, currentTTL,
	), &keyboard)
}

// handleToggleProxied toggles the proxied status
func (b *Bot) handleToggleProxied(chatID int64, userID int64, messageID int) {
	ctx := context.Background()

	zoneName, _ := b.stateManager.GetData(userID, "edit_zone")
	recordName, _ := b.stateManager.GetData(userID, "edit_record_name")
	recordID, _ := b.stateManager.GetData(userID, "edit_record_id")
	recordType, _ := b.stateManager.GetData(userID, "edit_type")
	currentContent, _ := b.stateManager.GetData(userID, "edit_current_content")
	currentTTL, _ := b.stateManager.GetData(userID, "edit_current_ttl")
	currentProxied, _ := b.stateManager.GetData(userID, "edit_current_proxied")

	// Toggle proxied status
	newProxied := !currentProxied.(bool)

	input := usecase.UpdateRecordInput{
		ZoneName: zoneName.(string),
		RecordID: recordID.(string),
		Name:     recordName.(string),
		Type:     recordType.(string),
		Content:  currentContent.(string),
		TTL:      currentTTL.(int),
		Proxied:  newProxied,
	}

	_, err := b.dnsUsecase.UpdateRecord(ctx, input)
	if err != nil {
		b.editMessage(chatID, messageID, fmt.Sprintf("❌ Error updating record: %v", err), nil)
		return
	}

	// Update stored proxied value
	b.stateManager.SetData(userID, "edit_current_proxied", newProxied)

	proxiedStr := "❌ No"
	if newProxied {
		proxiedStr = "✅ Yes"
	}

	keyboard := tgbotapi.NewInlineKeyboardMarkup(
		tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData("📝 Edit Content", "edit_content"),
			tgbotapi.NewInlineKeyboardButtonData("⏱️ Edit TTL", "edit_ttl"),
		),
		tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData("🔀 Toggle Proxy", "edit_proxied"),
			tgbotapi.NewInlineKeyboardButtonData("◀️ Back", fmt.Sprintf("refresh:zone:%s", zoneName)),
		),
	)

	b.editMessage(chatID, messageID, fmt.Sprintf(
		"*✏️ Edit Record*\n\nName: `%s`\nType: `%s`\nContent: `%s`\nTTL: `%d`\nProxied: `%s`\n\nProxy status updated!",
		recordName, recordType, currentContent, currentTTL, proxiedStr,
	), &keyboard)

	// Clear edit state after successful toggle
	b.stateManager.ClearState(userID)
}

// handleEditRecordContent handles editing record content
func (b *Bot) handleEditRecordContent(chatID int64, userID int64, messageID int, content string) {
	ctx := context.Background()

	zoneName, _ := b.stateManager.GetData(userID, "edit_zone")
	recordName, _ := b.stateManager.GetData(userID, "edit_record_name")
	recordID, _ := b.stateManager.GetData(userID, "edit_record_id")
	recordType, _ := b.stateManager.GetData(userID, "edit_type")
	currentTTL, _ := b.stateManager.GetData(userID, "edit_current_ttl")
	currentProxied, _ := b.stateManager.GetData(userID, "edit_current_proxied")

	input := usecase.UpdateRecordInput{
		ZoneName: zoneName.(string),
		RecordID: recordID.(string),
		Name:     recordName.(string),
		Type:     recordType.(string),
		Content:  content,
		TTL:      currentTTL.(int),
		Proxied:  currentProxied.(bool),
	}

	_, err := b.dnsUsecase.UpdateRecord(ctx, input)
	if err != nil {
		b.editMessage(chatID, messageID, fmt.Sprintf("❌ Error updating record: %v", err), nil)
		return
	}

	// Update stored content
	b.stateManager.SetData(userID, "edit_current_content", content)

	keyboard := tgbotapi.NewInlineKeyboardMarkup(
		tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData("📝 Edit Content", "edit_content"),
			tgbotapi.NewInlineKeyboardButtonData("⏱️ Edit TTL", "edit_ttl"),
		),
		tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData("🔀 Toggle Proxy", "edit_proxied"),
			tgbotapi.NewInlineKeyboardButtonData("◀️ Back", fmt.Sprintf("refresh:zone:%s", zoneName)),
		),
	)

	proxiedStr := "❌ No"
	if currentProxied.(bool) {
		proxiedStr = "✅ Yes"
	}

	b.editMessage(chatID, messageID, fmt.Sprintf(
		"*✏️ Edit Record*\n\nName: `%s`\nType: `%s`\nContent: `%s`\nTTL: `%d`\nProxied: `%s`\n\n✅ Content updated!",
		recordName, recordType, content, currentTTL, proxiedStr,
	), &keyboard)

	// Clear edit state after successful update
	b.stateManager.ClearState(userID)
}

// handleEditRecordTTL handles editing record TTL
func (b *Bot) handleEditRecordTTL(chatID int64, userID int64, messageID int, ttlStr string) {
	ctx := context.Background()

	ttl, err := strconv.Atoi(ttlStr)
	if err != nil {
		b.editMessage(chatID, messageID, "❌ Invalid TTL value", nil)
		return
	}

	zoneName, _ := b.stateManager.GetData(userID, "edit_zone")
	recordName, _ := b.stateManager.GetData(userID, "edit_record_name")
	recordID, _ := b.stateManager.GetData(userID, "edit_record_id")
	recordType, _ := b.stateManager.GetData(userID, "edit_type")
	currentContent, _ := b.stateManager.GetData(userID, "edit_current_content")
	currentProxied, _ := b.stateManager.GetData(userID, "edit_current_proxied")

	input := usecase.UpdateRecordInput{
		ZoneName: zoneName.(string),
		RecordID: recordID.(string),
		Name:     recordName.(string),
		Type:     recordType.(string),
		Content:  currentContent.(string),
		TTL:      ttl,
		Proxied:  currentProxied.(bool),
	}

	_, err = b.dnsUsecase.UpdateRecord(ctx, input)
	if err != nil {
		b.editMessage(chatID, messageID, fmt.Sprintf("❌ Error updating record: %v", err), nil)
		return
	}

	// Update stored TTL
	b.stateManager.SetData(userID, "edit_current_ttl", ttl)

	keyboard := tgbotapi.NewInlineKeyboardMarkup(
		tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData("📝 Edit Content", "edit_content"),
			tgbotapi.NewInlineKeyboardButtonData("⏱️ Edit TTL", "edit_ttl"),
		),
		tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData("🔀 Toggle Proxy", "edit_proxied"),
			tgbotapi.NewInlineKeyboardButtonData("◀️ Back", fmt.Sprintf("refresh:zone:%s", zoneName)),
		),
	)

	proxiedStr := "❌ No"
	if currentProxied.(bool) {
		proxiedStr = "✅ Yes"
	}

	b.editMessage(chatID, messageID, fmt.Sprintf(
		"*✏️ Edit Record*\n\nName: `%s`\nType: `%s`\nContent: `%s`\nTTL: `%d`\nProxied: `%s`\n\n✅ TTL updated!",
		recordName, recordType, currentContent, ttl, proxiedStr,
	), &keyboard)

	// Clear edit state after successful update
	b.stateManager.ClearState(userID)
}

// handleInputRecordTTL handles custom TTL input (not from button)
func (b *Bot) handleInputRecordTTL(chatID int64, userID int64, ttlStr string) {
	ttl, err := strconv.Atoi(ttlStr)
	if err != nil {
		b.sendMessage(chatID, "❌ Invalid TTL. Please enter a number.")
		return
	}

	b.stateManager.SetData(userID, "ttl", ttl)
	b.stateManager.SetStep(userID, StepInputRecordProxied)

	keyboard := tgbotapi.NewInlineKeyboardMarkup(
		tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData("✅ Yes (Proxied)", "proxied:true"),
			tgbotapi.NewInlineKeyboardButtonData("❌ No (DNS Only)", "proxied:false"),
		),
		tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData("❌ Cancel", "cancel_create"),
		),
	)

	zone, _ := b.stateManager.GetData(userID, "zone")
	recordType, _ := b.stateManager.GetData(userID, "type")
	name, _ := b.stateManager.GetData(userID, "name")
	content, _ := b.stateManager.GetData(userID, "content")
	b.sendMessageWithKeyboard(chatID, fmt.Sprintf(
		"*➕ Create DNS Record*\n\nZone: `%s`\nType: `%s`\nName: `%s`\nContent: `%s`\nTTL: `%d`\n\nStep 6/6: Enable Cloudflare proxy?",
		zone, recordType, name, content, ttl,
	), keyboard)
}

// showAPIKeysMenu shows the API key management menu
func (b *Bot) showAPIKeysMenu(chatID int64, userID int64) {
	keyboard := tgbotapi.NewInlineKeyboardMarkup(
		tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData("➕ Generate New Key", "apikey_generate"),
		),
		tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData("📋 List Keys", "apikey_list"),
		),
		tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData("🗑️ Delete Key", "apikey_delete"),
		),
		tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData("◀️ Back to MCP HTTP Server", "mcphttp"),
		),
	)

	b.sendMessageWithKeyboard(chatID, "*🔑 MCP API Key Management*\n\nManage API keys for MCP server access:", keyboard)
}

// handleGenerateAPIKey generates a new API key
func (b *Bot) handleGenerateAPIKey(chatID int64, userID int64) {
	if b.apiKeyStorage == nil {
		b.sendMessage(chatID, "❌ API key storage not configured.")
		return
	}

	key, err := b.generateRandomKey()
	if err != nil {
		b.sendMessage(chatID, fmt.Sprintf("❌ Error generating key: %v", err))
		return
	}

	if err := b.apiKeyStorage.AddAPIKey(key); err != nil {
		b.sendMessage(chatID, fmt.Sprintf("❌ Error saving key: %v", err))
		return
	}

	keyboard := tgbotapi.NewInlineKeyboardMarkup(
		tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData("➕ Generate Another", "apikey_generate"),
		),
		tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData("📋 List All Keys", "apikey_list"),
		),
		tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData("🏠 Main Menu", "menu"),
		),
	)

	b.sendMessageWithKeyboard(chatID, fmt.Sprintf(
		"✅ *API Key Generated Successfully!*\n\n"+
			"🔑 *Key:* `%s`\n\n"+
			"⚠️ *Important:* This key will only be shown once. Please copy it now!\n\n"+
			"This key can be used to authenticate with the MCP HTTP server.",
		key,
	), keyboard)
}

// handleListAPIKeys lists all API keys (masked)
func (b *Bot) handleListAPIKeys(chatID int64, userID int64) {
	if b.apiKeyStorage == nil {
		b.sendMessage(chatID, "❌ API key storage not configured.")
		return
	}

	keys, err := b.apiKeyStorage.GetAPIKeys()
	if err != nil {
		b.sendMessage(chatID, fmt.Sprintf("❌ Error retrieving keys: %v", err))
		return
	}

	if len(keys) == 0 {
		keyboard := tgbotapi.NewInlineKeyboardMarkup(
			tgbotapi.NewInlineKeyboardRow(
				tgbotapi.NewInlineKeyboardButtonData("➕ Generate Key", "apikey_generate"),
			),
			tgbotapi.NewInlineKeyboardRow(
				tgbotapi.NewInlineKeyboardButtonData("◀️ Back", "apikeys"),
			),
		)
		b.sendMessageWithKeyboard(chatID, "📭 *No API keys found.*\n\nGenerate a new key to get started.", keyboard)
		return
	}

	var text strings.Builder
	text.WriteString("*📋 API Keys:*\n\n")
	for i, key := range keys {
		maskedKey := b.maskAPIKey(key)
		text.WriteString(fmt.Sprintf("%d. `%s`\n", i+1, maskedKey))
	}
	text.WriteString(fmt.Sprintf("\n_Total: %d key(s)_", len(keys)))

	keyboard := tgbotapi.NewInlineKeyboardMarkup(
		tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData("➕ Generate New", "apikey_generate"),
			tgbotapi.NewInlineKeyboardButtonData("🗑️ Delete Key", "apikey_delete"),
		),
		tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData("◀️ Back", "apikeys"),
			tgbotapi.NewInlineKeyboardButtonData("🏠 Main Menu", "menu"),
		),
	)

	b.sendMessageWithKeyboard(chatID, text.String(), keyboard)
}

// startDeleteAPIKey starts the delete API key flow
func (b *Bot) startDeleteAPIKey(chatID int64, userID int64) {
	if b.apiKeyStorage == nil {
		b.sendMessage(chatID, "❌ API key storage not configured.")
		return
	}

	keys, err := b.apiKeyStorage.GetAPIKeys()
	if err != nil {
		b.sendMessage(chatID, fmt.Sprintf("❌ Error retrieving keys: %v", err))
		return
	}

	if len(keys) == 0 {
		keyboard := tgbotapi.NewInlineKeyboardMarkup(
			tgbotapi.NewInlineKeyboardRow(
				tgbotapi.NewInlineKeyboardButtonData("➕ Generate Key", "apikey_generate"),
			),
			tgbotapi.NewInlineKeyboardRow(
				tgbotapi.NewInlineKeyboardButtonData("◀️ Back", "apikeys"),
			),
		)
		b.sendMessageWithKeyboard(chatID, "📭 *No API keys to delete.*", keyboard)
		return
	}

	var rows [][]tgbotapi.InlineKeyboardButton
	for i, key := range keys {
		maskedKey := b.maskAPIKey(key)
		// Store the full key in callback data for deletion
		callbackData := fmt.Sprintf("confirm_delete_apikey:%s", key)
		// Truncate if too long for callback data (64 bytes max)
		if len(callbackData) > 64 {
			callbackData = callbackData[:64]
		}
		rows = append(rows, tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData(fmt.Sprintf("%d. %s", i+1, maskedKey), callbackData),
		))
	}

	rows = append(rows, tgbotapi.NewInlineKeyboardRow(
		tgbotapi.NewInlineKeyboardButtonData("◀️ Cancel", "apikeys"),
	))

	keyboard := tgbotapi.NewInlineKeyboardMarkup(rows...)
	b.sendMessageWithKeyboard(chatID, "*🗑️ Delete API Key*\n\nSelect a key to delete:", keyboard)
}

// generateRandomKey generates a random 32-byte hex encoded API key
func (b *Bot) generateRandomKey() (string, error) {
	bytes := make([]byte, 32)
	if _, err := rand.Read(bytes); err != nil {
		return "", err
	}
	return hex.EncodeToString(bytes), nil
}

// maskAPIKey masks an API key showing only first 8 and last 8 characters
func (b *Bot) maskAPIKey(key string) string {
	if len(key) <= 16 {
		return "****"
	}
	return key[:8] + "..." + key[len(key)-8:]
}

// handleConfirmDeleteAPIKey confirms and deletes an API key
func (b *Bot) handleConfirmDeleteAPIKey(chatID int64, userID int64, key string) {
	if b.apiKeyStorage == nil {
		b.sendMessage(chatID, "❌ API key storage not configured.")
		return
	}

	if err := b.apiKeyStorage.RemoveAPIKey(key); err != nil {
		b.sendMessage(chatID, fmt.Sprintf("❌ Error deleting key: %v", err))
		return
	}

	keyboard := tgbotapi.NewInlineKeyboardMarkup(
		tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData("📋 List Keys", "apikey_list"),
			tgbotapi.NewInlineKeyboardButtonData("🏠 Main Menu", "menu"),
		),
	)

	b.sendMessageWithKeyboard(chatID, "✅ *API Key deleted successfully!*", keyboard)
}

// showMCPHTTPMenu shows the MCP HTTP server management menu
func (b *Bot) showMCPHTTPMenu(chatID int64, userID int64) {
	if b.mcpHTTPController == nil {
		b.sendMessage(chatID, "❌ MCP HTTP server controller not configured.")
		return
	}

	status := "🔴 Stopped"
	if b.mcpHTTPController.IsRunning() {
		status = "🟢 Running"
	}
	port := b.mcpHTTPController.GetPort()

	var keyboard tgbotapi.InlineKeyboardMarkup
	if b.mcpHTTPController.IsRunning() {
		keyboard = tgbotapi.NewInlineKeyboardMarkup(
			tgbotapi.NewInlineKeyboardRow(
				tgbotapi.NewInlineKeyboardButtonData("🛑 Stop Server", "mcphttp_stop"),
			),
			tgbotapi.NewInlineKeyboardRow(
				tgbotapi.NewInlineKeyboardButtonData("🔢 Change Port", "mcphttp_port"),
				tgbotapi.NewInlineKeyboardButtonData("📊 Status", "mcphttp_status"),
			),
			tgbotapi.NewInlineKeyboardRow(
				tgbotapi.NewInlineKeyboardButtonData("🔑 MCP API Keys", "apikeys"),
			),
			tgbotapi.NewInlineKeyboardRow(
				tgbotapi.NewInlineKeyboardButtonData("◀️ Back to Menu", "menu"),
			),
		)
	} else {
		keyboard = tgbotapi.NewInlineKeyboardMarkup(
			tgbotapi.NewInlineKeyboardRow(
				tgbotapi.NewInlineKeyboardButtonData("▶️ Start Server", "mcphttp_start"),
			),
			tgbotapi.NewInlineKeyboardRow(
				tgbotapi.NewInlineKeyboardButtonData("🔢 Change Port", "mcphttp_port"),
				tgbotapi.NewInlineKeyboardButtonData("📊 Status", "mcphttp_status"),
			),
			tgbotapi.NewInlineKeyboardRow(
				tgbotapi.NewInlineKeyboardButtonData("🔑 MCP API Keys", "apikeys"),
			),
			tgbotapi.NewInlineKeyboardRow(
				tgbotapi.NewInlineKeyboardButtonData("◀️ Back to Menu", "menu"),
			),
		)
	}

	b.sendMessageWithKeyboard(chatID, fmt.Sprintf(
		"*🌐 MCP HTTP Server Management*\n\n"+
			"Status: %s\n"+
			"Port: `%s`\n\n"+
			"What would you like to do?",
		status, port,
	), keyboard)
}

// handleMCPHTTPStart starts the MCP HTTP server
func (b *Bot) handleMCPHTTPStart(chatID int64, userID int64) {
	if b.mcpHTTPController == nil {
		b.sendMessage(chatID, "❌ MCP HTTP server controller not configured.")
		return
	}

	if b.mcpHTTPController.IsRunning() {
		b.sendMessage(chatID, "⚠️ MCP HTTP server is already running.")
		return
	}

	if err := b.mcpHTTPController.Start(); err != nil {
		b.sendMessage(chatID, fmt.Sprintf("❌ Error starting server: %v", err))
		return
	}

	keyboard := tgbotapi.NewInlineKeyboardMarkup(
		tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData("🛑 Stop Server", "mcphttp_stop"),
			tgbotapi.NewInlineKeyboardButtonData("📊 Status", "mcphttp_status"),
		),
		tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData("🏠 Main Menu", "menu"),
		),
	)

	b.sendMessageWithKeyboard(chatID, fmt.Sprintf(
		"✅ *MCP HTTP Server Started!*\n\n"+
			"Port: `%s`\n"+
			"Status: 🟢 Running",
		b.mcpHTTPController.GetPort(),
	), keyboard)
}

// handleMCPHTTPStop stops the MCP HTTP server
func (b *Bot) handleMCPHTTPStop(chatID int64, userID int64) {
	if b.mcpHTTPController == nil {
		b.sendMessage(chatID, "❌ MCP HTTP server controller not configured.")
		return
	}

	if !b.mcpHTTPController.IsRunning() {
		b.sendMessage(chatID, "⚠️ MCP HTTP server is already stopped.")
		return
	}

	if err := b.mcpHTTPController.Stop(); err != nil {
		b.sendMessage(chatID, fmt.Sprintf("❌ Error stopping server: %v", err))
		return
	}

	keyboard := tgbotapi.NewInlineKeyboardMarkup(
		tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData("▶️ Start Server", "mcphttp_start"),
			tgbotapi.NewInlineKeyboardButtonData("📊 Status", "mcphttp_status"),
		),
		tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData("🏠 Main Menu", "menu"),
		),
	)

	b.sendMessageWithKeyboard(chatID, "✅ *MCP HTTP Server Stopped!*\n\nStatus: 🔴 Stopped", keyboard)
}

// handleMCPHTTPStatus shows the MCP HTTP server status
func (b *Bot) handleMCPHTTPStatus(chatID int64, userID int64) {
	if b.mcpHTTPController == nil {
		b.sendMessage(chatID, "❌ MCP HTTP server controller not configured.")
		return
	}

	status := "🔴 Stopped"
	if b.mcpHTTPController.IsRunning() {
		status = "🟢 Running"
	}
	port := b.mcpHTTPController.GetPort()

	keyboard := tgbotapi.NewInlineKeyboardMarkup(
		tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData("🔄 Refresh", "mcphttp_status"),
			tgbotapi.NewInlineKeyboardButtonData("◀️ Back", "mcphttp"),
		),
		tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData("🏠 Main Menu", "menu"),
		),
	)

	b.sendMessageWithKeyboard(chatID, fmt.Sprintf(
		"*📊 MCP HTTP Server Status*\n\n"+
			"Status: %s\n"+
			"Port: `%s`",
		status, port,
	), keyboard)
}

// startMCPHTTPPortChange starts the port change flow
func (b *Bot) startMCPHTTPPortChange(chatID int64, userID int64, messageID int) {
	if b.configStorage == nil {
		b.editMessage(chatID, messageID, "❌ Configuration storage not configured.", nil)
		return
	}

	currentPort, _ := b.configStorage.GetMCPHTTPPort()
	if currentPort == "" {
		currentPort = "8875"
	}

	b.stateManager.SetStep(userID, StepInputMCPHTTPPort)
	b.stateManager.SetData(userID, "mcphttp_message_id", messageID)

	var text string
	if b.mcpHTTPController.IsRunning() {
		text = fmt.Sprintf(
			"*🔢 Change MCP HTTP Server Port*\n\n"+
				"⚠️ *Server is currently running!*\n\n"+
				"Current port: `%s`\n\n"+
				"Enter the new port number (e.g., `8875`, `8080`, `3000`):\n\n"+
				"_Note: The server will be stopped and restarted with the new port._",
			currentPort,
		)
	} else {
		text = fmt.Sprintf(
			"*🔢 Change MCP HTTP Server Port*\n\n"+
				"Current port: `%s`\n\n"+
				"Enter the new port number (e.g., `8875`, `8080`, `3000`):",
			currentPort,
		)
	}

	keyboard := tgbotapi.NewInlineKeyboardMarkup(
		tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData("❌ Cancel", "mcphttp"),
		),
	)

	b.editMessage(chatID, messageID, text, &keyboard)
}

// handleMCPHTTPPortChange handles the port change input
func (b *Bot) handleMCPHTTPPortChange(chatID int64, userID int64, portStr string) {
	if b.configStorage == nil || b.mcpHTTPController == nil {
		b.sendMessage(chatID, "❌ Configuration not available.")
		return
	}

	// Validate port
	port, err := strconv.Atoi(portStr)
	if err != nil || port < 1 || port > 65535 {
		b.sendMessage(chatID, "❌ Invalid port number. Please enter a number between 1 and 65535.")
		return
	}

	// Store the new port in state for confirmation
	b.stateManager.SetData(userID, "new_port", portStr)

	// Get the message ID from state
	msgID, _ := b.stateManager.GetData(userID, "mcphttp_message_id")
	messageID := msgID.(int)

	wasRunning := b.mcpHTTPController.IsRunning()

	if wasRunning {
		// Show confirmation dialog
		keyboard := tgbotapi.NewInlineKeyboardMarkup(
			tgbotapi.NewInlineKeyboardRow(
				tgbotapi.NewInlineKeyboardButtonData("✅ Confirm", fmt.Sprintf("confirm_port_change:%s", portStr)),
			),
			tgbotapi.NewInlineKeyboardRow(
				tgbotapi.NewInlineKeyboardButtonData("❌ Cancel", "mcphttp"),
			),
		)

		b.editMessage(chatID, messageID, fmt.Sprintf(
			"*🔢 Confirm Port Change*\n\n"+
				"⚠️ *Server is currently running!*\n\n"+
				"New port: `%s`\n\n"+
				"The server will be:\n"+
				"1. Stopped\n"+
				"2. Port changed to `%s`\n"+
				"3. Restarted with new port\n\n"+
				"Confirm this action?",
			portStr, portStr,
		), &keyboard)
	} else {
		// Server not running, apply change directly
		b.applyPortChange(chatID, userID, messageID, portStr, false)
	}
}

// handleConfirmPortChange handles the port change confirmation
func (b *Bot) handleConfirmPortChange(chatID int64, userID int64, messageID int, portStr string) {
	b.applyPortChange(chatID, userID, messageID, portStr, true)
}

// applyPortChange applies the port change
func (b *Bot) applyPortChange(chatID int64, userID int64, messageID int, portStr string, wasRunning bool) {
	if wasRunning {
		// Stop server if running
		if err := b.mcpHTTPController.Stop(); err != nil {
			b.editMessage(chatID, messageID, fmt.Sprintf("❌ Error stopping server: %v", err), nil)
			return
		}
	}

	// Save new port
	if err := b.configStorage.SetMCPHTTPPort(portStr); err != nil {
		b.editMessage(chatID, messageID, fmt.Sprintf("❌ Error saving port: %v", err), nil)
		return
	}

	// Restart server if it was running
	if wasRunning {
		if err := b.mcpHTTPController.Start(); err != nil {
			b.editMessage(chatID, messageID, fmt.Sprintf("❌ Error restarting server with new port: %v", err), nil)
			return
		}
	}

	keyboard := tgbotapi.NewInlineKeyboardMarkup(
		tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData("🔄 Change Again", "mcphttp_port"),
			tgbotapi.NewInlineKeyboardButtonData("📊 Status", "mcphttp_status"),
		),
		tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData("🏠 Main Menu", "menu"),
		),
	)

	statusMsg := ""
	if wasRunning {
		statusMsg = "Server restarted with new port."
	} else {
		statusMsg = "Port saved. Start the server to use the new port."
	}

	b.editMessage(chatID, messageID, fmt.Sprintf(
		"✅ *Port Changed!*\n\n"+
			"New port: `%s`\n"+
			"%s",
		portStr, statusMsg,
	), &keyboard)
	b.stateManager.ClearState(userID)
}
