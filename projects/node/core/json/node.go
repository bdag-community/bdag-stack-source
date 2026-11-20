package json

// for pow diff
type PowDiff struct {
	CurrentDiff float64 `json:"current_diff"`
	PowType     string  `json:"pow_type,omitempty"`
}

// InfoNodeResult models the data returned by the node server getnodeinfo command.
type InfoNodeResult struct {
	ID                  string                              `json:"ID"`
	Addresss            []string                            `json:"address"`
	QNR                 string                              `json:"QNR,omitempty"`
	Version             int32                               `json:"version"`
	BuildVersion        string                              `json:"buildversion"`
	ProtocolVersion     int32                               `json:"protocolversion"`
	TotalSubsidy        uint64                              `json:"totalsubsidy,omitempty"`
	StateRoot           string                              `json:"stateroot,omitempty"`
	GraphState          *GetGraphStateResult                `json:"graphstate,omitempty"`
	TimeOffset          int64                               `json:"timeoffset,omitempty"`
	PowDiff             *PowDiff                            `json:"pow_diff,omitempty"`
	Confirmations       int32                               `json:"confirmations,omitempty"`
	CoinbaseMaturity    int32                               `json:"coinbasematurity,omitempty"`
	Errors              string                              `json:"errors,omitempty"`
	Modules             []string                            `json:"modules,omitempty"`
	DNS                 string                              `json:"dns,omitempty"`
	ConsensusDeployment map[string]*ConsensusDeploymentDesc `json:"consensusdeployment,omitempty"`
	Network             string                              `json:"network"`
	Connections         int32                               `json:"connections"`
}

// GetPeerInfoResult models the data returned from the getpeerinfo command.
type GetPeerInfoResult struct {
	ID             string               `json:"id"`
	QNR            string               `json:"qnr,omitempty"`
	Address        string               `json:"address"`
	State          bool                 `json:"state,omitempty"`
	Active         bool                 `json:"active,omitempty"`
	Protocol       uint32               `json:"protocol,omitempty"`
	Genesis        string               `json:"genesis,omitempty"`
	Services       string               `json:"services,omitempty"`
	Name           string               `json:"name,omitempty"`
	Direction      string               `json:"direction,omitempty"`
	StateRoot      string               `json:"stateroot,omitempty"`
	GraphState     *GetGraphStateResult `json:"graphstate,omitempty"`
	GSUpdate       string               `json:"gsupdate,omitempty"`
	SyncNode       bool                 `json:"syncnode,omitempty"`
	TimeOffset     int64                `json:"timeoffset"`
	LastSend       string               `json:"lastsend,omitempty"`
	LastRecv       string               `json:"lastrecv,omitempty"`
	Latency        string               `json:"latency,omitempty"`
	LatencyMS      int64                `json:"latency_ms,omitempty"`
	BytesSent      uint64               `json:"bytessent,omitempty"`
	BytesRecv      uint64               `json:"bytesrecv,omitempty"`
	ConnTime       string               `json:"conntime,omitempty"`
	Version        string               `json:"version,omitempty"`
	Network        string               `json:"network,omitempty"`
	Circuit        bool                 `json:"circuit,omitempty"`
	ReConnect      uint64               `json:"reconnect,omitempty"`
	Bads           []string             `json:"bads,omitempty"`
	PeerSource     string               `json:"peer_source,omitempty"`
	PeerSourceHint string               `json:"peer_source_hint,omitempty"`
	SeedPeer       bool                 `json:"seed_peer,omitempty"`
	RecentPeer     bool                 `json:"recent_peer,omitempty"`
	MempoolReqTime string               `json:"mempoolreqtime,omitempty"`
	Tasks          int                  `json:"tasks,omitempty"`
	Broadcast      int                  `json:"broadcast,omitempty"`
	DAGPort        int                  `json:"dagport,omitempty"`
	EVMPort        int                  `json:"evmport,omitempty"`
	Fork           *PeerForkInfo        `json:"fork,omitempty"`

	ForkStatus              string `json:"fork_status,omitempty"`
	ForkConfidence          string `json:"fork_confidence,omitempty"`
	ForkReason              string `json:"fork_reason,omitempty"`
	PeerCompatibility       string `json:"peer_compatibility,omitempty"`
	CompatibilityReason     string `json:"peer_compatibility_reason,omitempty"`
	CompatibilityProofAge   int64  `json:"peer_compatibility_proof_age_seconds,omitempty"`
	CompatibilityProofFresh bool   `json:"peer_compatibility_proof_fresh,omitempty"`
	PeerUsefulness          string `json:"peer_usefulness,omitempty"`
	PeerUsefulnessReason    string `json:"peer_usefulness_reason,omitempty"`
	UsefulForTarget         bool   `json:"useful_for_target,omitempty"`
	DatasetStatus           string `json:"dataset_status,omitempty"`
	DatasetReason           string `json:"dataset_reason,omitempty"`
	DatasetProof            string `json:"dataset_proof,omitempty"`
	SameNativeFork          *bool  `json:"same_native_fork,omitempty"`
	ForkLastChecked         string `json:"fork_last_checked,omitempty"`
	ForkLastError           string `json:"fork_last_error,omitempty"`
	PeerMainOrder           uint64 `json:"peer_main_order,omitempty"`
	PeerMainHeight          uint64 `json:"peer_main_height,omitempty"`
	PeerLayer               uint64 `json:"peer_layer,omitempty"`
	PeerTotal               uint64 `json:"peer_total,omitempty"`
	PeerMainTipHash         string `json:"peer_main_tip_hash,omitempty"`
	LocalMainOrder          uint64 `json:"local_main_order,omitempty"`
	LocalMainTipHash        string `json:"local_main_tip_hash,omitempty"`
	SyncPointHash           string `json:"sync_point_hash,omitempty"`
	DivergenceOrder         uint64 `json:"divergence_order,omitempty"`
	CommitmentStatus        string `json:"commitment_status,omitempty"`
	SampledBlockHash        string `json:"sampled_block_hash,omitempty"`
	SampledBlockOrder       uint64 `json:"sampled_block_order,omitempty"`
	SampledStateRootGot     string `json:"sampled_state_root_got,omitempty"`
	SampledStateRootWant    string `json:"sampled_state_root_want,omitempty"`
	EVMCheckpointNumber     uint64 `json:"evm_checkpoint_number,omitempty"`
	EVMCheckpointHash       string `json:"evm_checkpoint_hash,omitempty"`
	EVMCheckpointRoot       string `json:"evm_checkpoint_root,omitempty"`
}

type PeerForkInfo struct {
	ID             string `json:"id"`
	Address        string `json:"address,omitempty"`
	Name           string `json:"name,omitempty"`
	Active         bool   `json:"active,omitempty"`
	State          bool   `json:"state,omitempty"`
	Direction      string `json:"direction,omitempty"`
	Protocol       uint32 `json:"protocol,omitempty"`
	Services       string `json:"services,omitempty"`
	Version        string `json:"version,omitempty"`
	Network        string `json:"network,omitempty"`
	SyncNode       bool   `json:"syncnode,omitempty"`
	Latency        string `json:"latency,omitempty"`
	LatencyMS      int64  `json:"latency_ms,omitempty"`
	PeerSource     string `json:"peer_source,omitempty"`
	PeerSourceHint string `json:"peer_source_hint,omitempty"`
	SeedPeer       bool   `json:"seed_peer,omitempty"`
	RecentPeer     bool   `json:"recent_peer,omitempty"`

	ForkStatus              string `json:"fork_status"`
	ForkConfidence          string `json:"fork_confidence,omitempty"`
	ForkReason              string `json:"fork_reason,omitempty"`
	PeerCompatibility       string `json:"peer_compatibility,omitempty"`
	CompatibilityReason     string `json:"peer_compatibility_reason,omitempty"`
	CompatibilityProofAge   int64  `json:"peer_compatibility_proof_age_seconds,omitempty"`
	CompatibilityProofFresh bool   `json:"peer_compatibility_proof_fresh,omitempty"`
	PeerUsefulness          string `json:"peer_usefulness,omitempty"`
	PeerUsefulnessReason    string `json:"peer_usefulness_reason,omitempty"`
	UsefulForTarget         bool   `json:"useful_for_target,omitempty"`
	DatasetStatus           string `json:"dataset_status,omitempty"`
	DatasetReason           string `json:"dataset_reason,omitempty"`
	DatasetProof            string `json:"dataset_proof,omitempty"`
	SameNativeFork          *bool  `json:"same_native_fork,omitempty"`
	LastChecked             string `json:"last_checked,omitempty"`
	LastError               string `json:"last_error,omitempty"`

	PeerMainOrder   uint64   `json:"peer_main_order,omitempty"`
	PeerMainHeight  uint64   `json:"peer_main_height,omitempty"`
	PeerLayer       uint64   `json:"peer_layer,omitempty"`
	PeerTotal       uint64   `json:"peer_total,omitempty"`
	PeerMainTipHash string   `json:"peer_main_tip_hash,omitempty"`
	PeerTips        []string `json:"peer_tips,omitempty"`

	LocalMainOrder   uint64 `json:"local_main_order,omitempty"`
	LocalMainTipHash string `json:"local_main_tip_hash,omitempty"`
	SyncPointHash    string `json:"sync_point_hash,omitempty"`
	DivergenceOrder  uint64 `json:"divergence_order,omitempty"`

	CommitmentStatus    string `json:"commitment_status,omitempty"`
	EVMCheckpointNumber uint64 `json:"evm_checkpoint_number,omitempty"`
	EVMCheckpointHash   string `json:"evm_checkpoint_hash,omitempty"`
	EVMCheckpointRoot   string `json:"evm_checkpoint_root,omitempty"`

	SampledBlockHash     string `json:"sampled_block_hash,omitempty"`
	SampledBlockOrder    uint64 `json:"sampled_block_order,omitempty"`
	SampledStateRootGot  string `json:"sampled_state_root_got,omitempty"`
	SampledStateRootWant string `json:"sampled_state_root_want,omitempty"`
}

// PeerDiscoveryStatusResult summarizes bounded discovery health and the local
// evidence used to classify connected consensus peers.
type PeerDiscoveryStatusResult struct {
	DiscoveryEnabled       bool   `json:"discovery_enabled"`
	StaticPeersOnly        bool   `json:"static_peers_only"`
	Namespace              string `json:"namespace,omitempty"`
	LiveConsensusPeers     int    `json:"live_consensus_peers"`
	TargetLivePeers        int    `json:"target_live_peers"`
	LivePeerStarved        bool   `json:"live_peer_starved"`
	UsefulConsensusPeers   int    `json:"useful_consensus_peers"`
	TargetUsefulPeers      int    `json:"target_useful_peers"`
	UsefulPeerStarved      bool   `json:"useful_peer_starved"`
	PeerStarved            bool   `json:"peer_starved"`
	FindIntervalSeconds    int64  `json:"find_interval_seconds"`
	PeerStarvedReason      string `json:"peer_starved_reason,omitempty"`
	CompatibilityRule      string `json:"compatibility_rule,omitempty"`
	CompatibilityRuleNotes string `json:"compatibility_rule_notes,omitempty"`

	SourceTotalPeers     int                          `json:"source_total_peers"`
	SeedPeers            int                          `json:"seed_peers"`
	NonSeedPeers         int                          `json:"non_seed_peers"`
	IsolatedToSeeds      bool                         `json:"isolated_to_seeds"`
	RecentPeers          int                          `json:"recent_peers"`
	DiscoveredPeers      int                          `json:"discovered_peers"`
	PeerstorePeers       int                          `json:"peerstore_peers"`
	InboundPeers         int                          `json:"inbound_peers"`
	UnknownSourcePeers   int                          `json:"unknown_source_peers"`
	CompatibilityTotal   int                          `json:"compatibility_total_peers"`
	CompatiblePeers      int                          `json:"compatible_peers"`
	UnknownCompatibility int                          `json:"unknown_compatibility_peers"`
	StalePeers           int                          `json:"stale_peers"`
	WrongForkPeers       int                          `json:"wrong_fork_peers"`
	WrongDatasetPeers    int                          `json:"wrong_dataset_peers"`
	UnsafePeers          int                          `json:"unsafe_peers"`
	UsefulnessTotal      int                          `json:"usefulness_total_peers"`
	UsefulForTargetPeers int                          `json:"useful_for_target_peers"`
	PreferredPeers       int                          `json:"sync_mining_preferred_peers"`
	UsablePeers          int                          `json:"sync_mining_usable_peers"`
	NonFreshUsablePeers  int                          `json:"non_fresh_usable_peers"`
	RelayOnlyPeers       int                          `json:"discovery_relay_only_peers"`
	DeprioritizedPeers   int                          `json:"deprioritized_peers"`
	AvoidPeers           int                          `json:"avoid_peers"`
	UnknownUsefulness    int                          `json:"unknown_usefulness_peers"`
	PeerEvidence         []*PeerDiscoveryPeerEvidence `json:"peer_evidence,omitempty"`
}

// PeerDiscoveryPeerEvidence is advisory, read-only evidence. It does not
// modify dialing, sync selection, mining readiness, consensus, or EVM checks.
type PeerDiscoveryPeerEvidence struct {
	ID                      string `json:"id"`
	Address                 string `json:"address,omitempty"`
	PeerSource              string `json:"peer_source,omitempty"`
	PeerSourceHint          string `json:"peer_source_hint,omitempty"`
	SeedPeer                bool   `json:"seed_peer,omitempty"`
	RecentPeer              bool   `json:"recent_peer,omitempty"`
	ForkStatus              string `json:"fork_status,omitempty"`
	ForkReason              string `json:"fork_reason,omitempty"`
	PeerCompatibility       string `json:"peer_compatibility,omitempty"`
	CompatibilityReason     string `json:"peer_compatibility_reason,omitempty"`
	CompatibilityProofAge   int64  `json:"peer_compatibility_proof_age_seconds,omitempty"`
	CompatibilityProofFresh bool   `json:"peer_compatibility_proof_fresh,omitempty"`
	PeerUsefulness          string `json:"peer_usefulness,omitempty"`
	PeerUsefulnessReason    string `json:"peer_usefulness_reason,omitempty"`
	UsefulForTarget         bool   `json:"useful_for_target"`
	DatasetStatus           string `json:"dataset_status,omitempty"`
	DatasetReason           string `json:"dataset_reason,omitempty"`
	DatasetProof            string `json:"dataset_proof,omitempty"`
	EVMCheckpointNumber     uint64 `json:"evm_checkpoint_number,omitempty"`
	EVMCheckpointHash       string `json:"evm_checkpoint_hash,omitempty"`
	EVMCheckpointRoot       string `json:"evm_checkpoint_root,omitempty"`
	SampledBlockOrder       uint64 `json:"sampled_block_order,omitempty"`
	SampledStateRootGot     string `json:"sampled_state_root_got,omitempty"`
	SampledStateRootWant    string `json:"sampled_state_root_want,omitempty"`
	PeerMainOrder           uint64 `json:"peer_main_order,omitempty"`
	LocalMainOrder          uint64 `json:"local_main_order,omitempty"`
}

// GetGraphStateResult data
type GetGraphStateResult struct {
	Tips       []string `json:"tips"`
	MainOrder  uint32   `json:"mainorder"`
	MainHeight uint32   `json:"mainheight"`
	Layer      uint32   `json:"layer"`
}

type GetBanlistResult struct {
	PeerID string         `json:"peerid"`
	Bads   []*BadResponse `json:"bads"`
}

type BadResponse struct {
	ID    uint64 `json:"id"`
	Time  string `json:"time"`
	Error string `json:"error"`
}

type ConsensusDeploymentDesc struct {
	Status      string `json:"status"`
	StartHeight int64  `json:"startHeight,omitempty"`
}

type NetworkStat struct {
	TotalPeers     int            `json:"totalpeers"`
	MaxConnected   uint           `json:"maxconnected"`
	MaxInbound     int            `json:"maxinbound"`
	TotalConnected int            `json:"totalconnected"`
	TotalRelays    int            `json:"totalrelays"`
	Infos          []*NetworkInfo `json:"infos"`
}

type NetworkInfo struct {
	Name       string `json:"name"`
	Peers      int    `json:"peers"`
	Relays     int    `json:"relays"`
	Connecteds int    `json:"connecteds"`
	AverageGS  string `json:"averagegs,omitempty"`
	MaxGS      string `json:"maxgs,omitempty"`
	MinGS      string `json:"mings,omitempty"`
}

type SubsidyInfo struct {
	Mode               string `json:"mode"`
	TotalSubsidy       uint64 `json:"totalsubsidy"`
	TargetTotalSubsidy int64  `json:"targettotalsubsidy,omitempty"`
	LeftTotalSubsidy   int64  `json:"lefttotalsubsidy,omitempty"`
	TotalTime          string `json:"totalTime,omitempty"`
	LeftTotalTime      string `json:"lefttotalTime,omitempty"`
	BaseSubsidy        int64  `json:"basesubsidy"`
	NextSubsidy        int64  `json:"nextsubsidy"`
}
type BDAGInfoResult struct {
	Name                 string `json:"name"`
	Total                uint   `json:"total"`
	BlockCacheSize       uint64 `json:"bcachesize"`
	BlockCacheHeightSize uint64 `json:"bcacheheightsize"`
	BlockCacheRate       string `json:"bcacherate"`
	BlockDataCacheSize   string `json:"bdcachesize"`
	AnticoneSize         int    `json:"anticonesize"`
}

type ChainInfoResult struct {
	Count            uint64  `json:"count"`
	Start            string  `json:"start"`
	End              string  `json:"end"`
	BlocksPerSecond  float64 `json:"blockspersecond"`
	TxsPerSecond     float64 `json:"txspersecond"`
	SecondPerHeight  string  `json:"secondperheight"`
	Concurrency      float64 `json:"concurrency"`
	EmptyBlockRate   string  `json:"emptyblockrate"`
	ProcessQueueSize int32   `json:"processqueuesize"`
}
