package backend

import (
	"github.com/gagliardetto/solana-go"
	"github.com/gagliardetto/solana-go/rpc"
	"sync/atomic"
)

var (
	BHCachedSize = 3
)

func (backend *Backend) CacheRecentBlockHash() {
	defer backend.wg.Done()
	//ticker := time.NewTicker(time.Second * 2)
	for {
		select {
		case slot := <-backend.updateBH:
		L:
			for {
				select {
				case slot = <-backend.updateBH:
				default:
					break L
				}
			}
			slot = slot / 5 * 5
			reward := false
			getBlockResult, err := backend.clientBH.GetBlockWithOpts(backend.ctx, slot-20,
				&rpc.GetBlockOpts{
					Encoding:           solana.EncodingBase64,
					TransactionDetails: rpc.TransactionDetailsNone,
					Rewards:            &reward,
					Commitment:         rpc.CommitmentConfirmed,
				})
			if err != nil {
				backend.logger.Printf("GetBlock, err: %s", err.Error())
				continue
			}
			backend.logger.Printf("get recent block hash. (%s, %d, %d)",
				getBlockResult.Blockhash.String(), getBlockResult.BlockHeight, getBlockResult.ParentSlot)
			if backend.cachedBH[2] == getBlockResult.Blockhash {
				continue
			}
			for !atomic.CompareAndSwapInt32(&backend.lock, 0, 1) {
				continue
			}
			backend.cachedBH = append(backend.cachedBH, getBlockResult.Blockhash)
			backend.cachedBH = backend.cachedBH[1:]
			atomic.StoreInt32(&backend.lock, 0)
			backend.logger.Printf("receive block hash, %s", getBlockResult.Blockhash.String())
		case <-backend.ctx.Done():
			backend.logger.Printf("recent block hash cache exit")
			return
		}
	}
}

func (backend *Backend) GetRecentBlockHash(level int) solana.Hash {
	defer atomic.StoreInt32(&backend.lock, 0)
	for !atomic.CompareAndSwapInt32(&backend.lock, 0, 1) {
		continue
	}
	return backend.cachedBH[BHCachedSize-1-level]
}
