package rpc

import (
	"sync"

	// "github.com/BlockdagNetworkLabs/bdag/core/address"

	"github.com/ethereum/go-ethereum/common"
)

// wsClientFilter tracks relevant addresses for each websocket client for
// the `rescanblocks` extension. It is modified by the `loadtxfilter` command.
//
// NOTE: This extension was ported from github.com/decred/dcrd
type wsClientFilter struct {
	mu           sync.Mutex
	ethAddresses map[string]struct{}
}

type rescanKeys struct {
	addrs map[string]struct{}
}

// newWSClientFilter creates a new, empty wsClientFilter struct to be used
// for a websocket client.
//
// NOTE: This extension was ported from github.com/decred/dcrd
func newWSClientFilter(addresses []string) *wsClientFilter {
	filter := &wsClientFilter{
		ethAddresses: map[string]struct{}{},
	}

	for _, s := range addresses {
		filter.addAddressStr(s)
	}

	return filter
}

// addAddress adds an address to a wsClientFilter, treating it correctly based
// on the type of address passed as an argument.
//
// NOTE: This extension was ported from github.com/decred/dcrd
func (f *wsClientFilter) addAddress(a common.Address) {
	f.ethAddresses[a.Hex()] = struct{}{}
}

// addAddressStr parses an address from a string and then adds it to the
// wsClientFilter using addAddress.
//
// NOTE: This extension was ported from github.com/decred/dcrd
func (f *wsClientFilter) addAddressStr(s string) {
	// If address can't be decoded, no point in saving it since it should also
	// impossible to create the address from an inspected transaction output
	// script.
	a := common.HexToAddress(s)
	f.addAddress(a)
}

// existsAddress returns true if the passed address has been added to the
// wsClientFilter.
//
// NOTE: This extension was ported from github.com/decred/dcrd
func (f *wsClientFilter) existsAddress(a common.Address) bool {
	_, ok := f.ethAddresses[a.String()]
	return ok
}

// removeAddress removes the passed address, if it exists, from the
// wsClientFilter.
//
// NOTE: This extension was ported from github.com/decred/dcrd
func (f *wsClientFilter) removeAddress(a common.Address) {
	delete(f.ethAddresses, a.String())
}

// removeAddressStr parses an address from a string and then removes it from the
// wsClientFilter using removeAddress.
//
// NOTE: This extension was ported from github.com/decred/dcrd
func (f *wsClientFilter) removeAddressStr(s string) {
	a := common.HexToAddress(s)
	f.removeAddress(a)
}
