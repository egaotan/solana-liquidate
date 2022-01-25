package solend

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"github.com/gagliardetto/solana-go"
	"github.com/solana-liquidate/utils"
	"math/big"
)

var (
	LendingMarketLayoutSize        = 290
	LastUpdateLayoutSize           = 9
	DecimalLayoutSize              = 16
	ReserveLayoutSize              = 619
	ReserveLiquidityLayoutSize     = 185
	ReserveCollateralLayoutSize    = 72
	ReserveConfigLayoutSize        = 55 + ReserveFeesLayoutSize
	ReserveFeesLayoutSize          = 17
	ObligationFixLayoutSize        = 204
	ObligationCollateralLayoutSize = 88
	ObligationLiquidityLayoutSize  = 112
	ObligationLayoutSize           = ObligationFixLayoutSize + ObligationCollateralLayoutSize + 9*ObligationLiquidityLayoutSize
)

var (
	WAD                      = new(big.Float).SetInt64(1000000000000000000)
	INITIAL_COLLATERAL_RATIO = 1
	INITIAL_COLLATERAL_RATE  = new(big.Float).SetInt64(int64(INITIAL_COLLATERAL_RATIO))
)

type DecimalLayout struct {
	Value *big.Float
}

func (d *DecimalLayout) unpack(data []byte, isWad bool) error {
	dst := make([]byte, 16)
	utils.ReverseBytes(dst, data[0:16])
	value := new(big.Int).SetBytes(dst)
	d.Value = new(big.Float).SetInt(value)
	if isWad {
		d.Value = new(big.Float).Quo(d.Value, WAD)
	}
	return nil
}

func unpackPublicKey(data []byte) (solana.PublicKey, error) {
	if len(data) != 32 {
		return solana.PublicKey{}, fmt.Errorf("data lenght is not 32 for public key")
	}
	key := solana.PublicKey{}
	buf := bytes.NewReader(data[0:32])
	err := binary.Read(buf, binary.LittleEndian, &key)
	if err != nil {
		return solana.PublicKey{}, err
	}
	return key, nil
}

type LendingMarketLayout struct {
	Version           uint8
	BumpSeed          uint8
	Owner             solana.PublicKey
	QuoteCurrency     solana.PublicKey
	TokenProgramId    solana.PublicKey
	OracleProgramId   solana.PublicKey
	SwitchBoardOracle solana.PublicKey
	_                 [128]byte
}

func (lendingMarket *LendingMarketLayout) unpack(data []byte) error {
	buf := bytes.NewReader(data)
	return binary.Read(buf, binary.LittleEndian, lendingMarket)
}

type KeyedLendingMarket struct {
	LendingMarketLayout
	Height uint64
	Key    solana.PublicKey
}

type LastUpdateLayout struct {
	Slot  uint64
	Stale bool
}

func (lastUpdate *LastUpdateLayout) unpack(data []byte) error {
	lastUpdate.Slot = binary.LittleEndian.Uint64(data[0:8])
	if data[8] != 0 {
		lastUpdate.Stale = true
	}
	return nil
}

type ReserveLiquidityLayout struct {
	Mint                 solana.PublicKey
	MintDecimals         uint8
	Supply               solana.PublicKey
	Oracle               solana.PublicKey
	SwitchBoardOracle    solana.PublicKey
	AvailableAmount      uint64
	BorrowedAmount       DecimalLayout
	CumulativeBorrowRate DecimalLayout
	MarketPrice          DecimalLayout
}

func (liquidity *ReserveLiquidityLayout) unpack(data []byte) error {
	index := 0
	//
	key, err := unpackPublicKey(data[index : index+32])
	if err != nil {
		return err
	}
	index += 32
	liquidity.Mint = key
	liquidity.MintDecimals = data[index]
	index += 1
	//
	key, err = unpackPublicKey(data[index : index+32])
	if err != nil {
		return err
	}
	index += 32
	liquidity.Supply = key
	//
	key, err = unpackPublicKey(data[index : index+32])
	if err != nil {
		return err
	}
	index += 32
	liquidity.Oracle = key
	//
	key, err = unpackPublicKey(data[index : index+32])
	if err != nil {
		return err
	}
	index += 32
	liquidity.SwitchBoardOracle = key
	//
	liquidity.AvailableAmount = binary.LittleEndian.Uint64(data[index : index+8])
	index += 8
	liquidity.BorrowedAmount.unpack(data[index : index+DecimalLayoutSize], true)
	index += DecimalLayoutSize
	liquidity.CumulativeBorrowRate.unpack(data[index : index+DecimalLayoutSize], true)
	index += DecimalLayoutSize
	liquidity.MarketPrice.unpack(data[index : index+DecimalLayoutSize],true)
	index += DecimalLayoutSize
	return nil
}

type ReserveCollateralLayout struct {
	Mint            solana.PublicKey
	MintTotalSupply uint64
	Supply          solana.PublicKey
}

func (collateral *ReserveCollateralLayout) unpack(data []byte) error {
	index := 0
	//
	key, err := unpackPublicKey(data[index : index+32])
	if err != nil {
		return err
	}
	index += 32
	collateral.Mint = key
	//
	collateral.MintTotalSupply = binary.LittleEndian.Uint64(data[index : index+8])
	index += 8
	//
	key, err = unpackPublicKey(data[index : index+32])
	if err != nil {
		return err
	}
	index += 32
	collateral.Supply = key
	return nil
}

type ReserveFeesLayout struct {
	BorrowFee         *big.Float
	FlashLoanFee      *big.Float
	HostFeePercentage uint8
}

func (reserveFees *ReserveFeesLayout) unpack(data []byte) error {
	index := 0
	//
	borrowFeeWad := binary.LittleEndian.Uint64(data[index : index+8])
	reserveFees.BorrowFee = new(big.Float).Quo(
		new(big.Float).SetUint64(borrowFeeWad),
		WAD,
	)
	index += 8
	//
	flashLoanFeeWad := binary.LittleEndian.Uint64(data[index : index+8])
	reserveFees.FlashLoanFee = new(big.Float).Quo(
		new(big.Float).SetUint64(flashLoanFeeWad),
		WAD,
	)
	index += 8
	//
	reserveFees.HostFeePercentage = data[index]
	index += 1
	return nil
}

type ReserveConfigLayout struct {
	OptimalUtilizationRate uint8
	LoanToValueRatio       uint8
	LiquidationBonus       uint8
	LiquidationThreshold   uint8
	MinBorrowRate          uint8
	OptimalBorrowRate      uint8
	MaxBorrowRate          uint8
	ReserveFees            ReserveFeesLayout
	DepositLimit           uint64
	BorrowLimit            uint64
	FeeReceiver            solana.PublicKey
}

func (reserveConfig *ReserveConfigLayout) unpack(data []byte) error {
	index := 0
	//
	reserveConfig.OptimalUtilizationRate = data[index]
	index += 1
	//
	reserveConfig.LoanToValueRatio = data[index]
	index += 1
	//
	reserveConfig.LiquidationBonus = data[index]
	index += 1
	//
	reserveConfig.LiquidationThreshold = data[index]
	index += 1
	//
	reserveConfig.MinBorrowRate = data[index]
	index += 1
	//
	reserveConfig.OptimalBorrowRate = data[index]
	index += 1
	//
	reserveConfig.MaxBorrowRate = data[index]
	index += 1
	//
	reserveConfig.ReserveFees.unpack(data[index : index+ReserveFeesLayoutSize])
	index += ReserveFeesLayoutSize
	//
	reserveConfig.DepositLimit = binary.LittleEndian.Uint64(data[index : index+8])
	index += 8
	//
	reserveConfig.BorrowLimit = binary.LittleEndian.Uint64(data[index : index+8])
	index += 8
	//
	key, err := unpackPublicKey(data[index : index+32])
	if err != nil {
		return err
	}
	index += 32
	reserveConfig.FeeReceiver = key
	return nil
}

type ReserveLayout struct {
	Version           uint8
	LastUpdate        LastUpdateLayout
	LendingMarket     solana.PublicKey
	ReserveLiquidity  ReserveLiquidityLayout
	ReserveCollateral ReserveCollateralLayout
	ReserveConfig     ReserveConfigLayout
	_                 [248]byte
}

func (reserve *ReserveLayout) unpack(data []byte) error {
	index := 0
	reserve.Version = data[index]
	index += 1
	reserve.LastUpdate.unpack(data[index : index+LastUpdateLayoutSize])
	index += LastUpdateLayoutSize
	key, err := unpackPublicKey(data[index : index+32])
	if err != nil {
		return err
	}
	index += 32
	reserve.LendingMarket = key
	reserve.ReserveLiquidity.unpack(data[index : index+ReserveLiquidityLayoutSize])
	index += ReserveLiquidityLayoutSize
	reserve.ReserveCollateral.unpack(data[index : index+ReserveCollateralLayoutSize])
	index += ReserveCollateralLayoutSize
	reserve.ReserveConfig.unpack(data[index : index+ReserveConfigLayoutSize])
	index += ReserveConfigLayoutSize
	index += 248
	return nil
}

type KeyedReserve struct {
	ReserveLayout
	Height uint64
	Key    solana.PublicKey
}

func (reserve *KeyedReserve) totalLiquidity() *big.Float {
	return new(big.Float).Add(
		new(big.Float).SetInt64(int64(reserve.ReserveLiquidity.AvailableAmount)),
		reserve.ReserveLiquidity.BorrowedAmount.Value,
	)
}

func (reserve *KeyedReserve) collateralExchangeRate() *big.Float {
	if reserve.ReserveCollateral.MintTotalSupply == 0 {
		return INITIAL_COLLATERAL_RATE
	}
	totalLiquidity := reserve.totalLiquidity()
	if totalLiquidity.Sign() == 0 {
		return INITIAL_COLLATERAL_RATE
	}
	rate := new(big.Float).Quo(
		new(big.Float).Mul(
			new(big.Float).SetInt64(int64(reserve.ReserveCollateral.MintTotalSupply)),
			WAD,
		),
		totalLiquidity,
	)
	return rate
}

func (reserve *KeyedReserve) loanToValueRate() *big.Float {
	return new(big.Float).Quo(
		new(big.Float).SetInt64(int64(reserve.ReserveConfig.LoanToValueRatio)),
		new(big.Float).SetInt64(100),
	)
}

func (reserve *KeyedReserve) liquidationThresholdRate() *big.Float {
	return new(big.Float).Quo(
		new(big.Float).SetInt64(int64(reserve.ReserveConfig.LiquidationThreshold)),
		new(big.Float).SetInt64(100),
	)
}

type ObligationCollateralLayout struct {
	DepositReserve  solana.PublicKey
	DepositedAmount uint64
	MarketValue     DecimalLayout
	_               [32]byte
}

func (collateral *ObligationCollateralLayout) unpack(data []byte) error {
	index := 0
	key, err := unpackPublicKey(data[index : index+32])
	if err != nil {
		return err
	}
	index += 32
	collateral.DepositReserve = key
	collateral.DepositedAmount = binary.LittleEndian.Uint64(data[index : index+8])
	index += 8
	collateral.MarketValue.unpack(data[index : index+DecimalLayoutSize], true)
	index += DecimalLayoutSize
	return nil
}

type ObligationLiquidityLayout struct {
	BorrowReserve            solana.PublicKey
	CumulativeBorrowRate DecimalLayout
	BorrowedAmount      DecimalLayout
	MarketValue              DecimalLayout
	_                        [32]byte
}

func (liquidity *ObligationLiquidityLayout) unpack(data []byte) error {
	index := 0
	key, err := unpackPublicKey(data[index : index+32])
	if err != nil {
		return err
	}
	index += 32
	liquidity.BorrowReserve = key
	liquidity.CumulativeBorrowRate.unpack(data[index : index+DecimalLayoutSize], true)
	index += DecimalLayoutSize
	liquidity.BorrowedAmount.unpack(data[index : index+DecimalLayoutSize], true)
	index += DecimalLayoutSize
	liquidity.MarketValue.unpack(data[index : index+DecimalLayoutSize], true)
	index += DecimalLayoutSize
	index += 32
	return nil
}

type ObligationLayout struct {
	Version              uint8
	LastUpdate           LastUpdateLayout
	LendingMarket        solana.PublicKey
	Owner                solana.PublicKey
	DepositedValue       DecimalLayout
	BorrowedValue        DecimalLayout
	AllowedBorrowValue   DecimalLayout
	UnhealthyBorrowValue DecimalLayout
	_                    [64]byte
	DepositsLen          uint8
	BorrowsLen           uint8
	ObligationCollateral []ObligationCollateralLayout
	ObligationLiquidity  []ObligationLiquidityLayout
}

func (layout *ObligationLayout) unpack(data []byte) error {
	index := 0
	layout.Version = data[index]
	index += 1
	layout.LastUpdate.unpack(data[index : index+LastUpdateLayoutSize])
	index += LastUpdateLayoutSize
	key, err := unpackPublicKey(data[index : index+32])
	if err != nil {
		return err
	}
	index += 32
	layout.LendingMarket = key
	//
	key, err = unpackPublicKey(data[index : index+32])
	if err != nil {
		return err
	}
	index += 32
	layout.Owner = key
	layout.DepositedValue.unpack(data[index : index+DecimalLayoutSize], true)
	index += DecimalLayoutSize
	layout.BorrowedValue.unpack(data[index : index+DecimalLayoutSize], true)
	index += DecimalLayoutSize
	layout.AllowedBorrowValue.unpack(data[index : index+DecimalLayoutSize], true)
	index += DecimalLayoutSize
	layout.UnhealthyBorrowValue.unpack(data[index : index+DecimalLayoutSize], true)
	index += DecimalLayoutSize
	index += 64
	layout.DepositsLen = data[index]
	index += 1
	layout.BorrowsLen = data[index]
	index += 1
	layout.ObligationCollateral = make([]ObligationCollateralLayout, layout.DepositsLen)
	for i := 0; i < int(layout.DepositsLen); i++ {
		layout.ObligationCollateral[i].unpack(data[index : index+ObligationCollateralLayoutSize])
		index += ObligationCollateralLayoutSize
	}
	layout.ObligationLiquidity = make([]ObligationLiquidityLayout, layout.BorrowsLen)
	for i := 0; i < int(layout.BorrowsLen); i++ {
		layout.ObligationLiquidity[i].unpack(data[index : index+ObligationLiquidityLayoutSize])
		index += ObligationLiquidityLayoutSize
	}
	return nil
}

type KeyedObligation struct {
	ObligationLayout
	Height uint64
	Key    solana.PublicKey
}
