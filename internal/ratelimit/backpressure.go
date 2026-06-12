package ratelimit

import (
	"context"
	"errors"
	"sync"
	"time"
)

// Backpressure provides backpressure mechanisms.
type Backpressure struct {
	queue      chan *BackpressureRequest
	mu         sync.RWMutex
	wg         sync.WaitGroup
	active     int
	maxActive  int
	queueSize  int
	dropPolicy DropPolicy
	metrics    *BackpressureMetrics
	cancelFunc context.CancelFunc
}

// BackpressureRequest represents a request in the queue.
type BackpressureRequest struct {
	Ctx    context.Context
	Key    string
	Weight int
	Result chan *BackpressureResult
}

// BackpressureResult represents the result of a backpressure request.
type BackpressureResult struct {
	Allowed bool
	Error   error
}

// DropPolicy defines how to handle queue overflow.
type DropPolicy string

const (
	DropPolicyReject  DropPolicy = "reject"
	DropPolicyDropOld DropPolicy = "drop_oldest"
	DropPolicyBlock   DropPolicy = "block"
)

// BackpressureMetrics holds backpressure statistics.
type BackpressureMetrics struct {
	Accepted     int64
	Rejected     int64
	Dropped      int64
	Queued       int64
	Processed    int64
	AvgQueueTime time.Duration
}

// NewBackpressure creates a new Backpressure instance.
func NewBackpressure(maxActive, queueSize int, dropPolicy DropPolicy) *Backpressure {
	bp := &Backpressure{
		queue:      make(chan *BackpressureRequest, queueSize),
		maxActive:  maxActive,
		queueSize:  queueSize,
		dropPolicy: dropPolicy,
		metrics:    &BackpressureMetrics{},
	}

	return bp
}

// Submit submits a request for processing.
func (bp *Backpressure) Submit(ctx context.Context, key string, weight int) (*BackpressureResult, error) {
	result := &BackpressureResult{
		Allowed: false,
	}

	bp.mu.Lock()

	if bp.active+weight <= bp.maxActive {
		bp.active += weight
		bp.metrics.Accepted++
		result.Allowed = true
		bp.mu.Unlock()

		return result, nil
	}

	bp.metrics.Queued++
	bp.mu.Unlock()

	resultChan := make(chan *BackpressureResult, 1)

	select {
	case bp.queue <- &BackpressureRequest{
		Ctx:    ctx,
		Key:    key,
		Weight: weight,
		Result: resultChan,
	}:
		// Wait for actual processing result
		select {
		case bpResult := <-resultChan:
			return bpResult, bpResult.Error
		case <-ctx.Done():
			bp.mu.Lock()
			bp.metrics.Rejected++
			bp.mu.Unlock()
			result.Error = ErrQueueFull
			return result, result.Error
		}
	case <-ctx.Done():
		bp.mu.Lock()
		bp.metrics.Rejected++
		bp.mu.Unlock()
		result.Error = ErrQueueFull
		return result, result.Error
	}
}

// Start starts the processing loop.
//
// NOTE: This method starts a goroutine to process requests. Use Stop() to gracefully
// shutdown the processing loop and wait for all requests to complete.
func (bp *Backpressure) Start(ctx context.Context, handler func(context.Context, string) error) {
	bp.mu.Lock()
	if bp.cancelFunc != nil {
		bp.mu.Unlock()
		return // Already started
	}

	// Create a cancellable context for the processing loop
	processCtx, cancel := context.WithCancel(ctx)
	bp.cancelFunc = cancel
	bp.mu.Unlock()

	bp.wg.Add(1)
	go func() {
		defer bp.wg.Done()
		bp.processLoop(processCtx, handler)
	}()

	numWorkers := bp.maxActive
	if numWorkers < 1 {
		numWorkers = 1
	}
	for w := 1; w < numWorkers; w++ {
		bp.wg.Add(1)
		go func() {
			defer bp.wg.Done()
			bp.processLoop(processCtx, handler)
		}()
	}
}

// Stop gracefully stops the processing loop and waits for it to exit.
func (bp *Backpressure) Stop() {
	bp.mu.Lock()
	if bp.cancelFunc != nil {
		bp.cancelFunc()
		bp.cancelFunc = nil
	}
	bp.mu.Unlock()
	bp.wg.Wait()
}

// processLoop processes queued requests.
func (bp *Backpressure) processLoop(ctx context.Context, handler func(context.Context, string) error) {
	for {
		select {
		case <-ctx.Done():
			return
		case req := <-bp.queue:
			bp.mu.Lock()
			bp.active += req.Weight
			bp.metrics.Processed++
			bp.mu.Unlock()

			err := handler(req.Ctx, req.Key)

			bp.mu.Lock()
			bp.active -= req.Weight
			bp.mu.Unlock()

			req.Result <- &BackpressureResult{
				Allowed: err == nil,
				Error:   err,
			}
		}
	}
}

// Metrics returns current metrics.
func (bp *Backpressure) Metrics() *BackpressureMetrics {
	bp.mu.RLock()
	defer bp.mu.RUnlock()

	metrics := *bp.metrics
	metrics.Queued = int64(len(bp.queue))
	return &metrics
}

// Reset resets the backpressure state.
//
// NOTE: This method drains all queued requests and returns an error result to each.
// Uses non-blocking send with select+default to avoid deadlock when Result channel
// is already full (e.g., if request was already processed by processLoop).
func (bp *Backpressure) Reset() {
	bp.mu.Lock()
	defer bp.mu.Unlock()

	bp.metrics = &BackpressureMetrics{}
	bp.active = 0

	// Drain queue with non-blocking send to avoid deadlock
drainLoop:
	for len(bp.queue) > 0 {
		select {
		case req := <-bp.queue:
			// Non-blocking send: if Result channel is already full (request already processed),
			// skip this send to avoid deadlock. The caller will get the original result.
			select {
			case req.Result <- &BackpressureResult{
				Allowed: false,
				Error:   ErrReset,
			}:
			default:
				// Channel already full, request already processed, skip send
			}
		default:
			// No more items in queue
			break drainLoop
		}
	}
}

// Active returns the number of active requests.
func (bp *Backpressure) Active() int {
	bp.mu.RLock()
	defer bp.mu.RUnlock()

	return bp.active
}

// Queued returns the number of queued requests.
func (bp *Backpressure) Queued() int {
	return len(bp.queue)
}

// Backpressure errors.
var (
	ErrQueueFull = errors.New("queue full")
	ErrReset     = errors.New("backpressure reset")
)

// AdaptiveLimiter adjusts rate based on current load.
type AdaptiveLimiter struct {
	limiter        *TokenBucketLimiter
	mu             sync.RWMutex
	minRate        float64
	maxRate        float64
	currentRate    float64
	decreaseFactor float64
	increaseFactor float64
}

// NewAdaptiveLimiter creates a new AdaptiveLimiter.
func NewAdaptiveLimiter(base Limiter, minRate, maxRate float64) *AdaptiveLimiter {
	if base == nil {
		panic("base limiter cannot be nil")
	}
	rate := base.Rate()
	burst := int(rate)

	config := &LimiterConfig{
		Rate:    rate,
		Burst:   burst,
		Timeout: 30 * time.Second,
	}

	return &AdaptiveLimiter{
		limiter:        NewTokenBucketLimiter(config),
		minRate:        minRate,
		maxRate:        maxRate,
		currentRate:    rate,
		decreaseFactor: 0.9,
		increaseFactor: 1.1,
	}
}

// Allow checks if request is allowed.
func (a *AdaptiveLimiter) Allow(ctx context.Context) (bool, error) {
	a.mu.RLock()
	limiter := a.limiter
	a.mu.RUnlock()
	return limiter.Allow(ctx)
}

// Wait blocks until request can be processed.
func (a *AdaptiveLimiter) Wait(ctx context.Context) error {
	a.mu.RLock()
	limiter := a.limiter
	a.mu.RUnlock()
	return limiter.Wait(ctx)
}

// Reset resets the limiter.
func (a *AdaptiveLimiter) Reset() {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.limiter.Reset()
	a.currentRate = a.maxRate
}

// Rate returns current rate.
func (a *AdaptiveLimiter) Rate() float64 {
	a.mu.RLock()
	defer a.mu.RUnlock()
	return a.currentRate
}

// Increase increases the rate.
func (a *AdaptiveLimiter) Increase() {
	a.mu.Lock()
	defer a.mu.Unlock()

	a.currentRate *= a.increaseFactor
	if a.currentRate > a.maxRate {
		a.currentRate = a.maxRate
	}

	if a.limiter != nil {
		a.limiter.SetRate(a.currentRate)
	}
}

// Decrease decreases the rate.
func (a *AdaptiveLimiter) Decrease() {
	a.mu.Lock()
	defer a.mu.Unlock()

	a.currentRate *= a.decreaseFactor
	if a.currentRate < a.minRate {
		a.currentRate = a.minRate
	}

	if a.limiter != nil {
		a.limiter.SetRate(a.currentRate)
	}
}
