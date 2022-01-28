package env

import (
	"encoding/json"
	"github.com/gagliardetto/solana-go"
	"github.com/solana-liquidate/utils"
	"os"
)

type TokenSwap struct {
	Key            solana.PublicKey
	SwapA          solana.PublicKey
	SwapB          solana.PublicKey
	PoolToken      solana.PublicKey
	TokenA         solana.PublicKey
	TokenB         solana.PublicKey
	PoolFeeAccount solana.PublicKey
}
type SwapMarket struct {
	ProgramId solana.PublicKey
	TokenSwap TokenSwap
}

func (e *Env) loadMarkets() {
	infoJson, err := os.ReadFile(utils.MarketsFile)
	if err != nil {
		panic(err)
	}
	err = json.Unmarshal(infoJson, &e.markets)
	if err != nil {
		panic(err)
	}
}

func (e *Env) FindMarket(tokens []solana.PublicKey) *TokenSwap {
	for _, swap := range e.markets {
		if swap.TokenSwap.TokenA == tokens[0] && swap.TokenSwap.TokenB == tokens[1] {
			return &swap.TokenSwap
		}
		if swap.TokenSwap.TokenA == tokens[1] && swap.TokenSwap.TokenB == tokens[0] {
			return &swap.TokenSwap
		}
	}
	return nil
}
