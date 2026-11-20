package bdag

import (
	"errors"

	mparams "github.com/BlockdagNetworkLabs/bdag/bevm/params"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/crypto"
)

// ErrBlockedAddress is returned when a transaction involves an address present
// in the on-chain wallet blocklist while the WalletBlocklist upgrade is
// enforced.
var ErrBlockedAddress = errors.New("address is blocked by on-chain governance blocklist")

// ErrRestrictedAddress is returned when a restricted sender attempts to send to
// a destination not on its governance-approved whitelist while the
// SelectiveBlocklist upgrade is enforced.
var ErrRestrictedAddress = errors.New("sender is restricted: destination not in allowed-to whitelist")

type blocklistStateReader interface {
	GetState(common.Address, common.Hash) common.Hash
}

// blockedSlot computes the storage slot of blocked[addr] in the
// BlocklistGovernance contract: keccak256(abi.encode(addr, SlotBlockedBase)).
func blockedSlot(addr common.Address) common.Hash {
	return crypto.Keccak256Hash(
		common.LeftPadBytes(addr.Bytes(), 32),
		mparams.SlotBlockedBase.Bytes(),
	)
}

// restrictedSlot computes the storage slot of restricted[addr] in the
// SelectiveBlocklist contract: keccak256(abi.encode(addr, SlotRestrictedBase)).
func restrictedSlot(addr common.Address) common.Hash {
	return crypto.Keccak256Hash(
		common.LeftPadBytes(addr.Bytes(), 32),
		mparams.SlotRestrictedBase.Bytes(),
	)
}

// allowedToSlot computes the storage slot of allowedTo[from][to] in the
// SelectiveBlocklist contract. The nested mapping resolves in two steps:
//
//	level1 = keccak256(abi.encode(from, SlotAllowedToBase))
//	slot   = keccak256(abi.encode(to,   level1))
func allowedToSlot(from, to common.Address) common.Hash {
	level1 := crypto.Keccak256Hash(
		common.LeftPadBytes(from.Bytes(), 32),
		mparams.SlotAllowedToBase.Bytes(),
	)
	return crypto.Keccak256Hash(
		common.LeftPadBytes(to.Bytes(), 32),
		level1.Bytes(),
	)
}

// IsAddressBlocked reports whether addr is marked as blocked in the
// BlocklistGovernance contract's storage. A missing contract or unset slot
// reads as zero, i.e. "not blocked".
func IsAddressBlocked(statedb blocklistStateReader, addr common.Address) bool {
	if statedb == nil {
		return false
	}
	v := statedb.GetState(mparams.BlocklistContractAddr, blockedSlot(addr))
	return v != (common.Hash{})
}

// IsAddressRestricted reports whether addr is marked as restricted in the
// SelectiveBlocklist contract's storage. Restricted addresses may only send to
// destinations approved via IsAllowedTo. Returns false if the contract is not
// yet deployed.
func IsAddressRestricted(statedb blocklistStateReader, addr common.Address) bool {
	if statedb == nil {
		return false
	}
	v := statedb.GetState(mparams.SelectiveBlocklistContractAddr, restrictedSlot(addr))
	return v != (common.Hash{})
}

// IsAllowedTo reports whether the SelectiveBlocklist contract has approved
// from→to as a permitted send direction.
func IsAllowedTo(statedb blocklistStateReader, from, to common.Address) bool {
	if statedb == nil {
		return false
	}
	v := statedb.GetState(mparams.SelectiveBlocklistContractAddr, allowedToSlot(from, to))
	return v != (common.Hash{})
}
