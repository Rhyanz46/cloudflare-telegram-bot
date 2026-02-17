package cloudflare

import "context"

// Client defines the interface for Cloudflare API operations
type Client interface {
	// Zone operations
	ListZones(ctx context.Context) ([]Zone, error)
	GetZoneByName(ctx context.Context, name string) (*Zone, error)
	GetZone(ctx context.Context, zoneID string) (*Zone, error)

	// DNS Record operations
	ListDNSRecords(ctx context.Context, zoneID string, filter DNSRecordFilter) ([]DNSRecord, error)
	GetDNSRecord(ctx context.Context, zoneID, recordID string) (*DNSRecord, error)
	CreateDNSRecord(ctx context.Context, zoneID string, input CreateDNSRecordInput) (*DNSRecord, error)
	UpdateDNSRecord(ctx context.Context, zoneID, recordID string, input UpdateDNSRecordInput) (*DNSRecord, error)
	DeleteDNSRecord(ctx context.Context, zoneID, recordID string) error
}

// Zone represents a Cloudflare zone (domain)
type Zone struct {
	ID   string
	Name string
}

// DNSRecord represents a DNS record from Cloudflare
type DNSRecord struct {
	ID       string
	ZoneID   string
	ZoneName string
	Name     string
	Type     string
	Content  string
	TTL      int
	Proxied  bool
	Priority *uint16
}

// DNSRecordFilter represents filters for listing DNS records
type DNSRecordFilter struct {
	Name string
	Type string
}

// CreateDNSRecordInput represents input for creating a DNS record
type CreateDNSRecordInput struct {
	Name     string
	Type     string
	Content  string
	TTL      int
	Proxied  bool
	Priority *uint16
}

// UpdateDNSRecordInput represents input for updating a DNS record
type UpdateDNSRecordInput struct {
	Name     string
	Type     string
	Content  string
	TTL      int
	Proxied  bool
	Priority *uint16
}
