package telegram

import (
	"context"
	"fmt"
	"log"
	"strconv"
	"strings"

	"cf-dns-bot/internal/domain"
	"cf-dns-bot/internal/usecase"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
)

// Bot implements handler.BotHandler for Telegram with button-based UI
type Bot struct {
	dnsUsecase   usecase.DNSUsecase
	bot          *tgbotapi.BotAPI
	token        string
	allowedIDs   map[int64]bool
	stateManager *StateManager
}

// NewBot creates a new Telegram bot handler
func NewBot(dnsUsecase usecase.DNSUsecase, token string, allowedUsers []int64) *Bot {
	allowedIDs := make(map[int64]bool)
	for _, id := range allowedUsers {
		allowedIDs[id] = true
	}

	return &Bot{
		dnsUsecase:   dnsUsecase,
		token:        token,
		allowedIDs:   allowedIDs,
		stateManager: NewStateManager(),
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

	u := tgbotapi.NewUpdate(0)
	u.Timeout = 60

	updates := bot.GetUpdatesChan(u)

	for update := range updates {
		if update.Message != nil {
			if !b.isAuthorized(update.Message.From.ID) {
				b.sendMessage(update.Message.Chat.ID, "⛔ You are not authorized to use this bot.")
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
		b.handleInputRecordName(chatID, userID, msg.Text)
	case StepInputRecordContent:
		b.handleInputRecordContent(chatID, userID, msg.Text)
	case StepInputRecordTTL:
		b.handleInputRecordTTL(chatID, userID, msg.Text)
	case StepEditRecordContent:
		b.handleEditRecordContent(chatID, userID, msg.Text)
	case StepEditRecordTTL:
		b.handleEditRecordTTL(chatID, userID, msg.Text)
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
	}
}

// showMainMenu shows the main menu
func (b *Bot) showMainMenu(chatID int64) {
	keyboard := tgbotapi.NewInlineKeyboardMarkup(
		tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData("📋 List Zones", "zones"),
		),
		tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData("➕ Create Record", "create"),
		),
		tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData("🔍 Manage Records", "manage"),
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
}

// handleInputRecordName handles record name input
func (b *Bot) handleInputRecordName(chatID int64, userID int64, name string) {
	b.stateManager.SetData(userID, "name", name)
	b.stateManager.SetStep(userID, StepInputRecordContent)

	keyboard := tgbotapi.NewInlineKeyboardMarkup(
		tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData("❌ Cancel", "cancel_create"),
		),
	)

	zone, _ := b.stateManager.GetData(userID, "zone")
	recordType, _ := b.stateManager.GetData(userID, "type")
	b.sendMessageWithKeyboard(chatID, fmt.Sprintf(
		"*➕ Create DNS Record*\n\nZone: `%s`\nType: `%s`\nName: `%s`\n\nStep 4/6: Enter the content (IP for A/AAAA, domain for CNAME, etc.):",
		zone, recordType, name,
	), keyboard)
}

// handleInputRecordContent handles record content input
func (b *Bot) handleInputRecordContent(chatID int64, userID int64, content string) {
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
			tgbotapi.NewInlineKeyboardButtonData("❌ Cancel", "cancel_create"),
		),
	)

	zone, _ := b.stateManager.GetData(userID, "zone")
	recordType, _ := b.stateManager.GetData(userID, "type")
	name, _ := b.stateManager.GetData(userID, "name")
	b.sendMessageWithKeyboard(chatID, fmt.Sprintf(
		"*➕ Create DNS Record*\n\nZone: `%s`\nType: `%s`\nName: `%s`\nContent: `%s`\n\nStep 5/6: Select TTL:",
		zone, recordType, name, content,
	), keyboard)
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
				tgbotapi.NewInlineKeyboardButtonData("➕ Create Record", "create"),
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
		tgbotapi.NewInlineKeyboardButtonData("➕ Create", "create"),
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
		b.startCreateRecord(chatID, userID)
	default:
		b.showMainMenu(chatID)
	}
}

// startEditContent starts the edit content flow
func (b *Bot) startEditContent(chatID int64, userID int64, messageID int) {
	b.stateManager.SetStep(userID, StepEditRecordContent)

	zoneName, _ := b.stateManager.GetData(userID, "edit_zone")
	recordName, _ := b.stateManager.GetData(userID, "edit_record_name")
	currentContent, _ := b.stateManager.GetData(userID, "edit_current_content")

	keyboard := tgbotapi.NewInlineKeyboardMarkup(
		tgbotapi.NewInlineKeyboardRow(
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
}

// handleEditRecordContent handles editing record content
func (b *Bot) handleEditRecordContent(chatID int64, userID int64, content string) {
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
		b.sendMessage(chatID, fmt.Sprintf("❌ Error updating record: %v", err))
		return
	}

	// Update stored content
	b.stateManager.SetData(userID, "edit_current_content", content)

	keyboard := tgbotapi.NewInlineKeyboardMarkup(
		tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData("🔍 Back to Records", fmt.Sprintf("select_zone_manage:%s", zoneName)),
			tgbotapi.NewInlineKeyboardButtonData("🏠 Main Menu", "menu"),
		),
	)

	b.sendMessageWithKeyboard(chatID, fmt.Sprintf(
		"✅ *Content Updated!*\n\nRecord: `%s`\nNew content: `%s`",
		recordName, content,
	), keyboard)
	b.stateManager.ClearState(userID)
}

// handleEditRecordTTL handles editing record TTL
func (b *Bot) handleEditRecordTTL(chatID int64, userID int64, ttlStr string) {
	ctx := context.Background()

	ttl, err := strconv.Atoi(ttlStr)
	if err != nil {
		b.sendMessage(chatID, "❌ Invalid TTL value")
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
		b.sendMessage(chatID, fmt.Sprintf("❌ Error updating record: %v", err))
		return
	}

	// Update stored TTL
	b.stateManager.SetData(userID, "edit_current_ttl", ttl)

	keyboard := tgbotapi.NewInlineKeyboardMarkup(
		tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData("🔍 Back to Records", fmt.Sprintf("select_zone_manage:%s", zoneName)),
			tgbotapi.NewInlineKeyboardButtonData("🏠 Main Menu", "menu"),
		),
	)

	b.sendMessageWithKeyboard(chatID, fmt.Sprintf(
		"✅ *TTL Updated!*\n\nRecord: `%s`\nNew TTL: `%d`",
		recordName, ttl,
	), keyboard)
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
