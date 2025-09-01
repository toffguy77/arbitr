package orderbook

type Level struct { Price, Qty float64 }

type L2 struct {
	Bids []Level // sorted desc by price
	Asks []Level // sorted asc by price
}
