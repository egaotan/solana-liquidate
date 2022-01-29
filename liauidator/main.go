package main

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/solana-liquidate/config"
	"github.com/solana-liquidate/liauidator/liquidate"
	"github.com/solana-liquidate/utils"
	"os"
	"os/signal"
	"syscall"
	"time"
)

func main() {
	ctx, cancel := context.WithCancel(context.Background())
	quit := make(chan os.Signal, 1)
	signal.Notify(quit, os.Interrupt, syscall.SIGTERM, syscall.SIGABRT)
	go shutdown(cancel, quit)

	if len(os.Args) != 2 {
		panic("args is invalid")
	}
	workSpace := os.Args[1]
	os.Chdir(workSpace)

	infoJson, err := os.ReadFile(utils.ConfigFile)
	if err != nil {
		panic(err)
	}
	var cfg config.Config
	err = json.Unmarshal(infoJson, &cfg)
	if err != nil {
		panic(err)
	}

	//
	infoJson, err = os.ReadFile(utils.ValidatorFile)
	if err != nil {
		panic(err)
	}
	validatorNodes := make([]*config.Node, 0)
	err = json.Unmarshal(infoJson, &validatorNodes)
	if err != nil {
		panic(err)
	}

	//
	oldNodes := cfg.Endpoints
	usableNodes := make([]*config.Node, 0)
	for _, node := range oldNodes {
		if node.Usable {
			usableNodes = append(usableNodes, node)
		}
	}
	cfg.Endpoints = usableNodes
	//
	oldValidators := cfg.TxEndpoints
	usableValidators := make([]*config.Node, 0)
	for _, oldValidator := range oldValidators {
		if oldValidator.Usable {
			usableValidators = append(usableValidators, oldValidator)
		}
	}
	cfg.TxEndpoints = usableValidators
	//
	usableValidators1 := make([]*config.Node, 0)
	for _, validator := range validatorNodes {
		if validator.Usable {
			usableValidators1 = append(usableValidators1, validator)
		}
	}
	//
	selectedValidators := make([]*config.Node, 0)
	for i := 0; i < len(usableValidators1); i++ {
		if i%3 == cfg.NodeId {
			selectedValidators = append(selectedValidators, usableValidators1[i])
		}
		if len(selectedValidators) > cfg.TxEndpointSize {
			break
		}
	}
	cfg.TxEndpoints = append(cfg.TxEndpoints, selectedValidators...)

	//
	cfg.Usdc = cfg.Usdc * 1000000
	cfg.LiquidateThreshold = cfg.LiquidateThreshold * 1000000

	cfg.WorkSpace = workSpace
	workspace, _ := os.Getwd()
	fmt.Printf("work space: %s\n", workspace)

	//
	t := time.Now()
	t_str := t.Format("2006-01-02")
	dir := fmt.Sprintf("./%s_log/", t_str)
	os.Mkdir(dir, os.ModePerm)
	utils.LogPath = dir

	at := liquidate.NewLiquidate(ctx, &cfg)
	at.Service()
}

func shutdown(cancel context.CancelFunc, quit <-chan os.Signal) {
	osCall := <-quit
	fmt.Printf("System call: %v, auto trader is shutting down......\n", osCall)
	cancel()
}
