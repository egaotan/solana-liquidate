module github.com/solana-liquidate

go 1.16

require (
	github.com/gagliardetto/solana-go v1.0.4
	github.com/shopspring/decimal v1.3.1
	gorm.io/driver/mysql v1.2.3
	gorm.io/gorm v1.22.5
)

replace github.com/gagliardetto/solana-go v1.0.4 => github.com/egaotan/solana-go v1.0.4
