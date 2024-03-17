package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log"
	"math/big"
	"os"
	"time"

	"github.com/ethereum/go-ethereum"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/ethclient"
	"github.com/joho/godotenv"
)

// Gnosis Safe transaction bundle structs
type TransactionBundle struct {
	ChainID      string        `json:"chainId"`
	CreatedAt    int64         `json:"createdAt"`
	Meta         Meta          `json:"meta"`
	Transactions []Transaction `json:"transactions"`
}

type Meta struct {
	Name        string `json:"name"`
	Description string `json:"description"`
}

type Transaction struct {
	To    string `json:"to"`
	Value string `json:"value"`
}

// Util structs
type TxInfo struct {
	From   common.Address
	GasWei *big.Int
}

func fatalLog(err error) {
	if err != nil {
		log.Fatalf("Error: %v\n", err)
	}
}

func main() {
	_, err := os.Stat(".env")
	if !os.IsNotExist(err) {
		err := godotenv.Load()
		fatalLog(err)
	}

	var rpcURL string
	if rpcURL = os.Getenv("RPC_URL"); rpcURL == "" {
		fatalLog(fmt.Errorf("RPC_URL not set"))
	}

	// 10 second timeout for all RPC requests
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	// Set up the client
	client, err := ethclient.Dial(rpcURL)
	fatalLog(err)
	defer client.Close()

	// Create a buffer to store a text report
	var report bytes.Buffer

	// Get block bounds for report
	startBlockNumber := big.NewInt(18949176) // STARTING BLOCK
	startBlock, err := client.BlockByNumber(ctx, startBlockNumber)
	fatalLog(err)

	latestBlock, err := client.BlockByNumber(ctx, nil)
	fatalLog(err)

	startBlockTime, latestBlockTime := time.Unix(int64(startBlock.Time()), 0), time.Unix(int64(latestBlock.Time()), 0)
	report.WriteString("# JuiceboxDAO Gas Reimbursements\n\n")
	report.WriteString(fmt.Sprintf("From %s to %s (block %s to block %s)\n\n", startBlockTime.Format(time.RFC1123),
		latestBlockTime.Format(time.RFC1123), startBlockNumber.String(), latestBlock.Number().String()))

	// The groups of transactions to get, specified by addresses and event topics
	txGroups := []struct {
		Label     string
		Addresses []common.Address
		Topics    [][]common.Hash
	}{
		{
			// Multisig
			Label: "Execute multisig tx",
			Addresses: []common.Address{
				common.HexToAddress("0xAF28bcB48C40dBC86f52D459A6562F658fc94B1e"),
			},
			Topics: [][]common.Hash{
				// ExecutionSuccess
				{common.HexToHash("0x442e715f626346e8c54381002da614f62bee8d27386535b2521ec8540898556e")},
			},
		},
		{
			// Terminals
			Label: "Distribute JuiceboxDAO payouts",
			Addresses: []common.Address{
				common.HexToAddress("0xFA391De95Fcbcd3157268B91d8c7af083E607A5C"), // JBETHPaymentTerminal3_1
				common.HexToAddress("0x457cD63bee88ac01f3cD4a67D5DCc921D8C0D573"), // JBETHPaymentTerminal3_1_1
				common.HexToAddress("0x1d9619E10086FdC1065B114298384aAe3F680CC0"), // JBETHPaymentTerminal3_1_2
			},
			Topics: [][]common.Hash{
				// DistributePayouts
				{common.HexToHash("0xc41a8d26c70cfcf1b9ea10f82482ac947b8be5bea2750bc729af844bbfde1e28")},
				{}, {},
				{common.HexToHash("0x0000000000000000000000000000000000000000000000000000000000000001")}, // projectId 1
			},
		},
		{
			Label: "Distribute JuiceboxDAO reserved tokens",
			Addresses: []common.Address{
				common.HexToAddress("0xFFdD70C318915879d5192e8a0dcbFcB0285b3C98"), // JBController
				common.HexToAddress("0xA139D37275d1fF7275e6F33821898934Bc8Cb7B6"), // JBController3_0_1
				common.HexToAddress("0x97a5b9D9F0F7cD676B69f584F29048D0Ef4BB59b"), // JBController3_1
			},
			Topics: [][]common.Hash{
				// DistributeReservedTokens
				{common.HexToHash("0xb12d7a78048433f69fe6d30145bf08aad8e82985b96e4db6d5c6a7e94d57086e")},
				{}, {},
				{common.HexToHash("0x0000000000000000000000000000000000000000000000000000000000000001")}, // projectId 1
			},
		},
	}

	includedTxs := make(map[common.Hash]TxInfo)
	reportDetails := make(map[common.Address]string)

	for _, txGroup := range txGroups {
		query := ethereum.FilterQuery{
			FromBlock: startBlockNumber,
			ToBlock:   latestBlock.Number(),
			Addresses: txGroup.Addresses,
			Topics:    txGroup.Topics,
		}

		logs, err := client.FilterLogs(ctx, query)
		fatalLog(err)

		for _, lg := range logs {
			// If we've already seen this transaction, skip it
			_, ok := includedTxs[lg.TxHash]
			if ok {
				continue
			}

			tx, _, err := client.TransactionByHash(ctx, lg.TxHash)
			fatalLog(err)

			from, err := client.TransactionSender(ctx, tx, lg.BlockHash, lg.Index)
			fatalLog(err)

			receipt, err := client.TransactionReceipt(ctx, lg.TxHash)
			fatalLog(err)

			// get the actual gas used
			gasCost := new(big.Int).Mul(receipt.EffectiveGasPrice, new(big.Int).SetUint64(receipt.GasUsed))

			fmted := new(big.Float).Quo(new(big.Float).SetInt(gasCost), new(big.Float).SetInt(big.NewInt(1e18)))
			reportDetails[from] += fmt.Sprintf("Type: %s\nTxHash: %s\nGas: %s ETH\nBlock: %d\n\n",
				txGroup.Label, lg.TxHash.Hex(), fmted.String(), lg.BlockNumber)

			includedTxs[lg.TxHash] = TxInfo{from, gasCost}
		}
	}

	// Calculate totals
	totals := make(map[common.Address]*big.Int)
	for _, v := range includedTxs {
		if totals[v.From] == nil {
			totals[v.From] = big.NewInt(0)
		}
		totals[v.From] = new(big.Int).Add(totals[v.From], v.GasWei)
	}

	// Finish the report
	for k, v := range reportDetails {
		report.WriteString("## Summary for " + k.Hex() + "\n\n")

		fmted := new(big.Float).Quo(new(big.Float).SetInt(totals[k]), new(big.Float).SetInt(big.NewInt(1e18)))
		report.WriteString("Total gas to reimburse: " + fmted.String() + " ETH\n\n")
		report.WriteString("### Transactions\n\n")
		report.WriteString(v)
	}

	bundle := TransactionBundle{
		ChainID:   "1",
		CreatedAt: time.Now().Unix(),
		Meta: Meta{
			Name:        "JuiceboxDAO Gas Reimbursements",
			Description: fmt.Sprintf("Gas reimbursements from block %s to %s", startBlockNumber.String(), latestBlock.Number().String()),
		},
		Transactions: []Transaction{},
	}

	for k, v := range totals {
		bundle.Transactions = append(bundle.Transactions, Transaction{
			To:    k.Hex(),
			Value: v.String(),
		})
	}

	json, err := json.Marshal(bundle)
	fatalLog(err)

	err = os.WriteFile("bundle.json", json, 0644)
	fatalLog(err)

	err = os.WriteFile("report.txt", report.Bytes(), 0644)
	fatalLog(err)
}
