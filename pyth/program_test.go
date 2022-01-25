package pyth

import (
	"context"
	"github.com/gagliardetto/solana-go"
	"github.com/gagliardetto/solana-go/rpc"
	"github.com/solana-liquidate/backend"
	"testing"
)

func TestPythProgramAccounts(t *testing.T) {
	ctx := context.Background()
	be := backend.NewBackend(ctx, rpc.MainNetBetaSerum_RPC, rpc.MainNetBetaSerum_WS, 0, nil, "", "")
	id := solana.MustPublicKeyFromBase58("FsJ3A3u2vn5cTVofAjvy6y5kwABJAqYWpe4975bi2epH")
	program := NewProgram(id, ctx, be)
	//
	be.Start()
	program.Start()
	//
	accounts, err := be.ProgramAccounts(id, nil)
	if err != nil {
		panic(err)
	}
	program.buildAccounts(accounts)
	program.Flash()
	be.StartSubscribeAccount()
	program.Stop()
	// calculate
}