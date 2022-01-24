package tokenlending

import (
	"bytes"
	"context"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"github.com/gagliardetto/solana-go"
	"github.com/solana-liquidate/backend"
	"github.com/solana-liquidate/utils"
	"log"
	"os"
)

type Program struct {
	ctx               context.Context
	logger            *log.Logger
	backend           *backend.Backend
	id                solana.PublicKey
	obligations       map[solana.PublicKey]*KeyedObligation
	reserves          map[solana.PublicKey]*KeyedReserve
	updateAccountChan chan *backend.Account
}

func NewProgram(id solana.PublicKey, ctx context.Context, be *backend.Backend) *Program {
	p := &Program{
		ctx:               ctx,
		logger:            log.Default(),
		backend:           be,
		id:                id,
		obligations:       make(map[solana.PublicKey]*KeyedObligation),
		reserves:          make(map[solana.PublicKey]*KeyedReserve),
		updateAccountChan: make(chan *backend.Account, 1024),
	}
	return p
}

func (p *Program) Name() string {
	return "solend"
}

func (p *Program) Id() solana.PublicKey {
	return p.id
}

func (p *Program) save2Cache() {
	type ProgramOutput struct {
		Reserves    map[solana.PublicKey]*KeyedReserve
		Obligations map[solana.PublicKey]*KeyedObligation
	}
	output := ProgramOutput{
		Reserves:    p.reserves,
		Obligations: p.obligations,
	}

	infoJson, _ := json.MarshalIndent(output, "", "    ")
	name := fmt.Sprintf("%s%s_%s.json", utils.CachePath, p.Name(), p.Id())
	err := os.WriteFile(name, infoJson, 0644)
	if err != nil {
		panic(err)
	}
}

func (p *Program) Start() error {
	p.logger = utils.NewLog(utils.LogPath, p.Name())
	p.logger.Printf("start %s, program: %s......", p.Name(), p.id)
	accounts, err := p.accounts()
	if err != nil {
		return err
	}
	err = p.buildAccounts(accounts)
	if err != nil {
		return err
	}
	return nil
}

func (p *Program) Stop() error {
	p.logger.Printf("stop %s, program: %s......", p.Name(), p.id)
	p.save2Cache()
	return nil
}

func (p *Program) Flash() error {
	p.logger.Printf("flash %s, program: %s......", p.Name(), p.id)
	go p.updateAccount()
	p.subscribeUpdate()
	return nil
}

func (p *Program) upsertObligation(pubkey solana.PublicKey, height uint64, obligation ObligationLayout) *KeyedObligation {
	keyedObligation, ok := p.obligations[pubkey]
	if !ok {
		keyedObligation = &KeyedObligation{
			Key:              pubkey,
			Height:           height,
			ObligationLayout: obligation,
		}
		p.obligations[pubkey] = keyedObligation
	} else {
		keyedObligation.Height = height
		keyedObligation.ObligationLayout = obligation
	}
	return keyedObligation
}

func (p *Program) upsertReserve(pubkey solana.PublicKey, height uint64, reserve ReserveLayout) *KeyedReserve {
	keyedReserve, ok := p.reserves[pubkey]
	if !ok {
		keyedReserve = &KeyedReserve{
			Key:           pubkey,
			Height:        height,
			ReserveLayout: reserve,
		}
		p.reserves[pubkey] = keyedReserve
	} else {
		keyedReserve.Height = height
		keyedReserve.ReserveLayout = reserve
	}
	return keyedReserve
}

func (p *Program) accounts() ([]*backend.Account, error) {
	//return p.backend.ProgramAccounts(p.id, []uint64{uint64(ObligationLayoutSize)})
	return p.backend.ProgramAccounts(p.id, []uint64{})
}

func (p *Program) buildAccount(account *backend.Account) error {
	if account.Account.Owner != p.id {
		return fmt.Errorf("account %s is not program account, expected: %s, actual: %s", account.PubKey, p.id, account.Account.Owner)
	}
	data := account.Account.Data.GetBinary()
	if len(data) == ObligationLayoutSize {
		obligation := ObligationLayout{}
		err := obligation.unpack(data)
		if err != nil {
			return err
		}
		p.upsertObligation(account.PubKey, account.Height, obligation)
	} else if len(data) == ReserveLayoutSize {
		reserve := ReserveLayout{}
		buf := bytes.NewReader(data)
		err := binary.Read(buf, binary.LittleEndian, &reserve)
		if err != nil {
			return err
		}
		p.upsertReserve(account.PubKey, account.Height, reserve)
	} else {
		return fmt.Errorf("unused account, data length: %d", len(data))
	}
	return nil
}

func (p *Program) buildAccounts(accounts []*backend.Account) error {
	for _, account := range accounts {
		err := p.buildAccount(account)
		if err != nil {
			p.logger.Printf(err.Error())
		}
	}
	return nil
}

func (p *Program) subscribeUpdate() {
	p.backend.SubscribeProgram(p.id, p)
}

func (p *Program) OnAccountUpdate(account *backend.Account) error {
	p.updateAccountChan <- account
	return nil
}

func (p *Program) updateAccount() {
	for {
		select {
		case updateAccount := <-p.updateAccountChan:
			p.buildAccount(updateAccount)
		case <-p.ctx.Done():
			return
		}
	}
}
