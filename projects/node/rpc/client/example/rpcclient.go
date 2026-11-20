// Example connect to local Blockdag RPC server using http.

package main

import (
	"io/ioutil"
	"log"
	"path/filepath"

	"github.com/BlockdagNetworkLabs/bdag/common/util"
	"github.com/BlockdagNetworkLabs/bdag/rpc/client"
)

func main() {
	connCfg := &client.ConnConfig{
		Host:         "localhost:1234",
		User:         "test",
		Pass:         "test",
		DisableTLS:   false,
		HTTPPostMode: true,
	}
	if !connCfg.DisableTLS {
		homeDir := util.AppDataDir("Blockdagd", false)
		certs, err := ioutil.ReadFile(filepath.Join(homeDir, "rpc.cert"))
		if err != nil {
			log.Fatal(err)
		}
		connCfg.Certificates = certs
	}
	client, err := client.New(connCfg, nil)
	if err != nil {
		log.Fatal(err)
	}
	// call RPC: getNodeInfo
	nodeInfo, err := client.GetNodeInfo()
	if err != nil {
		log.Fatal(err)
	}
	log.Printf("Node info: %v", nodeInfo)
}
