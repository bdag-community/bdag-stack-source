package consensus

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"math/big"
	"os"
	"testing"

	mparams "github.com/BlockdagNetworkLabs/bdag/bevm/params"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/common/hexutil"
	"github.com/ethereum/go-ethereum/core/rawdb"
	"github.com/ethereum/go-ethereum/core/state"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/crypto"
	"github.com/ethereum/go-ethereum/rlp"
	"github.com/ethereum/go-ethereum/trie"
	"github.com/ethereum/go-ethereum/triedb"
	"github.com/stretchr/testify/require"
)

type mainnetStateWitness struct {
	ChainID  uint64        `json:"chainId"`
	Header   *types.Header `json:"header"`
	Accounts []struct {
		Address common.Address `json:"address"`
		Code    hexutil.Bytes  `json:"code"`
		Proof   struct {
			Address      common.Address  `json:"address"`
			AccountProof []hexutil.Bytes `json:"accountProof"`
			Balance      *hexutil.Big    `json:"balance"`
			Nonce        hexutil.Uint64  `json:"nonce"`
			CodeHash     common.Hash     `json:"codeHash"`
			StorageHash  common.Hash     `json:"storageHash"`
			StorageProof []struct {
				Key   common.Hash     `json:"key"`
				Value *hexutil.Big    `json:"value"`
				Proof []hexutil.Bytes `json:"proof"`
			} `json:"storageProof"`
		} `json:"proof"`
	} `json:"accounts"`
}

// Every account/storage node is authenticated to the agreed public EVM root.
// This intentionally does not claim native history, full replay or finality.
func mainnetStateFromWitness(w *mainnetStateWitness) (*state.StateDB, error) {
	if w.ChainID != 1404 || w.Header == nil || len(w.Accounts) != 24 {
		return nil, fmt.Errorf("invalid public witness")
	}
	db := rawdb.NewMemoryDatabase()
	for _, a := range w.Accounts {
		for _, node := range a.Proof.AccountProof {
			rawdb.WriteLegacyTrieNode(db, crypto.Keccak256Hash(node), node)
		}
		for _, p := range a.Proof.StorageProof {
			for _, node := range p.Proof {
				rawdb.WriteLegacyTrieNode(db, crypto.Keccak256Hash(node), node)
			}
		}
	}
	seen := map[common.Address]bool{}
	for _, a := range w.Accounts {
		p := a.Proof
		if seen[a.Address] || a.Address != p.Address || p.Balance == nil {
			return nil, fmt.Errorf("invalid witness account")
		}
		seen[a.Address] = true
		encoded, err := trie.VerifyProof(w.Header.Root, crypto.Keccak256(a.Address.Bytes()), db)
		if err != nil {
			return nil, err
		}
		if encoded == nil {
			// A zero codeHash is accepted only after cryptographic non-inclusion.
			if len(a.Code) != 0 || p.Balance.ToInt().Sign() != 0 || p.Nonce != 0 || len(p.StorageProof) != 0 {
				return nil, fmt.Errorf("false absent account")
			}
			continue
		}
		var account types.StateAccount
		if err := rlp.DecodeBytes(encoded, &account); err != nil {
			return nil, err
		}
		if account.Nonce != uint64(p.Nonce) || account.Balance.ToBig().Cmp(p.Balance.ToInt()) != 0 || account.Root != p.StorageHash ||
			common.BytesToHash(account.CodeHash) != p.CodeHash || crypto.Keccak256Hash(a.Code) != p.CodeHash {
			return nil, fmt.Errorf("account/code proof mismatch")
		}
		for _, slot := range p.StorageProof {
			encoded, err := trie.VerifyProof(account.Root, crypto.Keccak256(slot.Key.Bytes()), db)
			if err != nil {
				return nil, err
			}
			var scalar []byte
			if encoded != nil {
				if err := rlp.DecodeBytes(encoded, &scalar); err != nil {
					return nil, err
				}
			}
			if slot.Value == nil || new(big.Int).SetBytes(scalar).Cmp(slot.Value.ToInt()) != 0 {
				return nil, fmt.Errorf("storage proof value mismatch")
			}
		}
		rawdb.WriteCode(db, p.CodeHash, a.Code)
	}
	return state.New(w.Header.Root, state.NewDatabase(triedb.NewDatabase(db, nil), nil))
}

func TestMainnetDAOPublicStateWitnessPreservesActualLegacyRestrictionAndHistoricalLedgerRoots(t *testing.T) {
	rawWitness, err := os.ReadFile("testdata/mainnet-dao/state-witness.json")
	require.NoError(t, err)
	var w mainnetStateWitness
	require.NoError(t, json.Unmarshal(rawWitness, &w))
	require.Equal(t, uint64(26959041), w.Header.Number.Uint64())
	require.Equal(t, common.HexToHash("0x8e6df8e857d475879a13e9a790119b56da5c71f5a9d5227ea215dc946f6d464a"), w.Header.Hash())
	require.Equal(t, common.HexToHash("0x490e751b7a440e408e1203fea0f082a07a2adee165edf29d523b788f340b5af1"), w.Header.Root)
	s, err := mainnetStateFromWitness(&w)
	require.NoError(t, err)
	c, h, _, _ := mainnetDAOTestFixture(t)
	raw, err := os.ReadFile("testdata/mainnet-dao/bundle.json")
	require.NoError(t, err)
	contextRaw, err := os.ReadFile("testdata/mainnet-dao/context.json")
	require.NoError(t, err)
	var ctx mainnetRuntimeContext
	require.NoError(t, json.Unmarshal(contextRaw, &ctx))
	c.genesis, c.baseline = ctx.GenesisHeader, ctx.BaselineHeader
	g := c.c.BdagMainnetDAO
	g.ArtifactSHA256 = common.Hash(sha256.Sum256(raw))
	g.EarliestEVMBlock = w.Header.Number.Uint64() + 2
	h.Number = new(big.Int).SetUint64(g.EarliestEVMBlock)
	c.parent.Number = new(big.Int).Sub(h.Number, big.NewInt(1))
	h.ParentHash = c.parent.Hash()
	// Future timing, parent and native threshold are explicitly synthetic. Only
	// the starting EVM root and all accessed state are actual public proof data.
	addresses := []common.Address{mparams.StakingContractAddr, mparams.BlocklistContractAddr, mparams.SelectiveBlocklistContractAddr, mparams.ChainTallyAddr}
	roots := map[common.Address]common.Hash{}
	balances := map[common.Address]*big.Int{}
	words := map[common.Address]map[common.Hash]common.Hash{}
	// Whitelist only the installer's documented mutable words, never every
	// supplied proof/read slot. This keeps the root check sensitive to an
	// unauthorized change in a known clock/financial slot as well as unknown maps.
	allowed := map[common.Address][]common.Hash{
		mparams.StakingContractAddr:            {common.Hash{}, erc1967ImplementationSlot},
		mparams.BlocklistContractAddr:          {common.Hash{}},
		mparams.SelectiveBlocklistContractAddr: {common.Hash{}},
		mparams.ChainTallyAddr: {mparams.SlotRewardVotingEpoch, mparams.SlotRewardVotingBootstrapEpochPlus,
			mparams.SlotRewardEpochScheduleLength, mparams.SlotGovernanceTimeFloor,
			separateStakingActiveAddressSlot, separateStakingCutoverEpochPlusSlot,
			rewardScheduleWordSlot(0), rewardScheduleWordSlot(1), rewardScheduleWordSlot(2),
			mainnetDAOCommitmentSlot, mainnetDAOAppliedSlot},
	}
	for _, a := range addresses {
		roots[a] = s.GetStorageRoot(a)
		balances[a] = new(big.Int).Set(s.GetBalance(a).ToBig())
		words[a] = map[common.Hash]common.Hash{}
		for _, slot := range allowed[a] {
			words[a][slot] = s.GetState(a, slot)
		}
	}
	require.NoError(t, s.Error())
	require.NoError(t, applyMainnetDAOFork(c, h, s, raw))
	for _, a := range addresses {
		require.Zero(t, balances[a].Cmp(s.GetBalance(a).ToBig()), "actual backing changed")
	}
	restored := s.Copy()
	for a, slots := range words {
		for key, value := range slots {
			restored.SetState(a, key, value)
		}
	}
	restored.IntermediateRoot(false)
	require.NoError(t, restored.Error())
	for _, a := range addresses {
		require.Equal(t, roots[a], restored.GetStorageRoot(a), "unrelated canonical storage changed at %s", a)
	}
	// A proof/read slot is not automatically permitted to change. In particular,
	// restoring authorized words must never conceal an altered Legacy clock.
	corrupted := s.Copy()
	corrupted.SetState(mparams.StakingContractAddr, mparams.SlotGenesisTime, common.BigToHash(big.NewInt(123)))
	for a, slots := range words {
		for key, value := range slots {
			corrupted.SetState(a, key, value)
		}
	}
	corrupted.IntermediateRoot(false)
	require.NoError(t, corrupted.Error())
	require.NotEqual(t, roots[mparams.StakingContractAddr], corrupted.GetStorageRoot(mparams.StakingContractAddr), "restoration hid an unauthorized financial/clock mutation")

	after := s.Copy()
	expected := after.IntermediateRoot(false)
	require.NoError(t, after.Error())
	require.NoError(t, applyMainnetDAOFork(c, h, s, raw))
	require.Equal(t, expected, s.IntermediateRoot(false))
	require.NoError(t, s.Error())
	t.Logf("public EVM witness block=%d root=%s: actual Legacy/restriction/ledger roots and backing preserved; synthetic future activation gates", w.Header.Number.Uint64(), w.Header.Root)
}

func TestMainnetDAOPublicWitnessRejectsTamperedRootCodeAndStorageValue(t *testing.T) {
	raw, err := os.ReadFile("testdata/mainnet-dao/state-witness.json")
	require.NoError(t, err)
	for _, mutation := range []func(*mainnetStateWitness){
		func(w *mainnetStateWitness) { w.Header.Root[0] ^= 1 },
		func(w *mainnetStateWitness) {
			for i := range w.Accounts {
				if len(w.Accounts[i].Code) > 0 {
					w.Accounts[i].Code[0] ^= 1
					return
				}
			}
		},
		func(w *mainnetStateWitness) {
			for i := range w.Accounts {
				if len(w.Accounts[i].Proof.StorageProof) > 0 {
					w.Accounts[i].Proof.StorageProof[0].Value = (*hexutil.Big)(big.NewInt(123))
					return
				}
			}
		},
	} {
		var w mainnetStateWitness
		require.NoError(t, json.Unmarshal(raw, &w))
		mutation(&w)
		_, err := mainnetStateFromWitness(&w)
		require.Error(t, err)
	}
}
