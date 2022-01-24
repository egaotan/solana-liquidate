package backend

import (
	"github.com/gagliardetto/solana-go"
	"github.com/gagliardetto/solana-go/rpc"
	"github.com/gagliardetto/solana-go/rpc/ws"
	"syscall"
)

type ProgramCallback interface {
	OnAccountUpdate(account *Account) error
}

type ProgramSubscription struct {
	sub *ws.ProgramSubscription
	cb  ProgramCallback
}

func (backend *Backend) SubscribeProgram(pubkey solana.PublicKey, cb ProgramCallback) error {
	sub, err := backend.wsClient.ProgramSubscribeWithOpts(pubkey, rpc.CommitmentProcessed, solana.EncodingBase64, nil)
	if err != nil {
		return err
	}
	backend.programSubs[pubkey] = ProgramSubscription{
		sub: sub,
		cb:  cb,
	}
	backend.wg.Add(1)
	go backend.RecvProgramAccounts(pubkey, cb, sub)
	return nil
}

func (backend *Backend) RecvProgramAccounts(key solana.PublicKey, cb ProgramCallback, sub *ws.ProgramSubscription) {
	defer backend.wg.Done()
	for {
		got, err := sub.Recv()
		if err != nil {
			backend.logger.Printf("RecvProgramAccounts err: %v", err)
			syscall.Kill(syscall.Getpid(), syscall.SIGABRT)
			return
		}
		if got == nil {
			backend.logger.Printf("RecvProgramAccounts exit")
			return
		}
		backend.logger.Printf("receive program accounts, %d", got.Context.Slot)
		data := got
		account := &Account{
			PubKey:  data.Value.Pubkey,
			Account: data.Value.Account,
			Height:  data.Context.Slot,
		}
		if cb != nil {
			cb.OnAccountUpdate(account)
		}
	}
}
