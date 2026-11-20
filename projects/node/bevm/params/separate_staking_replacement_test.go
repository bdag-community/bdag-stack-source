package params

import (
	"math/big"
	"testing"

	"github.com/ethereum/go-ethereum/common"
)

// This release replaces a future, unstarted testnet schedule. All transition
// bytecode, the EVM floor and the threshold/delay/notice rules stay unchanged.
func TestReplacementSeparateStakingScheduleRetainsSafetyGates(t *testing.T) {
	p := BdagSeparateStakingFork
	if p == nil || BdagSeparateStakingForkBlock == nil {
		t.Fatal("replacement schedule is missing")
	}
	if p.SignalStartLayer != 562000 {
		t.Fatalf("replacement native signal start = %d, want 562000", p.SignalStartLayer)
	}
	if p.ActivationLayer != p.SignalStartLayer+3999 || BdagSeparateStakingForkBlock.Cmp(big.NewInt(620000)) != 0 {
		t.Fatal("replacement changed the EVM floor or native window/delay")
	}
	if p.NoticePublishedAt <= 1793386800 || p.ReleaseDistributedAt > p.NoticePublishedAt || p.NotBeforeTimestamp-p.NoticePublishedAt < 86400 {
		t.Fatal("replacement must publish its own distribution and full notice")
	}
	if p.ArtifactSHA256 != common.HexToHash("0x3e93657b71f9cdf3562a61c963541e9a41417c6a1ee98e6e1933ef1acedf7436") {
		t.Fatal("replacement changed the reviewed contract artifact")
	}
	if err := p.Validate(big.NewInt(1043)); err != nil {
		t.Fatal(err)
	}
	if err := p.Validate(big.NewInt(1404)); err == nil {
		t.Fatal("replacement schedule must not authorize Mainnet")
	}
}
