package repository

import (
	"context"
	"log"

	"cf-dns-bot/external_resource/cloudflare"
	"cf-dns-bot/internal/domain"
)

// zoneRepository implements ZoneRepository using Cloudflare client
type zoneRepository struct {
	client cloudflare.Client
}

// NewZoneRepository creates a new zone repository
func NewZoneRepository(client cloudflare.Client) ZoneRepository {
	return &zoneRepository{
		client: client,
	}
}

// ListZones returns all accessible zones
func (r *zoneRepository) ListZones(ctx context.Context) ([]domain.Zone, error) {
	zones, err := r.client.ListZones(ctx)
	if err != nil {
		return nil, err
	}

	result := make([]domain.Zone, len(zones))
	for i, z := range zones {
		result[i] = domain.Zone{
			ID:   z.ID,
			Name: z.Name,
		}
	}

	return result, nil
}

// GetZoneByName returns a zone by its name
func (r *zoneRepository) GetZoneByName(ctx context.Context, name string) (*domain.Zone, error) {
	log.Printf("[GetZoneByName] START name=%s", name)
	zone, err := r.client.GetZoneByName(ctx, name)
	if err != nil {
		log.Printf("[GetZoneByName] ERROR: %v", err)
		return nil, err
	}
	log.Printf("[GetZoneByName] SUCCESS: ID=%s, Name=%s", zone.ID, zone.Name)

	return &domain.Zone{
		ID:   zone.ID,
		Name: zone.Name,
	}, nil
}

// GetZone returns a zone by its ID
func (r *zoneRepository) GetZone(ctx context.Context, zoneID string) (*domain.Zone, error) {
	zone, err := r.client.GetZone(ctx, zoneID)
	if err != nil {
		return nil, err
	}

	return &domain.Zone{
		ID:   zone.ID,
		Name: zone.Name,
	}, nil
}
