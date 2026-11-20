package p2p

import (
	"fmt"
	"math"
	"strings"
	"time"

	"github.com/BlockdagNetworkLabs/bdag/common/marshal"
	"github.com/BlockdagNetworkLabs/bdag/core/json"
	"github.com/BlockdagNetworkLabs/bdag/core/protocol"
	p2ppeers "github.com/BlockdagNetworkLabs/bdag/p2p/peers"
	"github.com/BlockdagNetworkLabs/bdag/p2p/synch"
	"github.com/BlockdagNetworkLabs/bdag/rpc/api"
	"github.com/BlockdagNetworkLabs/bdag/rpc/client/cmds"
	ethcommon "github.com/ethereum/go-ethereum/common"
	golog "github.com/ipfs/go-log/v2"
	"github.com/libp2p/go-libp2p/core/peer"
)

func (s *Service) APIs() []api.API {
	return []api.API{
		{
			NameSpace: cmds.DefaultServiceNameSpace,
			Service:   NewPublicP2PAPI(s),
			Public:    true,
		},
		{
			NameSpace: cmds.P2PNameSpace,
			Service:   NewPrivateP2PAPI(s),
			Public:    false,
		},
	}
}

type PublicP2PAPI struct {
	s *Service
}

func NewPublicP2PAPI(s *Service) *PublicP2PAPI {
	return &PublicP2PAPI{s}
}

// Return the peer info
func (api *PublicP2PAPI) GetPeerInfo(verbose *bool, pid *string) (any, error) {
	vb := false
	if verbose != nil {
		vb = *verbose
	}
	pidStr := ""
	if pid != nil {
		pidStr = *pid
	}

	ps := api.s
	ps.refreshPeerForkObservations()
	peerSnapshots := ps.Peers().StatsSnapshots()
	activePeers := ps.Peers().ActiveIDSet()
	var syncPeerID peer.ID
	hasSyncPeer := false
	if syncPeer := ps.PeerSync().SyncPeer(); syncPeer != nil {
		syncPeerID = syncPeer.GetID()
		hasSyncPeer = true
	}
	infos := make([]*json.GetPeerInfoResult, 0, len(peerSnapshots))
	for _, p := range peerSnapshots {
		_, active := activePeers[p.PeerID]
		// A peer is considered healthy if it is active, handshake completed (State),
		// has a known address, and has valid services (not Unknown).
		healthy := active && p.State && p.Address != "" && p.Services != protocol.Unknown && p.Services != 0

		if !vb {
			if !healthy {
				continue
			}
		}
		if len(pidStr) > 0 {
			if p.PeerID.String() != pidStr &&
				!strings.Contains(p.PeerID.String(), pidStr) {
				continue
			}
		}
		info := &json.GetPeerInfoResult{
			ID:        p.PeerID.String(),
			Name:      p.Name,
			Address:   p.Address,
			BytesSent: p.BytesSent,
			BytesRecv: p.BytesRecv,
			Circuit:   p.IsCircuit,
			Bads:      p.Bads,
			ReConnect: p.ReConnect,
			Active:    active,
			DAGPort:   p.DAGPort,
			EVMPort:   p.EVMPort,
		}
		applyPeerSourceInfo(info, ps.peerSourceForStats(p, active))
		if latency := ps.host.Peerstore().LatencyEWMA(p.PeerID); latency > 0 {
			info.Latency = latency.Truncate(time.Millisecond).String()
			info.LatencyMS = latency.Milliseconds()
		}
		info.Protocol = p.Protocol
		info.Services = p.Services.String()
		if p.Genesis != nil {
			info.Genesis = p.Genesis.String()
		}
		if len(p.StateRoot) > 0 {
			info.StateRoot = p.StateRoot
		}
		if p.IsTheSameNetwork() {
			info.State = p.State
		}
		if len(p.Version) > 0 {
			info.Version = p.Version
		}
		if len(p.Network) > 0 {
			info.Network = p.Network
		}

		if p.State {
			info.TimeOffset = p.TimeOffset
			if p.Genesis != nil {
				info.Genesis = p.Genesis.String()
			}
			info.Direction = p.Direction.String()
			if p.GraphState != nil {
				info.GraphState = marshal.GetGraphStateResult(p.GraphState)
			}
			info.SyncNode = hasSyncPeer && p.PeerID == syncPeerID
			info.ConnTime = p.ConnTime.Truncate(time.Second).String()
			info.GSUpdate = p.GraphStateDur.Truncate(time.Second).String()
		}
		if !p.LastSend.IsZero() {
			info.LastSend = p.LastSend.String()
		}
		if !p.LastRecv.IsZero() {
			info.LastRecv = p.LastRecv.String()
		}
		if !p.MempoolReqTime.IsZero() {
			info.MempoolReqTime = p.MempoolReqTime.String()
		}
		if len(p.QNR) > 0 {
			info.QNR = p.QNR
		}
		applyPeerForkInfo(info, peerForkInfoFromStats(ps, p, active, syncPeerID, hasSyncPeer))
		infos = append(infos, info)
	}
	return infos, nil
}

func (api *PublicP2PAPI) GetPeerForks(verbose *bool, pid *string) (any, error) {
	vb := false
	if verbose != nil {
		vb = *verbose
	}
	pidStr := ""
	if pid != nil {
		pidStr = *pid
	}

	ps := api.s
	ps.refreshPeerForkObservations()
	peerSnapshots := ps.Peers().StatsSnapshots()
	var syncPeerID peer.ID
	hasSyncPeer := false
	if syncPeer := ps.PeerSync().SyncPeer(); syncPeer != nil {
		syncPeerID = syncPeer.GetID()
		hasSyncPeer = true
	}
	infos := make([]*json.PeerForkInfo, 0, len(peerSnapshots))
	for _, p := range peerSnapshots {
		active := ps.Peers().IsActiveID(p.PeerID)
		healthy := active && p.State && p.Address != "" && p.Services != protocol.Unknown && p.Services != 0
		if !vb && !healthy {
			continue
		}
		if len(pidStr) > 0 {
			if p.PeerID.String() != pidStr &&
				!strings.Contains(p.PeerID.String(), pidStr) {
				continue
			}
		}
		infos = append(infos, peerForkInfoFromStats(ps, p, active, syncPeerID, hasSyncPeer))
	}
	return infos, nil
}

// GetPeerDiscoveryStatus returns advisory discovery and compatibility evidence.
// It does not change peer selection, synchronization, mining, or consensus.
func (api *PublicP2PAPI) GetPeerDiscoveryStatus() (*json.PeerDiscoveryStatusResult, error) {
	if api == nil || api.s == nil {
		return (*Service)(nil).peerDiscoveryStatusResult(), nil
	}
	api.s.refreshPeerForkObservations()
	return api.s.peerDiscoveryStatusResult(), nil
}

func (s *Service) refreshPeerForkObservations() {
	if s == nil || s.sy == nil || s.Peers() == nil {
		return
	}
	peerSync := s.PeerSync()
	if peerSync == nil {
		return
	}
	for _, pe := range s.Peers().CanSyncPeers() {
		peerSync.UpdatePeerForkObservation(pe)
	}
}

func peerForkInfoFromStats(s *Service, p *p2ppeers.StatsSnap, active bool, syncPeerID peer.ID, hasSyncPeer bool) *json.PeerForkInfo {
	info := &json.PeerForkInfo{
		ID:             p.PeerID.String(),
		Address:        p.Address,
		Name:           p.Name,
		Active:         active,
		State:          p.State,
		Direction:      p.Direction.String(),
		Protocol:       p.Protocol,
		Services:       p.Services.String(),
		Version:        p.Version,
		Network:        p.Network,
		ForkStatus:     "unknown",
		ForkConfidence: "none",
		ForkReason:     "not observed yet",
	}
	if s != nil {
		applyPeerSourceForkInfo(info, s.peerSourceForStats(p, active))
	}
	if s != nil && s.host != nil {
		if latency := s.host.Peerstore().LatencyEWMA(p.PeerID); latency > 0 {
			info.Latency = latency.Truncate(time.Millisecond).String()
			info.LatencyMS = latency.Milliseconds()
		}
	}
	if hasSyncPeer {
		info.SyncNode = p.PeerID == syncPeerID
	}
	observation := p.Fork
	if s != nil && s.sy != nil && s.PeerSync() != nil {
		observation = s.PeerSync().CurrentPeerForkObservation(p)
	}
	applyPeerCompatibilityAssessment(info, p2ppeers.AssessPeerCompatibility(observation, time.Now()))
	if observation == nil {
		return info
	}
	info.ForkStatus = string(observation.Status)
	info.ForkConfidence = observation.Confidence
	info.ForkReason = observation.Reason
	if observation.SameNativeForkKnown {
		same := observation.SameNativeFork
		info.SameNativeFork = &same
	}
	if !observation.LastChecked.IsZero() {
		info.LastChecked = observation.LastChecked.Format(time.RFC3339)
	}
	info.LastError = observation.LastError
	info.PeerMainOrder = observation.MainOrder
	info.PeerMainHeight = observation.MainHeight
	info.PeerLayer = observation.Layer
	info.PeerTotal = observation.Total
	info.PeerMainTipHash = hashForJSON(observation.MainTip)
	for _, tip := range observation.Tips {
		if hash := hashForJSON(tip); hash != "" {
			info.PeerTips = append(info.PeerTips, hash)
		}
	}
	info.LocalMainOrder = observation.LocalMainOrder
	info.LocalMainTipHash = hashForJSON(observation.LocalMainTip)
	info.SyncPointHash = hashForJSON(observation.SyncPoint)
	info.DivergenceOrder = observation.DivergenceOrder
	info.CommitmentStatus = observation.CommitmentStatus
	info.EVMCheckpointNumber = observation.EVMCheckpointNumber
	info.EVMCheckpointHash = hashForJSON(observation.EVMCheckpointHash)
	info.EVMCheckpointRoot = hashForJSON(observation.EVMCheckpointRoot)
	info.SampledBlockHash = hashForJSON(observation.SampledBlockHash)
	info.SampledBlockOrder = observation.SampledBlockOrder
	info.SampledStateRootGot = hashForJSON(observation.SampledStateRootGot)
	info.SampledStateRootWant = hashForJSON(observation.SampledStateRootWant)
	return info
}

func applyPeerCompatibilityAssessment(info *json.PeerForkInfo, assessment p2ppeers.PeerCompatibilityAssessment) {
	if info == nil {
		return
	}
	info.PeerCompatibility = assessment.Compatibility
	info.CompatibilityReason = assessment.CompatibilityReason
	info.CompatibilityProofAge = assessment.ProofAgeSeconds
	info.CompatibilityProofFresh = assessment.ProofFresh
	info.PeerUsefulness = assessment.Usefulness
	info.PeerUsefulnessReason = assessment.UsefulnessReason
	info.UsefulForTarget = assessment.UsefulForTarget
	info.DatasetStatus = assessment.DatasetStatus
	info.DatasetReason = assessment.DatasetReason
	info.DatasetProof = assessment.DatasetProof
}

func applyPeerForkInfo(info *json.GetPeerInfoResult, fork *json.PeerForkInfo) {
	if info == nil || fork == nil {
		return
	}
	info.Fork = fork
	info.ForkStatus = fork.ForkStatus
	info.ForkConfidence = fork.ForkConfidence
	info.ForkReason = fork.ForkReason
	info.PeerCompatibility = fork.PeerCompatibility
	info.CompatibilityReason = fork.CompatibilityReason
	info.CompatibilityProofAge = fork.CompatibilityProofAge
	info.CompatibilityProofFresh = fork.CompatibilityProofFresh
	info.PeerUsefulness = fork.PeerUsefulness
	info.PeerUsefulnessReason = fork.PeerUsefulnessReason
	info.UsefulForTarget = fork.UsefulForTarget
	info.DatasetStatus = fork.DatasetStatus
	info.DatasetReason = fork.DatasetReason
	info.DatasetProof = fork.DatasetProof
	info.PeerSource = fork.PeerSource
	info.PeerSourceHint = fork.PeerSourceHint
	info.SeedPeer = fork.SeedPeer
	info.RecentPeer = fork.RecentPeer
	info.SameNativeFork = fork.SameNativeFork
	info.ForkLastChecked = fork.LastChecked
	info.ForkLastError = fork.LastError
	info.PeerMainOrder = fork.PeerMainOrder
	info.PeerMainHeight = fork.PeerMainHeight
	info.PeerLayer = fork.PeerLayer
	info.PeerTotal = fork.PeerTotal
	info.PeerMainTipHash = fork.PeerMainTipHash
	info.LocalMainOrder = fork.LocalMainOrder
	info.LocalMainTipHash = fork.LocalMainTipHash
	info.SyncPointHash = fork.SyncPointHash
	info.DivergenceOrder = fork.DivergenceOrder
	info.CommitmentStatus = fork.CommitmentStatus
	info.SampledBlockHash = fork.SampledBlockHash
	info.SampledBlockOrder = fork.SampledBlockOrder
	info.SampledStateRootGot = fork.SampledStateRootGot
	info.SampledStateRootWant = fork.SampledStateRootWant
	info.EVMCheckpointNumber = fork.EVMCheckpointNumber
	info.EVMCheckpointHash = fork.EVMCheckpointHash
	info.EVMCheckpointRoot = fork.EVMCheckpointRoot
}

func hashForJSON(hash ethcommon.Hash) string {
	if hash == (ethcommon.Hash{}) {
		return ""
	}
	return hash.String()
}

// Reload All Peers
func (api *PrivateP2PAPI) ReloadPeers() error {
	api.s.connectFromPeerStore()
	return nil
}

// Return IsCurrent
func (api *PublicP2PAPI) IsCurrent() (any, error) {
	return api.s.IsCurrent(), nil
}

// GetGoodbyeLog returns recent peer goodbye / silent-disconnect records.
// limit defaults to 50 and is capped at the in-memory ring capacity.
func (api *PublicP2PAPI) GetGoodbyeLog(limit *int) (any, error) {
	n := 50
	if limit != nil && *limit > 0 {
		n = *limit
	}
	sy := api.s.sy
	if sy == nil {
		return map[string]any{
			"catalog": synch.GoodbyeCodeCatalog(),
			"counts":  map[string]int{},
			"records": []any{},
		}, nil
	}
	return map[string]any{
		"catalog": synch.GoodbyeCodeCatalog(),
		"counts":  sy.GoodbyeCounts(),
		"records": sy.GoodbyeSnapshot(n),
	}, nil
}

func (api *PublicP2PAPI) GetNetworkInfo() (any, error) {
	ps := api.s
	peers := ps.Peers().StatsSnapshots()
	activePeers := ps.Peers().ActiveIDSet()
	nstat := &json.NetworkStat{MaxConnected: ps.Config().MaxPeers,
		MaxInbound: ps.Config().MaxInbound, Infos: []*json.NetworkInfo{}}
	infos := map[string]*json.NetworkInfo{}
	gsups := map[string][]time.Duration{}

	for _, p := range peers {
		nstat.TotalPeers++

		if p.Services&protocol.Relay > 0 {
			nstat.TotalRelays++
		}
		//
		if len(p.Network) <= 0 {
			continue
		}

		info, ok := infos[p.Network]
		if !ok {
			info = &json.NetworkInfo{Name: p.Network}
			infos[p.Network] = info
			nstat.Infos = append(nstat.Infos, info)

			gsups[p.Network] = []time.Duration{0, 0, math.MaxInt64}
		}
		info.Peers++
		if _, active := activePeers[p.PeerID]; active {
			info.Connecteds++
			nstat.TotalConnected++

			gsups[p.Network][0] = gsups[p.Network][0] + p.GraphStateDur
			if p.GraphStateDur > gsups[p.Network][1] {
				gsups[p.Network][1] = p.GraphStateDur
			}
			if p.GraphStateDur < gsups[p.Network][2] {
				gsups[p.Network][2] = p.GraphStateDur
			}
		}
		if p.Services&protocol.Relay > 0 {
			info.Relays++
		}
	}
	for k, gu := range gsups {
		info, ok := infos[k]
		if !ok {
			continue
		}
		if info.Connecteds > 0 {
			avegs := time.Duration(0)
			if info.Connecteds > 2 {
				avegs = gu[0] - gu[1] - gu[2]
				if avegs < 0 {
					avegs = 0
				}
				cons := info.Connecteds - 2
				avegs = time.Duration(int64(avegs) / int64(cons))

			} else {
				avegs = time.Duration(int64(gu[0]) / int64(info.Connecteds))
			}

			info.AverageGS = avegs.Truncate(time.Second).String()
			info.MaxGS = gu[1].Truncate(time.Second).String()
			info.MinGS = gu[2].Truncate(time.Second).String()
		}
	}
	return nstat, nil
}

type PrivateP2PAPI struct {
	s *Service
}

func NewPrivateP2PAPI(s *Service) *PrivateP2PAPI {
	return &PrivateP2PAPI{s}
}

func (api *PrivateP2PAPI) AddPeer(qmaddr string) (any, error) {
	err := api.s.ConnectToPeer(qmaddr, true)
	if err != nil {
		return false, err
	}
	return true, nil
}

func (api *PrivateP2PAPI) DelPeer(pid string) (any, error) {
	peid, err := peer.Decode(pid)
	if err != nil {
		return false, err
	}

	pe := api.s.Peers().Get(peid)
	if pe == nil {
		return false, fmt.Errorf("No peer:%s", peid.String())
	}
	api.s.PeerSync().TryDisconnect(pe)
	return true, nil
}

func (api *PrivateP2PAPI) Ping(addr string, port uint, protocol string) (any, error) {
	if len(protocol) <= 0 {
		protocol = "tcp"
	}
	if port == 0 {
		port = api.s.cfg.TCPPort
	}
	return verifyConnectivity(addr, port, protocol)
}

func (api *PrivateP2PAPI) Pause() (any, error) {
	return api.s.PeerSync().Pause(), nil
}

func (api *PrivateP2PAPI) ResetPeers() (any, error) {
	for _, pe := range api.s.Peers().AllPeers() {
		if !api.s.Peers().IsActive(pe) {
			continue
		}
		api.s.PeerSync().TryDisconnect(pe)
	}
	<-time.After(time.Second)
	trynum := 0
	for _, pe := range api.s.Peers().AllPeers() {
		qa := pe.QAddress()
		if qa == nil {
			continue
		}
		err := api.s.ConnectToPeer(qa.String(), true)
		if err != nil {
			log.Error(err.Error())
		} else {
			trynum++
		}
	}

	bootstrap := api.s.cfg.BootstrapNodeAddr
	bootstrap = append(bootstrap, api.s.cfg.StaticPeers...)
	for _, qa := range bootstrap {
		err := api.s.ConnectToPeer(qa, true)
		if err != nil {
			log.Error(err.Error())
		} else {
			trynum++
		}
	}
	return trynum, nil
}

func (api *PrivateP2PAPI) SetLibp2pLogLevel(level string) (any, error) {
	if len(level) <= 0 {
		return "DEBUG, INFO, WARN, ERROR, DPANIC, PANIC, FATAL", nil
	}
	l, err := golog.LevelFromString(level)
	if err != nil {
		log.Error(err.Error())
		return level, err
	}
	golog.SetAllLoggers(l)
	return level, nil
}

// Banlist
func (api *PrivateP2PAPI) Banlist() (any, error) {
	bl := api.s.GetBanlist()
	bls := []*json.GetBanlistResult{}
	for k, v := range bl {
		bls = append(bls, &json.GetBanlistResult{PeerID: k.String(), Bads: v})
	}
	return bls, nil
}

// RemoveBan
func (api *PrivateP2PAPI) RemoveBan(id *string) (any, error) {
	ho := ""
	if id != nil {
		ho = *id
	}
	api.s.RemoveBan(ho)
	return true, nil
}

func (api *PrivateP2PAPI) CheckConsistency(hashOrNumber string) (any, error) {
	hn, err := protocol.NewHashOrNumber(hashOrNumber)
	if err != nil {
		log.Warn("Will use the default block", "error", err.Error())
		return api.s.sy.CheckConsistency(nil)
	}
	return api.s.sy.CheckConsistency(hn)
}

func (api *PrivateP2PAPI) DisableRelayTx(dis bool) (any, error) {
	api.s.cfg.DisableRelayTx = dis
	return api.s.cfg.DisableRelayTx, nil
}
