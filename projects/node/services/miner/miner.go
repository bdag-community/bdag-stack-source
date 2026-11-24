package miner

import (
	"bytes"
	"context"
	"crypto/rand"
	ejson "encoding/json"
	"errors"
	"fmt"
	"math/big"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/BlockdagNetworkLabs/bdag/core/protocol"
	"github.com/ethereum/go-ethereum/common"
	ethcommon "github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/txpool/legacypool"

	"github.com/BlockdagNetworkLabs/bdag/bevm/bdag"
	blockdag "github.com/BlockdagNetworkLabs/bdag/blockdag"
	"github.com/BlockdagNetworkLabs/bdag/common/roughtime"
	"github.com/BlockdagNetworkLabs/bdag/config"
	"github.com/BlockdagNetworkLabs/bdag/consensus/model"
	"github.com/BlockdagNetworkLabs/bdag/core/blockchain"
	"github.com/BlockdagNetworkLabs/bdag/core/event"
	"github.com/BlockdagNetworkLabs/bdag/core/json"
	"github.com/BlockdagNetworkLabs/bdag/core/types"
	"github.com/BlockdagNetworkLabs/bdag/core/types/pow"
	"github.com/BlockdagNetworkLabs/bdag/engine/txscript"
	"github.com/BlockdagNetworkLabs/bdag/node/service"
	"github.com/BlockdagNetworkLabs/bdag/params"
	"github.com/BlockdagNetworkLabs/bdag/rpc"
	"github.com/BlockdagNetworkLabs/bdag/services/mining"
)

const (
	// gbtRegenerateSeconds is the number of seconds that must pass before
	// a new template is generated when the previous block hash has not
	// changed and there have been changes to the available transactions
	// in the memory pool.
	gbtRegenerateSeconds = 60

	// This is the timeout for HTTP requests to notify external miners.
	NotifyURLTimeout = 1 * time.Second

	// recentTemplateRetention bounds how long a superseded template stays
	// eligible for submitBlockHeader matching. Templates are rebuilt on DAG,
	// mempool and stall events, which together fire far more often than a
	// miner solves a job, so this window has to be measured in time rather
	// than in rebuild count: a count-based window shrinks to nothing exactly
	// when template churn is highest, discarding solved blocks.
	recentTemplateRetention = 60 * time.Second

	// recentTemplateMaxEntries caps retained templates so a rebuild storm
	// cannot grow the cache without bound.
	recentTemplateMaxEntries = 256

	// mempoolTemplateRebuildMinInterval throttles template rebuilds triggered
	// by mempool churn alone. Parent changes, stalls and forced rebuilds are
	// never throttled; only the pending-count trigger, which otherwise fires
	// on every transaction arrival and removal.
	mempoolTemplateRebuildMinInterval = 500 * time.Millisecond

	// submitMainTipLagTolerance is how far a submitted block's main parent may
	// trail the main chain tip before submitBlock refuses it outright. This is
	// only a pre-filter on locally submitted work — ProcessBlock still decides
	// whether the DAG accepts the block, and peers validate independently.
	//
	// TargetTimePerBlock is one second, so any miner that takes a few seconds
	// to solve is necessarily several blocks behind by the time it submits.
	// Under live load the submit pipeline (template age + ASIC solve + submit
	// latency) puts the solved parent 11-18 blocks behind the tip; a tolerance
	// of StableConfirmations (10) rejected almost all of them as tip_overdue.
	// This is a DAG, so a block on a slightly-older parent is still a valid side
	// block, and this filter only gates local submits — ProcessBlock and peers
	// still decide. 32 clears the observed range with headroom.
	submitMainTipLagTolerance = 32
)

var (
	freezeDetectMu           sync.Mutex
	freezeDetectLastParent   string
	freezeDetectStartTime    time.Time
	freezeDetectLastWarnTime time.Time
)

// mining stats
type MiningStats struct {
	LastestGbt                        time.Time `json:"lastest_gbt"`
	LastestGbtRequest                 time.Time `json:"lastest_gbt_request"`
	LastestSubmit                     time.Time `json:"lastest_submit"`
	Lastest100GbtRequests             []int64   `json:"-"`
	Lastest100Gbts                    []int64   `json:"-"`
	Lastest100GbtAvgDuration          float64   `json:"lastest_100_gbt_avg_duration"`
	Lastest100GbtRequestAvgDuration   float64   `json:"lastest_100_gbt_request_avg_duration"`
	Last100Submits                    []int64   `json:"-"`
	Last100SubmitAvgDuration          float64   `json:"last_100_submit_avg_duration"`
	SubmitAvgDuration                 float64   `json:"submit_avg_duration"`
	TotalSubmitDuration               float64   `json:"total_submit_duration"`
	TotalGbtDuration                  float64   `json:"total_gbt_duration"`
	GbtAvgDuration                    float64   `json:"gbt_avg_duration"`
	TotalGbtRequestDuration           float64   `json:"total_gbt_request_duration"`
	GbtRequestAvgDuration             float64   `json:"gbt_request_avg_duration"`
	MaxGbtDuration                    float64   `json:"max_gbt_duration"`
	MaxGbtRequestDuration             float64   `json:"max_gbt_request_duration"`
	MaxGbtRequestTimeLongpollid       string    `json:"max_gbt_time_longpollid"`
	MaxSubmitDuration                 float64   `json:"max_submit_duration"`
	MaxSubmitDurationBlockHash        string    `json:"max_submit_duration_block_hash"`
	TotalGbts                         int64     `json:"total_gbts"`
	TotalGbtRequests                  int64     `json:"total_gbt_requests"`
	TotalEmptyGbts                    int64     `json:"total_empty_gbts"`
	TotalEmptyGbtDuarations           int64     `json:"total_empty_gbt_duarations"`
	TotalEmptyGbtResponse             int64     `json:"total_empty_gbt_response"`
	TotalSubmits                      int64     `json:"total_submits"`
	TotalTxEmptySubmits               int64     `json:"total_tx_empty_submits"`
	LastestMempoolEmptyTimestamp      int64     `json:"-"`
	TotalMempoolEmptyDuration         float64   `json:"total_mempool_empty_duration"`
	MempoolEmptyAvgDuration           float64   `json:"mempool_empty_avg_duration"`
	MempoolEmptyMaxDuration           float64   `json:"mempool_empty_max_duration"`
	MempoolEmptyWarns                 int64     `json:"mempool_empty_warns"`
	Lastest100MempoolEmptyDuration    []float64 `json:"lastest_100_mempool_empty_duration"`
	Lastest100MempoolEmptyAvgDuration float64   `json:"lastest_100_mempool_empty_avg_duration"`
	lastest100GbtRequestsSum          int64
	lastest100GbtRequestsSumReady     bool
	lastest100GbtsSum                 int64
	lastest100GbtsSumReady            bool
	last100SubmitsSum                 int64
	last100SubmitsSumReady            bool
	lastest100MempoolEmptySum         float64
	lastest100MempoolEmptySumReady    bool
}

func (ms *MiningStats) MarshalJSON() ([]byte, error) {
	tFormat := "2006-01-02 15:04:05"
	type MiningStatsOutput MiningStats
	tmpMiningStats := struct {
		MiningStatsOutput
		LastestGbt                        string `json:"lastest_gbt"`
		LastestGbtRequest                 string `json:"lastest_gbt_request"`
		LastestSubmit                     string `json:"lastest_submit"`
		Lastest1MempoolEmptyDuration      string `json:"lastest_1_mempool_empty_duration"`
		Lastest100GbtAvgDuration          string `json:"lastest_100_gbt_avg_duration"`
		Lastest100GbtRequestAvgDuration   string `json:"lastest_100_gbt_request_avg_duration"`
		Last100SubmitAvgDuration          string `json:"last_100_submit_avg_duration"`
		SubmitAvgDuration                 string `json:"submit_avg_duration"`
		GbtAvgDuration                    string `json:"gbt_avg_duration"`
		GbtRequestAvgDuration             string `json:"gbt_request_avg_duration"`
		MaxGbtDuration                    string `json:"max_gbt_duration"`
		MaxGbtRequestDuration             string `json:"max_gbt_request_duration"`
		MaxSubmitDuration                 string `json:"max_submit_duration"`
		MempoolEmptyAvgDuration           string `json:"mempool_empty_avg_duration"`
		Lastest100MempoolEmptyAvgDuration string `json:"lastest_100_mempool_empty_avg_duration"`
		MempoolEmptyMaxDuration           string `json:"mempool_empty_max_duration"`
	}{
		MiningStatsOutput:                 (MiningStatsOutput)(*ms),
		LastestGbt:                        ms.LastestGbt.Format(tFormat),
		LastestGbtRequest:                 ms.LastestGbtRequest.Format(tFormat),
		LastestSubmit:                     ms.LastestSubmit.Format(tFormat),
		Lastest100GbtAvgDuration:          fmt.Sprintf("%.3f s", ms.Lastest100GbtAvgDuration),
		Lastest100GbtRequestAvgDuration:   fmt.Sprintf("%.3f s", ms.Lastest100GbtRequestAvgDuration),
		Last100SubmitAvgDuration:          fmt.Sprintf("%.3f s", ms.Last100SubmitAvgDuration),
		SubmitAvgDuration:                 fmt.Sprintf("%.3f s", ms.SubmitAvgDuration),
		GbtAvgDuration:                    fmt.Sprintf("%.3f s", ms.GbtAvgDuration),
		GbtRequestAvgDuration:             fmt.Sprintf("%.3f s", ms.GbtRequestAvgDuration),
		MaxGbtDuration:                    fmt.Sprintf("%.3f s", ms.MaxGbtDuration),
		MaxGbtRequestDuration:             fmt.Sprintf("%.3f s", ms.MaxGbtRequestDuration),
		MaxSubmitDuration:                 fmt.Sprintf("%.3f s", ms.MaxSubmitDuration),
		MempoolEmptyAvgDuration:           fmt.Sprintf("%.3f s", ms.MempoolEmptyAvgDuration),
		Lastest100MempoolEmptyAvgDuration: fmt.Sprintf("%.3f s", ms.Lastest100MempoolEmptyAvgDuration),
		MempoolEmptyMaxDuration:           fmt.Sprintf("%.3f s", ms.MempoolEmptyMaxDuration),
	}
	if len(ms.Lastest100MempoolEmptyDuration) > 0 {
		tmpMiningStats.Lastest1MempoolEmptyDuration = fmt.Sprintf("%.3f s",
			ms.Lastest100MempoolEmptyDuration[len(ms.Lastest100MempoolEmptyDuration)-1 : len(ms.Lastest100MempoolEmptyDuration)][0])
	}
	return ejson.Marshal(tmpMiningStats)
}

// Miner creates blocks and searches for proof-of-work values.
type Miner struct {
	service.Service
	msgChan         chan any
	templateReqChan chan struct{}
	wg              sync.WaitGroup
	quit            chan struct{}

	cfg        *config.Config
	events     *event.Feed
	txpool     *legacypool.LegacyPool
	timeSource model.MedianTimeSource
	// minimumMedianTimeFn is nil in production; tests may provide the
	// consensus-owned calculation while still exercising the real template
	// installation and notification path.
	minimumMedianTimeFn func() time.Time
	// templateNotifyAdmissionHook is nil in production. Tests may use it to
	// hold a real subscription payload immediately before final admission so
	// generation invalidation and replacement installation can be ordered
	// without timing assumptions.
	templateNotifyAdmissionHook func(*json.RemoteGBTResult)
	consensus                   model.Consensus
	policy                      *mining.Policy
	sigCache                    *txscript.SigCache
	bdagPool                    pendingTemplateResetter
	worker                      IWorker
	buildTemplate               blockTemplateBuilder

	template        *types.BlockTemplate
	lastTxPending   int
	lastTemplate    time.Time
	minTimestamp    time.Time
	coinbaseAddress ethcommon.Address
	powType         pow.PowType

	gbtGenerationMu      sync.RWMutex
	gbtGenerationFailure error
	// gbtGenerationInvalidatedThrough is a monotonic sequence watermark. It
	// remains valid after a replacement clears the current failure so payloads
	// queued from the invalidated generation cannot be published later.
	gbtGenerationInvalidatedThrough uint64
	gbtGenerationInvalidated        bool

	// mempoolRebuildArmed records that a throttled mempool change already has
	// a deferred rebuild scheduled. Only touched from the handler goroutine,
	// which is the only caller of updateBlockTemplateForRequest.
	mempoolRebuildArmed bool

	// recentTemplates holds superseded templates (oldest first) for up to
	// recentTemplateRetention, so a header submitted against a template that
	// has since been replaced can still be matched instead of being rejected
	// outright. Only updateBlockTemplateForRequest (single-threaded via
	// m.handler()) writes it; submitBlockHeader reads it from RPC goroutines.
	// Guarded by the embedded sync.Mutex below, which is otherwise unused.
	recentTemplates []recentTemplate

	sync.Mutex
	// Protects submit counters only. Block validation/insertion is already
	// serialized by blockchain.ProcessBlock; holding this across submit would
	// make ASIC sibling candidates age into tip-overdue rejects.
	submitLocker sync.Mutex

	totalSubmit   int
	successSubmit int

	coinbaseFlags mining.CoinbaseFlags

	reqWG sync.WaitGroup

	RpcSer *rpc.RpcServer
	p2pSer model.P2PService
	stats  MiningStats

	// templateFeed broadcasts the latest RemoteGBTResult whenever the
	// internal block template is (re)created. Subscribers obtain a channel
	// via TemplateEvent() and receive every push; the stream is intended
	// for the Blockdag_subscribe("blockTemplate") WebSocket API.
	templateFeed event.Feed
	// templateSeq is a monotonically increasing sequence number stamped
	// onto each emitted RemoteGBTResult so subscribers can detect reorder
	// or dropped pushes.
	templateSeq atomic.Uint64

	templateHealthMu                            sync.RWMutex
	lastTemplateBuildAt                         time.Time
	lastTemplateBuildDuration                   time.Duration
	lastTemplateBuildReason                     string
	lastTemplateBuildErrorCode                  string
	lastTemplateBuildErrorMessage               string
	lastTemplateInvalidationAt                  time.Time
	lastTemplateInvalidationSeq                 uint64
	lastTemplateInvalidationCause               templateInvalidationCause
	lastTemplateInvalidationReason              string
	lastTemplateInvalidationSubmitObsoleteDepth int64
	lastNonceDriftRecoveryAt                    time.Time
	lastNonceDriftRecoveryLogAt                 time.Time

	templateReqMu      sync.Mutex
	pendingTemplateReq *templateBuildRequest
}

type pendingTemplateResetter interface {
	ResetTemplate() error
}

type miningPendingTemplateResetter interface {
	ResetTemplateForMining() error
}

type syncingPendingTemplateResetter interface {
	IsSyncing() bool
}

type canonicalMiningPendingTemplateResetter interface {
	CanonicalMiningTemplateReady() bool
	ResetTemplateForCanonicalMining() error
}

type blockTemplateBuilder func(payToAddress ethcommon.Address, skipPendingTransactions bool) (*types.BlockTemplate, error)

// TemplateEvent returns the feed of RemoteGBTResult pushes emitted whenever
// the internal block template is recreated. It is consumed by the
// Blockdag_subscribe("blockTemplate") subscription handler to stream updates
// to external stratum pools.
func (m *Miner) TemplateEvent() *event.Feed {
	return &m.templateFeed
}

func validateBlockTemplateCoinbase(template *types.BlockTemplate, expected *ethcommon.Address, context string) error {
	if template == nil || template.Block == nil {
		return fmt.Errorf("%s has no block template", context)
	}
	coinbase := template.Block.Header.CoinbaseAddress
	if coinbase == (ethcommon.Address{}) {
		return fmt.Errorf("%s has zero coinbase address; refusing to serve mining template", context)
	}
	if expected != nil && *expected != (ethcommon.Address{}) && coinbase != *expected {
		return fmt.Errorf("%s coinbase mismatch: template=%s expected=%s", context, coinbase.String(), expected.String())
	}
	return nil
}

func (m *Miner) validateCurrentTemplateCoinbase(context string) error {
	var expected *ethcommon.Address
	if m.coinbaseAddress != (ethcommon.Address{}) {
		expected = &m.coinbaseAddress
	}
	return validateBlockTemplateCoinbase(m.template, expected, context)
}

func (m *Miner) StatsEmptyGbt() {
	if m.stats.LastestMempoolEmptyTimestamp <= 0 {
		m.stats.LastestMempoolEmptyTimestamp = time.Now().Unix()
	}
}

func appendRollingInt64(values []int64, sum int64, initialized bool, value int64, limit int) ([]int64, int64, bool) {
	if limit <= 0 {
		return values[:0], 0, true
	}
	if !initialized {
		sum = 0
		for _, v := range values {
			sum += v
		}
	}
	for len(values) >= limit {
		sum -= values[0]
		values = values[1:]
	}
	values = append(values, value)
	sum += value
	return values, sum, true
}

func appendRollingFloat64(values []float64, sum float64, initialized bool, value float64, limit int) ([]float64, float64, bool) {
	if limit <= 0 {
		return values[:0], 0, true
	}
	if !initialized {
		sum = 0
		for _, v := range values {
			sum += v
		}
	}
	for len(values) >= limit {
		sum -= values[0]
		values = values[1:]
	}
	values = append(values, value)
	sum += value
	return values, sum, true
}

func (m *Miner) StatsGbtTxEmptyAvgTimes() {
	if m.stats.LastestMempoolEmptyTimestamp <= 0 || time.Now().Unix() <= m.stats.LastestMempoolEmptyTimestamp {
		return
	}
	m.stats.TotalEmptyGbtDuarations++
	duration := float64(time.Now().Unix() - m.stats.LastestMempoolEmptyTimestamp)
	m.stats.TotalMempoolEmptyDuration += duration
	mempoolEmptyDuration.Update(time.Duration(duration) * time.Second)
	if m.stats.TotalEmptyGbtDuarations > 0 {
		m.stats.MempoolEmptyAvgDuration = m.stats.TotalMempoolEmptyDuration / float64(m.stats.TotalEmptyGbtDuarations)
	}

	m.stats.Lastest100MempoolEmptyDuration, m.stats.lastest100MempoolEmptySum, m.stats.lastest100MempoolEmptySumReady = appendRollingFloat64(
		m.stats.Lastest100MempoolEmptyDuration,
		m.stats.lastest100MempoolEmptySum,
		m.stats.lastest100MempoolEmptySumReady,
		duration,
		100,
	)
	m.stats.Lastest100MempoolEmptyAvgDuration = m.stats.lastest100MempoolEmptySum / float64(len(m.stats.Lastest100MempoolEmptyDuration))

	if duration > m.stats.MempoolEmptyMaxDuration {
		m.stats.MempoolEmptyMaxDuration = duration
	}
}

func (m *Miner) StatsSubmit(start time.Time, bh string, txcount int) {
	currentReqMillSec := time.Since(start).Milliseconds()
	m.stats.TotalSubmits++
	totalSubmits.Update(m.stats.TotalSubmits)
	m.stats.Last100Submits, m.stats.last100SubmitsSum, m.stats.last100SubmitsSumReady = appendRollingInt64(
		m.stats.Last100Submits,
		m.stats.last100SubmitsSum,
		m.stats.last100SubmitsSumReady,
		currentReqMillSec,
		100,
	)
	m.stats.Last100SubmitAvgDuration = float64(m.stats.last100SubmitsSum) / float64(len(m.stats.Last100Submits)) / 1000
	m.stats.TotalSubmitDuration += float64(currentReqMillSec) / 1000
	if m.stats.TotalSubmits > 0 {
		m.stats.SubmitAvgDuration = m.stats.TotalSubmitDuration / float64(m.stats.TotalSubmits)
	}
	submitDuration.Update(time.Since(start))
	if float64(currentReqMillSec)/1000 > m.stats.MaxSubmitDuration {
		m.stats.MaxSubmitDuration = float64(currentReqMillSec) / 1000
		m.stats.MaxSubmitDurationBlockHash = bh
	}
	submitTxCount.Update(int64(txcount))
	if txcount < 1 {
		m.stats.TotalTxEmptySubmits++
		totalTxEmptySubmits.Update(m.stats.TotalTxEmptySubmits)
	}
}

func (m *Miner) StatsGbtRequest(currentReqMillSec int64, txcount int, longpollid string) {
	m.stats.Lastest100GbtRequests, m.stats.lastest100GbtRequestsSum, m.stats.lastest100GbtRequestsSumReady = appendRollingInt64(
		m.stats.Lastest100GbtRequests,
		m.stats.lastest100GbtRequestsSum,
		m.stats.lastest100GbtRequestsSumReady,
		currentReqMillSec,
		100,
	)
	m.stats.LastestGbtRequest = time.Now()
	m.stats.Lastest100GbtRequestAvgDuration = float64(m.stats.lastest100GbtRequestsSum) / float64(len(m.stats.Lastest100GbtRequests)) / 1000
	m.stats.TotalGbtRequestDuration += float64(currentReqMillSec) / 1000
	if m.stats.TotalGbtRequests > 0 {
		m.stats.GbtRequestAvgDuration = m.stats.TotalGbtRequestDuration / float64(m.stats.TotalGbtRequests)
	}
	gbtRequestDuration.Update(time.Duration(currentReqMillSec) * time.Millisecond)
	if float64(currentReqMillSec)/1000 > m.stats.MaxGbtRequestDuration {
		m.stats.MaxGbtRequestDuration = float64(currentReqMillSec) / 1000
		m.stats.MaxGbtRequestTimeLongpollid = longpollid
	}
	if txcount < 1 {
		m.stats.TotalEmptyGbtResponse++
	}
}

func (m *Miner) StatsGbt(currentReqMillSec int64, txcount int) {
	m.stats.Lastest100Gbts, m.stats.lastest100GbtsSum, m.stats.lastest100GbtsSumReady = appendRollingInt64(
		m.stats.Lastest100Gbts,
		m.stats.lastest100GbtsSum,
		m.stats.lastest100GbtsSumReady,
		currentReqMillSec,
		100,
	)
	m.stats.LastestGbt = time.Now()
	m.stats.Lastest100GbtAvgDuration = float64(m.stats.lastest100GbtsSum) / float64(len(m.stats.Lastest100Gbts)) / 1000

	m.stats.TotalGbtDuration += float64(currentReqMillSec) / 1000
	if m.stats.TotalGbts > 0 {
		m.stats.GbtAvgDuration = m.stats.TotalGbtDuration / float64(m.stats.TotalGbts)
	}
	gbtDuration.Update(time.Duration(currentReqMillSec) * time.Millisecond)
	if float64(currentReqMillSec)/1000 > m.stats.MaxGbtDuration {
		m.stats.MaxGbtDuration = float64(currentReqMillSec) / 1000
	}
	if txcount < 1 {
		m.StatsEmptyGbt()
		m.stats.TotalEmptyGbts++
	} else {
		m.StatsGbtTxEmptyAvgTimes()
		m.stats.LastestMempoolEmptyTimestamp = 0
	}
}

func (m *Miner) Start() error {
	if !m.cfg.Miner {
		return nil
	}
	if err := m.Service.Start(); err != nil {
		return err
	}

	//
	log.Info("Start Miner...")

	m.subscribe()

	m.wg.Add(1)
	go m.handler()
	return nil
}

func (m *Miner) Stop() error {
	if !m.cfg.Miner {
		return nil
	}
	log.Info("try stop miner")
	if err := m.Service.Stop(); err != nil {
		return err
	}
	log.Info("Stop Miner...")

	close(m.quit)
	m.wg.Wait()
	m.reqWG.Wait()

	return nil
}

func (m *Miner) handler() {
	stallTicker := time.NewTicker(params.ActiveNetParams.TargetTimePerBlock)
	defer stallTicker.Stop()

out:
	for {
		select {
		case mc := <-m.msgChan:
			switch msg := mc.(type) {
			case *BlockChainChangeMsg:
				previousTemplate := m.template
				if err := m.applyTemplateBuildRequest(templateBuildRequest{
					kind:              templateBuildBlockChain,
					reason:            templateInvalidationParentChanged.String(),
					invalidationCause: templateInvalidationParentChanged,
				}); err == nil {
					m.updateGBTWorkerAfterTemplateBuild(previousTemplate)
				} else {
					m.invalidateGBTWorkerGeneration(err)
				}
			case *MempoolChangeMsg:
				previousTemplate := m.template
				if err := m.applyTemplateBuildRequest(templateBuildRequest{
					kind:              templateBuildMempool,
					reason:            templateInvalidationMempoolRootChanged.String(),
					invalidationCause: templateInvalidationMempoolRootChanged,
				}); err == nil {
					m.updateGBTWorkerAfterTemplateBuild(previousTemplate)
				} else {
					// Mempool throttling/rebuild failures retain the current
					// generation semantics; only required chain/obsolete builds
					// invalidate GBT admission.
					m.invalidateGBTWorkerCache()
				}

			case *GBTMiningMsg:
				previousTemplate := m.template
				applied, buildErr := m.applyPendingTemplateBuildBeforeGBT()
				if buildErr != nil {
					m.invalidateGBTWorkerGeneration(buildErr)
					if msg.reply != nil {
						msg.reply <- &gbtResponse{nil, buildErr}
					}
					continue
				}
				if applied {
					m.updateGBTWorkerAfterTemplateBuild(previousTemplate)
				}
				if err := m.gbtGenerationError(); err != nil {
					if msg.reply != nil {
						msg.reply <- &gbtResponse{nil, err}
					}
					continue
				}
				if m.worker != nil {
					if m.worker.GetType() == GBTWorkerType {
						m.worker.(*GBTWorker).GetRequest(msg.request, msg.reply)
						continue
					}
					m.worker.Stop()
					m.worker = nil
				}
				worker := NewGBTWorker(m)
				m.worker = worker
				err := m.worker.Start()
				if err != nil {
					log.Error(err.Error())
					m.worker = nil
					if msg.reply != nil {
						msg.reply <- &gbtResponse{nil, err}
					}
					continue
				}
				worker.Update()
				worker.GetRequest(msg.request, msg.reply)

			default:
				log.Warn("Invalid message type in task handler: %T", msg)
			}

		case <-m.templateReqChan:
			req, ok := m.takeTemplateBuildRequest()
			if !ok {
				continue
			}
			previousTemplate := m.template
			if err := m.applyTemplateBuildRequest(req); err == nil {
				m.updateGBTWorkerAfterTemplateBuild(previousTemplate)
			} else {
				if req.kind == templateBuildMempool {
					m.invalidateGBTWorkerCache()
				} else {
					m.invalidateGBTWorkerGeneration(err)
				}
			}

		case <-stallTicker.C:
			m.handleStallSample()

		case <-m.quit:
			break out
		}
	}

cleanup:
	for {
		select {
		case <-m.msgChan:
		default:
			break cleanup
		}
	}

	if m.worker != nil {
		m.worker.Stop()
	}

	m.wg.Done()
	log.Trace("Miner handler done")
}

func (m *Miner) invalidateGBTWorkerCache() {
	if worker, ok := m.worker.(*GBTWorker); ok {
		worker.invalidateSerializedTemplateCache()
	}
}
func (m *Miner) recordGBTGenerationFailure(err error) {
	if err == nil {
		return
	}
	m.gbtGenerationMu.Lock()
	m.gbtGenerationFailure = err
	seq := m.templateSeq.Load()
	if !m.gbtGenerationInvalidated || seq > m.gbtGenerationInvalidatedThrough {
		m.gbtGenerationInvalidatedThrough = seq
	}
	m.gbtGenerationInvalidated = true
	m.gbtGenerationMu.Unlock()
}

func (m *Miner) gbtGenerationError() error {
	m.gbtGenerationMu.RLock()
	err := m.gbtGenerationFailure
	m.gbtGenerationMu.RUnlock()
	return err
}

func (m *Miner) clearGBTGenerationFailure(previous, current *types.BlockTemplate) {
	if previous == current || current == nil {
		return
	}
	m.gbtGenerationMu.Lock()
	m.gbtGenerationFailure = nil
	m.gbtGenerationMu.Unlock()
}

func (m *Miner) templatePayloadAdmitted(seq uint64) bool {
	m.gbtGenerationMu.RLock()
	invalidated := m.gbtGenerationInvalidated && seq <= m.gbtGenerationInvalidatedThrough
	m.gbtGenerationMu.RUnlock()
	return !invalidated
}

func (m *Miner) invalidateGBTWorkerGeneration(err error) {
	m.recordGBTGenerationFailure(err)
	if worker, ok := m.worker.(*GBTWorker); ok {
		worker.invalidateGeneration(err)
	}
}

func (m *Miner) updateGBTWorkerAfterTemplateBuild(previous *types.BlockTemplate) {
	if worker, ok := m.worker.(*GBTWorker); ok {
		worker.updateAfterTemplateBuild(previous, m.template)
		m.clearGBTGenerationFailure(previous, m.template)
		return
	}
	if m.worker != nil {
		m.worker.Update()
	}
	if previous != m.template && m.template != nil {
		m.clearGBTGenerationFailure(previous, m.template)
	}
}

func (m *Miner) updateBlockTemplate(force bool, ephemeralCoinbase *ethcommon.Address) error {
	return m.updateBlockTemplateForRequest(templateBuildRequest{force: force, ephemeralCoinbase: ephemeralCoinbase})
}

func (m *Miner) updateBlockTemplateForRequest(req templateBuildRequest) error {
	if err := m.CanBuildTemplate("updateBlockTemplate"); err != nil {
		return err
	}

	req = normalizeTemplateBuildRequest(req)
	force := req.force
	reCreate := false
	reCreateReason := ""
	reCreateCause := req.invalidationCause
	//
	if force {
		reCreate = true
		if !reCreateCause.valid() {
			reCreateCause = templateInvalidationStateChanged
		}
		reCreateReason = reCreateCause.String()
	} else if m.template == nil {
		reCreate = true
		reCreateCause = templateInvalidationStateChanged
		reCreateReason = reCreateCause.String()
	}
	if !reCreate {
		currentAddr := m.coinbaseAddress
		if req.ephemeralCoinbase != nil {
			currentAddr = *req.ephemeralCoinbase
		}
		hasCoinbaseAddr := currentAddr != ethcommon.HexToAddress("0x00")
		if hasCoinbaseAddr != m.template.ValidPayAddress {
			reCreate = true
			reCreateCause = templateInvalidationStateChanged
			reCreateReason = reCreateCause.String()
		}
		if m.template != nil && m.template.Block.Header.CoinbaseAddress != currentAddr {
			reCreate = true
			reCreateCause = templateInvalidationStateChanged
			reCreateReason = reCreateCause.String()
		}
	}
	if !reCreate {
		parentsSet := blockdag.NewHashSet()
		parentsSet.AddList(m.consensus.BlockChain().GetMiningTips(blockdag.MaxPriority))

		tparentSet := blockdag.NewHashSet()
		tparentSet.AddList(m.template.Block.Parents)
		if !parentsSet.IsEqual(tparentSet) {
			reCreate = true
			reCreateCause = templateInvalidationParentChanged
			reCreateReason = reCreateCause.String()
		} else {
			pending, _ := m.txpool.Stats()
			// The pending count moves on every transaction arrival and removal.
			// Coalesce those into at most one rebuild per interval, so mempool
			// churn cannot age superseded templates out from under the miners
			// still solving against them.
			mempoolDirty := pending != m.lastTxPending
			nextMempoolBuild := m.lastTemplate.Add(mempoolTemplateRebuildMinInterval)
			switch {
			case mempoolDirty && roughtime.Now().After(nextMempoolBuild):
				reCreate = true
				reCreateCause = templateInvalidationMempoolRootChanged
				reCreateReason = reCreateCause.String()
				m.mempoolRebuildArmed = false
			case mempoolDirty:
				// lastTxPending is left untouched, so the change is still
				// pending. Nothing else is guaranteed to call back in soon
				// enough on an otherwise idle chain, so schedule it.
				m.armMempoolRebuild(nextMempoolBuild)
			default:
				m.mempoolRebuildArmed = false
				if roughtime.Now().After(m.lastTemplate.Add(params.ActiveNetParams.TargetTimePerBlock * 2)) {
					reCreate = true
					reCreateCause = templateInvalidationSequenceObsolete
					reCreateReason = reCreateCause.String()
				}
			}
		}
	}
	if reCreate {
		if !reCreateCause.valid() {
			reCreateCause = templateInvalidationStateChanged
		}
		req.invalidationCause = reCreateCause
		req.reason = reCreateReason
		if !req.invalidationRecorded {
			req.invalidationRecorded = true
			m.recordTemplateInvalidation(req, time.Now())
		}
		m.stats.TotalGbts++ //gbt generates
		buildStart := time.Now()
		start := buildStart.UnixMilli()
		totalGbts.Update(m.stats.TotalGbts)
		if m.bdagPool == nil {
			e := fmt.Errorf("Failed to reset BdagPool template: BdagPool unavailable")
			log.Warn(e.Error())
			m.recordTemplateBuildError(reCreateReason, buildStart, e)
			return e
		}
		resetStart := time.Now()
		canonicalBypass := m.canonicalMiningTemplateBypassAllowed(resetStart)
		resetErr := resetTemplateForMining(m.bdagPool, canonicalBypass)
		bdagPoolResetDuration.UpdateSince(resetStart)
		if resetErr != nil {
			e := fmt.Errorf("Failed to reset BdagPool template: %w", resetErr)
			log.Warn(e.Error())
			m.recordTemplateBuildError(reCreateReason, buildStart, e)
			return e
		}

		addr := m.coinbaseAddress
		if req.ephemeralCoinbase != nil {
			addr = *req.ephemeralCoinbase
		}
		if addr == (ethcommon.Address{}) {
			e := fmt.Errorf("Refusing to create mining template with zero coinbase address; configure --miningaddr")
			log.Warn(e.Error())
			m.recordTemplateBuildError(reCreateReason, buildStart, e)
			return e
		}

		template, err, recoveredNonceDrift := m.buildTemplateWithNonceDriftFallback(addr)
		buildRecordReason := reCreateReason
		if recoveredNonceDrift {
			buildRecordReason = appendTemplateBuildReason(reCreateReason, templateHealthEVMPendingNonceDrift)
			m.logNonceDriftRecovery(reCreateReason)
		}
		if err != nil {
			e := fmt.Errorf("Failed to create new block template: %s", err.Error())
			log.Warn(e.Error())
			m.recordTemplateBuildError(reCreateReason, buildStart, e)
			return e
		}
		if err := validateBlockTemplateCoinbase(template, &addr, "mining template build"); err != nil {
			e := fmt.Errorf("Failed to create new block template: %s", err.Error())
			log.Warn(e.Error())
			m.recordTemplateBuildError(reCreateReason, buildStart, e)
			return e
		}
		m.pushRecentTemplate(m.template)
		previousTemplate := m.template
		m.template = template
		// The replacement has passed all template validation. Clear the
		// generation publication hold before the first notification so the
		// replacement can reach both RPC and subscription consumers.
		m.clearGBTGenerationFailure(previousTemplate, template)
		if worker, ok := m.worker.(*GBTWorker); ok {
			worker.updateAfterTemplateBuild(previousTemplate, template)
		}
		m.lastTxPending, _ = m.txpool.Stats()
		m.lastTemplate = time.Now()
		m.recordTemplateBuildSuccess(buildRecordReason, buildStart)
		if canonicalBypass {
			templateCanonicalSyncLatchBypassCounter.Inc(1)
		}
		// m.txpool.CleanDirty()

		// Get the minimum allowed timestamp for the block based on the
		// median timestamp of the last several blocks per the chain
		// consensus rules.
		if m.minimumMedianTimeFn != nil {
			m.minTimestamp = m.minimumMedianTimeFn()
		} else {
			m.minTimestamp = mining.MinimumMedianTime(m.consensus.BlockChain().(*blockchain.BlockChain))
		}
		m.StatsGbt(time.Now().UnixMilli()-start, len(template.Block.Transactions)-1) //exclude coinbase
		m.notifyBlockTemplate()

		// Freeze detection: template was recreated, check if parent changed
		newParent := template.Block.Header.ParentRoot.String()
		m.checkTemplateFreeze(newParent, true, reCreateReason)
		return nil
	} else {
		err := mining.UpdateBlockTime(m.template.Block, m.BlockChain(), m.timeSource, params.ActiveNetParams.Params)
		if err != nil {
			log.Warn(fmt.Sprintf("%s unable to update block template time: %v", m.worker.GetType(), err))
			m.recordTemplateBuildError("update_block_time", time.Now(), err)
			return err
		}
		// Freeze detection: template was NOT recreated
		if m.template != nil {
			currentParent := m.template.Block.Header.ParentRoot.String()
			m.checkTemplateFreeze(currentParent, false, "no_change")
		}
	}
	return nil
}

// armMempoolRebuild schedules one rebuild for a mempool change that was
// throttled, so pending transactions are still picked up promptly on a chain
// with no other template-invalidating activity. Handler goroutine only.
func (m *Miner) armMempoolRebuild(at time.Time) {
	if m.mempoolRebuildArmed {
		return
	}
	m.mempoolRebuildArmed = true
	time.AfterFunc(time.Until(at), func() {
		m.enqueueTemplateBuildRequest(templateBuildRequest{
			kind:              templateBuildMempool,
			reason:            templateInvalidationMempoolRootChanged.String(),
			invalidationCause: templateInvalidationMempoolRootChanged,
		})
	})
}

func resetTemplateForMining(resetter pendingTemplateResetter, canonicalBypass bool) error {
	if resetter == nil {
		return fmt.Errorf("BdagPool unavailable")
	}
	if canonicalBypass {
		if canonicalResetter, ok := resetter.(canonicalMiningPendingTemplateResetter); ok {
			return canonicalResetter.ResetTemplateForCanonicalMining()
		}
	}
	if miningResetter, ok := resetter.(miningPendingTemplateResetter); ok {
		return miningResetter.ResetTemplateForMining()
	}
	return resetter.ResetTemplate()
}

func (m *Miner) canonicalMiningTemplateBypassAllowed(now time.Time) bool {
	if held, _ := m.localEVMRecoveryStatus(); held {
		return false
	}
	if m == nil || m.consensus == nil || m.consensus.BlockChain() == nil || m.p2pSer == nil {
		return false
	}
	if !m.templateBackendSyncing() {
		return false
	}
	resetter, ok := m.bdagPool.(canonicalMiningPendingTemplateResetter)
	if !ok || !resetter.CanonicalMiningTemplateReady() {
		return false
	}
	snapshot := m.p2pSer.MiningPeerFreshnessSnapshot(now)
	return snapshot.AllowsMining(m.BlockChain().IsCurrent())
}

func (m *Miner) buildTemplateWithNonceDriftFallback(payToAddress ethcommon.Address) (*types.BlockTemplate, error, bool) {
	template, err := m.newBlockTemplate(payToAddress, false)
	if err == nil {
		return template, nil, false
	}
	if !isEVMPendingNonceDriftError(err) {
		return nil, err, false
	}

	recovered, recoverErr := m.newBlockTemplate(payToAddress, true)
	if recoverErr != nil {
		return nil, fmt.Errorf("evm pending nonce drift recovery failed after %w: %s", err, recoverErr.Error()), false
	}
	m.recordNonceDriftRecovery(time.Now())
	return recovered, nil, true
}

func (m *Miner) newBlockTemplate(payToAddress ethcommon.Address, skipPendingTransactions bool) (*types.BlockTemplate, error) {
	if m.buildTemplate != nil {
		return m.buildTemplate(payToAddress, skipPendingTransactions)
	}
	if skipPendingTransactions {
		return mining.NewBlockTemplateWithoutPendingTransactions(m.policy, params.ActiveNetParams.Params, m.sigCache, m.txpool, m.timeSource, m.consensus, payToAddress, nil, m.powType, m.coinbaseFlags)
	}
	return mining.NewBlockTemplate(m.policy, params.ActiveNetParams.Params, m.sigCache, m.txpool, m.timeSource, m.consensus, payToAddress, nil, m.powType, m.coinbaseFlags)
}

func isEVMPendingNonceDriftError(err error) bool {
	if err == nil {
		return false
	}
	msg := strings.ToLower(err.Error())
	return strings.Contains(msg, "nonce too low")
}

func (m *Miner) recordNonceDriftRecovery(now time.Time) {
	if now.IsZero() {
		now = time.Now()
	}
	m.templateHealthMu.Lock()
	m.lastNonceDriftRecoveryAt = now
	m.templateHealthMu.Unlock()
	templateNonceDriftRecoveryCounter.Inc(1)
}

func (m *Miner) logNonceDriftRecovery(reason string) {
	now := time.Now()
	m.templateHealthMu.Lock()
	lastLog := m.lastNonceDriftRecoveryLogAt
	if lastLog.IsZero() || now.Sub(lastLog) >= 30*time.Second {
		m.lastNonceDriftRecoveryLogAt = now
		m.templateHealthMu.Unlock()
		log.Warn("Recovered mining template by excluding stale EVM pending transactions", "reason", reason)
		return
	}
	m.templateHealthMu.Unlock()
}

func appendTemplateBuildReason(reason, suffix string) string {
	if reason == "" {
		return suffix
	}
	if suffix == "" {
		return reason
	}
	return reason + ":" + suffix
}

// checkTemplateFreeze detects when the same template parent has been served for too long
func (m *Miner) checkTemplateFreeze(parentHash string, templateRecreated bool, reason string) {
	freezeDetectMu.Lock()
	defer freezeDetectMu.Unlock()

	now := time.Now()

	if templateRecreated && parentHash != freezeDetectLastParent {
		// Parent changed - reset tracking
		if freezeDetectLastParent != "" {
			log.Debug("Template parent changed", "old", freezeDetectLastParent[:16]+"...", "new", parentHash[:16]+"...", "reason", reason)
		}
		freezeDetectLastParent = parentHash
		freezeDetectStartTime = now
		freezeDetectLastWarnTime = time.Time{}
		return
	}

	// Same parent - check duration
	if freezeDetectLastParent == "" {
		freezeDetectLastParent = parentHash
		freezeDetectStartTime = now
		return
	}

	staleDuration := now.Sub(freezeDetectStartTime)
	warnThreshold := 30 * time.Second
	warnRepeatInterval := 60 * time.Second

	if staleDuration >= warnThreshold {
		if freezeDetectLastWarnTime.IsZero() || now.Sub(freezeDetectLastWarnTime) >= warnRepeatInterval {
			freezeDetectLastWarnTime = now

			// Get current chain state for diagnostics
			mainHeight := m.BlockChain().Blockdag().GetMainChainTip().GetHeight()
			tips := m.consensus.BlockChain().GetMiningTips(blockdag.MaxPriority)

			log.Warn("⚠️⚠️⚠️ TEMPLATE FREEZE DETECTED ⚠️⚠️⚠️")
			log.Warn(fmt.Sprintf("Same parent hash for %.1f seconds!", staleDuration.Seconds()))
			log.Warn(fmt.Sprintf("  Parent: %s", parentHash))
			log.Warn(fmt.Sprintf("  MainHeight: %d", mainHeight))
			log.Warn(fmt.Sprintf("  MiningTips count: %d", len(tips)))
			log.Warn(fmt.Sprintf("  Template age: %s", now.Sub(m.lastTemplate).Truncate(time.Millisecond)))
			log.Warn("  Check for: deadlocks, sync issues, block processing errors")
		}
	}
}

func (m *Miner) subscribe() {
	ch := make(chan *event.Event)
	sub := m.events.Subscribe(ch)
	go func() {
		defer sub.Unsubscribe()
		for {
			select {
			case ev := <-ch:
				if ev.Data != nil {
					switch value := ev.Data.(type) {
					case *blockchain.Notification:
						m.handleNotifyMsg(value)
					case int:
						if value == event.Initialized {
							// no-op
						}
					}
				}
				if ev.Ack != nil {
					ev.Ack <- struct{}{}
				}
			case <-m.quit:
				log.Info("Close Miner Event Subscribe")
				return
			}
		}
	}()
}
func (m *Miner) handleNotifyMsg(notification *blockchain.Notification) {
	if m.worker == nil {
		return
	}
	switch notification.Type {
	case blockchain.BlockAccepted:
		band, ok := notification.Data.(*blockchain.BlockAcceptedNotifyData)
		if !ok {
			return
		}
		if band.IsMainChainTipChange {
			go m.BlockChainChange()
		}
	}
}

// submitBlock submits the passed block to network after ensuring it passes all
// of the consensus validation rules.
func (m *Miner) submitBlock(block *types.SerializedBlock) (json.SubmitBlockResult, error) {
	return m.submitBlockContext(context.Background(), block)
}

func (m *Miner) submitBlockContext(ctx context.Context, block *types.SerializedBlock) (json.SubmitBlockResult, error) {
	// Direct submitBlock does not pass through CanMining. Preserve that API,
	// but never let it bypass a local execution-state recovery hold.
	if held, reason := m.localEVMRecoveryStatus(); held {
		err := newSubmitOutcomeError(submitOutcomeNodeSyncing, nil, "%s", reason)
		res, _ := m.submitOutcomeFromError(err)
		if block != nil {
			res.BlockHash = block.Hash().String()
		}
		return res, err
	}
	if m.worker == nil {
		err := newSubmitOutcomeError(submitOutcomeMinerDisabled, nil, "You must enable miner by --miner.")
		res, _ := m.submitOutcomeFromError(err)
		res.BlockHash = block.Hash().String()
		return res, err
	}
	m.submitLocker.Lock()
	m.totalSubmit++
	m.submitLocker.Unlock()

	err := m.CheckSubMainChainTip(block.Block().Parents)
	if err != nil {
		go m.invalidateForSubmitObsoleteDepth(obsoleteDepthFromError(err))
		err = newSubmitOutcomeError(submitOutcomeTipOverdue, err, "The tips of block is expired:%s (error:%s)", block.Hash().String(), err.Error())
		res, _ := m.submitOutcomeFromError(err)
		res.BlockHash = block.Hash().String()
		return res, err
	}
	// Process this block using the same rules as blocks coming from other
	// nodes. This will in turn relay it to the network like normal.
	chain := m.consensus.BlockChain().(*blockchain.BlockChain)
	ib, IsOrphan, err := chain.ProcessBlockContext(ctx, block, blockchain.BFRPCAdd, nil)
	if err != nil {
		var rErr blockchain.RuleError
		isRuleError := errors.As(err, &rErr)
		status := chain.BlockSubmissionStatus(block.Hash())
		if status == blockchain.BlockSubmissionAccepted || status == blockchain.BlockSubmissionQueued ||
			(status == blockchain.BlockSubmissionRejected && (!isRuleError || rErr.ErrorCode == blockchain.ErrDuplicateBlock)) {
			if res, outcomeErr, handled := m.submitOutcomeForBlockStatus(status, block.Hash().String(), err); handled {
				return res, outcomeErr
			}
		}
		// Anything other than a rule violation is an unexpected error,
		// so preserve it as a retryable nonfinal outcome.
		if !isRuleError {
			err = newSubmitOutcomeError(submitOutcomeRetryable, err, "Unexpected error while processing block submitted miner: %v (%s)", err, m.worker.GetType())
			res, _ := m.submitOutcomeFromError(err)
			res.BlockHash = block.Hash().String()
			return res, err
		}
		// Occasionally errors are given out for timing errors with
		// ReduceMinDifficulty and high block works that is above
		// the target. Feed these to debug.
		if params.ActiveNetParams.Params.ReduceMinDifficulty &&
			rErr.ErrorCode == blockchain.ErrHighHash {
			err = newSubmitOutcomeError(submitOutcomeInvalidPOW, err, "Block submitted via miner rejected "+
				"because of ReduceMinDifficulty time sync failure: %v (%s)", err, m.worker.GetType())
			res, _ := m.submitOutcomeFromError(err)
			res.BlockHash = block.Hash().String()
			return res, err
		}
		// Other rule errors should be reported.
		err = newSubmitOutcomeError(submitOutcomeCodeFromRuleError(rErr.ErrorCode), err, "Block submitted via %s rejected: %v", m.worker.GetType(), err)
		res, _ := m.submitOutcomeFromError(err)
		res.BlockHash = block.Hash().String()
		return res, err
	}
	if IsOrphan {
		if res, outcomeErr, handled := m.submitOutcomeForBlockStatus(blockchain.BlockSubmissionQueued, block.Hash().String(), nil); handled {
			return res, outcomeErr
		}
	}

	m.submitLocker.Lock()
	m.successSubmit++
	m.submitLocker.Unlock()

	// The block was accepted.
	// coinbaseTxOuts := block.Block().Transactions[0].TxOut
	// coinbaseTxGenerated := uint64(0)
	// for _, out := range coinbaseTxOuts {
	// 	coinbaseTxGenerated += uint64(out.Amount.Value)
	// }
	res := m.submitOutcomeResult(submitOutcomeAccepted, "accepted")
	res.BlockHash = block.Hash().String()
	res.Order = blockdag.GetOrderLogStr(ib.GetOrder())
	res.Height = int64(ib.GetHeight())
	res.CoinbaseAmount = 10
	if len(block.Transactions()) > 0 {
		res.CoinbaseTxID = block.Transactions()[0].Hash().String()
	}
	return res, nil
}

func (m *Miner) submitBlockHeader(header *types.BlockHeader) (json.SubmitBlockResult, error) {
	return m.submitBlockHeaderContext(context.Background(), header)
}

func (m *Miner) submitBlockHeaderContext(ctx context.Context, header *types.BlockHeader) (json.SubmitBlockResult, error) {
	if !m.IsEnable() {
		err := newSubmitOutcomeError(submitOutcomeMinerDisabled, nil, "You must enable miner by --miner.")
		res, _ := m.submitOutcomeFromError(err)
		return res, err
	}
	if err := m.CanMining("submitBlockHeader"); err != nil {
		code := classifySubmitError(err)
		if code == submitOutcomeInternalError {
			code = submitOutcomeNodeSyncing
		}
		err = newSubmitOutcomeError(code, err, "%s", err.Error())
		res, _ := m.submitOutcomeFromError(err)
		return res, err
	}
	if m.template == nil {
		err := newSubmitOutcomeError(submitOutcomeTemplateSeqUnknown, nil, "no current mining template")
		res, _ := m.submitOutcomeFromError(err)
		return res, err
	}
	matchedTemplate := m.template
	if !IsEqualForMiner(&matchedTemplate.Block.Header, header) {
		if recent := m.recentTemplateForHeader(header); recent != nil {
			matchedTemplate = recent
		} else {
			// Deliberately no template invalidation here. Failing to find the
			// template a miner worked from says nothing about whether the live
			// template is stale, and rebuilding would evict another retained
			// template — starving the submits still in flight behind this one.
			// The invalidation in submitBlock is driven by a real obsolete
			// depth reported by the chain, which is actual evidence.
			err := newSubmitOutcomeError(submitOutcomeTipOverdue, nil, "You're overdue")
			res, _ := m.submitOutcomeFromError(err)
			return res, err
		}
	}
	// EVM chain: extranonce does not affect TxRoot. Check this before
	// cloning the full template block so malformed/stale submits fail fast.
	if matchedTemplate.Block.Header.TxRoot.Cmp(header.TxRoot) != 0 {
		err := newSubmitOutcomeError(submitOutcomeTxRootMismatch, nil, "tx root mismatch")
		res, _ := m.submitOutcomeFromError(err)
		return res, err
	}
	start := time.Now()
	block, err := matchedTemplate.Block.Clone()
	if err != nil {
		res, outcomeErr := m.submitOutcomeFromError(err)
		return res, outcomeErr
	}
	block.Header.TxRoot = header.TxRoot
	block.Header.Difficulty = header.Difficulty
	block.Header.Timestamp = header.Timestamp
	block.Header.Pow = header.Pow
	block.Header.PowRoot = header.PowRoot
	res, err := m.submitBlockContext(ctx, types.NewBlock(block))
	if err == nil {
		m.StatsSubmit(start, res.BlockHash, block.Transactions.Len()-1)
	}
	return res, err
}

func (m *Miner) CanMining(origin string) error {
	readiness := m.miningRPCReadiness()
	if !readiness.allowed {
		gbtDownload.Update(1)
		log.Trace("Client in initial download", "reason", readiness.reason, "origin", origin)
		return rpc.NodeIsSyncingError("Client in initial download ",
			readiness.rpcInitialDownloadMessage())
	}
	if params.ActiveNetParams.Net == protocol.PrivNet {
		return nil
	}
	// if m.BlockChain().GetSelfAdd() >= blockdag.StableConfirmations {
	// 	gbtSideChain.Update(1)
	// 	return fmt.Errorf("Side chain depth too large")
	// }
	return nil
}

func (m *Miner) CanBuildTemplate(origin string) error {
	readiness := m.miningRPCReadiness()
	if !readiness.buildTemplateReady {
		gbtDownload.Update(1)
		log.Trace("Client in initial download", "reason", readiness.getTemplateReason, "origin", origin)
		return rpc.NodeIsSyncingError("Client in initial download ",
			readiness.getTemplateReason)
	}
	return nil
}

func miningSyncAllowed(submitNoSynced, allowNearlySynced, current, nearlySynced, chainCurrent, miningFreshObserved, miningFresh bool, miningFreshReason string) bool {
	if miningFreshObserved && !p2pMiningFreshnessAllowsMining(chainCurrent, miningFreshObserved, miningFresh, miningFreshReason) {
		return false
	}
	if current {
		return true
	}
	if p2pMiningFreshnessAllowsMining(chainCurrent, miningFreshObserved, miningFresh, miningFreshReason) {
		return true
	}
	if submitNoSynced {
		return true
	}
	return allowNearlySynced && nearlySynced
}

func miningTemplateSyncAllowed(current, chainCurrent, miningFreshObserved, miningFresh bool, miningFreshReason string) bool {
	if miningFreshObserved && !p2pMiningFreshnessAllowsMining(chainCurrent, miningFreshObserved, miningFresh, miningFreshReason) {
		return false
	}
	if current {
		return true
	}
	return p2pMiningFreshnessAllowsMining(chainCurrent, miningFreshObserved, miningFresh, miningFreshReason)
}

func p2pMiningFreshnessAllowsMining(chainCurrent, observed, fresh bool, reason string) bool {
	if !observed {
		return false
	}
	return model.P2PMiningFreshnessSnapshot{
		MiningFresh:           fresh,
		MiningFreshReasonCode: reason,
	}.AllowsMining(chainCurrent)
}

func (m *Miner) IsEnable() bool {
	if !m.cfg.Miner {
		return false
	}
	if m.IsShutdown() {
		return false
	}
	if !m.IsStarted() {
		return false
	}
	return true
}

func (m *Miner) initCoinbase() error {
	if m.coinbaseAddress != ethcommon.HexToAddress("0x00") {
		return nil
	}
	mAddrs := m.cfg.GetMinningAddrs()
	if len(mAddrs) <= 0 {
		// Respond with an error if there are no addresses to pay the
		// created blocks to.
		return fmt.Errorf("No payment addresses specified via --miningaddr.")
	}
	// Choose a payment address cryptographically at random.
	if len(mAddrs) == 1 {
		m.coinbaseAddress = mAddrs[0]
	} else {
		// Use crypto/rand for secure random selection
		idx, err := rand.Int(rand.Reader, big.NewInt(int64(len(mAddrs))))
		if err != nil {
			// Fallback to first address if random generation fails
			log.Error("Failed to generate secure random index for coinbase selection: %v", err)
			m.coinbaseAddress = mAddrs[0]
		} else {
			m.coinbaseAddress = mAddrs[idx.Int64()]
		}
	}
	if m.GetCoinbasePKAddress() != nil {
		log.Info(fmt.Sprintf("Init Coinbase PK Address:%s    PKH Address:%s", m.GetCoinbasePKAddress().String(), m.GetCoinbasePKAddress().String()))
	} else {
		log.Info(fmt.Sprintf("Init Coinbase Address:%s", m.coinbaseAddress.String()))
	}

	return nil
}

func (m *Miner) GetCoinbasePKAddress() *ethcommon.Address {
	pka := m.coinbaseAddress
	return &pka
}

func (m *Miner) handleStallSample() {
	if m.IsShutdown() {
		return
	}

	if m.template == nil || m.template.Block == nil {
		return
	}
	timeout := params.ActiveNetParams.TargetTimePerBlock * 3
	if timeout < time.Minute {
		timeout = time.Minute
	}
	if roughtime.Now().After(m.template.Block.Header.Timestamp.Add(timeout)) {
		if err := m.CanBuildTemplate("handleStallSample"); err != nil {
			return
		}
		m.enqueueTemplateBuildRequest(templateBuildRequest{
			kind:              templateBuildStall,
			reason:            templateInvalidationSequenceObsolete.String(),
			invalidationCause: templateInvalidationSequenceObsolete,
		})
	}

}

func (m *Miner) BlockChainChange() {
	// Ignore if we are shutting down.
	if m.IsShutdown() {
		return
	}
	if err := m.CanBuildTemplate("BlockChainChange"); err != nil {
		return
	}

	m.enqueueTemplateBuildRequest(templateBuildRequest{
		kind:              templateBuildBlockChain,
		reason:            templateInvalidationParentChanged.String(),
		invalidationCause: templateInvalidationParentChanged,
	})
}

func (m *Miner) MempoolChange() {
	// Ignore if we are shutting down.
	if m.IsShutdown() {
		return
	}
	if m.worker == nil {
		return
	}
	if err := m.CanBuildTemplate("MempoolChange"); err != nil {
		return
	}
	m.enqueueTemplateBuildRequest(templateBuildRequest{
		kind:              templateBuildMempool,
		reason:            templateInvalidationMempoolRootChanged.String(),
		invalidationCause: templateInvalidationMempoolRootChanged,
	})
}

func (m *Miner) GBTMining(ctx context.Context, request *json.TemplateRequest, reply chan *gbtResponse) error {
	if ctx == nil {
		ctx = context.Background()
	}
	if !m.IsStarted() {
		if !m.cfg.Miner {
			m.cfg.Miner = true
		}
		if err := m.Start(); err != nil {
			log.Error(err.Error())
			return err
		}
	}
	// Ignore if we are shutting down.
	if m.IsShutdown() {
		return fmt.Errorf("Miner is shutdown")
	}
	if err := m.CanBuildTemplate("GBTMining"); err != nil {
		return err
	}

	timer := time.NewTimer(getBlockTemplateEnqueueTimeout)
	defer timer.Stop()
	select {
	case m.msgChan <- &GBTMiningMsg{request: request, reply: reply}:
		return nil
	case <-ctx.Done():
		return fmt.Errorf("getBlockTemplate request canceled before miner handler accepted it: %w", ctx.Err())
	case <-timer.C:
		return fmt.Errorf("getBlockTemplate request queue timed out after %s", getBlockTemplateEnqueueTimeout)
	case <-m.quit:
		return fmt.Errorf("Miner is shutdown")
	}
}

func (m *Miner) RemoteMining(powType pow.PowType, coinbaseFlags mining.CoinbaseFlags, reply chan *gbtResponse) error {
	return fmt.Errorf("Remote mining is disabled")
}

// notifyBlockTemplate publishes the current template on the templateFeed so
// that all Blockdag_subscribe("blockTemplate") subscribers receive the update
// as soon as a new template is created.
//
// Called from updateBlockTemplate after a successful template recreation.
// Subscribers receive a *json.RemoteGBTResult; slow subscribers are handled
// by their own buffered channels and do not block the miner.
func (m *Miner) notifyBlockTemplate() {
	if m.template == nil || m.template.Block == nil {
		return
	}
	if err := m.validateCurrentTemplateCoinbase("blockTemplate notification"); err != nil {
		log.Warn("Suppressing invalid mining template notification", "err", err)
		return
	}
	if !m.miningRPCReadiness().getTemplateReady {
		return
	}
	// Serialize the final generation check and sequence allocation. A failure
	// racing this check records the allocated sequence in the invalidation
	// watermark, so the payload remains rejected even if it was already queued.
	m.gbtGenerationMu.Lock()
	if m.gbtGenerationFailure != nil {
		m.gbtGenerationMu.Unlock()
		return
	}
	seq := m.templateSeq.Add(1)
	m.gbtGenerationMu.Unlock()
	bt := m.buildRemoteGBTResultWithSeq(seq)
	if bt == nil {
		return
	}
	m.templateFeed.Send(bt)
}

// buildRemoteGBTResult snapshots the current template into the stream payload.
// Returns nil when no template is available yet. Must be called with the
// miner holding m.template stably (i.e. from updateBlockTemplate or from a
// short read after m.template has been set).
func (m *Miner) buildRemoteGBTResult() *json.RemoteGBTResult {
	return m.buildRemoteGBTResultWithSeq(m.templateSeq.Load())
}

func (m *Miner) buildRemoteGBTResultWithSeq(seq uint64) *json.RemoteGBTResult {
	if m.template == nil || m.template.Block == nil {
		return nil
	}
	if err := m.validateCurrentTemplateCoinbase("blockTemplate stream"); err != nil {
		log.Warn("Suppressing invalid mining template stream payload", "err", err)
		return nil
	}
	if !m.miningRPCReadiness().getTemplateReady {
		return nil
	}
	header := &m.template.Block.Header
	return &json.RemoteGBTResult{
		Seq:             seq,
		Version:         uint32(header.Version),
		PreviousHash:    header.ParentRoot.String(),
		TxRoot:          header.TxRoot.String(),
		StateRoot:       header.StateRoot.String(),
		NBits:           strconv.FormatInt(int64(header.Difficulty), 16),
		CurTime:         header.Timestamp.Unix(),
		Height:          int64(m.template.Height),
		CoinbaseAddress: header.CoinbaseAddress.String(),
		Reward:          header.Reward,
		BlockFees:       m.template.BlockFees,
	}
}

func (m *Miner) sendNotification(url string, jsonData []byte) {
	defer m.reqWG.Done()
	req, err := http.NewRequest("POST", url, bytes.NewReader(jsonData))
	if err != nil {
		log.Error(err.Error())
		return
	}
	ctx, cancel := context.WithTimeout(m.Context(), NotifyURLTimeout)
	defer cancel()
	req = req.WithContext(ctx)
	req.Header.Set("Content-Type", "application/json")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		log.Error(err.Error())
	} else {
		defer resp.Body.Close()
		log.Trace(fmt.Sprintf("Notified remote miner:%s %s", url, resp.Status))
	}
}

func (m *Miner) BlockChain() *blockchain.BlockChain {
	return m.consensus.BlockChain().(*blockchain.BlockChain)
}

// Checking the sub main chain for the parents of tip
func (m *Miner) CheckSubMainChainTip(parents []*common.Hash) error {
	if len(parents) == 0 {
		return fmt.Errorf("Parents is empty")
	}
	var mt blockdag.IBlock
	for k, pa := range parents {
		ib := m.BlockChain().Blockdag().GetBlock(pa)
		if ib == nil {
			return fmt.Errorf("Parent(%s) is overdue\n", pa.String())
		}
		if k == 0 {
			mt = ib
		}
	}
	if mt == nil {
		return fmt.Errorf("No main tip:%v", parents)
	}
	mainTip := m.BlockChain().Blockdag().GetMainChainTip()
	if mt.GetHeight()+submitMainTipLagTolerance >= mainTip.GetHeight() {
		return nil
	}

	distance := mainTip.GetHeight() - mt.GetHeight()

	return fmt.Errorf("main chain tip is overdue,submit main parent:%v (%d), but main tip is :%v (%d). Obsolete depth:%d\n",
		mt.GetHash().String(), mt.GetHeight(), mainTip.GetHash().String(), mainTip.GetHeight(), distance)
}

func NewMiner(consensus model.Consensus, policy *mining.Policy, bdagpool *bdag.BdagPool, p2pSer model.P2PService) *Miner {
	m := Miner{
		msgChan:         make(chan any),
		templateReqChan: make(chan struct{}, 1),
		quit:            make(chan struct{}),
		cfg:             consensus.Config(),
		policy:          policy,
		sigCache:        consensus.SigCache(),
		txpool:          bdagpool.GetTxPool(),
		bdagPool:        bdagpool,
		timeSource:      consensus.MedianTimeSource(),
		// powType:       pow.BDAGXKECCAKV1,
		// powType: pow.BLAKE2S,
		powType:       pow.SCRYPT,
		events:        consensus.Events(),
		coinbaseFlags: mining.CoinbaseFlagsStatic,
		consensus:     consensus,
		p2pSer:        p2pSer,
	}

	return &m
}

func IsEqualForMiner(header *types.BlockHeader, other *types.BlockHeader) bool {
	if header.Version != other.Version ||
		header.ParentRoot.Cmp(other.ParentRoot) != 0 ||
		header.StateRoot.Cmp(other.StateRoot) != 0 {
		return false
	}
	return true
}

// recentTemplate is a superseded template together with the time it stopped
// being the live template, which is what bounds its eligibility for matching.
type recentTemplate struct {
	template *types.BlockTemplate
	addedAt  time.Time
}

// pushRecentTemplate records outgoing as a recently-superseded template
// before m.template is replaced, so a header mined against it can still be
// matched by recentTemplateForHeader. Only called from m.handler(), the
// single goroutine that mutates template state.
func (m *Miner) pushRecentTemplate(outgoing *types.BlockTemplate) {
	if outgoing == nil || outgoing.Block == nil {
		return
	}
	now := roughtime.Now()

	m.Lock()
	defer m.Unlock()
	// A rebuild that changes nothing a miner can observe (a stall-driven
	// refresh, say) would otherwise push a duplicate and age out a template
	// that is still distinct. Refresh the existing entry instead.
	if n := len(m.recentTemplates); n > 0 {
		if newest := m.recentTemplates[n-1].template; newest != nil && newest.Block != nil &&
			IsEqualForMiner(&newest.Block.Header, &outgoing.Block.Header) {
			m.recentTemplates[n-1].addedAt = now
			return
		}
	}
	m.recentTemplates = append(m.recentTemplates, recentTemplate{template: outgoing, addedAt: now})
	m.pruneRecentTemplatesLocked(now)
}

// pruneRecentTemplatesLocked drops templates past recentTemplateRetention, and
// then the oldest survivors above recentTemplateMaxEntries. m must be held.
func (m *Miner) pruneRecentTemplatesLocked(now time.Time) {
	cutoff := now.Add(-recentTemplateRetention)
	drop := 0
	for drop < len(m.recentTemplates) && m.recentTemplates[drop].addedAt.Before(cutoff) {
		drop++
	}
	if excess := len(m.recentTemplates) - drop - recentTemplateMaxEntries; excess > 0 {
		drop += excess
	}
	if drop <= 0 {
		return
	}
	kept := copy(m.recentTemplates, m.recentTemplates[drop:])
	for i := kept; i < len(m.recentTemplates); i++ {
		m.recentTemplates[i] = recentTemplate{}
	}
	m.recentTemplates = m.recentTemplates[:kept]
}

// recentTemplateForHeader looks up the most recent superseded template whose
// header still matches the miner-mutable fields of header, i.e. one the
// submitted header could actually have been mined against.
func (m *Miner) recentTemplateForHeader(header *types.BlockHeader) *types.BlockTemplate {
	now := roughtime.Now()

	m.Lock()
	defer m.Unlock()
	m.pruneRecentTemplatesLocked(now)
	for i := len(m.recentTemplates) - 1; i >= 0; i-- {
		candidate := m.recentTemplates[i].template
		if candidate == nil || candidate.Block == nil {
			continue
		}
		if IsEqualForMiner(&candidate.Block.Header, header) {
			return candidate
		}
	}
	return nil
}
