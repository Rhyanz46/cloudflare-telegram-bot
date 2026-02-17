package repository

import (
	"context"
	"time"

	"cf-dns-bot/external_resource/cloudflare"
	"cf-dns-bot/internal/domain"
)

// dnsRepository implements DNSRepository using Cloudflare client
type dnsRepository struct {
	client cloudflare.Client
}

// NewDNSRepository creates a new DNS repository
func NewDNSRepository(client cloudflare.Client) DNSRepository {
	return &dnsRepository{
		client: client,
	}
}

// ListRecords returns all DNS records for a zone
func (r *dnsRepository) ListRecords(ctx context.Context, zoneID string, filter domain.RecordFilter) ([]domain.DNSRecord, error) {
	cfFilter := cloudflare.DNSRecordFilter{
		Name: filter.Name,
		Type: filter.Type,
	}

	records, err := r.client.ListDNSRecords(ctx, zoneID, cfFilter)
	if err != nil {
		return nil, err
	}

	result := make([]domain.DNSRecord, len(records))
	for i, rec := range records {
		result[i] = mapToDomainRecord(rec)
	}

	return result, nil
}

// GetRecord returns a specific DNS record
func (r *dnsRepository) GetRecord(ctx context.Context, zoneID, recordID string) (*domain.DNSRecord, error) {
	record, err := r.client.GetDNSRecord(ctx, zoneID, recordID)
	if err != nil {
		return nil, err
	}

	result := mapToDomainRecord(*record)
	return &result, nil
}

// CreateRecord creates a new DNS record
func (r *dnsRepository) CreateRecord(ctx context.Context, zoneID string, record *domain.DNSRecord) (*domain.DNSRecord, error) {
	input := cloudflare.CreateDNSRecordInput{
		Name:     record.Name,
		Type:     record.Type,
		Content:  record.Content,
		TTL:      record.TTL,
		Proxied:  record.Proxied,
		Priority: record.Priority,
	}

	created, err := r.client.CreateDNSRecord(ctx, zoneID, input)
	if err != nil {
		return nil, err
	}

	result := mapToDomainRecord(*created)
	return &result, nil
}

// UpdateRecord updates an existing DNS record
func (r *dnsRepository) UpdateRecord(ctx context.Context, zoneID, recordID string, record *domain.DNSRecord) (*domain.DNSRecord, error) {
	input := cloudflare.UpdateDNSRecordInput{
		Name:     record.Name,
		Type:     record.Type,
		Content:  record.Content,
		TTL:      record.TTL,
		Proxied:  record.Proxied,
		Priority: record.Priority,
	}

	updated, err := r.client.UpdateDNSRecord(ctx, zoneID, recordID, input)
	if err != nil {
		return nil, err
	}

	result := mapToDomainRecord(*updated)
	return &result, nil
}

// DeleteRecord deletes a DNS record
func (r *dnsRepository) DeleteRecord(ctx context.Context, zoneID, recordID string) error {
	return r.client.DeleteDNSRecord(ctx, zoneID, recordID)
}

// FindByName finds a DNS record by name within a zone
func (r *dnsRepository) FindByName(ctx context.Context, zoneID, name string) (*domain.DNSRecord, error) {
	filter := cloudflare.DNSRecordFilter{
		Name: name,
	}

	records, err := r.client.ListDNSRecords(ctx, zoneID, filter)
	if err != nil {
		return nil, err
	}

	if len(records) == 0 {
		return nil, domain.ErrRecordNotFound
	}

	result := mapToDomainRecord(records[0])
	return &result, nil
}

// mapToDomainRecord maps external resource record to domain record
func mapToDomainRecord(r cloudflare.DNSRecord) domain.DNSRecord {
	return domain.DNSRecord{
		ID:       r.ID,
		ZoneID:   r.ZoneID,
		ZoneName: r.ZoneName,
		Name:     r.Name,
		Type:     r.Type,
		Content:  r.Content,
		TTL:      r.TTL,
		Proxied:  r.Proxied,
		Priority: r.Priority,
		Created:  time.Now(), // Cloudflare API doesn't return created time
		Modified: time.Now(), // Cloudflare API doesn't return modified time
	}
}
