package domain

import "errors"

// Domain errors
var (
	ErrRecordNotFound = errors.New("dns record not found")
	ErrZoneNotFound   = errors.New("zone not found")
	ErrInvalidRecord  = errors.New("invalid dns record")
	ErrInvalidZone    = errors.New("invalid zone")
	ErrDuplicateRecord = errors.New("dns record already exists")
	ErrUnauthorized   = errors.New("unauthorized")
)
