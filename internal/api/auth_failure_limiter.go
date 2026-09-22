package api

import (
	"sync"
	"time"
)

// authFailureLimiter 按 IP 限制鉴权失败次数（令牌桶，只对失败计数）。
//
// 用法是「先扣后比、成功退回」：acquire 在比对 token 之前原子地扣一个令牌，比对成功再 refund。
// 不能「先查余量、失败再扣」——一批并发请求会同时通过余量检查，各自都能完成一次比对。
// 不复用 probe.IPLimiter：x/time/rate 的 Reservation 过了生效时刻就不再退回令牌，做不出 refund。
type authFailureLimiter struct {
	mu        sync.Mutex
	buckets   map[string]*authFailureBucket
	perSecond float64
	burst     float64
	sweepAt   int              // 桶数达到该值时清理一次，见 sweepLocked
	now       func() time.Time // 测试可替换
}

type authFailureBucket struct {
	tokens float64
	last   time.Time
}

// authFailureSweepThreshold 首次清理的桶数门槛。
const authFailureSweepThreshold = 1024

// newAuthFailureLimiter 每个 IP 每分钟回补 perMinute 次失败额度，最多累积 burst 次。
func newAuthFailureLimiter(perMinute, burst int) *authFailureLimiter {
	return &authFailureLimiter{
		buckets:   make(map[string]*authFailureBucket),
		perSecond: float64(perMinute) / 60,
		burst:     float64(burst),
		sweepAt:   authFailureSweepThreshold,
		now:       time.Now,
	}
}

// acquire 为 ip 扣一个令牌；返回 false 表示额度已耗尽（未扣）。
func (l *authFailureLimiter) acquire(ip string) bool {
	l.mu.Lock()
	defer l.mu.Unlock()

	now := l.now()
	b := l.refillLocked(ip, now)
	if b.tokens < 1 {
		return false
	}
	b.tokens--
	return true
}

// refund 退回一次 acquire 扣掉的令牌（本次鉴权成功，不计为失败）。
func (l *authFailureLimiter) refund(ip string) {
	l.mu.Lock()
	defer l.mu.Unlock()

	b := l.refillLocked(ip, l.now())
	b.tokens = min(b.tokens+1, l.burst)
}

// refillLocked 取出（必要时新建）ip 的桶并按流逝时间回补；调用方须持有 l.mu。
func (l *authFailureLimiter) refillLocked(ip string, now time.Time) *authFailureBucket {
	b, ok := l.buckets[ip]
	if !ok {
		if len(l.buckets) >= l.sweepAt {
			l.sweepLocked(now)
		}
		b = &authFailureBucket{tokens: l.burst, last: now}
		l.buckets[ip] = b
		return b
	}
	if elapsed := now.Sub(b.last).Seconds(); elapsed > 0 {
		b.tokens = min(b.tokens+elapsed*l.perSecond, l.burst)
	}
	b.last = now
	return b
}

// sweepLocked 删除按当前时间已回满的桶——删掉与保留对后续判定完全等价。
//
// 这不是硬上限：未回满的桶必须留着，否则清理即重置额度。但桶回满后在下一次清理时必被删，
// 所以存活桶数随「失败请求速率」而非累计量增长。下次清理门槛设为清理后桶数的两倍，
// 摊还成 O(1)——固定门槛下，存活桶一多，每个新 IP 都会触发一次全表扫描。
func (l *authFailureLimiter) sweepLocked(now time.Time) {
	for ip, b := range l.buckets {
		if b.tokens+now.Sub(b.last).Seconds()*l.perSecond >= l.burst {
			delete(l.buckets, ip)
		}
	}
	l.sweepAt = max(authFailureSweepThreshold, 2*len(l.buckets))
}
