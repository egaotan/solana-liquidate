package tokenlending

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/gagliardetto/solana-go"
	"github.com/gagliardetto/solana-go/rpc"
	"github.com/solana-liquidate/backend"
	"github.com/solana-liquidate/utils"
	"math/big"
	"os"
	"testing"
)

func TestSolendProgramAccounts(t *testing.T) {
	ctx := context.Background()
	be := backend.NewBackend(ctx, rpc.MainNetBetaSerum_RPC, rpc.MainNetBetaSerum_WS, 0, nil, "", "")
	id := solana.MustPublicKeyFromBase58("So1endDq2YkqhipRh3WViPa8hdiSpxWy6z3Z6tMCpAo")
	program := NewProgram(id, ctx, be)
	program.Start()
	program.Flash()
	program.Stop()
	// calculate
	type LendingInfo struct {
		LiquidityMint solana.PublicKey
		CollateralMint solana.PublicKey
		Reserved *big.Int
		Borrowed *big.Int
	}
	infos := make(map[solana.PublicKey]*LendingInfo)
	for _, reserve := range program.reserves {
		liquidityMint := reserve.ReserveLiquidity.Mint
		collateralMint := reserve.ReserveCollateral.Mint
		info, ok := infos[reserve.Key]
		if !ok {
			info = &LendingInfo{
				LiquidityMint:  liquidityMint,
				CollateralMint: collateralMint,
				Reserved:       big.NewInt(0),
				Borrowed:       big.NewInt(0),
			}
			infos[reserve.Key] = info
		}
	}
	for _, obligation := range program.obligations {
		for _, deposit := range obligation.ObligationCollateral {
			info, ok := infos[deposit.DepositReserve]
			if !ok {
				fmt.Printf("no reserve")
				continue
			}
			info.Reserved = new(big.Int).Add(info.Reserved, big.NewInt(int64(deposit.DepositedAmount)))
		}
		for _, borrow := range obligation.ObligationLiquidity {
			info, ok := infos[borrow.BorrowReserve]
			if !ok {
				fmt.Printf("no reserve")
				continue
			}
			BorrowedAmount := new(big.Int).Div(borrow.BorrowedAmountWads.BigInt(), Pad)
			info.Borrowed = new(big.Int).Add(info.Borrowed, BorrowedAmount)
		}
	}
	if true {
		infoJson, _ := json.MarshalIndent(infos, "", "    ")
		name := fmt.Sprintf("%s%s.json", utils.CachePath, "TestSolendProgramAccounts")
		err := os.WriteFile(name, infoJson, 0644)
		if err != nil {
			panic(err)
		}
	}
}