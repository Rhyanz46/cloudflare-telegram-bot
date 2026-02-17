package cloudflare

import (
	"context"
	"fmt"
	"log"

	"github.com/cloudflare/cloudflare-go"
)

// cloudflareClient implements the Client interface using cloudflare-go SDK
type cloudflareClient struct {
	api *cloudflare.API
}

// NewClient creates a new Cloudflare client using API token
func NewClient(apiToken string) (Client, error) {
	api, err := cloudflare.NewWithAPIToken(apiToken)
	if err != nil {
		return nil, fmt.Errorf("failed to create cloudflare client: %w", err)
	}

	return &cloudflareClient{
		api: api,
	}, nil
}

// NewClientWithKey creates a new Cloudflare client using API key and email
func NewClientWithKey(apiKey, email string) (Client, error) {
	api, err := cloudflare.New(apiKey, email)
	if err != nil {
		return nil, fmt.Errorf("failed to create cloudflare client: %w", err)
	}

	return &cloudflareClient{
		api: api,
	}, nil
}

// ListZones returns all zones accessible by the client
func (c *cloudflareClient) ListZones(ctx context.Context) ([]Zone, error) {
	zones, err := c.api.ListZones(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to list zones: %w", err)
	}

	result := make([]Zone, len(zones))
	for i, z := range zones {
		result[i] = Zone{
			ID:   z.ID,
			Name: z.Name,
		}
	}

	return result, nil
}

// GetZoneByName returns a zone by its name
func (c *cloudflareClient) GetZoneByName(ctx context.Context, name string) (*Zone, error) {
	log.Printf("[CloudflareClient] GetZoneByName START name=%s", name)
	zoneID, err := c.api.ZoneIDByName(name)
	if err != nil {
		log.Printf("[CloudflareClient] GetZoneByName ERROR: %v", err)
		return nil, fmt.Errorf("failed to get zone by name %s: %w", name, err)
	}
	log.Printf("[CloudflareClient] GetZoneByName SUCCESS: zoneID=%s", zoneID)

	return &Zone{
		ID:   zoneID,
		Name: name,
	}, nil
}

// GetZone returns a zone by its ID
func (c *cloudflareClient) GetZone(ctx context.Context, zoneID string) (*Zone, error) {
	zone, err := c.api.ZoneDetails(ctx, zoneID)
	if err != nil {
		return nil, fmt.Errorf("failed to get zone %s: %w", zoneID, err)
	}

	return &Zone{
		ID:   zone.ID,
		Name: zone.Name,
	}, nil
}

// ListDNSRecords returns all DNS records for a zone
func (c *cloudflareClient) ListDNSRecords(ctx context.Context, zoneID string, filter DNSRecordFilter) ([]DNSRecord, error) {
	log.Printf("[CloudflareClient] ListDNSRecords START zoneID=%s", zoneID)
	listParams := cloudflare.ListDNSRecordsParams{}

	if filter.Name != "" {
		listParams.Name = filter.Name
	}
	if filter.Type != "" {
		listParams.Type = filter.Type
	}

	records, _, err := c.api.ListDNSRecords(ctx, cloudflare.ZoneIdentifier(zoneID), listParams)
	if err != nil {
		log.Printf("[CloudflareClient] ListDNSRecords ERROR: %v", err)
		return nil, fmt.Errorf("failed to list dns records: %w", err)
	}
	log.Printf("[CloudflareClient] ListDNSRecords SUCCESS: found %d records", len(records))

	result := make([]DNSRecord, len(records))
	for i, r := range records {
		result[i] = mapCloudflareRecord(r)
	}

	return result, nil
}

// GetDNSRecord returns a specific DNS record
func (c *cloudflareClient) GetDNSRecord(ctx context.Context, zoneID, recordID string) (*DNSRecord, error) {
	record, err := c.api.GetDNSRecord(ctx, cloudflare.ZoneIdentifier(zoneID), recordID)
	if err != nil {
		return nil, fmt.Errorf("failed to get dns record %s: %w", recordID, err)
	}

	result := mapCloudflareRecord(record)
	return &result, nil
}

// CreateDNSRecord creates a new DNS record
func (c *cloudflareClient) CreateDNSRecord(ctx context.Context, zoneID string, input CreateDNSRecordInput) (*DNSRecord, error) {
	createParams := cloudflare.CreateDNSRecordParams{
		Name:    input.Name,
		Type:    input.Type,
		Content: input.Content,
		TTL:     input.TTL,
	}
	if input.Proxied {
		createParams.Proxied = &input.Proxied
	}

	if input.Priority != nil {
		createParams.Priority = input.Priority
	}

	record, err := c.api.CreateDNSRecord(ctx, cloudflare.ZoneIdentifier(zoneID), createParams)
	if err != nil {
		return nil, fmt.Errorf("failed to create dns record: %w", err)
	}

	result := mapCloudflareRecord(record)
	return &result, nil
}

// UpdateDNSRecord updates an existing DNS record
func (c *cloudflareClient) UpdateDNSRecord(ctx context.Context, zoneID, recordID string, input UpdateDNSRecordInput) (*DNSRecord, error) {
	updateParams := cloudflare.UpdateDNSRecordParams{
		ID:      recordID,
		Name:    input.Name,
		Type:    input.Type,
		Content: input.Content,
		TTL:     input.TTL,
	}
	if input.Proxied {
		updateParams.Proxied = &input.Proxied
	}

	if input.Priority != nil {
		updateParams.Priority = input.Priority
	}

	record, err := c.api.UpdateDNSRecord(ctx, cloudflare.ZoneIdentifier(zoneID), updateParams)
	if err != nil {
		return nil, fmt.Errorf("failed to update dns record %s: %w", recordID, err)
	}

	result := mapCloudflareRecord(record)
	return &result, nil
}

// DeleteDNSRecord deletes a DNS record
func (c *cloudflareClient) DeleteDNSRecord(ctx context.Context, zoneID, recordID string) error {
	err := c.api.DeleteDNSRecord(ctx, cloudflare.ZoneIdentifier(zoneID), recordID)
	if err != nil {
		return fmt.Errorf("failed to delete dns record %s: %w", recordID, err)
	}

	return nil
}

// mapCloudflareRecord maps cloudflare-go DNSRecord to our DNSRecord
func mapCloudflareRecord(r cloudflare.DNSRecord) DNSRecord {
	proxied := false
	if r.Proxied != nil {
		proxied = *r.Proxied
	}
	return DNSRecord{
		ID:       r.ID,
		ZoneID:   r.ZoneID,
		ZoneName: r.ZoneName,
		Name:     r.Name,
		Type:     r.Type,
		Content:  r.Content,
		TTL:      r.TTL,
		Proxied:  proxied,
		Priority: r.Priority,
	}
}
