package synch

// MsgMemPool implements the Message interface and represents a bitcoin mempool
// message.  It is used to request a list of transactions still in the active
// memory pool of a relay.
//
// This message has no wire payload. InventoryCountHint is local-only context
// passed from the request handler to avoid recounting txpool contents.
type MsgMemPool struct {
	InventoryCountHint int
}

// MaxPayloadLength returns the maximum length the payload can be for the
// receiver.  This is part of the Message interface implementation.
func (msg *MsgMemPool) MaxPayloadLength(pver uint32) uint32 {
	return 0
}
