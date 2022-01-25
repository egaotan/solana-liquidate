package solend

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/gagliardetto/solana-go"
	"github.com/gagliardetto/solana-go/rpc"
	"github.com/solana-liquidate/backend"
	"github.com/solana-liquidate/env"
	pyth2 "github.com/solana-liquidate/pyth"
	"github.com/solana-liquidate/utils"
	"math/big"
	"os"
	"testing"
)

func TestSolendProgramAccounts(t *testing.T) {
	ctx := context.Background()
	be := backend.NewBackend(ctx, rpc.MainNetBetaSerum_RPC, rpc.MainNetBetaSerum_WS, 0, nil, "", "")
	env := env.NewEnv(ctx)
	pythId := solana.MustPublicKeyFromBase58("So1endDq2YkqhipRh3WViPa8hdiSpxWy6z3Z6tMCpAo")
	pyth := pyth2.NewProgram(pythId, ctx, be)
	id := solana.MustPublicKeyFromBase58("So1endDq2YkqhipRh3WViPa8hdiSpxWy6z3Z6tMCpAo")
	program := NewProgram(id, ctx, env, be, pyth)
	//
	env.Start()
	be.Start()
	pyth.Start()
	program.Start()
	pyth.Flash()
	program.Flash()
	be.StartSubscribeAccount()
	program.Stop()
	env.Stop()
	// calculate
	type LendingInfo struct {
		LiquidityMint solana.PublicKey
		CollateralMint solana.PublicKey
		ReservedAmount *big.Float
		ReserveValue *big.Float
		BorrowedAmount *big.Float
		BorrowedValue *big.Float
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
				ReservedAmount:       big.NewFloat(0),
				ReserveValue:       big.NewFloat(0),
				BorrowedAmount:       big.NewFloat(0),
				BorrowedValue:       big.NewFloat(0),
			}
			infos[reserve.Key] = info
		}
	}
	for _, obligation := range program.obligations {
		for _, deposit := range obligation.ObligationCollateral {
			info, ok := infos[deposit.DepositReserve]
			if !ok {
				fmt.Printf("no reserve\n")
				continue
			}
			info.ReservedAmount = new(big.Float).Add(info.ReservedAmount, big.NewFloat(float64(deposit.DepositedAmount)))
			info.ReserveValue = new(big.Float).Add(info.ReserveValue, deposit.MarketValue.Value)
		}
		for _, borrow := range obligation.ObligationLiquidity {
			info, ok := infos[borrow.BorrowReserve]
			if !ok {
				fmt.Printf("no reserve\n")
				continue
			}
			info.BorrowedAmount = new(big.Float).Add(info.BorrowedAmount, borrow.BorrowedAmount.Value)
			info.BorrowedValue = new(big.Float).Add(info.BorrowedValue, borrow.MarketValue.Value)
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