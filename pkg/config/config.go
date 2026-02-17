package config

import (
	"fmt"
	"os"
	"strconv"
	"strings"

	"github.com/joho/godotenv"
)

// Config holds all application configuration
type Config struct {
	// Telegram
	TelegramBotToken string
	AllowedUsers     []int64

	// Cloudflare
	CloudflareAPIToken string
	CloudflareAPIKey   string
	CloudflareEmail    string

	// Storage
	DataDir string
}

// Load loads configuration from environment variables
func Load() (*Config, error) {
	// Load .env file if exists
	_ = godotenv.Load()

	cfg := &Config{
		TelegramBotToken:   getEnv("TELEGRAM_BOT_TOKEN", ""),
		CloudflareAPIToken: getEnv("CLOUDFLARE_API_TOKEN", ""),
		CloudflareAPIKey:   getEnv("CLOUDFLARE_API_KEY", ""),
		CloudflareEmail:    getEnv("CLOUDFLARE_EMAIL", ""),
		DataDir:            getEnv("DATA_DIR", "./data"),
	}

	// Parse allowed users
	if usersStr := getEnv("TELEGRAM_ALLOWED_USERS", ""); usersStr != "" {
		userIDs := strings.Split(usersStr, ",")
		for _, idStr := range userIDs {
			idStr = strings.TrimSpace(idStr)
			if idStr == "" {
				continue
			}
			id, err := strconv.ParseInt(idStr, 10, 64)
			if err != nil {
				return nil, fmt.Errorf("invalid user ID in TELEGRAM_ALLOWED_USERS: %s", idStr)
			}
			cfg.AllowedUsers = append(cfg.AllowedUsers, id)
		}
	}

	// Validate
	if err := cfg.Validate(); err != nil {
		return nil, err
	}

	return cfg, nil
}

// Validate validates the configuration
func (c *Config) Validate() error {
	if c.TelegramBotToken == "" {
		return fmt.Errorf("TELEGRAM_BOT_TOKEN is required")
	}

	if c.CloudflareAPIToken == "" {
		if c.CloudflareAPIKey == "" || c.CloudflareEmail == "" {
			return fmt.Errorf("either CLOUDFLARE_API_TOKEN or both CLOUDFLARE_API_KEY and CLOUDFLARE_EMAIL are required")
		}
	}

	return nil
}

// UseAPIToken returns true if API token should be used
func (c *Config) UseAPIToken() bool {
	return c.CloudflareAPIToken != ""
}

func getEnv(key, defaultValue string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return defaultValue
}
