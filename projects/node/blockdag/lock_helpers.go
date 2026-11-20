package Blockdag

import "sync"

// peekThenLoad implements the "try a cheap read-locked lookup first; only
// pay for the exclusive lock if that lookup couldn't produce a definitive
// answer" pattern used by several hot BDAG lookups (GetBlockById,
// GetMainChainTip, and others in this session's stateLock contention fixes
// -- see 0b4ead2/26718da).
//
// peek must be safe to call while holding only rw.RLock(): it must never
// mutate state, and must return ok=false only when the answer is genuinely
// ambiguous from the read-locked view (e.g. a value that may exist but
// isn't cache-resident) -- not for "definitely absent", which should be a
// real, ok=true zero value instead. Conflating those two cases here would
// propagate incorrectly into every caller.
//
// load is the original, always-correct implementation. It may mutate state
// (e.g. populate a cache from disk), so it always runs under rw.Lock(), and
// must itself re-check anything peek already looked at, since another
// goroutine can run between the RUnlock and the Lock.
func peekThenLoad[T any](rw *sync.RWMutex, peek func() (T, bool), load func() T) T {
	rw.RLock()
	v, ok := peek()
	rw.RUnlock()
	if ok {
		return v
	}

	rw.Lock()
	defer rw.Unlock()
	return load()
}
