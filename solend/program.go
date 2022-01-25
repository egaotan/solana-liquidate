package solend

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/gagliardetto/solana-go"
	"github.com/solana-liquidate/backend"
	"github.com/solana-liquidate/env"
	"github.com/solana-liquidate/pyth"
	"github.com/solana-liquidate/utils"
	"log"
	"math/big"
	"os"
)

type Program struct {
	ctx               context.Context
	logger            *log.Logger
	backend           *backend.Backend
	env *env.Env
	id                solana.PublicKey
	oracle *pyth.Program
	obligations       map[solana.PublicKey]*KeyedObligation
	reserves          map[solana.PublicKey]*KeyedReserve
	updateAccountChan chan *backend.Account
}

func NewProgram(id solana.PublicKey, ctx context.Context, env *env.Env, be *backend.Backend, oracle *pyth.Program) *Program {
	p := &Program{
		ctx:               ctx,
		logger:            log.Default(),
		backend:           be,
		env: env,
		id:                id,
		oracle: oracle,
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
	err = p.buildModels()
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

func (p *Program) buildModels() error {
	keyCheck := make(map[solana.PublicKey]bool)
	priceKey := make([]solana.PublicKey, 0)
	for _, reserve := range p.reserves {
		_, ok := keyCheck[reserve.ReserveLiquidity.Oracle]
		if !ok {
			priceKey = append(priceKey, reserve.ReserveLiquidity.Oracle)
			keyCheck[reserve.ReserveLiquidity.Oracle] = true
		}
	}
	p.oracle.RetrievePrice(priceKey)
	return nil
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
		err := reserve.unpack(data)
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

func (p *Program) calculateRefreshedObligation(pubkey solana.PublicKey) error {
	obligation, ok := p.obligations[pubkey]
	if !ok {
		return fmt.Errorf("no obligation")
	}
	//
	p.logger.Printf("obligation: %s", obligation.Key.String())
	depositValue := new(big.Float).SetInt64(0)
	borrowValue := new(big.Float).SetInt64(0)
	allowedBorrowValue := new(big.Float).SetInt64(0)
	unhealthyBorrowValue := new(big.Float).SetInt64(0)
	for _, deposit := range obligation.ObligationCollateral {
		reserveKey := deposit.DepositReserve
		reserve, ok := p.reserves[reserveKey]
		if !ok {
			p.logger.Printf("no reserve for obligation: %s", reserveKey.String())
			continue
		}
		oracleKey := reserve.ReserveLiquidity.Oracle
		oracle := p.oracle.GetPrice(oracleKey)
		if oracle == nil {
			p.logger.Printf("no oracle for obligation: %s", oracleKey.String())
			continue
		}
		collateralExchangeRate := reserve.collateralExchangeRate()
		//
		marketValue := new(big.Float).Quo(
			new(big.Float).Mul(
				new(big.Float).Quo(
					new(big.Float).Mul(
						new(big.Float).SetInt64(int64(deposit.DepositedAmount)),
						WAD,
					),
					collateralExchangeRate,
				),
				new(big.Float).SetFloat64(oracle.Twap.Value),
			),
			new(big.Float).SetInt64(int64(oracle.Exponent)),
		)
		loanToValueRate := reserve.loanToValueRate()
		liquidationThresholdRate := reserve.liquidationThresholdRate()
		depositValue = new(big.Float).Add(
			depositValue,
			marketValue,
		)
		allowedBorrowValue = new(big.Float).Add(
			allowedBorrowValue,
			new(big.Float).Mul(
				marketValue,
				loanToValueRate,
			),
		)
		unhealthyBorrowValue = new(big.Float).Add(
			unhealthyBorrowValue,
			new(big.Float).Mul(
				marketValue,
				liquidationThresholdRate,
			),
		)
	}
	//
	for _, borrow := range obligation.ObligationLiquidity {
		reserveKey := borrow.BorrowReserve
		reserve, ok := p.reserves[reserveKey]
		if !ok {
			p.logger.Printf("no reserve for obligation: %s", reserveKey.String())
			continue
		}
		oracleKey := reserve.ReserveLiquidity.Oracle
		oracle := p.oracle.GetPrice(oracleKey)
		if oracle == nil {
			p.logger.Printf("no oracle for obligation: %s", oracleKey.String())
			continue
		}
		borrowAmountWadWithInterest := p.borrowedAmountWadWithInterest(
			reserve.ReserveLiquidity.CumulativeBorrowRate.Value,
			borrow.CumulativeBorrowRate.Value,
			borrow.BorrowedAmount.Value,
		)
		marketValue :=
			new(big.Float).Quo(
				new(big.Float).Mul(
					borrowAmountWadWithInterest,
					new(big.Float).SetFloat64(oracle.Twap.Value),
				),
				new(big.Float).SetUint64(oracle.ValidSlot),
			)
		borrowValue = new(big.Float).Add(borrowValue, marketValue)
	}
	//
	utilicationRatio :=
		new(big.Float).Mul(
			new(big.Float).Quo(borrowValue, depositValue),
			new(big.Float).SetFloat64(100),
		)
	if utilicationRatio.Sign() <= 0 {
		utilicationRatio = new(big.Float).SetInt64(0)
	}
	//
	if borrowValue.Cmp(unhealthyBorrowValue) <= 0 {
		p.logger.Printf("obligation is healthy")
		return nil
	}
	p.logger.Printf("obligation %s is underwater, borrowed value: %s, unhealthy borrow value: %s",
		obligation.Key.String(), borrowValue.String(), unhealthyBorrowValue.String(),
	)
	return nil
}
func (p *Program) borrowedAmountWadWithInterest(
	reserveCumulativeBorrowRateWads *big.Float,
	obligationCumulativeBorrowRateWads *big.Float,
	obligationBorrowAmountWads *big.Float,
	) *big.Float {
	c := reserveCumulativeBorrowRateWads.Cmp(obligationCumulativeBorrowRateWads)
	switch c {
	case -1:
		p.logger.Printf("interest rate cannot be negative")
		return obligationBorrowAmountWads
	case 0:
		return obligationBorrowAmountWads
	case 1:
		r := new(big.Float).Quo(reserveCumulativeBorrowRateWads, obligationBorrowAmountWads)
		return new(big.Float).Mul(obligationBorrowAmountWads, r)
	}
	return nil
}
