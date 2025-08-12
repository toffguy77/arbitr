package network

import "time"

type Region string

const (
	RegionEUWest   Region = "EU-West"
	RegionEUCentral       = "EU-Central"
)

type EgressManager struct {
	Region Region
}

func NewEgressManager(region string) *EgressManager {
	return &EgressManager{Region: Region(region)}
}

type RTTStats struct {
	Exchange string
	WSMedianMs   float64
	RESTMedianMs float64
	UpdatedAt    time.Time
}

// Monitor hooks for external pingers (stubs here)
func (e *EgressManager) UpdateRTT(s RTTStats) {}
