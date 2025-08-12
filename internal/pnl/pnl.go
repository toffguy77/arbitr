package pnl

type Tracker interface{
	RealizedUSD() float64
	UnrealizedUSD() float64
	TotalUSD() float64
}

type StubTracker struct{}

func (s StubTracker) RealizedUSD() float64 { return 0 }
func (s StubTracker) UnrealizedUSD() float64 { return 0 }
func (s StubTracker) TotalUSD() float64 { return 0 }
