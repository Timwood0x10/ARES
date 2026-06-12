# RateLimiter 设计文档

## 1. 概述

RateLimiter 模块提供限流和背压机制，用于控制请求速率、防止系统过载。

## 2. 限流策略

| 策略 | 适用场景 | 实现方式 |
|------|----------|----------|
| 令牌桶 | LLM 请求限流 | 平滑速率 |
| 滑动窗口 | 全局 QPS 控制 | 精确计数 |
| 信号量 | Agent 并发控制 | Channel-based |
| 加权信号量 | 资源权重控制 | 条件变量 |

## 3. 核心接口

```go
type Limiter interface {
    // Allow 检查是否允许通过
    Allow(ctx context.Context) (bool, error)
    
    // Wait 等待获取令牌
    Wait(ctx context.Context) error
    
    // Reset 重置限流器
    Reset()
    
    // Rate 返回当前速率
    Rate() float64
}
```

## 4. 限流器实现

### 4.1 令牌桶 (TokenBucketLimiter)

```go
type TokenBucketLimiter struct {
    tokens     float64
    maxTokens  float64
    rate       float64
    lastCheck  time.Time
    mu         sync.Mutex
    config     *LimiterConfig
}

// 核心方法
func (l *TokenBucketLimiter) Allow(ctx context.Context) (bool, error)
func (l *TokenBucketLimiter) Wait(ctx context.Context) error
func (l *TokenBucketLimiter) AvailableTokens() float64
func (l *TokenBucketLimiter) SetRate(rate float64)
func (l *TokenBucketLimiter) SetBurst(burst int)
```

### 4.2 滑动窗口 (SlidingWindowLimiter)

```go
type SlidingWindowLimiter struct {
    requests     []time.Time
    windowSize   time.Duration
    maxRequests  int
    mu           sync.Mutex
    config       *LimiterConfig
}

// 核心方法
func (l *SlidingWindowLimiter) Allow(ctx context.Context) (bool, error)
func (l *SlidingWindowLimiter) Wait(ctx context.Context) error
func (l *SlidingWindowLimiter) CurrentCount() int
func (l *SlidingWindowLimiter) Remaining() int
```

### 4.3 信号量 (SemaphoreLimiter)

基于 Channel 实现的信号量限流器。

```go
type SemaphoreLimiter struct {
    sem      chan struct{}
    acquired map[string]int
    mu       sync.RWMutex
    config   *LimiterConfig
}

// 核心方法
func (l *SemaphoreLimiter) Acquire(ctx context.Context, key string) error
func (l *SemaphoreLimiter) Release(key string)
func (l *SemaphoreLimiter) Allow(ctx context.Context) (bool, error)
func (l *SemaphoreLimiter) Available() int
```

### 4.4 加权信号量 (WeightedSemaphoreLimiter)

支持按权重获取资源的信号量。

```go
type WeightedSemaphoreLimiter struct {
    mu        sync.Mutex
    available int
    used      int
    weighted  map[string]int
    cond      *sync.Cond
    config    *LimiterConfig
}

// 核心方法
func (l *WeightedSemaphoreLimiter) Acquire(ctx context.Context, key string, weight int) error
func (l *WeightedSemaphoreLimiter) Release(key string, weight int)
func (l *WeightedSemaphoreLimiter) Allow(ctx context.Context, weight int) (bool, error)
```

## 5. 工厂模式

通过工厂创建限流器：

```go
type Factory struct {
    creators map[LimiterType]func(*LimiterConfig) Limiter
}

// 创建限流器
limiter, err := ratelimit.CreateLimiter(ratelimit.LimiterTypeTokenBucket, config)
limiter, err := ratelimit.CreateLimiter(ratelimit.LimiterTypeSlidingWindow, config)
limiter, err := ratelimit.CreateLimiter(ratelimit.LimiterTypeSemaphore, config)
```

## 6. 使用示例

```go
// 创建限流器
config := &ratelimit.LimiterConfig{
    Rate:  10,            // 每秒请求数
    Burst: 50,            // 突发容量
}

llmLimiter := ratelimit.NewTokenBucketLimiter(config)

// 在请求处理中使用
func handleRequest(ctx context.Context) error {
    // 非阻塞检查
    if ok, _ := llmLimiter.Allow(ctx); !ok {
        return ErrRateLimitExceeded
    }
    
    // 或阻塞等待
    if err := llmLimiter.Wait(ctx); err != nil {
        return err
    }
    
    // 处理请求
    return doProcess(ctx)
}
```

### 带 key 的信号量

```go
semLimiter := ratelimit.NewSemaphoreLimiter(config)

// 按用户限流
userID := getUserID(ctx)
if err := semLimiter.Acquire(ctx, userID); err != nil {
    return err
}
defer semLimiter.Release(userID)
```

## 7. 配置参数

| 参数 | 默认值 | 说明 |
|------|--------|------|
| Rate | 10 | 每秒请求数 |
| Burst | 10 | 突发容量 |
| Timeout | 30s | 等待超时 |
| RefillRate | 1s | 令牌补充间隔 |
