package backend

import (
	"github.com/gagliardetto/solana-go"
	"github.com/gagliardetto/solana-go/rpc"
	"github.com/gagliardetto/solana-go/rpc/ws"
	"syscall"
)

type AccountCallback interface {
	OnAccountUpdate(account *Account) error
}

type AccountSubscription struct {
	sub *ws.AccountSubscription
	cb  []AccountCallback
}

func (backend *Backend) SubscribeAccount(pubkey solana.PublicKey, cb AccountCallback) error {
	accountSub, ok := backend.accountSubs[pubkey]
	if !ok {
		accountSub = &AccountSubscription{
			cb: make([]AccountCallback, 0),
		}
		backend.accountSubs[pubkey] = accountSub
	}
	accountSub.cb = append(accountSub.cb, cb)
	return nil
}

func (backend *Backend) StartSubscribeAccount() error {
	for pubkey, accountSub := range backend.accountSubs {
		sub, err := backend.wsClient.AccountSubscribeWithOpts(pubkey, rpc.CommitmentProcessed, solana.EncodingBase64)
		if err != nil {
			backend.logger.Printf("%s", err.Error())
			continue
		}
		accountSub.sub = sub
		backend.wg.Add(1)
		go backend.RecvAccount(pubkey, accountSub)
	}
	return nil
}

func (backend *Backend) RecvAccount(key solana.PublicKey, accountSub *AccountSubscription) {
	defer backend.wg.Done()
	for {
		got, err := accountSub.sub.Recv()
		if err != nil {
			backend.logger.Printf("RecvAccount err: %v", err)
			syscall.Kill(syscall.Getpid(), syscall.SIGABRT)
			return
		}
		if got == nil {
			backend.logger.Printf("RecvAccount exit")
			return
		}
		backend.logger.Printf("receive account, %d", got.Context.Slot)
		data := got
		account := &Account{
			PubKey:  key,
			Account: &data.Value.Account,
			Height:  data.Context.Slot,
		}
		for _, cb := range accountSub.cb {
			cb.OnAccountUpdate(account)
		}
	}
}
