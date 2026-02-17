package domain

import "time"

// DNSRecord represents a DNS record in the domain
type DNSRecord struct {
	ID       string
	ZoneID   string
	ZoneName string
	Name     string
	Type     string // A, AAAA, CNAME, MX, TXT, NS, SRV, CAA
	Content  string
	TTL      int
	Proxied  bool
	Priority *uint16 // for MX, SRV records
	Created  time.Time
	Modified time.Time
}

// RecordFilter represents filters for listing DNS records
type RecordFilter struct {
	Name string
	Type string
}

// RecordTypes contains all supported DNS record types for Cloudflare free tier
var RecordTypes = []string{
	"A",
	"AAAA",
	"CNAME",
	"MX",
	"TXT",
	"NS",
	"SRV",
	"CAA",
}

// IsValidRecordType checks if the given type is a valid DNS record type
func IsValidRecordType(recordType string) bool {
	for _, t := range RecordTypes {
		if t == recordType {
			return true
		}
	}
	return false
}
