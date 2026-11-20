// Example connect to local Blockdag RPC server using websockets.

package main

import (
	"encoding/hex"
	"fmt"
	"io/ioutil"
	"log"
	"path/filepath"
	"time"

	"github.com/BlockdagNetworkLabs/bdag/common/util"
	j "github.com/BlockdagNetworkLabs/bdag/core/json"
	"github.com/BlockdagNetworkLabs/bdag/core/types"
	"github.com/BlockdagNetworkLabs/bdag/core/types/pow"
	"github.com/BlockdagNetworkLabs/bdag/rpc/client"
	"github.com/BlockdagNetworkLabs/bdag/rpc/client/cmds"
)

func main() {
	ntfnHandlers := client.NotificationHandlers{
		OnBlockConnected: func(hash *common.Hash, height, order int64, t time.Time, txs []*types.Transaction) {
			fmt.Println("OnBlockConnected", hash, order, len(txs))
		},
		OnBlockDisconnected: func(hash *common.Hash, height, order int64, t time.Time, txs []*types.Transaction) {
			fmt.Println("OnBlockDisconnected", hash, order, len(txs))
		},
		OnBlockAccepted: func(hash *common.Hash, height, order int64, t time.Time, txs []*types.Transaction) {
			fmt.Println("OnBlockAccepted", hash, height, order, len(txs))
		},
		OnReorganization: func(hash *common.Hash, order int64, olds []*common.Hash) {
			fmt.Println("OnReorganization", hash, order, len(olds))
		},
		OnTxConfirm: func(txConfirm *cmds.TxConfirmResult) {
			fmt.Println("OnTxConfirm", txConfirm.Tx, txConfirm.Confirms, txConfirm.Order)
		},
		OnTxAcceptedVerbose: func(c *client.Client, tx *j.DecodeRawTransactionResult) {
			fmt.Println("OnTxAcceptedVerbose", tx.Hash, tx.Confirms, tx.Order, tx.Txvalid, tx.IsBlue, tx.Duplicate)
		},
		OnRescanProgress: func(rescanPro *cmds.RescanProgressNtfn) {
			fmt.Println("OnRescanProgress", rescanPro.Order, rescanPro.Hash)
		},
		OnRescanFinish: func(rescanFinish *cmds.RescanFinishedNtfn) {
			fmt.Println("OnRescanFinish", rescanFinish.Order, rescanFinish.Hash)
		},
		OnNodeExit: func(p *cmds.NodeExitNtfn) {
			fmt.Println("OnNodeExit", p)
		},
	}

	connCfg := &client.ConnConfig{
		Host:       "127.0.0.1:1234",
		Endpoint:   "ws",
		User:       "test",
		Pass:       "test",
		DisableTLS: true,
	}
	if !connCfg.DisableTLS {
		homeDir := util.AppDataDir("Blockdagd", false)
		certs, err := ioutil.ReadFile(filepath.Join(homeDir, "rpc.cert"))
		if err != nil {
			log.Fatal(err)
		}
		connCfg.Certificates = certs
	}

	c, err := client.New(connCfg, &ntfnHandlers)
	if err != nil {
		log.Fatal(err)
	}
	// Register for block connect and disconnect notifications.
	if err := c.NotifyBlocks(); err != nil {
		log.Fatal(err)
	}
	log.Println("NotifyBlocks: Registration Complete")
	// time.Sleep(20 * time.Second)
	// Register for transaction connect and disconnect notifications.
	if err := c.NotifyNewTransactions(true); err != nil {
		log.Fatal(err)
	}
	// first add addresses
	if err := c.NotifyTxsByAddr(false, []string{"RmBCkqCZ17hXsUTGhKXT3s6CZ5b6MuNhwcZ"}, nil); err != nil {
		log.Fatal(err)
	}
	log.Println("NotifyTxsByAddr: NotifyTxsByAddr Complete")
	if err := c.NotifyTxsConfirmed([]cmds.TxConfirm{
		{
			Txid:          "f5a95e6cfa754a8c81579fb8871bc97e965383d98c61197a74561f0cd8e24be2",
			Order:         1,
			Confirmations: 5,
			EndHeight:     100,
		},
	}); err != nil {
		log.Fatal(err)
	}
	log.Println("NotifyTransaction: Registration Complete")

	if err := c.RemoveTxsConfirmed([]cmds.TxConfirm{
		{
			Txid: "f5a95e6cfa754a8c81579fb8871bc97e965383d98c61197a74561f0cd8e24be2",
		},
	}); err != nil {
		log.Fatal(err)
	}
	time.Sleep(1 * time.Second)
	if err := c.Rescan(0, 110, []string{"RmBCkqCZ17hXsUTGhKXT3s6CZ5b6MuNhwcZ"}, nil); err != nil {
		log.Fatal(err)
	}
	log.Println("Rescan: Rescan Complete")
	// call RPC: getNodeInfo
	nodeInfo, err := c.GetNodeInfo()
	if err != nil {
		log.Fatal(err)
	}
	log.Printf("Node info: %v", nodeInfo)
	for i := 0; i < 100; i++ {
		h, err := c.GetRemoteGBT(8)
		if err != nil {
			fmt.Println(err)
		}
		for n := uint64(0); n < 0xffffffffffff; n++ {
			h.Pow.(*pow.bdagXKeccakV1).SetNonce(n)
			r := common.HashbdagXKeccakV1(h.BlockData()[:117])
			target := "0000000000000000000000000000000000000000000000000000000000ffff7f"
			b, _ := hex.DecodeString(target)
			t := common.Hash{}
			_ = t.SetBytes(b)
			fmt.Println(i, "===GetRemoteGBT", r.String(), t.String())
			if pow.HashToBig(&r).Cmp(pow.HashToBig(&t)) <= 0 {
				go func(h *types.BlockHeader, i int) {
					txId, err := c.SubmitBlockHeader(h)
					if err != nil {
						fmt.Println(err)
					}
					fmt.Println(i, "===SubmitBlockHeader", txId)
				}(h, i)

				break
			}
		}
	}

	waitTime := time.Second * 300
	log.Printf("Client shutdown in %s...\n", waitTime.String())
	time.AfterFunc(waitTime, func() {
		log.Println("Client shutting down...")
		c.Shutdown()
		log.Println("Client shutdown complete.")
	})
	c.WaitForShutdown()
}
