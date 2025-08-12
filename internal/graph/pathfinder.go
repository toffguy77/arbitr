package graph

import "sync"

type Node struct { Exchange, Asset string }

type Edge struct { From, To Node; FeeBps float64 }

type PathFinder interface {
	FindPaths(start Node) []Path
}

type Path struct { Nodes []Node }

// Pools for reuse
var RawOpportunityPool = sync.Pool{New: func() any { return new(RawOpportunity) }}
var ArbPlanPool = sync.Pool{New: func() any { return new(ArbPlan) }}

type RawOpportunity struct{ /* details */ }
type ArbPlan struct{ /* details */ }
