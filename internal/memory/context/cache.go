package context

import (
	"context"
	"sync"
	"time"
)

// Cache provides in-memory caching capabilities.
type Cache struct {
	items     map[string]*CacheItem
	mu        sync.RWMutex
	maxSize   int
	ttl       time.Duration
	stopCh    chan struct{}
	stopOnce  sync.Once
	startOnce sync.Once
	wg        sync.WaitGroup
}

// CacheItem represents a cache entry.
type CacheItem struct {
	Key        string
	Value      interface{}
	Expiration time.Time
}

// NewCache creates a new Cache.
// The cleanup goroutine is started automatically to prevent memory leaks.
func NewCache(maxSize int, ttl time.Duration) *Cache {
	cache := &Cache{
		items:   make(map[string]*CacheItem),
		maxSize: maxSize,
		ttl:     ttl,
		stopCh:  make(chan struct{}),
	}
	// Start cleanup goroutine automatically to prevent memory leaks
	cache.Start()
	return cache
}

// Start starts the cleanup goroutine.
// This method is idempotent - calling it multiple times has no additional effect.
func (c *Cache) Start() {
	c.startOnce.Do(func() {
		c.wg.Add(1)
		go func() {
			defer c.wg.Done()
			c.cleanupLoop()
		}()
	})
}

// Set stores a value in cache.
func (c *Cache) Set(ctx context.Context, key string, value interface{}) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	if len(c.items) >= c.maxSize {
		c.evictOldest()
	}

	expiration := time.Now().Add(c.ttl)
	c.items[key] = &CacheItem{
		Key:        key,
		Value:      value,
		Expiration: expiration,
	}

	return nil
}

// Get retrieves a value from cache.
func (c *Cache) Get(ctx context.Context, key string) (interface{}, bool) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	item, exists := c.items[key]
	if !exists || time.Now().After(item.Expiration) {
		return nil, false
	}
	return item.Value, true
}

// Delete removes a cache entry.
func (c *Cache) Delete(ctx context.Context, key string) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	delete(c.items, key)
	return nil
}

// Clear removes all entries.
func (c *Cache) Clear(ctx context.Context) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	c.items = make(map[string]*CacheItem)
	return nil
}

// Size returns the number of items.
func (c *Cache) Size() int {
	c.mu.RLock()
	defer c.mu.RUnlock()

	return len(c.items)
}

// evictOldest removes the oldest item.
func (c *Cache) evictOldest() {
	var oldest *CacheItem
	var oldestKey string

	for key, item := range c.items {
		if oldest == nil || item.Expiration.Before(oldest.Expiration) {
			oldest = item
			oldestKey = key
		}
	}

	if oldestKey != "" {
		delete(c.items, oldestKey)
	}
}

// cleanupLoop periodically removes expired items.
func (c *Cache) cleanupLoop() {
	interval := c.ttl / 2
	if interval < time.Millisecond {
		interval = time.Millisecond
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	for {
		select {
		case <-ticker.C:
			c.cleanup()
		case <-c.stopCh:
			return
		}
	}
}

// Stop stops the cleanup goroutine.
// This method is idempotent and safe to call multiple times.
// Subsequent calls after the first will be no-ops.
func (c *Cache) Stop() {
	c.stopOnce.Do(func() {
		close(c.stopCh)
	})
	c.wg.Wait()
}

// cleanup removes expired items.
func (c *Cache) cleanup() {
	c.mu.Lock()
	defer c.mu.Unlock()

	now := time.Now()
	for key, item := range c.items {
		if now.After(item.Expiration) {
			delete(c.items, key)
		}
	}
}

// CacheWithTTL creates a cache with custom TTL per item.
type CacheWithTTL struct {
	items     map[string]*CacheItem
	mu        sync.RWMutex
	maxSize   int
	stopCh    chan struct{}
	stopOnce  sync.Once
	startOnce sync.Once
	wg        sync.WaitGroup
}

// NewCacheWithTTL creates a new CacheWithTTL.
// The cleanup goroutine is started automatically to prevent memory leaks.
func NewCacheWithTTL(maxSize int) *CacheWithTTL {
	c := &CacheWithTTL{
		items:   make(map[string]*CacheItem),
		maxSize: maxSize,
		stopCh:  make(chan struct{}),
	}
	// Start cleanup goroutine automatically to prevent memory leaks
	c.Start()
	return c
}

// Start starts the cleanup goroutine.
// This method is idempotent - calling it multiple times has no additional effect.
func (c *CacheWithTTL) Start() {
	c.startOnce.Do(func() {
		c.wg.Add(1)
		go func() {
			defer c.wg.Done()
			c.cleanupLoop()
		}()
	})
}

// SetWithTTL stores a value with custom TTL.
func (c *CacheWithTTL) SetWithTTL(ctx context.Context, key string, value interface{}, ttl time.Duration) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	if len(c.items) >= c.maxSize {
		c.evictOldest()
	}

	expiration := time.Now().Add(ttl)
	c.items[key] = &CacheItem{
		Key:        key,
		Value:      value,
		Expiration: expiration,
	}

	return nil
}

// Get retrieves a value from cache.
func (c *CacheWithTTL) Get(ctx context.Context, key string) (interface{}, bool) {
	c.mu.RLock()
	item, exists := c.items[key]
	if !exists {
		c.mu.RUnlock()
		return nil, false
	}

	if time.Now().After(item.Expiration) {
		c.mu.RUnlock()
		return nil, false
	}

	c.mu.RUnlock()
	return item.Value, true
}

// Delete removes a cache entry.
func (c *CacheWithTTL) Delete(ctx context.Context, key string) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	delete(c.items, key)
	return nil
}

// Clear removes all entries.
func (c *CacheWithTTL) Clear(ctx context.Context) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	c.items = make(map[string]*CacheItem)
	return nil
}

// Size returns the number of items.
func (c *CacheWithTTL) Size() int {
	c.mu.RLock()
	defer c.mu.RUnlock()

	return len(c.items)
}

// evictOldest removes the oldest item.
func (c *CacheWithTTL) evictOldest() {
	var oldest *CacheItem
	var oldestKey string

	for key, item := range c.items {
		if oldest == nil || item.Expiration.Before(oldest.Expiration) {
			oldest = item
			oldestKey = key
		}
	}

	if oldestKey != "" {
		delete(c.items, oldestKey)
	}
}

// cleanupLoop periodically removes expired items.
func (c *CacheWithTTL) cleanupLoop() {
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ticker.C:
			c.cleanup()
		case <-c.stopCh:
			return
		}
	}
}

// cleanup removes expired items.
func (c *CacheWithTTL) cleanup() {
	c.mu.Lock()
	defer c.mu.Unlock()

	now := time.Now()
	for key, item := range c.items {
		if now.After(item.Expiration) {
			delete(c.items, key)
		}
	}
}

// Stop stops the cleanup goroutine.
// This method is idempotent and safe to call multiple times.
func (c *CacheWithTTL) Stop() {
	c.stopOnce.Do(func() {
		close(c.stopCh)
	})
	c.wg.Wait()
}

// LRU Cache implementation.
type LRUCache struct {
	items   map[string]*LRUItem
	head    *LRUItem
	tail    *LRUItem
	mu      sync.RWMutex
	maxSize int
	size    int
}

// LRUItem represents a cache item with doubly-linked list pointers.
type LRUItem struct {
	Key   string
	Value interface{}
	prev  *LRUItem
	next  *LRUItem
}

// NewLRUCache creates a new LRU Cache.
func NewLRUCache(maxSize int) *LRUCache {
	return &LRUCache{
		items:   make(map[string]*LRUItem),
		maxSize: maxSize,
	}
}

// Get retrieves a value and moves it to front.
func (c *LRUCache) Get(ctx context.Context, key string) (interface{}, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()

	item, exists := c.items[key]
	if !exists {
		return nil, false
	}

	c.moveToFront(item)
	return item.Value, true
}

// Set stores a value.
func (c *LRUCache) Set(ctx context.Context, key string, value interface{}) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	if item, exists := c.items[key]; exists {
		item.Value = value
		c.moveToFront(item)
		return nil
	}

	newItem := &LRUItem{
		Key:   key,
		Value: value,
	}

	c.items[key] = newItem
	c.addToFront(newItem)
	c.size++

	if c.size > c.maxSize {
		c.removeLeastRecentlyUsed()
	}

	return nil
}

// Delete removes a cache entry.
func (c *LRUCache) Delete(ctx context.Context, key string) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	item, exists := c.items[key]
	if !exists {
		return nil
	}

	c.remove(item)
	delete(c.items, key)
	c.size--

	return nil
}

// Size returns the number of items.
func (c *LRUCache) Size() int {
	c.mu.RLock()
	defer c.mu.RUnlock()

	return len(c.items)
}

func (c *LRUCache) addToFront(item *LRUItem) {
	item.prev = nil
	item.next = c.head

	if c.head != nil {
		c.head.prev = item
	}

	c.head = item

	if c.tail == nil {
		c.tail = item
	}
}

func (c *LRUCache) remove(item *LRUItem) {
	if item.prev != nil {
		item.prev.next = item.next
	} else {
		c.head = item.next
	}

	if item.next != nil {
		item.next.prev = item.prev
	} else {
		c.tail = item.prev
	}
}

func (c *LRUCache) moveToFront(item *LRUItem) {
	c.remove(item)
	c.addToFront(item)
}

func (c *LRUCache) removeLeastRecentlyUsed() {
	if c.tail == nil {
		return
	}

	oldTail := c.tail
	c.remove(oldTail)
	delete(c.items, oldTail.Key)
	c.size--
}
