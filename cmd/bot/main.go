package main

import (
	"context"
	"log"
	"os"
	"os/signal"
	"syscall"

	"cf-dns-bot/external_resource/cloudflare"
	"cf-dns-bot/internal/handler"
	"cf-dns-bot/internal/handler/telegram"
	"cf-dns-bot/internal/repository"
	"cf-dns-bot/internal/usecase"
	"cf-dns-bot/pkg/config"
	"cf-dns-bot/pkg/storage"
)

func main() {
	// Load configuration
	cfg, err := config.Load()
	if err != nil {
		log.Fatalf("Failed to load config: %v", err)
	}

	// Initialize storage
	configStorage := storage.NewJSONStorage(cfg.DataDir)

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

	// Initialize Telegram bot handler
	botHandler := telegram.NewBot(dnsUsecase, cfg.TelegramBotToken, storageConfig.AllowedUsers)

	// Start bot in a goroutine
	go func() {
		log.Println("Starting Telegram bot...")
		if err := botHandler.Start(); err != nil {
			log.Fatalf("Bot error: %v", err)
		}
	}()

	// Wait for interrupt signal
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)

	log.Println("Bot is running. Press Ctrl+C to stop.")
	<-sigChan

	log.Println("Shutting down...")
	if err := botHandler.Stop(); err != nil {
		log.Printf("Error stopping bot: %v", err)
	}

	log.Println("Bot stopped.")
}

// ensure Bot implements handler.BotHandler
var _ handler.BotHandler = (*telegram.Bot)(nil)
