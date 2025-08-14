package backtest

import (
	"encoding/csv"
	"fmt"
	"io"
	"os"
	"strconv"
	"time"

	"arbitr/internal/infra/metrics"
	"arbitr/internal/strategy"
)

// Simple CSV-based backtest harness for triangle evaluation.
// CSV format: ts,ab_bid,ab_ask,bc_bid,bc_ask,ca_bid,ca_ask
// Env var: ARBITR_BACKTEST_CSV=/path/to/file.csv
func RunSimpleCSV() error {
	path := os.Getenv("ARBITR_BACKTEST_CSV")
	if path == "" { return nil }
	f, err := os.Open(path)
	if err != nil { return err }
	defer f.Close()
	r := csv.NewReader(f)
	var (
		rows int
		opp int
	)
	for {
		rec, err := r.Read()
		if err == io.EOF { break }
		if err != nil { return err }
		if len(rec) < 7 { continue }
		rows++
		// Parse bid/ask
		p := func(s string) float64 { v, _ := strconv.ParseFloat(s, 64); return v }
		abBid, abAsk := p(rec[1]), p(rec[2])
		bcBid := p(rec[3])
		caBid, caAsk := p(rec[5]), p(rec[6])
		grossF, netF := strategy.EvalTriangleForward(abBid, bcBid, caBid, 10.0, 1.0, 0.5)
		grossR, netR := strategy.EvalTriangleReverse(abAsk, bcBid, caAsk, 10.0, 1.0, 0.5)
		_ = grossF; _ = grossR
		net := netF
		if netR > netF { net = netR }
		metrics.TriangleNetBps.Observe(net)
		if net >= 5.0 { opp++ }
	}
	fmt.Printf("backtest rows=%d opportunities=%d ratio=%.4f at %s\n", rows, opp, float64(opp)/float64(rows), time.Now().Format(time.RFC3339))
	return nil
}
