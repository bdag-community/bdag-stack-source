// Copyright (c) 2014-2016 The btcsuite developers
// Use of this source code is governed by an ISC
// license that can be found in the LICENSE file.

package params

import (
	"encoding/hex"
	"errors"
	"strings"
	"time"

	"github.com/BlockdagNetworkLabs/bdag/core/protocol"
	"github.com/BlockdagNetworkLabs/bdag/core/types"
	"github.com/BlockdagNetworkLabs/bdag/core/types/pow"
	"github.com/ethereum/go-ethereum/common"
)

// CheckForDuplicateHashes checks for duplicate hashes when validating blocks.
// Because of the rule inserting the height into the second (nonce) txOut, there
// should never be a duplicate transaction hash that overwrites another. However,
// because there is a 2^128 chance of a collision, the paranoid user may wish to
// turn this feature on.
var CheckForDuplicateHashes = false

// SigHashOptimization is an optimization for verification of transactions that
// do CHECKSIG operations with hashType SIGHASH_ALL. Although there should be no
// consequences to daemons that are simply running a node, it may be the case
// that you could cause database corruption if you turn this code on, create and
// manipulate your own MsgTx, then include them in blocks. For safety, if you're
// using the daemon with wallet or mining with the daemon this should be disabled.
// If you believe that any MsgTxs in your daemon will be used mutably, do NOT
// turn on this feature. It is disabled by default.
// This feature is considered EXPERIMENTAL, enable at your own risk!
var SigHashOptimization = false

// Checkpoint identifies a known good point in the block chain.  Using
// checkpoints allows a few optimizations for old blocks during initial download
// and also prevents forks from old blocks.
//
// Each checkpoint is selected based upon several factors.  See the
// documentation for blockchain.IsCheckpointCandidate for details on the
// selection criteria.
type Checkpoint struct {
	Layer uint64
	Hash  *common.Hash
}

// Params defines a Blockdag network by its parameters.  These parameters may be
// used by Blockdag applications to differentiate networks as well as addresses
// and keys for one network from those intended for use on another network.
type Params struct {
	// Name defines a human-readable identifier for the network.
	Name string

	// Net defines the magic bytes used to identify the network.
	Net protocol.Network

	// TCPPort defines the default peer-to-peer tcp port for the network.
	DefaultPort string

	// Bootstrap defines a list of boot node for the network that are used
	// as one method to discover peers.
	Bootstrap []string

	// GenesisBlock defines the first block of the chain.
	GenesisBlock *types.SerializedBlock

	// GenesisHash is the starting block hash.
	GenesisHash *common.Hash

	// PowConfig defines the highest allowed proof of work value for a block or lowest difficulty for a block
	PowConfig *pow.PowConfig

	// WorkDiffAlpha is the stake difficulty EMA calculation alpha (smoothing)
	// value. It is different from a normal EMA alpha. Closer to 1 --> smoother.
	WorkDiffAlpha int64

	// WorkDiffWindowSize is the number of windows (intervals) used for calculation
	// of the exponentially weighted average.
	WorkDiffWindowSize int64

	// WorkDiffWindows is the number of windows (intervals) used for calculation
	// of the exponentially weighted average.
	WorkDiffWindows int64

	// CoinbaseMaturity is the number of blocks required before newly mined
	// coins (coinbase transactions) can be spent.
	CoinbaseMaturity uint64

	// TargetTimespan is the desired amount of time that should elapse
	// before the block difficulty requirement is examined to determine how
	// it should be changed in order to maintain the desired block
	// generation rate.
	TargetTimespan time.Duration

	// TargetTimePerBlock is the desired amount of time to generate each
	// block.
	TargetTimePerBlock time.Duration

	// RetargetAdjustmentFactor is the adjustment factor used to limit
	// the minimum and maximum amount of adjustment that can occur between
	// difficulty retargets.
	RetargetAdjustmentFactor int64

	// ReduceMinDifficulty defines whether the network should reduce the
	// minimum required difficulty after a long enough period of time has
	// passed without finding a block.  This is really only useful for test
	// networks and should not be set on a main network.
	ReduceMinDifficulty bool

	// MinDiffReductionTime is the amount of time after which the minimum
	// required difficulty should be reduced when a block hasn't been found.
	//
	// NOTE: This only applies if ReduceMinDifficulty is true.
	MinDiffReductionTime time.Duration

	// MaximumBlockSizes are the maximum sizes of a block that can be
	// generated on the network.  It is an array because the max block size
	// can be different values depending on the results of a voting agenda.
	// The first entry is the initial block size for the network, while the
	// other entries are potential block size changes which take effect when
	// the vote for the associated agenda succeeds.
	MaximumBlockSizes []int

	// MaxTxSize is the maximum number of bytes a serialized transaction can
	// be in order to be considered valid by consensus.
	MaxTxSize int

	// UpgradeActivationDelayBlocks defines the number of layers between an
	// upgrade locking in and becoming active.
	UpgradeActivationDelayBlocks uint64

	// P2PProtocol46ActivationLayer defines the first layer at which peers
	// must advertise protocol version 46. A zero value activates it from
	// genesis.
	P2PProtocol46ActivationLayer uint64

	// Subsidy parameters.
	//
	// Subsidy calculation for exponential reductions:
	// 0 for i in range (0, height / SubsidyReductionInterval):
	// 1     subsidy *= MulSubsidy
	// 2     subsidy /= DivSubsidy
	//
	// Caveat: Don't overflow the int64 register!!

	// BaseSubsidy is the starting subsidy amount for mined blocks.
	BaseSubsidy int64

	// Subsidy reduction multiplier.
	MulSubsidy int64

	// Subsidy reduction divisor.
	DivSubsidy int64

	// SubsidyReductionInterval is the reduction interval in blocks. (Legacy: Block-based)
	SubsidyReductionInterval int64

	// HalvingInterval is the halving interval in seconds.
	HalvingInterval int64

	// TargetTotalSubsidy is the target total subsidy.
	TargetTotalSubsidy int64

	// WorkRewardProportion is the comparative amount of the subsidy given for
	// creating a block.
	WorkRewardProportion uint16

	// StakeRewardProportion is the comparative amount of the subsidy given for
	// casting stake votes (collectively, per block).
	StakeRewardProportion uint16

	// BlockTaxProportion is the inverse of the percentage of funds for each
	// block to allocate to the developer organization.
	// e.g. 10% --> 10 (or 1 / (1/10))
	// Special case: disable taxes with a value of 0
	BlockTaxProportion uint16

	// StakingActivationEpoch defines the epoch number at which the new
	// Staking Contract 70/30 reward split goes live. Before this epoch,
	// 100% of rewards go to the miner.
	StakingActivationEpoch uint64

	// StakingEpochDurationSeconds defines the staking epoch duration used by
	// consensus-managed staking state. When unset, it falls back to the legacy
	// SubsidyReductionInterval value.
	StakingEpochDurationSeconds int64

	// It must be hourglass block.
	// Checkpoints ordered from oldest to newest.
	Checkpoints []Checkpoint

	// EVMStateCommitmentStartOrder defines the first native block order whose
	// header StateRoot must commit to the locally executed EVM result. A zero
	// value leaves the EVM state guard inactive for this network.
	EVMStateCommitmentStartOrder uint64

	// DAG
	BlockDelay    float64
	BlockRate     float64
	SecurityLevel float64
}

type CoinbaseConfig struct {
	Height                    int64
	Version                   string
	ExtraDataIncludedVer      bool
	ExtraDataIncludedNodeInfo bool
}

// TotalSubsidyProportions is the sum of POW Reward, POS Reward, and Tax
// proportions.
func (p *Params) TotalSubsidyProportions() uint16 {
	return p.WorkRewardProportion + p.StakeRewardProportion + p.BlockTaxProportion
}

func (p *Params) StakingEpochSeconds() uint64 {
	if p.StakingEpochDurationSeconds > 0 {
		return uint64(p.StakingEpochDurationSeconds)
	}
	if p.SubsidyReductionInterval > 0 {
		return uint64(p.SubsidyReductionInterval)
	}
	return 7 * 24 * 60 * 60
}

func (p *Params) IsDevelopDiff() bool {
	return p.PowConfig.DifficultyMode == pow.DIFFICULTY_MODE_DEVELOP
}

// RequiredP2PProtocolVersion returns the minimum p2p protocol version required
// for peers at the provided local layer.
func (p *Params) RequiredP2PProtocolVersion(layer uint64) uint32 {
	if p == nil {
		return protocol.ProtocolVersion
	}
	if p.P2PProtocol46ActivationLayer == 0 || layer >= p.P2PProtocol46ActivationLayer {
		return protocol.EVMCheckpointProtocolVersion
	}
	return protocol.BroadcastblockProtocolVersion
}

var (
	// ErrDuplicateNet describes an error where the parameters for a network
	// could not be set due to the network already being a standard
	// network or previously-registered into this package.
	ErrDuplicateNet = errors.New("duplicate network")

	// ErrUnknownHDKeyID describes an error where the provided id which
	// is intended to identify the network for a hierarchical deterministic
	// private extended key is not registered.
	ErrUnknownHDKeyID = errors.New("unknown hd private extended key bytes")
)

var (
	registeredNets       = make(map[protocol.Network]struct{})
	pubKeyHashAddrIDs    = make(map[[2]byte]struct{})
	scriptHashAddrIDs    = make(map[[2]byte]struct{})
	hdPrivToPubKeyIDs    = make(map[[4]byte][]byte)
	bech32SegwitPrefixes = make(map[string]struct{})
)

// Register registers the network parameters for a Bitcoin network.  This may
// error with ErrDuplicateNet if the network is already registered (either
// due to a previous Register call, or the network being one of the default
// networks).
//
// Network parameters should be registered into this package by a main package
// as early as possible.  Then, library packages may lookup networks or network
// parameters based on inputs and work regardless of the network being standard
// or not.
func Register(params *Params) error {
	if _, ok := registeredNets[params.Net]; ok {
		return ErrDuplicateNet
	}
	registeredNets[params.Net] = struct{}{}
	return nil
}

// mustRegister performs the same function as Register except it panics if there
// is an error.  This should only be called from package init functions.
func mustRegister(params *Params) {
	if err := Register(params); err != nil {
		panic("failed to register network: " + err.Error())
	}
}

func init() {
	// Register all default networks when the package is initialized.
	mustRegister(&MainNetParams)
	mustRegister(&TestNetParams)
	mustRegister(&PrivNetParams)
	mustRegister(&MixNetParams)
	mustRegister(&DevNetParams)
}

// TODO, move to hex util
func hexMustDecode(hexStr string) []byte {
	b, err := hex.DecodeString(hexStr)
	if err != nil {
		panic(err)
	}
	return b
}

// IsBech32SegwitPrefix returns whether the prefix is a known prefix for segwit
// addresses on any default or registered network.  This is used when decoding
// an address string into a specific address type.
func IsBech32SegwitPrefix(prefix string) bool {
	prefix = strings.ToLower(prefix)
	_, ok := bech32SegwitPrefixes[prefix]
	return ok
}
