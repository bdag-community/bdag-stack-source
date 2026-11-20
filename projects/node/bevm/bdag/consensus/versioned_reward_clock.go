package consensus

import (
	"fmt"
	"math"
	"math/big"

	"github.com/BlockdagNetworkLabs/bdag/bevm/params"
	"github.com/BlockdagNetworkLabs/bdag/common/epochclock"
	qparams "github.com/BlockdagNetworkLabs/bdag/params"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/state"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/crypto"
	ethparams "github.com/ethereum/go-ethereum/params"
)

// No shipped manifest or runtime writes this marker. A distinct, fully gated
// timing migration must install it atomically with the clock and runtimes.
// Historical RewardGovernanceV3 bootstrap and its marker are separate.
var testnetTimingAppliedSlot = crypto.Keccak256Hash([]byte("bdag.testnet.timing.v1.activation"))

// prepareVersionedRewardClock runs before transactions so staking checkpoints,
// ballots and both native reward chambers agree at rollover. It never enables
// the new clock itself. Every fallible validation precedes the first write.
func prepareVersionedRewardClock(config *ethparams.ChainConfig, header *types.Header, s *state.StateDB) error {
	if s == nil {
		return nil
	}
	mainnet := s.GetState(params.ChainTallyAddr, mainnetDAOAppliedSlot) != (common.Hash{})
	testnet := s.GetState(params.ChainTallyAddr, testnetTimingAppliedSlot) != (common.Hash{})
	if !mainnet && !testnet {
		return nil
	}
	if mainnet && testnet {
		return fmt.Errorf("conflicting native clock namespaces")
	}
	activationSlot := testnetTimingAppliedSlot
	genesis, duration := uint64(0), uint64(60)
	if mainnet {
		if config == nil || config.BdagMainnetDAO == nil || qparams.ActiveNetParams == nil || qparams.ActiveNetParams.Net != qparams.MainNetParams.Net {
			return fmt.Errorf("Mainnet reward clock requires its production profile")
		}
		if err := config.BdagMainnetDAO.Validate(config.ChainID); err != nil {
			return err
		}
		if header == nil || header.Number == nil || header.Number.Cmp(new(big.Int).SetUint64(config.BdagMainnetDAO.EarliestEVMBlock)) < 0 {
			return fmt.Errorf("Mainnet clock precedes production EVM floor")
		}
		activationSlot = mainnetDAOAppliedSlot
		genesis, duration = config.BdagMainnetDAO.LegacyGenesisTime, 604800
	} else {
		if config == nil || config.ChainID == nil || config.ChainID.Cmp(params.BdagTestnetChainConfig.ChainID) != 0 ||
			qparams.ActiveNetParams == nil || qparams.ActiveNetParams.Net != qparams.TestNetParams.Net {
			return fmt.Errorf("versioned testnet clock is restricted to chain 1043")
		}
		if config.BdagGovernanceForkBlock == nil || config.BdagGovernanceFork == nil ||
			header == nil || header.Number == nil ||
			header.Number.Cmp(config.BdagGovernanceForkBlock) < 0 || s.GetState(params.ChainTallyAddr, governanceAppliedSlot) == (common.Hash{}) {
			return fmt.Errorf("versioned clock requires prior governance bootstrap")
		}
		genesis = uint64(qparams.TestNetParams.GenesisBlock.Block().Header.Timestamp.Unix())
	}
	if header == nil || header.Number == nil || header.Number.Sign() <= 0 ||
		s.GetState(params.ChainTallyAddr, activationSlot).Big().Cmp(header.Number) > 0 {
		return fmt.Errorf("versioned clock activation marker exceeds block")
	}
	clock, err := rewardEpochScheduleFromState(s)
	if err != nil {
		return err
	}
	origin, err := clock.Bounds(0)
	if err != nil {
		return err
	}
	if origin.Start != genesis || origin.Duration != duration || (mainnet && s.GetState(params.ChainTallyAddr, params.SlotRewardEpochScheduleLength) != common.BigToHash(big.NewInt(1))) {
		return fmt.Errorf("versioned clock cannot rewrite its historical network clock")
	}
	read := func(slot common.Hash) (uint64, error) {
		value := s.GetState(params.ChainTallyAddr, slot).Big()
		if value.BitLen() > 64 {
			return 0, epochclock.ErrOverflow
		}
		return value.Uint64(), nil
	}
	var words [4]uint64
	for i, slot := range []common.Hash{params.SlotRewardVotingEpoch, params.SlotGovernanceTimeFloor,
		params.SlotRewardVotingBootstrapEpochPlus, params.SlotRewardVotingFirstEpochPlus} {
		words[i], err = read(slot)
		if err != nil {
			return err
		}
	}
	current, timeFloor, bootstrapPlus, firstPlus := words[0], words[1], words[2], words[3]
	if current == math.MaxUint64 || bootstrapPlus == 0 || bootstrapPlus-1 > current ||
		(firstPlus != 0 && (firstPlus <= bootstrapPlus || firstPlus > current)) {
		return fmt.Errorf("invalid existing reward eligibility markers")
	}
	now := max(header.Time, timeFloor)
	status, err := clock.At(now, current)
	if err != nil {
		return err
	}
	if status.Epoch == math.MaxUint64 {
		return epochclock.ErrOverflow
	}
	// Advancing eligibility preserves the original bootstrap and first complete
	// epoch. Installing a slower clock must not reset earned voting history.
	advanceRewardVotingEpoch(s, status.Epoch)
	s.SetState(params.ChainTallyAddr, params.SlotGovernanceTimeFloor,
		common.BigToHash(new(big.Int).SetUint64(max(now, status.Start))))
	return nil
}
