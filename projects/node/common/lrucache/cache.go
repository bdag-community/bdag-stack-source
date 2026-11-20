package lrucache

import (
	"context"
	"errors"
	"runtime"
	"sync"
	"time"

	gocache "github.com/patrickmn/go-cache"
)

var (
	// Common errors
	ErrKeyNotFound = errors.New("key not found in cache")
	ErrCacheClosed = errors.New("cache is closed")
	ErrInvalidTTL  = errors.New("TTL must be positive")
	ErrInvalidSize = errors.New("max size must be positive")
	ErrKeyExists   = errors.New("key already exists")
)

// CacheConfig holds configuration for the cache
type CacheConfig struct {
	// MaxSize is the maximum number of entries in the cache (0 = unlimited)
	MaxSize int

	// DefaultTTL is the default time-to-live for cache entries
	DefaultTTL time.Duration

	// CleanupInterval determines how often expired items are removed
	CleanupInterval time.Duration

	// EvictionPolicy determines how items are evicted when cache is full
	EvictionPolicy EvictionPolicy

	// OnEvicted callback function called when an item is evicted
	OnEvicted func(key string, value interface{}, reason EvictionReason)

	// OnExpired callback function called when an item expires
	OnExpired func(key string, value interface{})

	// EnableMetrics enables collection of cache metrics
	EnableMetrics bool
}

// EvictionPolicy defines how items are evicted when cache reaches capacity
type EvictionPolicy int

const (
	LRU  EvictionPolicy = iota // Least Recently Used
	FIFO                       // First In, First Out
	LFU                        // Least Frequently Used
)

// EvictionReason indicates why an item was evicted
type EvictionReason int

const (
	Expired  EvictionReason = iota // Item expired naturally
	Evicted                        // Item evicted due to capacity
	Deleted                        // Item explicitly deleted
	Replaced                       // Item replaced by new value
)

// CacheMetrics holds cache performance metrics
type CacheMetrics struct {
	Hits        int64         `json:"hits"`
	Misses      int64         `json:"misses"`
	Sets        int64         `json:"sets"`
	Deletes     int64         `json:"deletes"`
	Evictions   int64         `json:"evictions"`
	Size        int           `json:"size"`
	MaxSize     int           `json:"max_size"`
	HitRate     float64       `json:"hit_rate"`
	MemoryUsage int64         `json:"memory_usage_bytes"`
	LastAccess  time.Time     `json:"last_access"`
	Uptime      time.Duration `json:"uptime"`
}

// cacheItem wraps cache values with metadata
type cacheItem[V any] struct {
	value       V
	accessCount int64
	lastAccess  time.Time
}

// Cache is a thread-safe, generic cache with configurable eviction policies
type Cache[V any] struct {
	mu        sync.RWMutex
	cache     *gocache.Cache
	config    CacheConfig
	keyOrder  []string                 // For FIFO/LRU eviction
	keyIndex  map[string]int           // Quick lookup for key position
	items     map[string]*cacheItem[V] // Additional metadata for items
	metrics   CacheMetrics
	closed    bool
	startTime time.Time
	ctx       context.Context
	cancel    context.CancelFunc
}

// NewCache creates a new cache with the given configuration
func NewCache[V any](config CacheConfig) (*Cache[V], error) {
	if config.MaxSize < 0 {
		return nil, ErrInvalidSize
	}
	if config.DefaultTTL <= 0 {
		return nil, ErrInvalidTTL
	}
	if config.CleanupInterval <= 0 {
		config.CleanupInterval = 5 * time.Minute // Default cleanup interval
	}

	ctx, cancel := context.WithCancel(context.Background())

	c := &Cache[V]{
		cache:     gocache.New(config.DefaultTTL, config.CleanupInterval),
		config:    config,
		keyOrder:  make([]string, 0),
		keyIndex:  make(map[string]int),
		items:     make(map[string]*cacheItem[V]),
		startTime: time.Now(),
		ctx:       ctx,
		cancel:    cancel,
	}

	if config.EnableMetrics {
		c.metrics = CacheMetrics{
			MaxSize:    config.MaxSize,
			LastAccess: time.Now(),
		}
	}

	// Set up expiration callback
	c.cache.OnEvicted(func(key string, value interface{}) {
		go func() {
			c.mu.Lock()
			defer c.mu.Unlock()

			if item, exists := c.items[key]; exists {
				delete(c.items, key)
				c.removeFromOrder(key)

				if c.config.EnableMetrics {
					c.metrics.Evictions++
				}

				if c.config.OnExpired != nil {
					go c.config.OnExpired(key, item.value)
				}
			}
		}()
	})

	return c, nil
}

// Set adds or updates an item in the cache with default TTL
func (c *Cache[V]) Set(key string, value V) error {
	return c.SetWithTTL(key, value, c.config.DefaultTTL)
}

// SetWithTTL adds or updates an item in the cache with custom TTL
func (c *Cache[V]) SetWithTTL(key string, value V, ttl time.Duration) error {
	if ttl <= 0 {
		return ErrInvalidTTL
	}

	c.mu.Lock()
	defer c.mu.Unlock()

	if c.closed {
		return ErrCacheClosed
	}

	// Check if we need to evict items
	if c.config.MaxSize > 0 {
		for len(c.keyOrder) >= c.config.MaxSize {
			if !c.evictOne() {
				break
			}
		}
	}

	// Update existing item
	if existingItem, exists := c.items[key]; exists {
		existingItem.value = value
		existingItem.lastAccess = time.Now()
		c.cache.Set(key, value, ttl)

		if c.config.EnableMetrics {
			c.metrics.Sets++
		}

		if c.config.OnEvicted != nil {
			go c.config.OnEvicted(key, existingItem.value, Replaced)
		}
		return nil
	}

	// Add new item
	item := &cacheItem[V]{
		value:       value,
		accessCount: 1,
		lastAccess:  time.Now(),
	}

	c.cache.Set(key, value, ttl)
	c.items[key] = item
	c.keyOrder = append(c.keyOrder, key)
	c.keyIndex[key] = len(c.keyOrder) - 1

	if c.config.EnableMetrics {
		c.metrics.Sets++
		c.metrics.Size = len(c.items)
		c.metrics.LastAccess = time.Now()
	}

	return nil
}

// SetIfNotExists adds an item only if the key doesn't already exist
func (c *Cache[V]) SetIfNotExists(key string, value V) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	if c.closed {
		return ErrCacheClosed
	}

	if _, exists := c.items[key]; exists {
		return ErrKeyExists
	}

	c.mu.Unlock()
	err := c.Set(key, value)
	c.mu.Lock()

	return err
}

// Get retrieves an item from the cache
func (c *Cache[V]) Get(key string) (V, error) {
	c.mu.Lock()
	defer c.mu.Unlock()

	var zero V
	if c.closed {
		return zero, ErrCacheClosed
	}

	if item, exists := c.items[key]; exists {
		// Check if item exists in underlying cache (not expired)
		if _, found := c.cache.Get(key); found {
			item.accessCount++
			item.lastAccess = time.Now()

			// Update position for LRU
			if c.config.EvictionPolicy == LRU {
				c.removeFromOrder(key)
				c.keyOrder = append(c.keyOrder, key)
				c.keyIndex[key] = len(c.keyOrder) - 1
			}

			if c.config.EnableMetrics {
				c.metrics.Hits++
				c.metrics.LastAccess = time.Now()
			}

			return item.value, nil
		}

		// Item expired, clean up
		delete(c.items, key)
		c.removeFromOrder(key)
	}

	if c.config.EnableMetrics {
		c.metrics.Misses++
	}

	return zero, ErrKeyNotFound
}

// GetWithoutUpdate retrieves an item without updating access metrics
func (c *Cache[V]) GetWithoutUpdate(key string) (V, error) {
	c.mu.RLock()
	defer c.mu.RUnlock()

	var zero V
	if c.closed {
		return zero, ErrCacheClosed
	}

	if item, exists := c.items[key]; exists {
		if _, found := c.cache.Get(key); found {
			return item.value, nil
		}
	}

	return zero, ErrKeyNotFound
}

// Delete removes an item from the cache
func (c *Cache[V]) Delete(key string) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	if c.closed {
		return ErrCacheClosed
	}

	if item, exists := c.items[key]; exists {
		c.cache.Delete(key)
		delete(c.items, key)
		c.removeFromOrder(key)

		if c.config.EnableMetrics {
			c.metrics.Deletes++
			c.metrics.Size = len(c.items)
		}

		if c.config.OnEvicted != nil {
			go c.config.OnEvicted(key, item.value, Deleted)
		}

		return nil
	}

	return ErrKeyNotFound
}

// Exists checks if a key exists in the cache
func (c *Cache[V]) Exists(key string) bool {
	c.mu.RLock()
	defer c.mu.RUnlock()

	if c.closed {
		return false
	}

	_, exists := c.items[key]
	if exists {
		// Verify it's not expired
		_, found := c.cache.Get(key)
		return found
	}
	return false
}

// Keys returns all keys currently in the cache
func (c *Cache[V]) Keys() []string {
	c.mu.RLock()
	defer c.mu.RUnlock()

	if c.closed {
		return nil
	}

	keys := make([]string, 0, len(c.items))
	for key := range c.items {
		if _, found := c.cache.Get(key); found {
			keys = append(keys, key)
		}
	}
	return keys
}

// Size returns the current number of items in the cache
func (c *Cache[V]) Size() int {
	c.mu.RLock()
	defer c.mu.RUnlock()

	if c.closed {
		return 0
	}

	return c.cache.ItemCount()
}

// Clear removes all items from the cache
func (c *Cache[V]) Clear() {
	c.mu.Lock()
	defer c.mu.Unlock()

	if c.closed {
		return
	}

	// Trigger callbacks for all items
	if c.config.OnEvicted != nil {
		for key, item := range c.items {
			go c.config.OnEvicted(key, item.value, Deleted)
		}
	}

	c.cache.Flush()
	c.items = make(map[string]*cacheItem[V])
	c.keyOrder = c.keyOrder[:0]
	c.keyIndex = make(map[string]int)

	if c.config.EnableMetrics {
		c.metrics.Size = 0
	}
}

// Cleanup forces cleanup of expired items
func (c *Cache[V]) Cleanup() {
	c.mu.Lock()
	defer c.mu.Unlock()

	if c.closed {
		return
	}

	c.cache.DeleteExpired()

	// Clean up our metadata for expired items
	for key, item := range c.items {
		if _, found := c.cache.Get(key); !found {
			delete(c.items, key)
			c.removeFromOrder(key)

			if c.config.OnExpired != nil {
				go c.config.OnExpired(key, item.value)
			}
		}
	}

	if c.config.EnableMetrics {
		c.metrics.Size = len(c.items)
	}
}

// GetMetrics returns current cache metrics
func (c *Cache[V]) GetMetrics() CacheMetrics {
	c.mu.RLock()
	defer c.mu.RUnlock()

	if !c.config.EnableMetrics {
		return CacheMetrics{}
	}

	metrics := c.metrics
	metrics.Size = len(c.items)
	metrics.Uptime = time.Since(c.startTime)

	if metrics.Hits+metrics.Misses > 0 {
		metrics.HitRate = float64(metrics.Hits) / float64(metrics.Hits+metrics.Misses)
	}

	// Estimate memory usage
	var m runtime.MemStats
	runtime.ReadMemStats(&m)
	metrics.MemoryUsage = int64(m.Alloc)

	return metrics
}

// Close gracefully shuts down the cache
func (c *Cache[V]) Close() error {
	c.mu.Lock()
	defer c.mu.Unlock()

	if c.closed {
		return ErrCacheClosed
	}

	c.closed = true
	c.cancel()

	// Trigger callbacks for remaining items
	if c.config.OnEvicted != nil {
		for key, item := range c.items {
			go c.config.OnEvicted(key, item.value, Deleted)
		}
	}

	c.Clear()
	return nil
}

// IsClosed returns whether the cache is closed
func (c *Cache[V]) IsClosed() bool {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.closed
}

// SetTTL updates the TTL for an existing key
func (c *Cache[V]) SetTTL(key string, ttl time.Duration) error {
	if ttl <= 0 {
		return ErrInvalidTTL
	}

	c.mu.Lock()
	defer c.mu.Unlock()

	if c.closed {
		return ErrCacheClosed
	}

	if item, exists := c.items[key]; exists {
		if _, found := c.cache.Get(key); found {
			c.cache.Set(key, item.value, ttl)
			return nil
		}
	}

	return ErrKeyNotFound
}

// GetWithTTL returns the value and remaining TTL for a key
func (c *Cache[V]) GetWithTTL(key string) (V, time.Duration, error) {
	c.mu.Lock()
	defer c.mu.Unlock()

	var zero V
	if c.closed {
		return zero, 0, ErrCacheClosed
	}

	if item, exists := c.items[key]; exists {
		if cacheItem, expiration, found := c.cache.GetWithExpiration(key); found {
			item.accessCount++
			item.lastAccess = time.Now()

			remaining := time.Until(expiration)
			if remaining < 0 {
				remaining = 0
			}

			if c.config.EnableMetrics {
				c.metrics.Hits++
				c.metrics.LastAccess = time.Now()
			}

			return cacheItem.(V), remaining, nil
		}
	}

	if c.config.EnableMetrics {
		c.metrics.Misses++
	}

	return zero, 0, ErrKeyNotFound
}

// evictOne removes one item according to the eviction policy
func (c *Cache[V]) evictOne() bool {
	if len(c.keyOrder) == 0 {
		return false
	}

	var keyToEvict string

	switch c.config.EvictionPolicy {
	case FIFO:
		keyToEvict = c.keyOrder[0]
	case LRU:
		// Find least recently used
		oldestTime := time.Now()
		for _, key := range c.keyOrder {
			if item, exists := c.items[key]; exists {
				if item.lastAccess.Before(oldestTime) {
					oldestTime = item.lastAccess
					keyToEvict = key
				}
			}
		}
	case LFU:
		// Find least frequently used
		minCount := int64(^uint64(0) >> 1) // max int64
		for _, key := range c.keyOrder {
			if item, exists := c.items[key]; exists {
				if item.accessCount < minCount {
					minCount = item.accessCount
					keyToEvict = key
				}
			}
		}
	}

	if keyToEvict != "" {
		if item, exists := c.items[keyToEvict]; exists {
			c.cache.Delete(keyToEvict)
			delete(c.items, keyToEvict)
			c.removeFromOrder(keyToEvict)

			if c.config.EnableMetrics {
				c.metrics.Evictions++
			}

			if c.config.OnEvicted != nil {
				go c.config.OnEvicted(keyToEvict, item.value, Evicted)
			}

			return true
		}
	}

	return false
}

// removeFromOrder removes a key from the order tracking
func (c *Cache[V]) removeFromOrder(key string) {
	if index, exists := c.keyIndex[key]; exists {
		// Remove from slice
		c.keyOrder = append(c.keyOrder[:index], c.keyOrder[index+1:]...)
		delete(c.keyIndex, key)

		// Update indices for remaining keys
		for i := index; i < len(c.keyOrder); i++ {
			c.keyIndex[c.keyOrder[i]] = i
		}
	}
}

// Helper function to create a default configuration
func DefaultConfig() CacheConfig {
	return CacheConfig{
		MaxSize:         10000,
		DefaultTTL:      5 * time.Minute,
		CleanupInterval: 10 * time.Minute,
		EvictionPolicy:  LRU,
		EnableMetrics:   true,
	}
}

// Helper function to create a configuration for high-performance scenarios
func HighPerformanceConfig() CacheConfig {
	return CacheConfig{
		MaxSize:         100000,
		DefaultTTL:      10 * time.Minute,
		CleanupInterval: 1 * time.Minute,
		EvictionPolicy:  LRU,
		EnableMetrics:   true,
	}
}
