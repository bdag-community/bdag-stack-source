package consensus

import (
	"encoding/binary"
	"math/big"
	"testing"

	"github.com/BlockdagNetworkLabs/bdag/bevm/params"
	qparams "github.com/BlockdagNetworkLabs/bdag/params"
	"github.com/ethereum/go-ethereum/common"
	ethconsensus "github.com/ethereum/go-ethereum/consensus"
	"github.com/ethereum/go-ethereum/core/rawdb"
	"github.com/ethereum/go-ethereum/core/state"
	"github.com/ethereum/go-ethereum/core/types"
	ethparams "github.com/ethereum/go-ethereum/params"
	"github.com/ethereum/go-ethereum/triedb"
	"github.com/stretchr/testify/assert"
)

type finalizeTestChainReader struct {
	config *ethparams.ChainConfig
}

var _ ethconsensus.ChainHeaderReader = finalizeTestChainReader{}

func (cr finalizeTestChainReader) Config() *ethparams.ChainConfig                 { return cr.config }
func (cr finalizeTestChainReader) CurrentHeader() *types.Header                { return nil }
func (cr finalizeTestChainReader) GetHeader(common.Hash, uint64) *types.Header { return nil }
func (cr finalizeTestChainReader) GetHeaderByNumber(uint64) *types.Header      { return nil }
func (cr finalizeTestChainReader) GetHeaderByHash(common.Hash) *types.Header   { return nil }
func (cr finalizeTestChainReader) GetTd(common.Hash, uint64) *big.Int          { return nil }

func TestUpdateStakingTallies(t *testing.T) {
	// Setup StateDB
	// Setup DB
	db := rawdb.NewMemoryDatabase()
	// Wrap with triedb
	tdb := triedb.NewDatabase(db, nil)
	sdb := state.NewDatabase(tdb, nil)
	statedb, _ := state.New(common.Hash{}, sdb)

	engine := New(db)
	miner := common.HexToAddress("0x1234567890123456789012345678901234567890")

	// Initial state: current epoch 0
	statedb.SetState(params.ChainTallyAddr, params.SlotCurrentEpoch, common.Hash{0})
	initialBase := common.Hash{100}
	statedb.SetState(params.ChainTallyAddr, params.SlotCurrentTallyBase, initialBase) // Initial base hash

	// Block: Time = Genesis + 1s (Epoch 0)
	genesisTime := uint64(qparams.ActiveNetParams.GenesisBlock.Block().Header.Timestamp.Unix())
	header := &types.Header{
		Number:   big.NewInt(1),
		Time:     genesisTime + 1,
		Coinbase: miner,
	}

	reward := new(big.Int).SetInt64(1e18)
	engine.updateStakingTallies(nil, header, statedb, reward)

	// Verify tally for miner is 1
	tallySlot := calculateMappingSlot(miner, initialBase)
	tally := statedb.GetState(params.ChainTallyAddr, tallySlot).Big()
	assert.Equal(t, int64(1e18), tally.Int64())
}

func TestRewardVotingLedgerUsesEarnedMiningRewardAndSkipsPartialBootstrapEpoch(t *testing.T) {
	previousNet := qparams.ActiveNetParams
	qparams.ActiveNetParams = &qparams.DevNetParam
	defer func() { qparams.ActiveNetParams = previousNet }()

	db := rawdb.NewMemoryDatabase()
	tdb := triedb.NewDatabase(db, nil)
	sdb := state.NewDatabase(tdb, nil)
	statedb, _ := state.New(common.Hash{}, sdb)
	engine := New(db)
	minerA := common.HexToAddress("0x1000000000000000000000000000000000000001")
	minerB := common.HexToAddress("0x2000000000000000000000000000000000000002")
	genesisTime := uint64(qparams.ActiveNetParams.GenesisBlock.Block().Header.Timestamp.Unix())
	duration := uint64(qparams.ActiveNetParams.StakingEpochDurationSeconds)

	// Activation happens during epoch 0. Its rewards are recorded for audit, but
	// must never become voting power because the ledger did not observe it whole.
	engine.updateRewardVotingLedger(&types.Header{Number: big.NewInt(1), Time: genesisTime + 1, Coinbase: minerA}, statedb, big.NewInt(730))
	engine.updateRewardVotingLedger(&types.Header{Number: big.NewInt(2), Time: genesisTime + 2, Coinbase: minerB}, statedb, big.NewInt(365))
	if got := rewardVotingWeight(statedb, minerA, 0); got.Cmp(big.NewInt(730)) != 0 {
		t.Fatalf("bootstrap miner weight = %s, want 730", got)
	}
	if got := rewardVotingTotal(statedb, 0); got.Cmp(big.NewInt(1095)) != 0 {
		t.Fatalf("bootstrap total = %s, want 1095", got)
	}
	if rewardVotingEpochFinalized(statedb, 0) {
		t.Fatal("partially observed bootstrap epoch became vote eligible")
	}

	// The boundary block belongs to epoch 1. The old tally implementation is
	// intentionally separate and keeps its historical boundary convention.
	engine.updateRewardVotingLedger(&types.Header{Number: big.NewInt(3), Time: genesisTime + duration, Coinbase: minerA}, statedb, big.NewInt(730))
	if rewardVotingEpochFinalized(statedb, 0) {
		t.Fatal("bootstrap epoch finalized at first rollover")
	}
	if got := rewardVotingWeight(statedb, minerA, 1); got.Cmp(big.NewInt(730)) != 0 {
		t.Fatalf("epoch 1 miner weight = %s, want 730", got)
	}

	engine.updateRewardVotingLedger(&types.Header{Number: big.NewInt(4), Time: genesisTime + 2*duration, Coinbase: minerB}, statedb, big.NewInt(365))
	if !rewardVotingEpochFinalized(statedb, 1) {
		t.Fatal("first completely observed reward epoch was not finalized")
	}
	if got := rewardVotingTotal(statedb, 1); got.Cmp(big.NewInt(730)) != 0 {
		t.Fatalf("epoch 1 total = %s, want 730", got)
	}
	if got := rewardVotingWeight(statedb, minerB, 2); got.Cmp(big.NewInt(365)) != 0 {
		t.Fatalf("boundary reward was not attributed to epoch 2: %s", got)
	}
}

func TestStakingRewardVotingLedgerConservesPoolYieldAndIncludesOperatorShares(t *testing.T) {
	previousNet := qparams.ActiveNetParams
	qparams.ActiveNetParams = &qparams.DevNetParam
	defer func() { qparams.ActiveNetParams = previousNet }()

	db := rawdb.NewMemoryDatabase()
	tdb := triedb.NewDatabase(db, nil)
	sdb := state.NewDatabase(tdb, nil)
	statedb, _ := state.New(common.Hash{}, sdb)
	engine := New(db)
	pool := common.HexToAddress("0x3000000000000000000000000000000000000003")
	genesisTime := uint64(qparams.ActiveNetParams.GenesisBlock.Block().Header.Timestamp.Unix())
	header := &types.Header{Number: big.NewInt(1), Time: genesisTime + 1, Coinbase: pool}

	// 100 reward-bearing shares, 25 of which belong to the operator through
	// liquid and locked auto-stake positions.
	statedb.SetState(params.StakingContractAddr, calculateMappingSlot(pool, params.SlotPoolTotalSharesBase), common.BigToHash(big.NewInt(100)))
	statedb.SetState(params.StakingContractAddr, calculateDoubleMappingSlot(pool, pool, params.SlotStakerPoolSharesBase), common.BigToHash(big.NewInt(15)))
	statedb.SetState(params.StakingContractAddr, calculateDoubleMappingSlot(pool, pool, params.SlotStakerTotalLockedSharesBase), common.BigToHash(big.NewInt(10)))

	engine.updateStakingRewardVotingLedger(header, statedb, big.NewInt(270))
	const scale = int64(1_000_000)
	wantDelta := big.NewInt(270 * scale / 100)
	if got := stakingPoolEpochRewardPerShare(statedb, pool, 0); got.Cmp(wantDelta) != 0 {
		t.Fatalf("reward per share = %s, want %s", got, wantDelta)
	}
	wantTotal := new(big.Int).Mul(big.NewInt(100), wantDelta)
	if got := stakingEpochVotingTotal(statedb, 0); got.Cmp(wantTotal) != 0 {
		t.Fatalf("staking voting total = %s, want %s", got, wantTotal)
	}
	wantOperator := new(big.Int).Mul(big.NewInt(25), wantDelta)
	if got := operatorEpochVotingWeight(statedb, pool, 0); got.Cmp(wantOperator) != 0 {
		t.Fatalf("operator voting weight = %s, want %s", got, wantOperator)
	}
}

func TestEpochTransition(t *testing.T) {
	previousNet := qparams.ActiveNetParams
	qparams.ActiveNetParams = &qparams.MainNetParam
	defer func() {
		qparams.ActiveNetParams = previousNet
	}()

	db := rawdb.NewMemoryDatabase()
	tdb := triedb.NewDatabase(db, nil)
	sdb := state.NewDatabase(tdb, nil)
	statedb, _ := state.New(common.Hash{}, sdb)

	engine := New(db)
	miner := common.HexToAddress("0x1234567890123456789012345678901234567890")

	// Initial state
	statedb.SetState(params.ChainTallyAddr, params.SlotCurrentEpoch, common.Hash{0})
	initialBase := common.Hash{100}
	statedb.SetState(params.ChainTallyAddr, params.SlotCurrentTallyBase, initialBase)

	// Block at the canonical mainnet timestamp epoch boundary.
	genesisTime := uint64(qparams.ActiveNetParams.GenesisBlock.Block().Header.Timestamp.Unix())
	interval := uint64(qparams.ActiveNetParams.SubsidyReductionInterval)
	header := &types.Header{
		Number:   big.NewInt(1),
		Time:     genesisTime + interval,
		Coinbase: miner,
	}

	reward := new(big.Int).SetInt64(1e18)
	engine.updateStakingTallies(nil, header, statedb, reward)

	// Verify epoch incremented
	epoch := statedb.GetState(params.ChainTallyAddr, params.SlotCurrentEpoch).Big()
	assert.Equal(t, int64(1), epoch.Int64())

	// Verify LastTallyBase is now initialBase (Rotation success)
	lastBase := statedb.GetState(params.ChainTallyAddr, params.SlotLastTallyBase)
	assert.Equal(t, initialBase, lastBase)

	// Verify CurrentTallyBase has changed (New base for new epoch)
	currentBase := statedb.GetState(params.ChainTallyAddr, params.SlotCurrentTallyBase)
	assert.NotEqual(t, initialBase, currentBase)
}

func TestMainnetStakingTalliesRotateAtTimestampBoundaryBeforeBlockBoundary(t *testing.T) {
	previousNet := qparams.ActiveNetParams
	qparams.ActiveNetParams = &qparams.MainNetParam
	defer func() {
		qparams.ActiveNetParams = previousNet
	}()

	db := rawdb.NewMemoryDatabase()
	tdb := triedb.NewDatabase(db, nil)
	sdb := state.NewDatabase(tdb, nil)
	statedb, _ := state.New(common.Hash{}, sdb)

	engine := New(db)
	miner := common.HexToAddress("0x8d629b9ff1af3bcb3f08da65d95abb929c62c485")

	currentEpoch := big.NewInt(15)
	initialBase := common.HexToHash("0x3d725c5ee53025f027da36bea8d3af3b6a3e9d2d1542d47c162631de48e66c1c")
	lastBase := common.HexToHash("0x7d74985e988688526ac76b8ff8f86df2934c34abd4c430c49bf3b8a821b4e87e")
	statedb.SetState(params.ChainTallyAddr, params.SlotCurrentEpoch, common.BigToHash(currentEpoch))
	statedb.SetState(params.ChainTallyAddr, params.SlotCurrentTallyBase, initialBase)
	statedb.SetState(params.ChainTallyAddr, params.SlotLastTallyBase, lastBase)

	genesisTime := uint64(qparams.ActiveNetParams.GenesisBlock.Block().Header.Timestamp.Unix())
	interval := uint64(qparams.ActiveNetParams.SubsidyReductionInterval)
	header := &types.Header{
		Number:   big.NewInt(9537626),
		Time:     genesisTime + 16*interval,
		Coinbase: miner,
	}

	reward := new(big.Int).SetInt64(1e18)
	engine.updateStakingTallies(nil, header, statedb, reward)

	epoch := statedb.GetState(params.ChainTallyAddr, params.SlotCurrentEpoch).Big()
	assert.Equal(t, int64(16), epoch.Int64())
	assert.NotEqual(t, initialBase, statedb.GetState(params.ChainTallyAddr, params.SlotCurrentTallyBase))
	assert.Equal(t, initialBase, statedb.GetState(params.ChainTallyAddr, params.SlotLastTallyBase))
}

func TestMainnetChainTallyEpochUsesTimestampDuration(t *testing.T) {
	previousNet := qparams.ActiveNetParams
	qparams.ActiveNetParams = &qparams.MainNetParam
	defer func() {
		qparams.ActiveNetParams = previousNet
	}()

	genesisTime := uint64(qparams.ActiveNetParams.GenesisBlock.Block().Header.Timestamp.Unix())
	interval := uint64(qparams.ActiveNetParams.SubsidyReductionInterval)
	header := &types.Header{
		Number: big.NewInt(9537626),
		Time:   genesisTime + 16*interval,
	}

	assert.Equal(t, uint64(16), stakingEpochForHeader(header))
	assert.Equal(t, uint64(16), chainTallyEpochForHeader(header))
}

func TestMainnetStakingEpochUsesHardcodedTimestampDuration(t *testing.T) {
	previousNet := qparams.ActiveNetParams
	previousDuration := qparams.MainNetParams.StakingEpochDurationSeconds
	qparams.ActiveNetParams = &qparams.MainNetParam
	qparams.MainNetParams.StakingEpochDurationSeconds = 0
	defer func() {
		qparams.MainNetParams.StakingEpochDurationSeconds = previousDuration
		qparams.ActiveNetParams = previousNet
	}()

	genesisTime := uint64(qparams.ActiveNetParams.GenesisBlock.Block().Header.Timestamp.Unix())
	interval := uint64(qparams.ActiveNetParams.SubsidyReductionInterval)
	header := &types.Header{
		Number: big.NewInt(9537626),
		Time:   genesisTime + 16*interval,
	}

	assert.Equal(t, uint64(16), stakingEpochForHeader(header))
}

func TestStakingEpochForHeaderMainnetCanonicalBoundaries(t *testing.T) {
	previousNet := qparams.ActiveNetParams
	qparams.ActiveNetParams = &qparams.MainNetParam
	defer func() {
		qparams.ActiveNetParams = previousNet
	}()

	genesisTime := uint64(qparams.ActiveNetParams.GenesisBlock.Block().Header.Timestamp.Unix())
	interval := uint64(qparams.ActiveNetParams.SubsidyReductionInterval)

	assert.Equal(t, uint64(16), stakingEpochForHeader(&types.Header{
		Number: big.NewInt(9537626),
		Time:   genesisTime + 16*interval,
	}))
	assert.Equal(t, uint64(15), stakingEpochForHeader(&types.Header{
		Number: big.NewInt(9676799),
		Time:   genesisTime + 16*interval - 1,
	}))
	assert.Equal(t, uint64(16), stakingEpochForHeader(&types.Header{
		Number: big.NewInt(9676800),
		Time:   genesisTime + 16*interval,
	}))
}

func TestStakingEpochForHeaderDevnetUsesExplicitDuration(t *testing.T) {
	previousNet := qparams.ActiveNetParams
	qparams.ActiveNetParams = &qparams.DevNetParam
	defer func() {
		qparams.ActiveNetParams = previousNet
	}()

	genesisTime := uint64(qparams.ActiveNetParams.GenesisBlock.Block().Header.Timestamp.Unix())

	assert.Equal(t, uint64(1), stakingEpochForHeader(&types.Header{
		Number: big.NewInt(1),
		Time:   genesisTime + uint64(qparams.ActiveNetParams.StakingEpochDurationSeconds),
	}))
}

func TestFinalizeIgnoresLocalEpochMinerSplitSidecar(t *testing.T) {
	previousNet := qparams.ActiveNetParams
	qparams.ActiveNetParams = &qparams.MainNetParam
	defer func() {
		qparams.ActiveNetParams = previousNet
	}()

	db := rawdb.NewMemoryDatabase()
	tdb := triedb.NewDatabase(db, nil)
	sdb := state.NewDatabase(tdb, nil)
	statedb, _ := state.New(common.Hash{}, sdb)

	const (
		reward            = uint64(1000)
		onchainMinerSplit = int64(70)
		staleMinerSplit   = byte(100)
	)
	miner := common.HexToAddress("0x1234567890123456789012345678901234567890")
	genesisTime := uint64(qparams.ActiveNetParams.GenesisBlock.Block().Header.Timestamp.Unix())
	header := &types.Header{
		Number:   big.NewInt(12_073_951),
		Time:     genesisTime + 19*mainnetStakingEpochDurationSeconds + 1,
		Coinbase: miner,
		Extra:    EncodeRewardExtra(reward),
	}

	epoch := stakingEpochForHeader(header)
	if err := db.Put(testEpochMinerSplitKey(epoch), []byte{staleMinerSplit}); err != nil {
		t.Fatalf("put stale miner split: %v", err)
	}
	statedb.SetState(params.StakingContractAddr, params.SlotMinerSplit, common.BigToHash(big.NewInt(onchainMinerSplit)))

	engine := New(db)
	engine.Finalize(finalizeTestChainReader{config: params.BdagMainnetChainConfig}, header, statedb, &types.Body{})

	totalSubsidy, err := RewardExtraToWei(header.Extra)
	if err != nil {
		t.Fatalf("RewardExtraToWei() error = %v", err)
	}
	wantMinerReward := new(big.Int).Mul(totalSubsidy, big.NewInt(onchainMinerSplit))
	wantMinerReward.Div(wantMinerReward, big.NewInt(100))

	gotMinerReward := statedb.GetBalance(miner).ToBig()
	if gotMinerReward.Cmp(wantMinerReward) != 0 {
		t.Fatalf("miner reward = %s, want on-chain split reward %s", gotMinerReward, wantMinerReward)
	}
}

func testEpochMinerSplitKey(epoch uint64) []byte {
	enc := make([]byte, 8)
	binary.BigEndian.PutUint64(enc, epoch)
	return append([]byte("EMS"), enc...)
}
