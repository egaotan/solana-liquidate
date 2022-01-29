package main

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/gagliardetto/solana-go"
	"github.com/solana-liquidate/dingsdk"
	"io/ioutil"
	"net/http"
	"net/url"
	"os"
	"os/signal"
	"sync"
	"syscall"
	"time"
)

var (
	SolScanUrl            = "https://api.solscan.io/account/transaction"
	SolScanTransactionUrl = "https://api.solscan.io/transaction"
	Solend                = solana.MustPublicKeyFromBase58("So1endDq2YkqhipRh3WViPa8hdiSpxWy6z3Z6tMCpAo")
	ObligationLiquidate   = "liquidate-obligation"
	Unknown               = "Unknown"
	StatusSuccess         = "Success"
	DingUrl               = "https://oapi.dingtalk.com/robot/send?access_token=1e914e86061ac415387234df0accc3d5e94c996922c7e4b9aa2998ec60fc803e"
)

var (
	Position = ""
)

type ParsedInstruction struct {
	ProgramId string `json:"programId"`
	Type      string `json:"type"`
}

type Transaction struct {
	BlockTime         uint64               `json:"blockTime"`
	Slot              uint64               `json:"slot"`
	TxHash            string               `json:"txHash"`
	Status            string               `json:"status"`
	ParsedInstruction []*ParsedInstruction `json:"parsedInstruction"`
}

type TransactionRsp struct {
	Success bool           `json:"succcess"`
	Data    []*Transaction `json:"data"`
}

type InnerInstruction struct {
	ParsedInstruction []*ParsedInstruction `json:"parsedInstructions"`
}

type TransactionDetail struct {
	BlockTime         uint64              `json:"blockTime"`
	Slot              uint64              `json:"slot"`
	TxHash            string              `json:"txHash"`
	Status            string              `json:"status"`
	InnerInstructions []*InnerInstruction `json:"innerInstructions"`
}

func GetTransactionDetail(hash string) *TransactionDetail {
	req, err := http.NewRequest("GET", SolScanTransactionUrl, nil)
	if err != nil {
		panic(err)
	}
	q := url.Values{}
	q.Add("tx", hash)
	req.Header.Set("Accepts", "application/json")
	req.URL.RawQuery = q.Encode()

	client := &http.Client{}
	resp, err := client.Do(req)
	if err != nil {
		panic(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		panic(fmt.Errorf("response status code: %d", resp.StatusCode))
	}
	respBody, _ := ioutil.ReadAll(resp.Body)
	fmt.Printf("GetTransactionDetail response: %s\n", string(respBody))
	var body TransactionDetail
	err = json.Unmarshal(respBody, &body)
	if err != nil {
		panic(err)
	}
	return &body
}

func GetTransactionsLimit(before string) []*Transaction {
	req, err := http.NewRequest("GET", SolScanUrl, nil)
	if err != nil {
		panic(err)
	}
	q := url.Values{}
	q.Add("address", Solend.String())
	if before != "" {
		q.Add("before", before)
	}
	req.Header.Set("Accepts", "application/json")
	req.URL.RawQuery = q.Encode()

	client := &http.Client{}
	resp, err := client.Do(req)
	if err != nil {
		panic(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		panic(fmt.Errorf("response status code: %d", resp.StatusCode))
	}
	respBody, _ := ioutil.ReadAll(resp.Body)
	fmt.Printf("GetTransactionsLimit response: %s\n", string(respBody))
	var body TransactionRsp
	err = json.Unmarshal(respBody, &body)
	if err != nil {
		panic(err)
	}
	return body.Data
}

func GetObligationLiquidateTransactions() []*Transaction {
	fmt.Printf("GetObligationLiquidateTransactions position: %s\n", Position)
	before := ""
	transactions := make([]*Transaction, 0)
	for true {
		txs := GetTransactionsLimit(before)
		if len(txs) == 0 {
			break
		}
		if len(Position) == 0 {
			transactions = append(transactions, txs[0])
			break
		}
		getPosition := false
		for _, tx := range txs {
			if tx.TxHash != Position {
				transactions = append(transactions, tx)
			} else {
				getPosition = true
				break
			}
		}
		if getPosition == true {
			break
		}
		before = txs[len(txs)-1].TxHash
	}
	if len(transactions) == 0 {
		return transactions
	}
	Position = transactions[0].TxHash
	//
	successTransactions := make([]*Transaction, 0)
	for _, tx := range transactions {
		if tx.Status != StatusSuccess {
			continue
		}
		successTransactions = append(successTransactions, tx)
	}
	//
	obligationTransactions := make([]*Transaction, 0)
	notSureTransactions := make([]*Transaction, 0)
	for _, tx := range successTransactions {
		isObligationTransaction := false
		hasUnknow := false
		for _, instruction := range tx.ParsedInstruction {
			if instruction.Type == ObligationLiquidate {
				isObligationTransaction = true
				break
			}
			if instruction.Type == Unknown {
				hasUnknow = true
			}
		}
		if isObligationTransaction == true {
			obligationTransactions = append(obligationTransactions, tx)
		} else {
			if hasUnknow == true {
				notSureTransactions = append(notSureTransactions, tx)
			}
		}
	}
	for _, tx := range notSureTransactions {
		transactionDetail := GetTransactionDetail(tx.TxHash)
		isObligationTransaction := false
		for _, innerInstructions := range transactionDetail.InnerInstructions {
			for _, paredInstruction := range innerInstructions.ParsedInstruction {
				if paredInstruction.Type == ObligationLiquidate {
					isObligationTransaction = true
					break
				}
			}
			if isObligationTransaction == true {
				break
			}
		}
		if isObligationTransaction == true {
			obligationTransactions = append(obligationTransactions, tx)
		}
		/*
			txSig := solana.MustSignatureFromBase58(tx.TxHash)
			getTransactionResult, err := client.GetTransaction(context.Background(), txSig, &rpc.GetTransactionOpts{
				Encoding:   solana.EncodingBase64,
				Commitment: rpc.CommitmentConfirmed,
			})
			if err != nil {
				fmt.Printf("%s", err.Error())
				continue
			}
			parsedTransaction := getTransactionResult.Transaction.GetParsedTransaction()
			if parsedTransaction == nil {
				continue
			}
			for _, instrction := range parsedTransaction.Message.Instructions {
				if instrction.Parsed != nil && instrction.Parsed.InstructionType == ObligationLiquidate {
					obligationTransactions = append(obligationTransactions, tx)
					break
				}
			}
		*/
	}
	return obligationTransactions
}

func Notify(obligationTx *Transaction, dsdk *dingsdk.DingSdk) {
	dingNotify := &dingsdk.DingNotify{
		MsgType: "text",
		Text: dingsdk.DingContent{
			Content: fmt.Sprintf("liquidator, time: %s, hash: %s",
				time.Now().Format("2006-01-02 15:04:05"),
				obligationTx.TxHash),
		},
		At: dingsdk.DingAt{
			IsAtAll: false,
		},
	}
	dsdk.Notify(dingNotify)
}

func main() {
	ctx, cancel := context.WithCancel(context.Background())
	quit := make(chan os.Signal, 1)
	signal.Notify(quit, os.Interrupt, syscall.SIGTERM, syscall.SIGABRT)
	go shutdown(cancel, quit)

	obligationChan := make(chan *Transaction, 1024)
	wg := sync.WaitGroup{}
	wg.Add(1)
	go func() {
		defer wg.Done()
		ticker := time.NewTicker(time.Second * 15)
		for {
			select {
			case <-ticker.C:
				obligationLiquidate := GetObligationLiquidateTransactions()
				for _, tx := range obligationLiquidate {
					obligationChan <- tx
				}
			case <-ctx.Done():
				return
			}
		}
	}()

	wg.Add(1)
	go func() {
		defer wg.Done()
		dsdk := dingsdk.NewDingSdk(DingUrl)
		for {
			select {
			case obligationTx := <-obligationChan:
				Notify(obligationTx, dsdk)
			case <-ctx.Done():
				return
			}
		}

	}()

	wg.Wait()
}

func shutdown(cancel context.CancelFunc, quit <-chan os.Signal) {
	osCall := <-quit
	fmt.Printf("System call: %v, liquidator is shutting down......\n", osCall)
	cancel()
}
