package lrucache

import "github.com/ethereum/go-ethereum/common"

// LRUCache is a least-recently-used cache for any type
// that's able to be indexed by Hash
type LRUCache struct {
	cache    map[common.Hash]any
	capacity int
}

// New creates a new LRUCache
func New(capacity int, preallocate bool) *LRUCache {
	var cache map[common.Hash]any
	if preallocate {
		cache = make(map[common.Hash]any, capacity+1)
	} else {
		cache = make(map[common.Hash]any)
	}
	return &LRUCache{
		cache:    cache,
		capacity: capacity,
	}
}

// Add adds an entry to the LRUCache
func (c *LRUCache) Add(key *common.Hash, value any) {
	c.cache[*key] = value

	if len(c.cache) > c.capacity {
		c.evictRandom()
	}
}

// Get returns the entry for the given key, or (nil, false) otherwise
func (c *LRUCache) Get(key *common.Hash) (any, bool) {
	value, ok := c.cache[*key]
	if !ok {
		return nil, false
	}
	return value, true
}

// Has returns whether the LRUCache contains the given key
func (c *LRUCache) Has(key *common.Hash) bool {
	_, ok := c.cache[*key]
	return ok
}

// Remove removes the entry for the the given key. Does nothing if
// the entry does not exist
func (c *LRUCache) Remove(key *common.Hash) {
	delete(c.cache, *key)
}

func (c *LRUCache) evictRandom() {
	var keyToEvict common.Hash
	for key := range c.cache {
		keyToEvict = key
		break
	}
	c.Remove(&keyToEvict)
}
