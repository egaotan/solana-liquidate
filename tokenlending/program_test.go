package tokenlending

import (
	"context"
	"github.com/gagliardetto/solana-go"
	"github.com/gagliardetto/solana-go/rpc"
	"github.com/solana-liquidate/backend"
	"testing"
)

func TestLendingMarketAccounts(t *testing.T) {
	ctx := context.Background()
	be := backend.NewBackend(ctx, rpc.MainNetBetaSerum_RPC, rpc.MainNetBetaSerum_WS, 0, nil, "", "")
	id := solana.MustPublicKeyFromBase58("So1endDq2YkqhipRh3WViPa8hdiSpxWy6z3Z6tMCpAo")
	program := NewProgram(id, ctx, be)
	program.Start()
	program.Flash()
	program.Stop()
}
