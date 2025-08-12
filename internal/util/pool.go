package util

import "sync"

var RawOpportunityPool = sync.Pool{New: func() any { return new(struct{}) }}
var ArbPlanPool = sync.Pool{New: func() any { return new(struct{}) }}
