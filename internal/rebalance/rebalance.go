package rebalance

import "context"

type Rebalancer interface{ Rebalance(ctx context.Context) error }

type Stub struct{}

func (s Stub) Rebalance(ctx context.Context) error { return nil }
