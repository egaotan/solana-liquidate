package tokenlending

import (
	"bytes"
	"encoding/binary"
	"github.com/gagliardetto/solana-go"
)

var (
	LendingMarketLayoutSize        = 258
	ReserveLayoutSize              = 571
	ObligationFixLayoutSize        = 140
	ObligationCollateralLayoutSize = 56
	ObligationLiquidityLayoutSize  = 80
	ObligationLayoutSize           = ObligationFixLayoutSize + ObligationCollateralLayoutSize + 9*ObligationLiquidityLayoutSize
)

type LendingMarketLayout struct {
	Version         uint8
	BumpSeed        uint8
	Owner           solana.PublicKey
	QuoteCurrency   solana.PublicKey
	TokenProgramId  solana.PublicKey
	OracleProgramId solana.PublicKey
	_               [128]byte
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

type ReserveLiquidityLayout struct {
	Mint                     solana.PublicKey
	MintDecimals             uint8
	Supply                   solana.PublicKey
	FeeReceiver              solana.PublicKey
	Oracle                   solana.PublicKey
	AvailableAmount          uint64
	BorrowedAmount           [2]uint64
	CumulativeBorrowRateWads [2]uint64
	MarketPrice              [2]uint64
}

type ReserveCollateralLayout struct {
	Mint            solana.PublicKey
	MintTotalSupply uint64
	Supply          solana.PublicKey
}

type ReserveFeesLayout struct {
	BorrowFeeWad      uint64
	FlashLoanFeeWad   uint64
	HostFeePercentage uint8
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

type KeyedReserve struct {
	ReserveLayout
	Height uint64
	Key    solana.PublicKey
}

type ObligationCollateralLayout struct {
	DepositReserve  solana.PublicKey
	DepositedAmount uint64
	MarketValue     [2]uint64
}

type ObligationLiquidityLayout struct {
	BorrowReserve            solana.PublicKey
	CumulativeBorrowRateWads [2]uint64
	BorrowedAmountWads       [2]uint64
	MarketValue              [2]uint64
}

type ObligationFixLayout struct {
	Version              uint8
	LastUpdate           LastUpdateLayout
	LendingMarket        solana.PublicKey
	Owner                solana.PublicKey
	DepositedValue       [2]uint64
	BorrowedValue        [2]uint64
	AllowedBorrowValue   [2]uint64
	UnhealthyBorrowValue [2]uint64
	DepositsLen          uint8
	BorrowsLen           uint8
}

type ObligationLayout struct {
	ObligationFixLayout
	ObligationCollateral []ObligationCollateralLayout
	ObligationLiquidity  []ObligationLiquidityLayout
}

func (layout *ObligationLayout) unpack(data []byte) error {
	index := 0
	buf := bytes.NewReader(data[index : index+ObligationFixLayoutSize])
	err := binary.Read(buf, binary.LittleEndian, &layout.ObligationFixLayout)
	if err != nil {
		return err
	}
	index += ObligationFixLayoutSize
	layout.ObligationCollateral = make([]ObligationCollateralLayout, layout.DepositsLen)
	for i := 0; i < int(layout.DepositsLen); i++ {
		buf = bytes.NewReader(data[index : index+ObligationCollateralLayoutSize])
		err = binary.Read(buf, binary.LittleEndian, &layout.ObligationCollateral[i])
		if err != nil {
			return err
		}
		index += ObligationCollateralLayoutSize
	}
	layout.ObligationLiquidity = make([]ObligationLiquidityLayout, layout.BorrowsLen)
	for i := 0; i < int(layout.BorrowsLen); i++ {
		buf = bytes.NewReader(data[index : index+ObligationLiquidityLayoutSize])
		err = binary.Read(buf, binary.LittleEndian, &layout.ObligationLiquidity[i])
		if err != nil {
			return err
		}
		index += ObligationLiquidityLayoutSize
	}
	return nil
}

type KeyedObligation struct {
	ObligationLayout
	Height uint64
	Key    solana.PublicKey
}
