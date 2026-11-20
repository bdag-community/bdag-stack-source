package bdag

import (
	"context"
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"sync"
	"syscall"
	"time"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/rpc"
)

// Replay may execute a whole block. Keep one bounded lane, and expose it only
// over the local IPC socket rather than the public HTTP or websocket APIs.
var poolRewardReplayLane = make(chan struct{}, 1)

type poolRewardIPCService struct{ mc *BdagChain }

// startPoolRewardIPC exposes only the reward method through an owner-only
// socket in an owner-only directory. An abandoned socket may be reclaimed;
// live sockets and other filesystem entries are never removed.
func startPoolRewardIPC(chain *BdagChain, path string) (func(), error) {
	if path == "" {
		return nil, nil
	}
	if !filepath.IsAbs(path) || filepath.Clean(path) != path || len(path) >= 104 {
		return nil, errors.New("invalid pool reward IPC path")
	}
	dir, err := os.Lstat(filepath.Dir(path))
	if err != nil || !dir.IsDir() || dir.Mode().Perm()&0077 != 0 {
		return nil, errors.New("pool reward IPC parent must be a private directory")
	}
	owner, ok := dir.Sys().(*syscall.Stat_t)
	if !ok || owner.Uid != uint32(os.Geteuid()) {
		return nil, errors.New("pool reward IPC directory owner differs")
	}
	// Serialize socket inspection and binding without creating another file.
	directory, err := os.Open(filepath.Dir(path))
	if err != nil {
		return nil, errors.New("pool reward IPC directory unavailable")
	}
	defer directory.Close()
	opened, err := directory.Stat()
	if err != nil || !os.SameFile(dir, opened) {
		return nil, errors.New("pool reward IPC directory changed")
	}
	if err = syscall.Flock(int(directory.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		return nil, errors.New("pool reward IPC startup already in progress")
	}
	defer syscall.Flock(int(directory.Fd()), syscall.LOCK_UN)
	if existing, statErr := os.Lstat(path); statErr == nil {
		entryOwner, valid := existing.Sys().(*syscall.Stat_t)
		if existing.Mode()&os.ModeSocket == 0 || !valid || entryOwner.Uid != uint32(os.Geteuid()) {
			return nil, errors.New("pool reward IPC path is already in use")
		}
		conn, dialErr := net.DialTimeout("unix", path, 200*time.Millisecond)
		if dialErr == nil {
			conn.Close()
			return nil, errors.New("pool reward IPC path is already in use")
		}
		if !errors.Is(dialErr, syscall.ECONNREFUSED) && !errors.Is(dialErr, os.ErrNotExist) {
			return nil, errors.New("pool reward IPC socket state is unknown")
		}
		current, currentErr := os.Lstat(path)
		if currentErr == nil {
			if !os.SameFile(existing, current) || current.Mode()&os.ModeSocket == 0 {
				return nil, errors.New("pool reward IPC socket changed")
			}
			if err = os.Remove(path); err != nil {
				return nil, errors.New("stale pool reward IPC socket could not be removed")
			}
		} else if !errors.Is(currentErr, os.ErrNotExist) {
			return nil, errors.New("pool reward IPC socket state is unknown")
		}
	} else if !errors.Is(statErr, os.ErrNotExist) {
		return nil, errors.New("pool reward IPC path is unavailable")
	}
	server := rpc.NewServer()
	if err = server.RegisterName("bdag", &poolRewardIPCService{mc: chain}); err != nil {
		return nil, err
	}
	listener, err := net.Listen("unix", path)
	if err != nil {
		server.Stop()
		return nil, fmt.Errorf("pool reward IPC listen: %w", err)
	}
	if err = os.Chmod(path, 0600); err != nil {
		listener.Close()
		server.Stop()
		return nil, err
	}
	go func() { _ = server.ServeListener(listener) }()
	var once sync.Once
	return func() {
		once.Do(func() {
			_ = listener.Close()
			server.Stop()
			// Wait until a replay already in progress has left the chain.
			poolRewardReplayLane <- struct{}{}
			<-poolRewardReplayLane
		})
	}, nil
}

type PoolRewardEvidence struct {
	NativeBlockHash common.Hash    `json:"native_block_hash"`
	NativeOrder     uint64         `json:"native_order"`
	NativeHeight    uint64         `json:"native_height"`
	EVMBlockHash    common.Hash    `json:"evm_block_hash"`
	EVMBlockNumber  uint64         `json:"evm_block_number"`
	EVMStateRoot    common.Hash    `json:"evm_state_root"`
	Wallet          common.Address `json:"wallet"`
	SubsidyWei      string         `json:"subsidy_wei"`
	OtherFeesWei    string         `json:"other_sender_fees_wei"`
	SelfPaidFeesWei string         `json:"self_paid_fees_wei"`
}

// GetPoolRewardEvidence replays the exact observed native and EVM block to
// identify wallet credits. It is not a finality, maturity, stake, or payment
// authorization. Call promptly while historical parent state is retained;
// independently recheck canonical identity and maturity before any payment.
func (api *poolRewardIPCService) GetPoolRewardEvidence(ctx context.Context, nativeHash common.Hash, wallet common.Address) (*PoolRewardEvidence, error) {
	if ctx == nil || ctx.Err() != nil || rpc.PeerInfoFromContext(ctx).Transport != "ipc" {
		return nil, errors.New("pool reward evidence requires local IPC")
	}
	select {
	case poolRewardReplayLane <- struct{}{}:
		defer func() { <-poolRewardReplayLane }()
	default:
		return nil, errors.New("pool reward replay is busy")
	}
	if api == nil || api.mc == nil || api.mc.consensus == nil || api.mc.consensus.BlockChain() == nil || api.mc.chain == nil || api.mc.chain.Ether() == nil || api.mc.chain.Ether().BlockChain() == nil {
		return nil, errors.New("pool reward chain unavailable")
	}
	nativeChain, ok := api.mc.consensus.BlockChain().(nativeRewardChain)
	if !ok {
		return nil, errors.New("native reward observation unavailable")
	}
	evidence, err := replayNativeReward(nativeChain, api.mc.chain.Ether().BlockChain(), nativeHash, wallet)
	if err != nil {
		return nil, err
	}
	if err = ctx.Err(); err != nil {
		return nil, err
	}
	return &PoolRewardEvidence{
		NativeBlockHash: evidence.Native.BlockHash, NativeOrder: evidence.Native.Order, NativeHeight: evidence.Native.Height,
		EVMBlockHash: evidence.EVM.BlockHash, EVMBlockNumber: evidence.EVM.BlockNumber, EVMStateRoot: evidence.EVM.StateRoot,
		Wallet: wallet, SubsidyWei: evidence.EVM.SubsidyWei.String(), OtherFeesWei: evidence.EVM.OtherSenderFeesWei.String(), SelfPaidFeesWei: evidence.EVM.SelfPaidFeesWei.String(),
	}, nil
}
