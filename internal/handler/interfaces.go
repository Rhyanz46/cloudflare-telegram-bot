package handler

// BotHandler defines the interface for bot handlers
// This allows swapping between different bot implementations (Telegram, Discord, etc.)
type BotHandler interface {
	Start() error
	Stop() error
}
