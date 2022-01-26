package solend

import "github.com/solana-liquidate/pyth"

type Model struct {
	Obligation *KeyedObligation
	Reserve    *KeyedReserve
	Oracle     *pyth.KeyedPrice
}
