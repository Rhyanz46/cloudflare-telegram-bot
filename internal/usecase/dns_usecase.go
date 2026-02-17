package usecase

import (
	"context"
	"fmt"
	"log"
	"strings"

	"cf-dns-bot/internal/domain"
	"cf-dns-bot/internal/repository"
	"cf-dns-bot/pkg/storage"
)

// dnsUsecase implements DNSUsecase interface
type dnsUsecase struct {
	zoneRepo       repository.ZoneRepository
	dnsRepo        repository.DNSRepository
	configStorage  storage.ConfigStorage
}

// NewDNSUsecase creates a new DNS usecase
func NewDNSUsecase(
	zoneRepo repository.ZoneRepository,
	dnsRepo repository.DNSRepository,
	configStorage storage.ConfigStorage,
) DNSUsecase {
	return &dnsUsecase{
		zoneRepo:      zoneRepo,
		dnsRepo:       dnsRepo,
		configStorage: configStorage,
	}
}

// ListZones returns all accessible zones
func (u *dnsUsecase) ListZones(ctx context.Context) ([]domain.Zone, error) {
	return u.zoneRepo.ListZones(ctx)
}

// ListRecords returns all DNS records for a zone
func (u *dnsUsecase) ListRecords(ctx context.Context, zoneName string) ([]domain.DNSRecord, error) {
	log.Printf("[ListRecords] START zoneName=%s", zoneName)
	zone, err := u.zoneRepo.GetZoneByName(ctx, zoneName)
	if err != nil {
		log.Printf("[ListRecords] ERROR GetZoneByName: %v", err)
		return nil, fmt.Errorf("failed to get zone %s: %w", zoneName, err)
	}
	log.Printf("[ListRecords] Got zone: ID=%s, Name=%s", zone.ID, zone.Name)

	log.Printf("[ListRecords] Calling ListRecords for zoneID=%s", zone.ID)
	records, err := u.dnsRepo.ListRecords(ctx, zone.ID, domain.RecordFilter{})
	if err != nil {
		log.Printf("[ListRecords] ERROR ListRecords: %v", err)
		return nil, fmt.Errorf("failed to list records: %w", err)
	}
	log.Printf("[ListRecords] SUCCESS: Found %d records", len(records))

	return records, nil
}

// GetRecord returns a specific DNS record by name
func (u *dnsUsecase) GetRecord(ctx context.Context, zoneName, recordName string) (*domain.DNSRecord, error) {
	zone, err := u.zoneRepo.GetZoneByName(ctx, zoneName)
	if err != nil {
		return nil, fmt.Errorf("failed to get zone %s: %w", zoneName, err)
	}

	// Ensure record name is fully qualified
	fullRecordName := u.ensureFullRecordName(recordName, zone.Name)

	record, err := u.dnsRepo.FindByName(ctx, zone.ID, fullRecordName)
	if err != nil {
		return nil, err
	}

	return record, nil
}

// CreateRecord creates a new DNS record
func (u *dnsUsecase) CreateRecord(ctx context.Context, input CreateRecordInput) (*domain.DNSRecord, error) {
	// Validate record type
	if !domain.IsValidRecordType(input.Type) {
		return nil, fmt.Errorf("%w: invalid record type %s", domain.ErrInvalidRecord, input.Type)
	}

	// Get zone
	zone, err := u.zoneRepo.GetZoneByName(ctx, input.ZoneName)
	if err != nil {
		return nil, fmt.Errorf("failed to get zone %s: %w", input.ZoneName, err)
	}

	// Apply defaults from config if needed
	config, err := u.configStorage.Load()
	if err == nil {
		if input.TTL == 0 {
			input.TTL = config.DefaultTTL
		}
	}

	// Ensure record name is fully qualified
	fullRecordName := u.ensureFullRecordName(input.Name, zone.Name)

	// Check if record already exists
	existing, _ := u.dnsRepo.FindByName(ctx, zone.ID, fullRecordName)
	if existing != nil {
		return nil, domain.ErrDuplicateRecord
	}

	record := &domain.DNSRecord{
		ZoneID:   zone.ID,
		ZoneName: zone.Name,
		Name:     fullRecordName,
		Type:     input.Type,
		Content:  input.Content,
		TTL:      input.TTL,
		Proxied:  input.Proxied,
		Priority: input.Priority,
	}

	created, err := u.dnsRepo.CreateRecord(ctx, zone.ID, record)
	if err != nil {
		return nil, fmt.Errorf("failed to create record: %w", err)
	}

	return created, nil
}

// UpdateRecord updates an existing DNS record
func (u *dnsUsecase) UpdateRecord(ctx context.Context, input UpdateRecordInput) (*domain.DNSRecord, error) {
	// Validate record type
	if !domain.IsValidRecordType(input.Type) {
		return nil, fmt.Errorf("%w: invalid record type %s", domain.ErrInvalidRecord, input.Type)
	}

	// Get zone
	zone, err := u.zoneRepo.GetZoneByName(ctx, input.ZoneName)
	if err != nil {
		return nil, fmt.Errorf("failed to get zone %s: %w", input.ZoneName, err)
	}

	// Find existing record
	fullRecordName := u.ensureFullRecordName(input.Name, zone.Name)
	existing, err := u.dnsRepo.FindByName(ctx, zone.ID, fullRecordName)
	if err != nil {
		return nil, err
	}

	record := &domain.DNSRecord{
		ZoneID:   zone.ID,
		ZoneName: zone.Name,
		Name:     fullRecordName,
		Type:     input.Type,
		Content:  input.Content,
		TTL:      input.TTL,
		Proxied:  input.Proxied,
		Priority: input.Priority,
	}

	updated, err := u.dnsRepo.UpdateRecord(ctx, zone.ID, existing.ID, record)
	if err != nil {
		return nil, fmt.Errorf("failed to update record: %w", err)
	}

	return updated, nil
}

// DeleteRecord deletes a DNS record
func (u *dnsUsecase) DeleteRecord(ctx context.Context, zoneName, recordName string) error {
	// Get zone
	zone, err := u.zoneRepo.GetZoneByName(ctx, zoneName)
	if err != nil {
		return fmt.Errorf("failed to get zone %s: %w", zoneName, err)
	}

	// Find record
	fullRecordName := u.ensureFullRecordName(recordName, zone.Name)
	record, err := u.dnsRepo.FindByName(ctx, zone.ID, fullRecordName)
	if err != nil {
		return err
	}

	return u.dnsRepo.DeleteRecord(ctx, zone.ID, record.ID)
}

// UpsertRecord creates or updates a DNS record
func (u *dnsUsecase) UpsertRecord(ctx context.Context, input CreateRecordInput) (*domain.DNSRecord, error) {
	// Validate record type
	if !domain.IsValidRecordType(input.Type) {
		return nil, fmt.Errorf("%w: invalid record type %s", domain.ErrInvalidRecord, input.Type)
	}

	// Get zone
	zone, err := u.zoneRepo.GetZoneByName(ctx, input.ZoneName)
	if err != nil {
		return nil, fmt.Errorf("failed to get zone %s: %w", input.ZoneName, err)
	}

	// Apply defaults from config if needed
	config, err := u.configStorage.Load()
	if err == nil {
		if input.TTL == 0 {
			input.TTL = config.DefaultTTL
		}
	}

	// Ensure record name is fully qualified
	fullRecordName := u.ensureFullRecordName(input.Name, zone.Name)

	// Check if record exists
	existing, err := u.dnsRepo.FindByName(ctx, zone.ID, fullRecordName)

	record := &domain.DNSRecord{
		ZoneID:   zone.ID,
		ZoneName: zone.Name,
		Name:     fullRecordName,
		Type:     input.Type,
		Content:  input.Content,
		TTL:      input.TTL,
		Proxied:  input.Proxied,
		Priority: input.Priority,
	}

	if err == nil && existing != nil {
		// Update existing record
		return u.dnsRepo.UpdateRecord(ctx, zone.ID, existing.ID, record)
	}

	// Create new record
	return u.dnsRepo.CreateRecord(ctx, zone.ID, record)
}

// ensureFullRecordName ensures the record name includes the zone name
func (u *dnsUsecase) ensureFullRecordName(recordName, zoneName string) string {
	// If record name already ends with zone name, return as is
	if strings.HasSuffix(recordName, zoneName) {
		return recordName
	}

	// If record name is @ or empty, return zone name
	if recordName == "@" || recordName == "" {
		return zoneName
	}

	// Otherwise, append zone name
	return recordName + "." + zoneName
}
