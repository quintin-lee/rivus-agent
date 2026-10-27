package httpapi

import (
	"net"
	"net/http"
	"sync"
	"time"

	"golang.org/x/time/rate"
)

// maxTrackedClients 限制客户端表内存（每个客户端一个令牌桶）。
const maxTrackedClients = 1024

type clientLimiter struct {
	lim      *rate.Limiter
	lastSeen time.Time
}

// rateLimiter 按 X-Owner-ID（优先）或来源 IP 维护每客户端令牌桶。
type rateLimiter struct {
	mu      sync.Mutex
	rpm     int
	clients map[string]*clientLimiter
}

func newRateLimiter(rpm int) *rateLimiter {
	return &rateLimiter{rpm: rpm, clients: make(map[string]*clientLimiter)}
}

// clientKey 不信任 X-Forwarded-For（可伪造）。
func clientKey(r *http.Request) string {
	if v := r.Header.Get("X-Owner-ID"); v != "" {
		return "owner:" + v
	}
	if host, _, err := net.SplitHostPort(r.RemoteAddr); err == nil {
		return "ip:" + host
	}
	return "ip:" + r.RemoteAddr
}

// allow 报告请求是否放行；拒绝时 retryAfter 为建议等待时长。
func (rl *rateLimiter) allow(r *http.Request) (bool, time.Duration) {
	if rl.rpm <= 0 {
		return true, 0
	}
	key := clientKey(r)
	now := time.Now()
	rl.mu.Lock()
	cl, ok := rl.clients[key]
	if !ok {
		if len(rl.clients) >= maxTrackedClients {
			rl.evictOldestLocked()
		}
		cl = &clientLimiter{lim: rate.NewLimiter(rate.Every(time.Minute/time.Duration(rl.rpm)), rl.rpm)}
		rl.clients[key] = cl
	}
	cl.lastSeen = now
	rl.mu.Unlock()

	res := cl.lim.Reserve()
	if res.Delay() == 0 {
		return true, 0 // 令牌立即可用（已被消费）
	}
	wait := res.Delay()
	res.Cancel() // 归还预约：被拒请求不得占用未来预算
	return false, wait
}

// evictOldestLocked 调用方须已持锁；O(n) 扫描仅在表满时触发。
func (rl *rateLimiter) evictOldestLocked() {
	var oldestKey string
	var oldest time.Time
	first := true
	for k, cl := range rl.clients {
		if first || cl.lastSeen.Before(oldest) {
			oldestKey, oldest, first = k, cl.lastSeen, false
		}
	}
	delete(rl.clients, oldestKey)
}
