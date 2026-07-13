package middleware

import (
	"context"
	"encoding/json"
	"log/slog"
	"net"
	"net/http"
	"sync"
	"time"

	rate "golang.org/x/time/rate"
)

type visitor struct {
	limiter  *rate.Limiter
	lastSeen time.Time
}

type RateLimiter struct {
	mu       sync.Mutex
	visitors map[string]*visitor
	rps      rate.Limit
	burst    int
}

func NewRateLimiter(rps float64, burst int) *RateLimiter {
	return &RateLimiter{
		visitors: make(map[string]*visitor),
		rps:      rate.Limit(rps),
		burst:    burst,
	}
}

func (rl *RateLimiter) getLimiter(key string) *rate.Limiter {
	rl.mu.Lock()
	defer rl.mu.Unlock()

	v, ok := rl.visitors[key]
	if !ok {
		v = &visitor{limiter: rate.NewLimiter(rl.rps, rl.burst)}
		rl.visitors[key] = v
	}
	v.lastSeen = time.Now()
	return v.limiter
}

func (rl *RateLimiter) CleanupStale(maxAge time.Duration) {
	rl.mu.Lock()
	defer rl.mu.Unlock()

	for key, v := range rl.visitors {
		if time.Since(v.lastSeen) > maxAge {
			delete(rl.visitors, key)
		}
	}
}

func (rl *RateLimiter) Run(ctx context.Context, interval, maxAge time.Duration) {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			rl.CleanupStale(maxAge)
		}
	}
}

func clientKey(r *http.Request) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}

func RateLimitMiddleware(rl *RateLimiter, logger *slog.Logger) Middleware {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			key := clientKey(r)

			if !rl.getLimiter(key).Allow() {
				logger.Warn("rate limit exceeded", "client", key, "path", r.URL.Path, "request_id", GetRequestId(r.Context()))
				w.Header().Set("Retry-After", "1")
				w.WriteHeader(http.StatusTooManyRequests)
				json.NewEncoder(w).Encode(map[string]string{"error": "rate_limited"})
				return
			}

			next.ServeHTTP(w, r)
		})
	}
}
