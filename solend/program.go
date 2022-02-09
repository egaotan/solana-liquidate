package solend

import (
	"bytes"
	"context"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"github.com/gagliardetto/solana-go"
	"github.com/solana-liquidate/backend"
	"github.com/solana-liquidate/env"
	"github.com/solana-liquidate/program"
	"github.com/solana-liquidate/pyth"
	"github.com/solana-liquidate/utils"
	"log"
	"math/big"
	"os"
	"strings"
	"time"
)

type UpdateInfo struct {
	Key solana.PublicKey
}

type Status struct {
	Ignore bool
	Backup bool
	Id uint64
}

type Program struct {
	ctx               context.Context
	logger            *log.Logger
	backend           *backend.Backend
	env               *env.Env
	id                solana.PublicKey
	flashloan         bool
	usdcAmount        uint64
	threshold uint64
	oracle            *pyth.Program
	lendingMarkets    map[solana.PublicKey]*KeyedLendingMarket
	obligations       map[solana.PublicKey]*KeyedObligation
	reserves          map[solana.PublicKey]*KeyedReserve
	updateAccountChan chan *backend.Account
	updated           chan *UpdateInfo
	cached map[solana.PublicKey]uint64
	ignore map[solana.PublicKey]*Status
}

func NewProgram(id solana.PublicKey, ctx context.Context, env *env.Env, be *backend.Backend, flashloan bool, oracle *pyth.Program, usdcAmount uint64, threshold uint64) *Program {
	p := &Program{
		ctx:               ctx,
		logger:            log.Default(),
		backend:           be,
		env:               env,
		id:                id,
		flashloan:         flashloan,
		usdcAmount: usdcAmount,
		threshold: threshold,
		oracle:            oracle,
		lendingMarkets:    make(map[solana.PublicKey]*KeyedLendingMarket),
		obligations:       make(map[solana.PublicKey]*KeyedObligation),
		reserves:          make(map[solana.PublicKey]*KeyedReserve),
		updateAccountChan: make(chan *backend.Account, 1024),
		updated:           make(chan *UpdateInfo, 1024),
		cached: make(map[solana.PublicKey]uint64),
		ignore: make(map[solana.PublicKey]*Status),
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
	go p.Calculate()
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

func (p *Program) upsertLendingMarket(pubkey solana.PublicKey, height uint64, lendingMarket LendingMarketLayout) *KeyedLendingMarket {
	keyedLendingMarket, ok := p.lendingMarkets[pubkey]
	if !ok {
		keyedLendingMarket = &KeyedLendingMarket{
			Key:                 pubkey,
			Height:              height,
			LendingMarketLayout: lendingMarket,
		}
		p.lendingMarkets[pubkey] = keyedLendingMarket
	} else {
		keyedLendingMarket.Height = height
		keyedLendingMarket.LendingMarketLayout = lendingMarket
	}
	return keyedLendingMarket
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
	p.oracle.SubscribePrice(priceKey, p)
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
		if obligation.LendingMarket.IsZero() {
			return fmt.Errorf("invalid obligation")
		}
		p.upsertObligation(account.PubKey, account.Height, obligation)
	} else if len(data) == ReserveLayoutSize {
		reserve := ReserveLayout{}
		err := reserve.unpack(data)
		if err != nil {
			return err
		}
		if reserve.LendingMarket.IsZero() {
			return fmt.Errorf("invalid reserve")
		}
		if strings.Index(reserve.ReserveLiquidity.Oracle.String(), "11111111") != -1 {
			return fmt.Errorf("invalid oracle")
		}
		p.upsertReserve(account.PubKey, account.Height, reserve)
	} else if len(data) == LendingMarketLayoutSize {
		lendingMarket := LendingMarketLayout{}
		buf := bytes.NewReader(data)
		err := binary.Read(buf, binary.LittleEndian, &lendingMarket)
		if err != nil {
			return err
		}
		p.upsertLendingMarket(account.PubKey, account.Height, lendingMarket)
	} else {
		return fmt.Errorf("unused account, data length: %d", len(data))
	}
	return nil
}

func (p *Program) buildAccounts(accounts []*backend.Account) error {
	for _, account := range accounts {
		err := p.buildAccount(account)
		p.ignore[account.PubKey] = &Status{
			Ignore: false,
			Backup: false,
			Id: 0,
		}
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
			_, ok := p.obligations[updateAccount.PubKey]
			if !ok {
				continue
			}
			status, ok := p.ignore[updateAccount.PubKey]
			if !ok {
				continue
			}
			p.buildAccount(updateAccount)
			status.Ignore = false
		case <-p.ctx.Done():
			return
		}
	}
}

func (p *Program) OnUpdate(price *pyth.KeyedPrice) error {
	p.updated <- &UpdateInfo{
	}
	return nil
}

func (p *Program) Calculate() {
	for {
		select {
		case info := <-p.updated:
		L:
			for {
				select {
				case info = <-p.updated:
				default:
					break L
				}
			}
			p.calculate(info)
		}
	}
}

func (p *Program) calculate(info *UpdateInfo) {
	for k, _ := range p.obligations {
		err := p.calculateRefreshedObligation(k)
		if err != nil {
			p.logger.Printf("1 %s %s", k.String(), err.Error())
			status, _ := p.ignore[k]
			status.Ignore = true
		}
	}
}

func (p *Program) calculateRefreshedObligation(pubkey solana.PublicKey) error {
	ig, ok := p.ignore[pubkey]
	if ok && ig.Ignore {
		return nil
	}
	newId := time.Now().UnixNano() / 1000
	if ok && ig.Backup && uint64(newId) - ig.Id < 1000000 * 60 * 5 {
		return nil
	}
	ig.Backup = false
	ig.Id = uint64(newId)

	cachedId, ok := p.cached[pubkey]
	if ok && uint64(newId) - cachedId < uint64(100000) {
		return fmt.Errorf("too much in 100 milliseconds")
	}
	obligation, ok := p.obligations[pubkey]
	if !ok {
		return fmt.Errorf("no obligation")
	}
	//
	//p.logger.Printf("obligation: %s", obligation.Key.String())
	/*
		for _, deposit := range obligation.ObligationCollateral {
			reserveKey := deposit.DepositReserve
			reserve, ok := p.reserves[reserveKey]
			if !ok {
				return fmt.Errorf("no reserve for obligation: %s", reserveKey.String())
			}
			token := p.env.Token(reserve.ReserveLiquidity.Mint)
			if token == nil {
				return fmt.Errorf("no token info, token: %s", reserve.ReserveLiquidity.Mint.String())
			}
		}
		for _, borrow := range obligation.ObligationLiquidity {
			reserveKey := borrow.BorrowReserve
			reserve, ok := p.reserves[reserveKey]
			if !ok {
				return fmt.Errorf("no reserve for obligation: %s", reserveKey.String())
			}
			token := p.env.Token(reserve.ReserveLiquidity.Mint)
			if token == nil {
				return fmt.Errorf("no token info, token: %s", reserve.ReserveLiquidity.Mint.String())
			}
		}
	*/
	depositValue := new(big.Float).SetInt64(0)
	borrowValue := new(big.Float).SetInt64(0)
	allowedBorrowValue := new(big.Float).SetInt64(0)
	unhealthyBorrowValue := new(big.Float).SetInt64(0)
	selectedDeposit := -1
	//selectedDepositMarketValue := &big.Float{}
	selectedBorrow := -1
	//selectedBorrowMarketValue := &big.Float{}
	selectedUSDCDeposit := -1
	selectedUSDCDepositMarketValue := &big.Float{}
	selectedUSDCBorrow := -1
	selectedUSDCBorrowMarketValue := &big.Float{}
	for i, deposit := range obligation.ObligationCollateral {
		//
		reserveKey := deposit.DepositReserve
		reserve, ok := p.reserves[reserveKey]
		if !ok {
			return fmt.Errorf("no reserve for obligation: %s", reserveKey.String())
		}
		//
		oracleKey := reserve.ReserveLiquidity.Oracle
		oracle := p.oracle.GetPrice(oracleKey)
		if oracle == nil {
			return fmt.Errorf("no oracle for obligation: %s", oracleKey.String())
		}
		//
		token := p.env.Token(reserve.ReserveLiquidity.Mint)
		if token == nil {
			return fmt.Errorf("no token info, token: %s", reserve.ReserveLiquidity.Mint.String())
		}
		//
		collateralExchangeRate := reserve.collateralExchangeRate()
		loanToValueRate := reserve.loanToValueRate()
		liquidationThresholdRate := reserve.liquidationThresholdRate()
		//
		marketValue :=
			new(big.Float).Quo(
				new(big.Float).Mul(
					new(big.Float).Quo(
						new(big.Float).SetInt64(int64(deposit.DepositedAmount)),
						collateralExchangeRate,
					),
					new(big.Float).SetFloat64(oracle.Price),
				),
				new(big.Float).SetInt64(int64(token.Decimal)),
			)
		//
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
		//
		collateralUser := p.env.TokenUser(reserve.ReserveCollateral.Mint)
		if !collateralUser.IsZero() {
			if selectedDeposit == -1 {
				selectedDeposit = i
				//selectedDepositMarketValue = marketValue
			} else {
				if marketValue.Cmp(obligation.ObligationCollateral[selectedDeposit].MarketValue.Value) > 0 {
					selectedDeposit = i
					//selectedDepositMarketValue = marketValue
				}
			}
		}
		if reserve.ReserveLiquidity.Mint == program.USDC {
			if selectedUSDCDeposit == -1 {
				selectedUSDCDeposit = i
				selectedUSDCDepositMarketValue = marketValue
			} else {
				if marketValue.Cmp(obligation.ObligationCollateral[selectedUSDCDeposit].MarketValue.Value) > 0 {
					selectedUSDCDeposit = i
					selectedUSDCDepositMarketValue = marketValue
				}
			}
		}
	}
	//
	for i, borrow := range obligation.ObligationLiquidity {
		//
		reserveKey := borrow.BorrowReserve
		reserve, ok := p.reserves[reserveKey]
		if !ok {
			return fmt.Errorf("no reserve for obligation: %s", reserveKey.String())
		}
		//
		oracleKey := reserve.ReserveLiquidity.Oracle
		oracle := p.oracle.GetPrice(oracleKey)
		if oracle == nil {
			return fmt.Errorf("no oracle for obligation: %s", oracleKey.String())
		}
		//
		token := p.env.Token(reserve.ReserveLiquidity.Mint)
		if token == nil {
			return fmt.Errorf("no token info, token: %s", reserve.ReserveLiquidity.Mint.String())
		}
		borrowAmountWithInterest := p.borrowedAmountWadWithInterest(
			obligation.Key,
			reserve.ReserveLiquidity.CumulativeBorrowRate.Value,
			borrow.CumulativeBorrowRate.Value,
			borrow.BorrowedAmount.Value,
		)
		//
		marketValue :=
			new(big.Float).Quo(
				new(big.Float).Mul(
					borrowAmountWithInterest,
					new(big.Float).SetFloat64(oracle.Price),
				),
				new(big.Float).SetUint64(token.Decimal),
			)
		//
		borrowValue = new(big.Float).Add(borrowValue, marketValue)
		//
		borrowUser := p.env.TokenUser(reserve.ReserveLiquidity.Mint)
		if !borrowUser.IsZero() {
			if selectedBorrow == -1 {
				selectedBorrow = i
				//selectedBorrowMarketValue = marketValue
			} else {
				if marketValue.Cmp(obligation.ObligationLiquidity[selectedBorrow].MarketValue.Value) > 0 {
					selectedBorrow = i
					//selectedBorrowMarketValue = marketValue
				}
			}
		}
		if reserve.ReserveLiquidity.Mint == program.USDC {
			if selectedUSDCBorrow == -1 {
				selectedUSDCBorrow = i
				selectedUSDCBorrowMarketValue = marketValue
			} else {
				if marketValue.Cmp(obligation.ObligationCollateral[selectedUSDCBorrow].MarketValue.Value) > 0 {
					selectedUSDCBorrow = i
					selectedUSDCBorrowMarketValue = marketValue
				}
			}
		}
	}
	/*
	if depositValue.Sign() <= 0 || borrowValue.Sign() <= 0 {
		return fmt.Errorf("value is invalid")
	}
	//
	utilizationRatio :=
		new(big.Float).Mul(
			new(big.Float).Quo(borrowValuno reserve for obligatione, depositValue),
			new(big.Float).SetFloat64(100),
		)
	if utilizationRatio.Sign() <= 0 {
		utilizationRatio = new(big.Float).SetInt64(0)
	}
	 */
	//
	p.logger.Printf("obligation %s, borrowed value: %s, unhealthy borrow value: %s",
		obligation.Key.String(), borrowValue.String(), unhealthyBorrowValue.String(),
	)

	threshold := new(big.Float).SetUint64(p.threshold * 2)
	if depositValue.Cmp(threshold) <= 0 || borrowValue.Cmp(threshold) <= 0 {
		status, _ := p.ignore[obligation.Key]
		status.Ignore = true
		return nil
	}
	/*
	p.logger.Printf("obligation: %s deposit value：%s, borrow value: %s, allowed borrow value: %s, unhealthy borrow value: %s",
		obligation.Key.String(), depositValue.String(), borrowValue.String(), allowedBorrowValue.String(), unhealthyBorrowValue.String())
	*/

	p.logger.Printf("====== obligation %s, borrowed value: %s, unhealthy borrow value: %s",
		obligation.Key.String(), borrowValue.String(), unhealthyBorrowValue.String(),
	)

	checkedBorrow := new(big.Float).Mul(borrowValue, new(big.Float).SetFloat64(1.1))
	if checkedBorrow.Cmp(unhealthyBorrowValue) <= 0 {
		status, _ := p.ignore[obligation.Key]
		status.Backup = true
		return nil
	}

	//
	if borrowValue.Cmp(unhealthyBorrowValue) <= 0 {
		p.logger.Printf("obligation is healthy")
		return nil
	}
	p.logger.Printf("obligation %s is underwater, borrowed value: %s, unhealthy borrow value: %s",
		obligation.Key.String(), borrowValue.String(), unhealthyBorrowValue.String(),
	)
	//
	selected1 := -1
	selected2 := -1
	if p.flashloan {
		if selectedUSDCBorrow == -1 {
			selected1 = selectedUSDCDeposit
			selected2 = selectedBorrow
		} else if selectedUSDCDeposit == -1 {
			selected1 = selectedDeposit
			selected2 = selectedUSDCBorrow
		} else {
			if selectedUSDCBorrowMarketValue.Cmp(selectedUSDCDepositMarketValue) > 0 {
				selected1 = selectedDeposit
				selected2 = selectedUSDCBorrow
			} else {
				selected1 = selectedUSDCDeposit
				selected2 = selectedBorrow
			}
		}
	} else {
		if selectedUSDCBorrow == -1 || selectedDeposit == -1 {
			return fmt.Errorf("no usdc deposit or borrow found")
		}
		selected1 = selectedDeposit
		selected2 = selectedUSDCBorrow
	}
	if selected1 == -1 || selected2 == -1 {
		return fmt.Errorf("cannot select deposit or borrow")
	}
	xx := obligation.ObligationLiquidity[selected2].BorrowedAmount.Value
	if xx.Cmp(new(big.Float).SetUint64(p.threshold)) < 0 {
		return fmt.Errorf("too small borrowed value")
	}
	err := p.liquidateObligation(obligation, selected1, selected2)
	if err != nil {
		return err
	}
	return nil
}

func (p *Program) liquidateObligationLocal(obligation *KeyedObligation, selectDeposit int, selectBorrow int) error {
	ins := make([]solana.Instruction, 0)
	depositKeys := make([]solana.PublicKey, 0)
	borrowKeys := make([]solana.PublicKey, 0)
	reserves := make(map[solana.PublicKey]*KeyedReserve)
	// refresh all reserves
	for _, deposit := range obligation.ObligationCollateral {
		reserveKey := deposit.DepositReserve
		reserve, ok := p.reserves[reserveKey]
		if !ok {
			return fmt.Errorf("no reserve for key: %s", reserveKey.String())
		}
		_, ok = reserves[reserveKey]
		if ok {
			continue
		}
		in, err := p.InstructionRefreshReserve(reserve.Key, reserve.ReserveLiquidity.Oracle, reserve.ReserveLiquidity.SwitchBoardOracle)
		if err != nil {
			return err
		}
		ins = append(ins, in)
		depositKeys = append(depositKeys, reserve.Key)
		reserves[reserveKey] = reserve
	}
	for _, borrow := range obligation.ObligationLiquidity {
		reserveKey := borrow.BorrowReserve
		reserve, ok := p.reserves[reserveKey]
		if !ok {
			return fmt.Errorf("no reserve for key: %s", reserveKey.String())
		}
		_, ok = reserves[reserveKey]
		if ok {
			continue
		}
		in, err := p.InstructionRefreshReserve(reserve.Key, reserve.ReserveLiquidity.Oracle, reserve.ReserveLiquidity.SwitchBoardOracle)
		if err != nil {
			return err
		}
		ins = append(ins, in)
		borrowKeys = append(borrowKeys, reserve.Key)
		reserves[reserveKey] = reserve
	}
	// refresh obligation
	in, err := p.InstructionRefreshObligation(obligation.Key, depositKeys, borrowKeys)
	if err != nil {
		return err
	}
	ins = append(ins, in)
	//
	// liquidate
	repayReserveKey := obligation.ObligationLiquidity[selectBorrow].BorrowReserve
	repayReserve, ok := p.reserves[repayReserveKey]
	if !ok {
		return fmt.Errorf("no reserve for obligation: %s", repayReserveKey.String())
	}
	repay := p.env.TokenUser(repayReserve.ReserveLiquidity.Mint)
	if repay.IsZero() {
		return fmt.Errorf("no token user: %s", repayReserve.ReserveLiquidity.Mint.String())
	}
	owner := p.env.UsersOwner(repay)
	if owner.IsZero() {
		return fmt.Errorf("no user owner: %s", repay.String())
	}
	withdrawReserveKey := obligation.ObligationCollateral[selectDeposit].DepositReserve
	withdrawReserve, ok := p.reserves[withdrawReserveKey]
	if !ok {
		return fmt.Errorf("no reserve for obligation: %s", withdrawReserveKey.String())
	}
	withdraw := p.env.TokenUser(withdrawReserve.ReserveCollateral.Mint)
	if withdraw.IsZero() {
		return fmt.Errorf("no token user: %s", withdrawReserve.ReserveCollateral.Mint.String())
	}
	lendingMarket, ok := p.lendingMarkets[obligation.LendingMarket]
	if !ok {
		return fmt.Errorf("no lending market: %s", obligation.LendingMarket.String())
	}
	seed := make([]byte, 1)
	seed[0] = lendingMarket.BumpSeed
	authority, err := solana.CreateProgramAddress([][]byte{obligation.LendingMarket.Bytes(), seed}, p.id)
	if err != nil {
		return fmt.Errorf("hahahahahaha")
	}
	in, err = p.InstructionLiquidateObligation(
		uint64(1000000), repay, withdraw, repayReserve.Key, repayReserve.ReserveLiquidity.Supply,
		withdrawReserve.Key, withdrawReserve.ReserveCollateral.Supply, obligation.Key, obligation.LendingMarket, authority, owner)
	if err != nil {
		return err
	}
	ins = append(ins, in)
	// redeem collateral
	withdrawOwner := p.env.UsersOwner(withdraw)
	if owner.IsZero() {
		return fmt.Errorf("no user owner: %s", repay.String())
	}
	withdrawLiquidity := p.env.TokenUser(withdrawReserve.ReserveLiquidity.Mint)
	if withdrawLiquidity.IsZero() {
		return fmt.Errorf("no token user: %s", withdrawReserve.ReserveLiquidity.Mint.String())
	}
	in, err = p.InstructionRedeemReserveCollateral(
		uint64(100000), withdraw, withdrawLiquidity, withdrawReserve.Key, withdrawReserve.ReserveCollateral.Mint,
		withdrawReserve.ReserveLiquidity.Supply, lendingMarket.Key, authority, withdrawOwner)
	if err != nil {
		return err
	}
	ins = append(ins, in)
	//
	id := time.Now().UnixNano() / 1000
	p.backend.Commit(0, uint64(id), ins, true, nil)
	p.cached[obligation.Key] = uint64(id)
	return nil
}

func (p *Program) liquidateObligation(obligation *KeyedObligation, selectDeposit int, selectBorrow int) error {
	ins := make([]solana.Instruction, 0)
	depositKeys := make([]solana.PublicKey, 0)
	borrowKeys := make([]solana.PublicKey, 0)
	reserves := make(map[solana.PublicKey]*KeyedReserve)
	// refresh all reserves
	for _, deposit := range obligation.ObligationCollateral {
		reserveKey := deposit.DepositReserve
		reserve, ok := p.reserves[reserveKey]
		if !ok {
			return fmt.Errorf("no reserve for key: %s", reserveKey.String())
		}
		depositKeys = append(depositKeys, reserve.Key)
		_, ok = reserves[reserveKey]
		if ok {
			continue
		}
		in, err := p.InstructionRefreshReserve(reserve.Key, reserve.ReserveLiquidity.Oracle, reserve.ReserveLiquidity.SwitchBoardOracle)
		if err != nil {
			return err
		}
		ins = append(ins, in)
		reserves[reserveKey] = reserve
	}
	for _, borrow := range obligation.ObligationLiquidity {
		reserveKey := borrow.BorrowReserve
		reserve, ok := p.reserves[reserveKey]
		if !ok {
			return fmt.Errorf("no reserve for key: %s", reserveKey.String())
		}
		borrowKeys = append(borrowKeys, reserve.Key)
		_, ok = reserves[reserveKey]
		if ok {
			continue
		}
		in, err := p.InstructionRefreshReserve(reserve.Key, reserve.ReserveLiquidity.Oracle, reserve.ReserveLiquidity.SwitchBoardOracle)
		if err != nil {
			return err
		}
		ins = append(ins, in)
		reserves[reserveKey] = reserve
	}
	// refresh obligation
	in, err := p.InstructionRefreshObligation(obligation.Key, depositKeys, borrowKeys)
	if err != nil {
		return err
	}
	ins = append(ins, in)
	//
	// liquidate
	repayReserveKey := obligation.ObligationLiquidity[selectBorrow].BorrowReserve
	repayReserve, ok := p.reserves[repayReserveKey]
	if !ok {
		return fmt.Errorf("no reserve for obligation: %s", repayReserveKey.String())
	}
	repay := p.env.TokenUser(repayReserve.ReserveLiquidity.Mint)
	if repay.IsZero() {
		return fmt.Errorf("no token user: %s", repayReserve.ReserveLiquidity.Mint.String())
	}
	owner := p.env.UsersOwner(repay)
	if owner.IsZero() {
		return fmt.Errorf("no user owner: %s", repay.String())
	}
	withdrawReserveKey := obligation.ObligationCollateral[selectDeposit].DepositReserve
	withdrawReserve, ok := p.reserves[withdrawReserveKey]
	if !ok {
		return fmt.Errorf("no reserve for obligation: %s", withdrawReserveKey.String())
	}
	withdrawCollateral := p.env.TokenUser(withdrawReserve.ReserveCollateral.Mint)
	if withdrawCollateral.IsZero() {
		return fmt.Errorf("no token user: %s", withdrawReserve.ReserveCollateral.Mint.String())
	}
	lendingMarket, ok := p.lendingMarkets[obligation.LendingMarket]
	if !ok {
		return fmt.Errorf("no lending market: %s", obligation.LendingMarket.String())
	}
	seed := make([]byte, 1)
	seed[0] = lendingMarket.BumpSeed
	lendingMarketAuthority, err := solana.CreateProgramAddress([][]byte{obligation.LendingMarket.Bytes(), seed}, p.id)
	if err != nil {
		return fmt.Errorf("hahahahahaha")
	}
	withdraw := p.env.TokenUser(withdrawReserve.ReserveLiquidity.Mint)
	if withdraw.IsZero() {
		return fmt.Errorf("no token user: %s", withdrawReserve.ReserveLiquidity.Mint.String())
	}
	swap := p.env.FindMarket([]solana.PublicKey{withdrawReserve.ReserveLiquidity.Mint, repayReserve.ReserveLiquidity.Mint})
	if swap == nil {
		return fmt.Errorf("no swap market")
	}
	swapAuthority, _, err := solana.FindProgramAddress([][]byte{swap.Key.Bytes()}, program.Swap)
	if err != nil {
		return err
	}
	swapSrc := swap.SwapA
	swapDst := swap.SwapB
	if withdrawReserve.ReserveLiquidity.Mint == swap.TokenB {
		swapSrc = swap.SwapB
		swapDst = swap.SwapA
	}
	if p.flashloan {
		in, err = p.InstructionLiquidateWithFlashloan(uint64(0), repayReserve.ReserveLiquidity.Supply, repay, repayReserve.Key,
			repayReserve.ReserveConfig.FeeReceiver, repay, lendingMarket.Key, lendingMarketAuthority, program.Token, program.Liquidate,
			withdrawCollateral, withdraw, withdrawReserve.Key, withdrawReserve.ReserveCollateral.Supply, withdrawReserve.ReserveCollateral.Mint,
			withdrawReserve.ReserveLiquidity.Supply, program.Solend, obligation.Key, program.Swap, swap.Key, swapAuthority,
			swapSrc, swapDst, swap.PoolToken, swap.PoolFeeAccount, owner)
		if err != nil {
			return err
		}
	} else {
		in, err = p.InstructionLiquidate(p.usdcAmount, repayReserve.ReserveLiquidity.Supply, repay, repayReserve.Key,
			lendingMarket.Key, lendingMarketAuthority, program.Token,
			withdrawCollateral, withdraw, withdrawReserve.Key, withdrawReserve.ReserveCollateral.Supply, withdrawReserve.ReserveCollateral.Mint,
			withdrawReserve.ReserveLiquidity.Supply, program.Solend, obligation.Key, program.Swap, swap.Key, swapAuthority,
			swapSrc, swapDst, swap.PoolToken, swap.PoolFeeAccount, owner)
		if err != nil {
			return err
		}
	}
	ins = append(ins, in)
	//
	id := time.Now().UnixNano() / 1000
	p.backend.Commit(0, uint64(id), ins, false, nil)
	p.cached[obligation.Key] = uint64(id)
	return nil
}

func (p *Program) InstructionRefreshReserve(reserve solana.PublicKey, oracle solana.PublicKey, switchboardFeedAddress solana.PublicKey) (solana.Instruction, error) {
	data := make([]byte, 1)
	data[0] = 3
	instruction := &program.Instruction{
		IsAccounts: []*solana.AccountMeta{
			{PublicKey: reserve, IsSigner: false, IsWritable: true},
			{PublicKey: oracle, IsSigner: false, IsWritable: false},
		},
		IsData:      data,
		IsProgramID: p.id,
	}
	if !switchboardFeedAddress.IsZero() {
		instruction.IsAccounts = append(instruction.IsAccounts,
			&solana.AccountMeta{PublicKey: switchboardFeedAddress, IsSigner: false, IsWritable: false})
	}
	instruction.IsAccounts = append(instruction.IsAccounts,
		&solana.AccountMeta{PublicKey: program.SysClock, IsSigner: false, IsWritable: false})
	return instruction, nil
}

func (p *Program) InstructionRefreshObligation(obligation solana.PublicKey, deposits []solana.PublicKey, borrows []solana.PublicKey) (solana.Instruction, error) {
	data := make([]byte, 1)
	data[0] = 7
	instruction := &program.Instruction{
		IsAccounts: []*solana.AccountMeta{
			{PublicKey: obligation, IsSigner: false, IsWritable: true},
			{PublicKey: program.SysClock, IsSigner: false, IsWritable: false},
		},
		IsData:      data,
		IsProgramID: p.id,
	}
	//
	for _, deposit := range deposits {
		instruction.IsAccounts = append(instruction.IsAccounts,
			&solana.AccountMeta{PublicKey: deposit, IsSigner: false, IsWritable: false})
	}
	for _, borrow := range borrows {
		instruction.IsAccounts = append(instruction.IsAccounts,
			&solana.AccountMeta{PublicKey: borrow, IsSigner: false, IsWritable: false})
	}
	return instruction, nil
}

func (p *Program) InstructionLiquidateObligation(
	amount uint64, repayAccount solana.PublicKey, withdrawAccount solana.PublicKey,
	repayReserve solana.PublicKey, repaySupply solana.PublicKey,
	withdrawReserve solana.PublicKey, withdrawSupply solana.PublicKey,
	obligation solana.PublicKey, lendingMarket solana.PublicKey, authority solana.PublicKey,
	player solana.PublicKey,
) (solana.Instruction, error) {
	data := make([]byte, 9)
	data[0] = 12
	binary.LittleEndian.PutUint64(data[1:], amount)
	instruction := &program.Instruction{
		IsAccounts: []*solana.AccountMeta{
			{PublicKey: repayAccount, IsSigner: false, IsWritable: true},
			{PublicKey: withdrawAccount, IsSigner: false, IsWritable: true},
			{PublicKey: repayReserve, IsSigner: false, IsWritable: true},
			{PublicKey: repaySupply, IsSigner: false, IsWritable: true},
			{PublicKey: withdrawReserve, IsSigner: false, IsWritable: false},
			{PublicKey: withdrawSupply, IsSigner: false, IsWritable: true},
			{PublicKey: obligation, IsSigner: false, IsWritable: true},
			{PublicKey: lendingMarket, IsSigner: false, IsWritable: false},
			{PublicKey: authority, IsSigner: false, IsWritable: false},
			{PublicKey: player, IsSigner: true, IsWritable: false},
			{PublicKey: program.SysClock, IsSigner: false, IsWritable: false},
			{PublicKey: program.Token, IsSigner: false, IsWritable: false},
		},
		IsData:      data,
		IsProgramID: p.id,
	}
	return instruction, nil
}

func (p *Program) InstructionRedeemReserveCollateral(amount uint64,
	srcCollateralAccount solana.PublicKey, dstLiquidityAccount solana.PublicKey,
	reserve solana.PublicKey, reserveCollateralMint solana.PublicKey, reserveLiquiditySupply solana.PublicKey,
	lendingMarket solana.PublicKey, authority solana.PublicKey,
	owner solana.PublicKey) (solana.Instruction, error) {
	data := make([]byte, 9)
	data[0] = 5
	binary.LittleEndian.PutUint64(data[1:], amount)
	instruction := &program.Instruction{
		IsAccounts: []*solana.AccountMeta{
			{PublicKey: srcCollateralAccount, IsSigner: false, IsWritable: true},
			{PublicKey: dstLiquidityAccount, IsSigner: false, IsWritable: true},
			{PublicKey: reserve, IsSigner: false, IsWritable: true},
			{PublicKey: reserveCollateralMint, IsSigner: false, IsWritable: true},
			{PublicKey: reserveLiquiditySupply, IsSigner: false, IsWritable: true},
			{PublicKey: lendingMarket, IsSigner: false, IsWritable: false},
			{PublicKey: authority, IsSigner: false, IsWritable: false},
			{PublicKey: owner, IsSigner: true, IsWritable: false},
			{PublicKey: program.SysClock, IsSigner: false, IsWritable: false},
			{PublicKey: program.Token, IsSigner: false, IsWritable: false},
		},
		IsData:      data,
		IsProgramID: p.id,
	}
	return instruction, nil
}

func (p *Program) InstructionLiquidateWithFlashloan(amount uint64,
	repaySupply solana.PublicKey, repay solana.PublicKey, repayReserve solana.PublicKey,
	repayReserveLiquidityFee solana.PublicKey, repayReserveLiquidityHostFee solana.PublicKey,
	lendingMarket solana.PublicKey, lendingMarketAuthority solana.PublicKey, tokenProgram solana.PublicKey,
	flashloanReceiveProgram solana.PublicKey,
	withdrawCollateral solana.PublicKey, withdraw solana.PublicKey,
	withdrawReserve solana.PublicKey, withdrawCollateralSupply solana.PublicKey,
	withdrawCollateralMint solana.PublicKey, withdrawLiquiditySupply solana.PublicKey,
	solendProgram solana.PublicKey, obligation solana.PublicKey,
	swapProgram solana.PublicKey, swapMarket solana.PublicKey, swapMarketAuthority solana.PublicKey,
	swapSource solana.PublicKey, swapDestination solana.PublicKey,
	swapPoolMint solana.PublicKey, swapFee solana.PublicKey, owner solana.PublicKey) (solana.Instruction, error) {
	data := make([]byte, 9)
	data[0] = 13
	binary.LittleEndian.PutUint64(data[1:], 0)
	instruction := &program.Instruction{
		IsAccounts: []*solana.AccountMeta{
			{PublicKey: repaySupply, IsSigner: false, IsWritable: true},
			{PublicKey: repay, IsSigner: false, IsWritable: true},
			{PublicKey: repayReserve, IsSigner: false, IsWritable: true},
			{PublicKey: repayReserveLiquidityFee, IsSigner: false, IsWritable: true},
			{PublicKey: repayReserveLiquidityHostFee, IsSigner: false, IsWritable: true},
			{PublicKey: lendingMarket, IsSigner: false, IsWritable: false},
			{PublicKey: lendingMarketAuthority, IsSigner: false, IsWritable: false},
			{PublicKey: tokenProgram, IsSigner: false, IsWritable: false},
			{PublicKey: flashloanReceiveProgram, IsSigner: false, IsWritable: false},
			{PublicKey: withdrawCollateral, IsSigner: false, IsWritable: true},
			{PublicKey: withdraw, IsSigner: false, IsWritable: true},
			{PublicKey: repayReserve, IsSigner: false, IsWritable: true},
			{PublicKey: withdrawReserve, IsSigner: false, IsWritable: true},
			{PublicKey: withdrawCollateralSupply, IsSigner: false, IsWritable: true},
			{PublicKey: withdrawCollateralMint, IsSigner: false, IsWritable: true},
			{PublicKey: withdrawLiquiditySupply, IsSigner: false, IsWritable: true},
			{PublicKey: solendProgram, IsSigner: false, IsWritable: false},
			{PublicKey: obligation, IsSigner: false, IsWritable: true},
			{PublicKey: lendingMarket, IsSigner: false, IsWritable: false},
			{PublicKey: lendingMarketAuthority, IsSigner: false, IsWritable: false},
			{PublicKey: swapProgram, IsSigner: false, IsWritable: false},
			{PublicKey: swapMarket, IsSigner: false, IsWritable: true},
			{PublicKey: swapMarketAuthority, IsSigner: false, IsWritable: false},
			{PublicKey: swapSource, IsSigner: false, IsWritable: true},
			{PublicKey: swapDestination, IsSigner: false, IsWritable: true},
			{PublicKey: swapPoolMint, IsSigner: false, IsWritable: true},
			{PublicKey: swapFee, IsSigner: false, IsWritable: true},
			{PublicKey: owner, IsSigner: true, IsWritable: false},
			{PublicKey: program.SysClock, IsSigner: false, IsWritable: false},
		},
		IsData:      data,
		IsProgramID: p.id,
	}
	return instruction, nil
}

func (p *Program) InstructionLiquidate(amount uint64,
	repaySupply solana.PublicKey, repay solana.PublicKey, repayReserve solana.PublicKey,
	lendingMarket solana.PublicKey, lendingMarketAuthority solana.PublicKey, tokenProgram solana.PublicKey,
	withdrawCollateral solana.PublicKey, withdraw solana.PublicKey,
	withdrawReserve solana.PublicKey, withdrawCollateralSupply solana.PublicKey,
	withdrawCollateralMint solana.PublicKey, withdrawLiquiditySupply solana.PublicKey,
	solendProgram solana.PublicKey, obligation solana.PublicKey,
	swapProgram solana.PublicKey, swapMarket solana.PublicKey, swapMarketAuthority solana.PublicKey,
	swapSource solana.PublicKey, swapDestination solana.PublicKey,
	swapPoolMint solana.PublicKey, swapFee solana.PublicKey, owner solana.PublicKey) (solana.Instruction, error) {
	data := make([]byte, 9)
	// very dangerous here
	data[0] = 1
	binary.LittleEndian.PutUint64(data[1:], amount)
	instruction := &program.Instruction{
		IsAccounts: []*solana.AccountMeta{
			{PublicKey: repay, IsSigner: false, IsWritable: true},
			{PublicKey: repaySupply, IsSigner: false, IsWritable: true},
			{PublicKey: tokenProgram, IsSigner: false, IsWritable: false},
			{PublicKey: withdrawCollateral, IsSigner: false, IsWritable: true},
			{PublicKey: withdraw, IsSigner: false, IsWritable: true},
			{PublicKey: repayReserve, IsSigner: false, IsWritable: true},
			{PublicKey: withdrawReserve, IsSigner: false, IsWritable: true},
			{PublicKey: withdrawCollateralSupply, IsSigner: false, IsWritable: true},
			{PublicKey: withdrawCollateralMint, IsSigner: false, IsWritable: true},
			{PublicKey: withdrawLiquiditySupply, IsSigner: false, IsWritable: true},
			{PublicKey: solendProgram, IsSigner: false, IsWritable: false},
			{PublicKey: obligation, IsSigner: false, IsWritable: true},
			{PublicKey: lendingMarket, IsSigner: false, IsWritable: false},
			{PublicKey: lendingMarketAuthority, IsSigner: false, IsWritable: false},
			{PublicKey: swapProgram, IsSigner: false, IsWritable: false},
			{PublicKey: swapMarket, IsSigner: false, IsWritable: true},
			{PublicKey: swapMarketAuthority, IsSigner: false, IsWritable: false},
			{PublicKey: swapSource, IsSigner: false, IsWritable: true},
			{PublicKey: swapDestination, IsSigner: false, IsWritable: true},
			{PublicKey: swapPoolMint, IsSigner: false, IsWritable: true},
			{PublicKey: swapFee, IsSigner: false, IsWritable: true},
			{PublicKey: owner, IsSigner: true, IsWritable: false},
			{PublicKey: program.SysClock, IsSigner: false, IsWritable: false},
		},
		IsData:      data,
		IsProgramID: program.Liquidate,
	}
	return instruction, nil
}

func (p *Program) borrowedAmountWadWithInterest(
	key solana.PublicKey,
	reserveCumulativeBorrowRate *big.Float,
	obligationCumulativeBorrowRate *big.Float,
	obligationBorrowAmount *big.Float,
) *big.Float {
	c := reserveCumulativeBorrowRate.Cmp(obligationCumulativeBorrowRate)
	switch c {
	case -1:
		p.logger.Printf("interest rate cannot be negative, %s", key.String())
		return obligationBorrowAmount
	case 0:
		return obligationBorrowAmount
	case 1:
		r := new(big.Float).Quo(reserveCumulativeBorrowRate, obligationCumulativeBorrowRate)
		return new(big.Float).Mul(obligationBorrowAmount, r)
	}
	return nil
}
