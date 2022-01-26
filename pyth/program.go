package pyth

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

type UpdateCallback interface {
	OnUpdate(price *KeyedPrice) error
}

type UpdateSubscribe struct {
	cb []UpdateCallback
}

type Program struct {
	ctx               context.Context
	logger            *log.Logger
	backend           *backend.Backend
	id                solana.PublicKey
	products          map[solana.PublicKey]*KeyedProduct
	prices            map[solana.PublicKey]*KeyedPrice
	updateAccountChan chan *backend.Account
	updateSubs        map[solana.PublicKey]*UpdateSubscribe
}

func NewProgram(id solana.PublicKey, ctx context.Context, be *backend.Backend) *Program {
	p := &Program{
		ctx:               ctx,
		logger:            log.Default(),
		backend:           be,
		id:                id,
		products:          make(map[solana.PublicKey]*KeyedProduct),
		prices:            make(map[solana.PublicKey]*KeyedPrice),
		updateAccountChan: make(chan *backend.Account, 1024),
		updateSubs:        make(map[solana.PublicKey]*UpdateSubscribe),
	}
	return p
}

func (p *Program) Name() string {
	return "price oracle"
}

func (p *Program) Id() solana.PublicKey {
	return p.id
}

func (p *Program) save2Cache() {
	type ProgramOutput struct {
		Products map[solana.PublicKey]*KeyedProduct
		Prices   map[solana.PublicKey]*KeyedPrice
	}
	output := ProgramOutput{
		Products: p.products,
		Prices:   p.prices,
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

func (p *Program) upsertProduct(pubkey solana.PublicKey, height uint64, product ProductLayout) *KeyedProduct {
	keyedProduct, ok := p.products[pubkey]
	if !ok {
		keyedProduct = &KeyedProduct{
			Key:           pubkey,
			Height:        height,
			ProductLayout: product,
		}
		p.products[pubkey] = keyedProduct
	} else {
		keyedProduct.Height = height
		keyedProduct.ProductLayout = product
	}
	return keyedProduct
}

func (p *Program) upsertPrice(pubkey solana.PublicKey, height uint64, price PriceLayout) *KeyedPrice {
	keyedPrice, ok := p.prices[pubkey]
	if !ok {
		keyedPrice = &KeyedPrice{
			Key:         pubkey,
			Height:      height,
			PriceLayout: price,
		}
		p.prices[pubkey] = keyedPrice
	} else {
		keyedPrice.Height = height
		keyedPrice.PriceLayout = price
	}
	return keyedPrice
}

func (p *Program) buildAccount(account *backend.Account) error {
	if account.Account == nil {
		return fmt.Errorf("cannot get account info, %s", account.PubKey.String())
	}
	if account.Account.Owner != p.id {
		return fmt.Errorf("account %s is not program account, expected: %s, actual: %s", account.PubKey, p.id, account.Account.Owner)
	}
	data := account.Account.Data.GetBinary()
	base := BaseLayout{}
	buf := bytes.NewReader(data[0:BaseLayoutSize])
	err := binary.Read(buf, binary.LittleEndian, &base)
	if err != nil {
		return err
	}
	if base.Type == uint32(ProductType) {
		product := ProductLayout{}
		err = product.unpack(data)
		if err != nil {
			return err
		}
		p.upsertProduct(account.PubKey, account.Height, product)
	} else if base.Type == uint32(PriceType) {
		price := PriceLayout{}
		err = price.unpack(data)
		if err != nil {
			return err
		}
		p.upsertPrice(account.PubKey, account.Height, price)
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

func (p *Program) SubscribePrice(pubkeys []solana.PublicKey, cb UpdateCallback) error {
	accounts, err := p.backend.Accounts(pubkeys)
	if err != nil {
		return err
	}
	p.buildAccounts(accounts)
	//
	for _, pubkey := range pubkeys {
		_, ok := p.prices[pubkey]
		if !ok {
			continue
		}
		sub, ok := p.updateSubs[pubkey]
		if !ok {
			sub = &UpdateSubscribe{}
			p.updateSubs[pubkey] = sub
		}
		sub.cb = append(sub.cb, cb)
	}
	return nil
}

func (p *Program) GetPrice(key solana.PublicKey) *KeyedPrice {
	user, ok := p.prices[key]
	if !ok {
		return nil
	}
	return user
}

func (p *Program) subscribeUpdate() {
	for pubkey, _ := range p.updateSubs {
		p.backend.SubscribeAccount(pubkey, p)
	}
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
			price, ok := p.prices[updateAccount.PubKey]
			if !ok {
				continue
			}
			sub, ok := p.updateSubs[updateAccount.PubKey]
			if !ok {
				continue
			}
			for _, cb := range sub.cb {
				cb.OnUpdate(price)
			}
		case <-p.ctx.Done():
			return
		}
	}
}
