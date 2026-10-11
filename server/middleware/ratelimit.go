// Package middleware 提供 HTTP 中间件。
package middleware

import (
	"sync"
	"time"

	"github.com/gofiber/fiber/v2"
)

// apiRateLimiter 是极简的每 IP 固定窗口限流器。
// 局域网自用场景默认阈值宽松；仅用于缓解误用/脚本失控，不是严格的防 DDoS 方案。
type apiRateLimiter struct {
	mu      sync.Mutex
	limit   int
	window  time.Duration
	buckets map[string]*ipWindow
}

type ipWindow struct {
	count int
	reset time.Time
}

// NewAPIRateLimit 返回一个按 IP 限流的中间件：每 window 最多 limit 次请求，超限返回 429。
// limit <= 0 时直接放行（相当于关闭限流）。
func NewAPIRateLimit(limit int, window time.Duration) fiber.Handler {
	if limit <= 0 || window <= 0 {
		return func(c *fiber.Ctx) error { return c.Next() }
	}
	rl := &apiRateLimiter{
		limit:   limit,
		window:  window,
		buckets: make(map[string]*ipWindow),
	}
	// 周期清理过期桶，防止 map 无界增长
	go func() {
		ticker := time.NewTicker(window)
		defer ticker.Stop()
		for range ticker.C {
			now := time.Now()
			rl.mu.Lock()
			for k, b := range rl.buckets {
				if now.After(b.reset) {
					delete(rl.buckets, k)
				}
			}
			rl.mu.Unlock()
		}
	}()
	return func(c *fiber.Ctx) error {
		ip := c.IP()
		now := time.Now()
		rl.mu.Lock()
		b, ok := rl.buckets[ip]
		if !ok || now.After(b.reset) {
			rl.buckets[ip] = &ipWindow{count: 1, reset: now.Add(rl.window)}
			rl.mu.Unlock()
			return c.Next()
		}
		b.count++
		over := b.count > rl.limit
		rl.mu.Unlock()
		if over {
			c.Set("Retry-After", "60")
			return c.Status(fiber.StatusTooManyRequests).JSON(fiber.Map{"error": "rate limit exceeded"})
		}
		return c.Next()
	}
}
