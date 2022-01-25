package config

import "github.com/gagliardetto/solana-go"

type Node struct {
	Rpc    string `json:"rpc"`
	Ws     string `json:"ws"`
	Usable bool   `json:"usable"`
}

type Config struct {
	Endpoints         []*Node            `json:"endpoints"`
	TxEndpoints       []*Node            `json:"tx_endpoints"`
	TxEndpointSize    int                `json:"tx_endpoint_size"`
	BlockHashEndpoint string             `json:"blockhash_endpoint"`
	TpuEndpoint       string             `json:"tpu_endpoint"`
	SendTx            int                `json:"send_tx"`
	NodeId            int                `json:"node_id"`
	Programs          []solana.PublicKey `json:"programs"`
	User              solana.PublicKey   `json:"user"`
	Key               string             `json:"key"`
	Which             int                `json:"which"`
	WorkSpace         string             `json:"workspace"`
	DingUrl           string             `json:"ding-url"`
	DBUrl             string             `json:"db_url"`
	DBScheme          string             `json:"db_scheme"`
	DBUser            string             `json:"db_user"`
	DBPasswd          string             `json:"db_passwd"`
	Listen            string             `json:"listen"`
	Usdc              uint64             `json:"usdc"`
	UsdcAccount       string             `json:"usdc_account"`
}
