# Per-transaction GBT fee identity (#655)

The existing pending-block builder stores the negative aggregate fee sentinel at
Fees[0], followed by one fee per actual EVM transaction in block order. The EVM
transaction list contains no coinbase transaction. The getter previously used the
sentinel for transaction zero and shifted later transaction fees.

The getter now uses Fees[i+1] and rejects a nonempty template whose fee layout
does not contain exactly one sentinel plus one slot per transaction. Empty
transaction responses retain existing compatibility. Fee arithmetic, aggregate
BlockFees, rewards, transaction identity/RLP/order, header roots, JSON tags,
weight, depends, capabilities and authorization are unchanged.

Golden vectors exercise empty/single/multiple/zero-fee/reordered transactions,
wire fees and whole-response sentinel independence. Malformed nil/empty/short/
extra layouts fail without a partial response and recover on a valid template.
A temporary private-network genesis fixture admits a generated synthetic pending
transaction through the actual EVM pool and block builder into the actual getter;
fixture keys stay in memory. No live peer, operational wallet or signing custody
is used.

Qualification is separate from protected-target review, full Core/security/
history/network/quality and final release acceptance. System51 #655/#564/#49;
the original A1-A3 project remains open.
