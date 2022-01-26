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
	"time"
)

type Program struct {
	ctx               context.Context
	logger            *log.Logger
	backend           *backend.Backend
	env               *env.Env
	id                solana.PublicKey
	oracle            *pyth.Program
	lendingMarkets    map[solana.PublicKey]*KeyedLendingMarket
	obligations       map[solana.PublicKey]*KeyedObligation
	reserves          map[solana.PublicKey]*KeyedReserve
	updateAccountChan chan *backend.Account
	updated           chan bool
}

func NewProgram(id solana.PublicKey, ctx context.Context, env *env.Env, be *backend.Backend, oracle *pyth.Program) *Program {
	p := &Program{
		ctx:               ctx,
		logger:            log.Default(),
		backend:           be,
		env:               env,
		id:                id,
		oracle:            oracle,
		lendingMarkets:    make(map[solana.PublicKey]*KeyedLendingMarket),
		obligations:       make(map[solana.PublicKey]*KeyedObligation),
		reserves:          make(map[solana.PublicKey]*KeyedReserve),
		updateAccountChan: make(chan *backend.Account, 1024),
		updated:           make(chan bool, 1024),
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
			p.updated <- true
		case <-p.ctx.Done():
			return
		}
	}
}

func (p *Program) OnUpdate(price *pyth.KeyedPrice) error {
	p.updated <- true
	return nil
}

func (p *Program) Calculate() {
	for {
		select {
		case <-p.updated:
			p.calculate()
		}
	}
}

func (p *Program) calculate() {
	for k, _ := range p.obligations {
		p.calculateRefreshedObligation(k)
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
	selectedDeposit := -1
	selectedBorrow := -1
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
		collateralExchangeRate := reserve.collateralExchangeRate()
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
		loanToValueRate := reserve.loanToValueRate()
		liquidationThresholdRate := reserve.liquidationThresholdRate()
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
		if selectedDeposit == -1 {
			selectedDeposit = i
		} else {
			if marketValue.Cmp(obligation.ObligationCollateral[selectedDeposit].MarketValue.Value) > 0 {
				selectedDeposit = i
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
		if selectedBorrow == -1 {
			selectedBorrow = i
		} else {
			if marketValue.Cmp(obligation.ObligationLiquidity[selectedBorrow].MarketValue.Value) > 0 {
				selectedBorrow = i
			}
		}
	}
	//
	utilizationRatio :=
		new(big.Float).Mul(
			new(big.Float).Quo(borrowValue, depositValue),
			new(big.Float).SetFloat64(100),
		)
	if utilizationRatio.Sign() <= 0 {
		utilizationRatio = new(big.Float).SetInt64(0)
	}
	//
	p.logger.Printf("obligation: %s deposit value：%s, borrow value: %s, allowed borrow value: %s, unhealthy borrow value: %s",
		obligation.Key.String(), depositValue.String(), borrowValue.String(), allowedBorrowValue.String(), unhealthyBorrowValue.String())
	//
	if borrowValue.Cmp(unhealthyBorrowValue) <= 0 {
		p.logger.Printf("obligation is healthy")
		//return nil
	}
	p.logger.Printf("obligation %s is underwater, borrowed value: %s, unhealthy borrow value: %s",
		obligation.Key.String(), borrowValue.String(), unhealthyBorrowValue.String(),
	)
	//
	if selectedDeposit == -1 || selectedBorrow == -1 {
		p.logger.Printf("no deposit or borrow found")
		return nil
	}
	err := p.liquidateObligation(obligation, selectedDeposit, selectedBorrow)
	if err != nil {
		p.logger.Printf("%s", err.Error())
		return err
	}
	return nil
}

func (p *Program) liquidateObligation(obligation *KeyedObligation, selectDeposit int, selectBorrow int) error {
	ins := make([]solana.Instruction, 0)
	depositKeys := make([]solana.PublicKey, 0)
	borrowKeys := make([]solana.PublicKey, 0)
	// refresh all reserves
	for _, deposit := range obligation.ObligationCollateral {
		reserveKey := deposit.DepositReserve
		reserve, ok := p.reserves[reserveKey]
		if !ok {
			return fmt.Errorf("no reserve for key: %s", reserveKey.String())
		}
		in, err := p.InstructionRefreshReserve(reserve.Key, reserve.ReserveLiquidity.Oracle)
		if err != nil {
			return err
		}
		ins = append(ins, in)
		depositKeys = append(depositKeys, reserve.Key)
	}
	for _, borrow := range obligation.ObligationLiquidity {
		reserveKey := borrow.BorrowReserve
		reserve, ok := p.reserves[reserveKey]
		if !ok {
			return fmt.Errorf("no reserve for key: %s", reserveKey.String())
		}
		in, err := p.InstructionRefreshReserve(reserve.Key, reserve.ReserveLiquidity.Oracle)
		if err != nil {
			return err
		}
		ins = append(ins, in)
		borrowKeys = append(borrowKeys, reserve.Key)
	}
	// refresh obligation
	in, err := p.InstructionRefreshObligation(obligation.Key, depositKeys, borrowKeys)
	if err != nil {
		return err
	}
	ins = append(ins, in)
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
	authority, _, err := solana.FindProgramAddress([][]byte{obligation.LendingMarket.Bytes(), seed}, p.id)
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
	// redeem colla
	withdrawLiquidity := p.env.TokenUser(withdrawReserve.ReserveLiquidity.Mint)
	if withdrawLiquidity.IsZero() {
		return fmt.Errorf("no token user: %s", withdrawReserve.ReserveLiquidity.Mint.String())
	}
	in, err = p.InstructionRedeemReserveCollateral(
		uint64(100000), withdraw, withdrawLiquidity, withdrawReserve.Key, withdrawReserve.ReserveCollateral.Mint,
		withdrawReserve.ReserveLiquidity.Supply, lendingMarket.Key, authority, owner)
	if err != nil {
		return err
	}
	ins = append(ins, in)
	//
	id := time.Now().UnixNano() / 1000
	p.backend.Commit(0, uint64(id), ins, true, nil)
	return nil
}

func (p *Program) InstructionRefreshReserve(reserve solana.PublicKey, oracle solana.PublicKey) (solana.Instruction, error) {
	data := make([]byte, 1)
	data[0] = 3
	instruction := &program.Instruction{
		IsAccounts: []*solana.AccountMeta{
			{PublicKey: reserve, IsSigner: false, IsWritable: true},
			{PublicKey: oracle, IsSigner: false, IsWritable: false},
			{PublicKey: program.SysClock, IsSigner: false, IsWritable: false},
		},
		IsData:      data,
		IsProgramID: p.id,
	}
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
	data := make([]byte, 5)
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
	data := make([]byte, 5)
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

func (p *Program) borrowedAmountWadWithInterest(
	reserveCumulativeBorrowRate *big.Float,
	obligationCumulativeBorrowRate *big.Float,
	obligationBorrowAmount *big.Float,
) *big.Float {
	c := reserveCumulativeBorrowRate.Cmp(obligationCumulativeBorrowRate)
	switch c {
	case -1:
		p.logger.Printf("interest rate cannot be negative")
		return obligationBorrowAmount
	case 0:
		return obligationBorrowAmount
	case 1:
		r := new(big.Float).Quo(reserveCumulativeBorrowRate, obligationCumulativeBorrowRate)
		return new(big.Float).Mul(obligationCumulativeBorrowRate, r)
	}
	return nil
}
