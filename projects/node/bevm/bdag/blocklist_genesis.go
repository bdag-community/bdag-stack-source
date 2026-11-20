package bdag

import (
	"os"
	"strings"

	mparams "github.com/BlockdagNetworkLabs/bdag/bevm/params"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/common/hexutil"
	"github.com/ethereum/go-ethereum/core"
	"github.com/ethereum/go-ethereum/core/types"
)

// ApplyBlocklistGenesisSeed optionally predeploys the blocklist governance
// contract and/or seeds blocked addresses into the genesis state, controlled
// entirely by environment variables. When none of the variables are set this
// is a no-op, so the default genesis (and its hash) is unchanged. This is a
// dev/test facility; mainnet uses a regular contract deployment instead.
//
//	BLOCKLIST_GENESIS_BLOCKED  Comma-separated 0x addresses to mark as blocked
//	                           at genesis (writes the mapping slot directly;
//	                           sufficient to exercise the consensus rule with
//	                           no contract code).
//	BLOCKLIST_GENESIS_OWNER    0x address stored at the owner slot (slot 0).
//	BLOCKLIST_CONTRACT_CODE    Hex runtime bytecode of the governance contract
//	                           to predeploy at BlocklistContractAddr, enabling
//	                           on-chain governance writes at runtime.
//
// Reads always happen at the hardcoded mparams.BlocklistContractAddr.
func ApplyBlocklistGenesisSeed(genesis *core.Genesis) {
	blocked := strings.TrimSpace(os.Getenv("BLOCKLIST_GENESIS_BLOCKED"))
	owner := strings.TrimSpace(os.Getenv("BLOCKLIST_GENESIS_OWNER"))
	codeHex := strings.TrimSpace(os.Getenv("BLOCKLIST_CONTRACT_CODE"))
	if blocked == "" && owner == "" && codeHex == "" {
		return
	}

	if genesis.Alloc == nil {
		genesis.Alloc = make(types.GenesisAlloc)
	}
	addr := mparams.BlocklistContractAddr
	acct := genesis.Alloc[addr]
	if acct.Storage == nil {
		acct.Storage = make(map[common.Hash]common.Hash)
	}

	one := common.BigToHash(common.Big1)
	if codeHex != "" {
		if code, err := hexutil.Decode(codeHex); err == nil {
			acct.Code = code
		} else {
			log.Warn("Ignoring invalid BLOCKLIST_CONTRACT_CODE", "err", err)
		}
	}
	if owner != "" {
		o := common.HexToAddress(owner)
		acct.Storage[mparams.SlotBlocklistOwner] = common.BytesToHash(o.Bytes())
		log.Info("Genesis blocklist owner seed", "owner", o.Hex())
	}
	if blocked != "" {
		for _, entry := range strings.Split(blocked, ",") {
			entry = strings.TrimSpace(entry)
			if entry == "" {
				continue
			}
			a := common.HexToAddress(entry)
			acct.Storage[blockedSlot(a)] = one
			log.Info("Genesis blocklist seed", "address", a.Hex())
		}
	}

	genesis.Alloc[addr] = acct
}
