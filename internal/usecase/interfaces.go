package usecase

import (
	"context"

	"cf-dns-bot/internal/domain"
)

// DNSUsecase defines the interface for DNS management use cases
// This interface is handler-agnostic and can be used by Telegram bot, REST API, or any other handler
type DNSUsecase interface {
	// Zone operations
	ListZones(ctx context.Context) ([]domain.Zone, error)

	// Record operations
	ListRecords(ctx context.Context, zoneName string) ([]domain.DNSRecord, error)
	GetRecord(ctx context.Context, zoneName, recordName string) (*domain.DNSRecord, error)
	CreateRecord(ctx context.Context, input CreateRecordInput) (*domain.DNSRecord, error)
	UpdateRecord(ctx context.Context, input UpdateRecordInput) (*domain.DNSRecord, error)
	DeleteRecord(ctx context.Context, zoneName, recordName string) error
	UpsertRecord(ctx context.Context, input CreateRecordInput) (*domain.DNSRecord, error)
}

// CreateRecordInput represents input for creating a DNS record
type CreateRecordInput struct {
	ZoneName string
	Name     string
	Type     string
	Content  string
	TTL      int
	Proxied  bool
	Priority *uint16
}

// UpdateRecordInput represents input for updating a DNS record
type UpdateRecordInput struct {
	ZoneName string
	RecordID string
	Name     string
	Type     string
	Content  string
	TTL      int
	Proxied  bool
	Priority *uint16
}
