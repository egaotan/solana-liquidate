package program

import "github.com/gagliardetto/solana-go"

var (
	Pyth  = solana.MustPublicKeyFromBase58("FsJ3A3u2vn5cTVofAjvy6y5kwABJAqYWpe4975bi2epH")
	Solend    = solana.MustPublicKeyFromBase58("So1endDq2YkqhipRh3WViPa8hdiSpxWy6z3Z6tMCpAo")
	Token     = solana.MustPublicKeyFromBase58("TokenkegQfeZyiNwAJbNbGKPFXCWuBvf9Ss623VQ5DA")
	System    = solana.MustPublicKeyFromBase58("11111111111111111111111111111111")
	SysClock  = solana.MustPublicKeyFromBase58("SysvarC1ock11111111111111111111111111111111")
	SysRent   = solana.MustPublicKeyFromBase58("SysvarRent111111111111111111111111111111111")
)