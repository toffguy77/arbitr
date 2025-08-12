package compliance

import (
	"context"
	"sync"

	"arbitr/internal/infra/metrics"
)

type ExchangePolicy string
const (
	PolicyAllow ExchangePolicy = "allow"
	PolicyDeny  ExchangePolicy = "deny"
)

type Status struct {
	Exchange string
	Policy   ExchangePolicy
	KYCLevel string
	SanctionsFlag bool
	Restricted bool // computed: true if exchange restricted for RU residents
}

type Guard interface {
	Check(ctx context.Context, exchange string, action string) (allowed bool, reason string)
	UpdateStatus(s Status)
}

type GuardImpl struct {
	mu sync.RWMutex
	status map[string]Status
}

func NewGuard() *GuardImpl { return &GuardImpl{status: map[string]Status{}} }

func (g *GuardImpl) UpdateStatus(s Status) {
	g.mu.Lock(); defer g.mu.Unlock()
	g.status[s.Exchange] = s
}

func (g *GuardImpl) Check(ctx context.Context, exchange string, action string) (bool, string) {
	g.mu.RLock(); s, ok := g.status[exchange]; g.mu.RUnlock()
	if !ok { return false, "unknown_exchange" }
	if s.SanctionsFlag || s.Policy == PolicyDeny || s.Restricted {
		metrics.ComplianceBlocksTotal.Inc()
		metrics.RestrictedActionsTotal.Inc()
		return false, "restricted_exchange"
	}
	return true, ""
}
