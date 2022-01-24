package backend

import (
	"context"
	"github.com/gagliardetto/solana-go"
	"github.com/gagliardetto/solana-go/rpc"
	"github.com/gagliardetto/solana-go/rpc/ws"
	"github.com/solana-liquidate/backend/tpu"
	"github.com/solana-liquidate/store"
	"github.com/solana-liquidate/utils"
	"log"
	"sync"
)

type Backend struct {
	ctx         context.Context
	wg          sync.WaitGroup
	logger      *log.Logger
	rpcClient   *rpc.Client
	wsClient    *ws.Client
	accountSubs map[solana.PublicKey]AccountSubscription
	programSubs map[solana.PublicKey]ProgramSubscription
	slotSubs    *ws.SlotSubscription
	sendTx      int
	txClients   []*rpc.Client
	txChans     []chan *TxCommand
	tpuProxy    *tpu.Proxy
	clientBH    *rpc.Client
	cachedBH    []solana.Hash
	updateBH    chan uint64
	lock        int32
	store       *store.Store
	player      solana.PublicKey
	wallets     []*Wallet
}

func NewBackend(ctx context.Context, rpcEndpoint string, wsEndpoint string, sendTx int, txEndpoints []string, tpuEndpoint string, bhEndpoint string) *Backend {
	rpcClient := rpc.New(rpcEndpoint)
	wsClients, err := ws.Connect(ctx, wsEndpoint)
	if err != nil {
		panic(err)
	}
	backend := &Backend{
		ctx:         ctx,
		logger:      utils.NewLog(utils.LogPath, utils.BackendLog),
		rpcClient:   rpcClient,
		wsClient:    wsClients,
		accountSubs: make(map[solana.PublicKey]AccountSubscription, 0),
		programSubs: make(map[solana.PublicKey]ProgramSubscription, 0),
		sendTx:      sendTx,
		tpuProxy:    tpu.NewProxy(ctx, tpuEndpoint),
		clientBH:    rpc.New(bhEndpoint),
		cachedBH:    make([]solana.Hash, 0, BHCachedSize),
		updateBH:    make(chan uint64, 1024),
		wallets:     make([]*Wallet, 0),
	}
	txChans := make([]chan *TxCommand, 0, len(txEndpoints))
	txClients := make([]*rpc.Client, 0, len(txEndpoints))
	for _, txEndpoint := range txEndpoints {
		backend.logger.Printf("transaction endpoint (%s)", txEndpoint)
		txChans = append(txChans, make(chan *TxCommand, 1024))
		txClients = append(txClients, rpc.New(txEndpoint))
	}
	backend.txChans = txChans
	backend.txClients = txClients
	return backend
}

func (backend *Backend) Start() {
	if backend.sendTx == 0 {
		return
	}
	//
	backend.startExecutor()
	// start recent block hash cache
	backend.wg.Add(1)
	go backend.CacheRecentBlockHash()
	//backend.updateBlockHash <- true
	backend.cachedBH = append(backend.cachedBH, []solana.Hash{{}, {}, {}}...)
	backend.tpuProxy.Start()
}

func (backend *Backend) Stop() {
	if backend.sendTx == 0 {
		return
	}
	for _, accountSub := range backend.accountSubs {
		accountSub.sub.Unsubscribe()
	}
	backend.wg.Wait()
}
