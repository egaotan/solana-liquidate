package liquidate

import (
	"context"
	"github.com/solana-liquidate/backend"
	"github.com/solana-liquidate/config"
	"github.com/solana-liquidate/env"
	"github.com/solana-liquidate/program"
	"github.com/solana-liquidate/pyth"
	"github.com/solana-liquidate/solend"
	"github.com/solana-liquidate/utils"
	"log"
	"sync"
)

type Liquidate struct {
	ctx       context.Context
	log       *log.Logger
	config    *config.Config
	wg        sync.WaitGroup
	status    int32
	backend   *backend.Backend
	env       *env.Env
	pyth      *pyth.Program
	liquidate *solend.Program
	nodeId    int
}

func NewLiquidate(ctx context.Context, cfg *config.Config) *Liquidate {
	liquidte := &Liquidate{
		ctx:    ctx,
		config: cfg,
		nodeId: cfg.NodeId,
	}
	//
	liquidte.log = utils.NewLog(utils.LogPath, "liquidate")
	//
	txEndpints := make([]string, 0)
	for _, txEndpoint := range cfg.TxEndpoints {
		txEndpints = append(txEndpints, txEndpoint.Rpc)
	}
	be := backend.NewBackend(ctx, cfg.Endpoints[0].Rpc, cfg.Endpoints[0].Ws, 3, txEndpints, cfg.TpuEndpoint, cfg.BlockHashEndpoint)
	be.ImportWallet(cfg.Key)
	be.SetPlayer(cfg.User)
	liquidte.backend = be
	e := env.NewEnv(ctx)
	liquidte.env = e
	//
	pyth := pyth.NewProgram(program.Pyth, ctx, be)
	liquidte.pyth = pyth
	//
	liquidate := solend.NewProgram(program.Solend, ctx, e, be, false, pyth, cfg.Usdc, cfg.LiquidateThreshold)
	liquidte.liquidate = liquidate
	return liquidte
}

func (liq *Liquidate) Service() {
	liq.Start()
	<-liq.ctx.Done()
	liq.Stop()
}

func (liq *Liquidate) Start() {
	liq.env.Start()
	liq.backend.Start()
	liq.pyth.Start()
	liq.liquidate.Start()
	liq.backend.SubscribeSlot(nil)
	liq.liquidate.Flash()
	liq.pyth.Flash()
	liq.backend.StartSubscribeAccount()
	liq.log.Printf("liquidator has started......")
}

func (liq *Liquidate) Stop() {
	liq.liquidate.Stop()
	liq.pyth.Stop()
	liq.backend.Stop()
	liq.env.Stop()
	liq.log.Printf("liquidator has stopped......")
}
