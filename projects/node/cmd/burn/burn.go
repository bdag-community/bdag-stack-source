package main

import (
	"flag"
	"fmt"
	"math/rand"
	"os"
	"strings"
	"time"

	"github.com/BlockdagNetworkLabs/bdag/common/encode/base58"
	"github.com/BlockdagNetworkLabs/bdag/params"
	ver "github.com/BlockdagNetworkLabs/bdag/version"
)

const addrSize int = 35
const alphabet string = "123456789ABCDEFGHJKLMNPQRSTUVWXYZabcdefghijkmnopqrstuvwxyz"
const defaultNetwork string = "testnet"

// The generator of the Blockdag burn addresses. The tool which generate a valid
// Blockdag-base58check encoded address for the specified network (the default
// is testnet).
// Note: The template need to be long enough to remain the strong security.
// (recommend at least 16 words)
// See https://en.bitcoin.it/wiki/Vanitygen for the details
func main() {
	var template string
	var network string
	var generate bool
	var showVer bool
	flag.StringVar(&template, "t", "", "template")
	flag.StringVar(&network, "n", defaultNetwork, "network [mainnet|testnet|0.9testnet|mixnet|privnet]")
	flag.BoolVar(&generate, "new", false, "generate new address")
	flag.BoolVar(&showVer, "version", false, "show version")
	flag.Parse()
	if showVer {
		version()
		os.Exit(0)
	}
	p, err := getParams(network)
	exitIfErr(err)
	if template == "" {
		template = genTemplateByParams(p, network)
	}
	addr, err := getAddr(template, p, generate)
	exitIfErr(err)
	fmt.Printf(" network = %s \n", network)
	fmt.Printf("template = %s \n", template)
	fmt.Printf("    addr = %v \n", string(addr))
}

func version() {
	fmt.Printf("Burn version : %q\n", ver.String())
}

func exitIfErr(err error) {
	if err != nil {
		fmt.Printf("error: %v\n", err)
		os.Exit(-1)
	}
}

type NetParams struct {
	Name                 string
	NetworkAddressPrefix string
	PubKeyHashAddrID     [2]byte
}

func getParams(network string) (*NetParams, error) {
	switch network {
	case "testnet":
		p := NetParams{
			Name: params.TestNetParams.Name,
			// NetworkAddressPrefix: params.TestNetParams.NetworkAddressPrefix,
			// PubKeyHashAddrID:     params.TestNetParams.PubKeyHashAddrID,
		}
		return &p, nil
	case "privnet":
		p := NetParams{
			Name: params.PrivNetParams.Name,
			// NetworkAddressPrefix: params.PrivNetParams.NetworkAddressPrefix,
			// PubKeyHashAddrID:     params.PrivNetParams.PubKeyHashAddrID,
		}
		return &p, nil
	case "mainnet":
		p := NetParams{
			Name: params.MainNetParams.Name,
			// NetworkAddressPrefix: params.MainNetParams.NetworkAddressPrefix,
			// PubKeyHashAddrID:     params.MainNetParams.PubKeyHashAddrID,
		}
		return &p, nil
	case "mixnet":
		p := NetParams{
			Name: params.MixNetParams.Name,
			// NetworkAddressPrefix: params.MixNetParams.NetworkAddressPrefix,
			// PubKeyHashAddrID:     params.MixNetParams.PubKeyHashAddrID,
		}
		return &p, nil
	case "devnet":
		p := NetParams{
			Name: params.DevNetParams.Name,
			// NetworkAddressPrefix: params.DevNetParams.NetworkAddressPrefix,
			// PubKeyHashAddrID:     params.DevNetParams.PubKeyHashAddrID,
		}
		return &p, nil
	default:
		return nil, fmt.Errorf("unknown network %s", network)
	}
}
func genTemplateByParams(p *NetParams, network string) string {
	var sb strings.Builder
	sb.WriteString(p.NetworkAddressPrefix)
	if network == "testnet" {
		sb.WriteString("n")
	} else {
		sb.WriteString("m")
	}
	sb.WriteString("Blockdag")
	sb.WriteString(strings.Title(p.Name))
	sb.WriteString("BurnAddress")
	return sb.String()
}

func getAddr(template string, p *NetParams, randomSuffix bool) ([]byte, error) {
	pickSize := addrSize - len(template)
	var sb strings.Builder
	sb.WriteString(template)
	if randomSuffix {
		rand.Seed(time.Now().UnixNano())
		for i := 0; i < pickSize; i++ {
			randomIndex := rand.Intn(58)
			pick := alphabet[randomIndex]
			sb.WriteString(string(pick))
		}
	} else {
		for i := 0; i < pickSize; i++ {
			sb.WriteString("X")
		}
	}
	decoded := base58.Decode([]byte(sb.String()))
	if len(decoded) == 0 {
		return nil, fmt.Errorf("incorrect base58 encoded template %s", sb.String())
	}
	addr, err := base58.BlockdagCheckEncode(decoded[2:22], p.PubKeyHashAddrID[:])
	if err != nil {
		return nil, err
	}
	_, _, err = base58.BlockdagCheckDecode(string(addr))
	if err != nil {
		return nil, err
	}
	return addr, nil
}
