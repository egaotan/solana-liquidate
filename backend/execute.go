package backend

import (
	"encoding/json"
	"fmt"
	"github.com/gagliardetto/solana-go"
	"github.com/gagliardetto/solana-go/rpc"
	"github.com/solana-liquidate/store"
	"github.com/solana-liquidate/utils"
	"log"
	"time"
)

const (
	ExecutorSize = 8
	Try          = 0
)

type TxCommand struct {
	Id       uint64
	Trx      *solana.Transaction
	Simulate bool
	Accounts []solana.PublicKey
}

func (backend *Backend) Executor(id int, tx chan *TxCommand, client *rpc.Client) {
	i := id / 1000
	j := id % 1000
	logger := utils.NewLog(utils.LogPath, fmt.Sprintf("%s_%d_%d", utils.Executor, i, j))
	defer func() {
		backend.logger.Printf("executor %d exit", id)
		logger.Printf("executor %d exit", id)
	}()
	logger.Printf("executor %d start", id)
	for {
		select {
		case command := <-tx:
			backend.Execute(command, client, id, logger)
		case <-backend.ctx.Done():
			return
		}
	}
}

func (backend *Backend) Execute(command *TxCommand, client *rpc.Client, id int, logger *log.Logger) {
	defer func() {
		logger.Printf("end execute command: %d", command.Id)
	}()
	logger.Printf("start execute command: %d, time: %s", command.Id,
		time.Unix(int64(command.Id)/1000000, int64(command.Id)%1000000*1000).Format("2006-01-02 15:04:05.000000"))
	if command.Simulate {
		backend.executeSimulate(command, client, id, logger)
		return
	} else {
		backend.execute(command, client, id, logger)
		return
	}
}

func (backend *Backend) execute(command *TxCommand, client *rpc.Client, id int, logger *log.Logger) {
	executedArbitrage := &store.ExecutedArbitrage{
		Id:           command.Id,
		ExecuteId:    id,
		SendTime:     0,
		ResponseTime: 0,
		Signature:    "",
	}
	defer func() {
		if backend.store != nil && id/1000 == 1 {
			backend.store.StoreExecutedArbitrage(executedArbitrage)
		}
	}()
	trx := command.Trx
	send := func() solana.Signature {
		signature, err := client.SendTransactionWithOpts(backend.ctx, trx, true, rpc.CommitmentFinalized)
		if err != nil {
			logger.Printf("SendTransactionWithOpts err: %s", err.Error())
		}
		return signature
	}
	check := func(signature solana.Signature) error {
		if signature.IsZero() {
			return fmt.Errorf("no transaction hash")
		}
		_, err := client.GetTransaction(backend.ctx, signature, &rpc.GetTransactionOpts{
			Encoding:   solana.EncodingBase64,
			Commitment: rpc.CommitmentConfirmed,
		})
		if err != nil {
			return err
		}
		tt := time.Now().UnixNano() / time.Microsecond.Nanoseconds()
		executedArbitrage.FinishTime = uint64(tt)
		return nil
	}
	//
	tt := time.Now().UnixNano() / time.Microsecond.Nanoseconds()
	if uint64(tt)-command.Id > 20000000 {
		logger.Printf("the arbitrage command is too old")
		return
	}
	executedArbitrage.SendTime = uint64(tt)
	logger.Printf("trying %d......", 1)
	signature := send()
	tt2 := time.Now().UnixNano() / time.Microsecond.Nanoseconds()
	executedArbitrage.ResponseTime = uint64(tt2)
	executedArbitrage.Signature = signature.String()
	executedArbitrage.ExecuteCounter = 1
	counter := 1
	finished := false
	for !finished && counter < Try {
		counter++
		err := check(signature)
		if err == nil {
			finished = true
			logger.Printf("transaction success")
			break
		}
		logger.Printf("check err: %s", err.Error())
		tt = time.Now().UnixNano() / time.Microsecond.Nanoseconds()
		if uint64(tt)-command.Id > 20000000 {
			logger.Printf("the arbitrage command is too old")
			return
		}
		logger.Printf("trying %d......", counter)
		signature = send()
		if !signature.IsZero() {
			executedArbitrage.Signature = signature.String()
		}
		time.Sleep(time.Millisecond * 100)
	}
	executedArbitrage.ExecuteCounter = counter
	counter = 0
	for !finished && counter < Try {
		counter++
		err := check(signature)
		if err == nil {
			finished = true
			logger.Printf("transaction success")
			break
		}
		logger.Printf("check err: %s", err.Error())
		time.Sleep(time.Millisecond * 500)
	}
	trxJson, _ := json.MarshalIndent(trx, "", "    ")
	logger.Printf("transaction: %s", trxJson)
}

func (backend *Backend) executeSimulate(command *TxCommand, client *rpc.Client, id int, logger *log.Logger) {
	trx := command.Trx
	send := func() {
		response, err := client.SimulateTransactionWithOpts(backend.ctx, trx, &rpc.SimulateTransactionOpts{
			SigVerify:              false,
			Commitment:             rpc.CommitmentFinalized,
			ReplaceRecentBlockhash: true,
		})
		if err != nil {
			logger.Printf("SimulateTransactionWithOpts err: %s", err.Error())
			return
		}

		simulateTransactionResponse := response.Value
		if simulateTransactionResponse.Logs == nil {
			logger.Printf("log is nil")
		}
		logsJson, _ := json.MarshalIndent(simulateTransactionResponse.Logs, "", "    ")
		logger.Printf("%s", logsJson)
		if simulateTransactionResponse.Err != nil {
			logger.Printf("simulate err: %v", simulateTransactionResponse.Err)
		}
		return
	}
	//
	tt := time.Now().UnixNano() / time.Microsecond.Nanoseconds()
	if uint64(tt)-command.Id > 20000000 {
		logger.Printf("the arbitrage command is too old")
		return
	}
	logger.Printf("trying %d......", 1)
	send()
	trxJson, _ := json.MarshalIndent(trx, "", "    ")
	logger.Printf("transaction: %s", trxJson)
}

func (backend *Backend) startExecutor() {
	for i := 0; i < len(backend.txChans); i++ {
		for j := 0; j < ExecutorSize; j++ {
			id := (i+1)*1000 + (j + 1)
			go backend.Executor(id, backend.txChans[i], backend.txClients[i])
		}
	}
}

func (backend *Backend) Commit(level int, id uint64, ins []solana.Instruction, simulate bool, accounts []solana.PublicKey) {
	// build transaction
	builder := solana.NewTransactionBuilder()
	for _, i := range ins {
		builder.AddInstruction(i)
	}
	builder.SetRecentBlockHash(backend.GetRecentBlockHash(level))
	builder.SetFeePayer(backend.player)
	trx, err := builder.Build()
	if err != nil {
		backend.logger.Printf("build err: %s", err.Error())
		return
	}
	trx.Sign(backend.getWallet)
	//
	command := &TxCommand{
		Id:       id,
		Trx:      trx,
		Simulate: simulate,
		Accounts: accounts,
	}
	if backend.sendTx == 2 || backend.sendTx == 3 {
		backend.logger.Printf("send transaction to tpu")
		txData, err := trx.MarshalBinary()
		if err != nil {
			backend.logger.Printf("trx.MarshalBinary err: %s", err.Error())
		}
		backend.tpuProxy.CommitTransaction(txData)
	}
	if backend.sendTx == 1 || backend.sendTx == 3 {
		backend.logger.Printf("sent transaction to rpc")
		for i := 0; i < len(backend.txChans); i++ {
			backend.txChans[i] <- command
		}
	}
}
