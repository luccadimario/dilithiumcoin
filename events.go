package main

type Log struct {
	ContractAddress string   `json:"contract_address"`
	Topics          []string `json:"topics"`
	Data            []byte   `json:"data"`
	BlockHeight     int64    `json:"block_height"`
	TxSignature     string   `json:"tx_signature"`
	LogIndex        int      `json:"log_index"`
}

type ContractReceipt struct {
	TxSignature     string `json:"tx_signature"`
	ContractAddress string `json:"contract_address,omitempty"`
	GasUsed         uint64 `json:"gas_used"`
	Success         bool   `json:"success"`
	ReturnData      []byte `json:"return_data,omitempty"`
	Logs            []*Log `json:"logs,omitempty"`
}
