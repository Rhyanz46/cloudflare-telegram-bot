package repository

import (
	"context"

	"cf-dns-bot/internal/domain"
)

// DNSRepository defines the interface for DNS record storage operations
type DNSRepository interface {
	// ListRecords returns all DNS records for a zone with optional filters
	ListRecords(ctx context.Context, zoneID string, filter domain.RecordFilter) ([]domain.DNSRecord, error)

	// GetRecord returns a specific DNS record by ID
	GetRecord(ctx context.Context, zoneID, recordID string) (*domain.DNSRecord, error)

	// CreateRecord creates a new DNS record
	CreateRecord(ctx context.Context, zoneID string, record *domain.DNSRecord) (*domain.DNSRecord, error)

	// UpdateRecord updates an existing DNS record
	UpdateRecord(ctx context.Context, zoneID, recordID string, record *domain.DNSRecord) (*domain.DNSRecord, error)

	// DeleteRecord deletes a DNS record
	DeleteRecord(ctx context.Context, zoneID, recordID string) error

	// FindByName finds a DNS record by name within a zone
	FindByName(ctx context.Context, zoneID, name string) (*domain.DNSRecord, error)
}

// ZoneRepository defines the interface for zone operations
type ZoneRepository interface {
	// ListZones returns all accessible zones
	ListZones(ctx context.Context) ([]domain.Zone, error)

	// GetZoneByName returns a zone by its name
	GetZoneByName(ctx context.Context, name string) (*domain.Zone, error)

	// GetZone returns a zone by its ID
	GetZone(ctx context.Context, zoneID string) (*domain.Zone, error)
}
