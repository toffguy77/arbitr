package filters

import "github.com/prometheus/client_golang/prometheus"

var SampledEvents = prometheus.NewCounter(prometheus.CounterOpts{Name: "sampling_events_total"})
