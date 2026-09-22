package api

import (
	"fmt"
	"net/http"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// 并发猜测不能越过额度：先查余量再扣的写法下，一批并发请求会全部通过检查。
func TestAdminAuthLimiter_ConcurrentWrongTokensCappedAtBurst(t *testing.T) {
	const burst = 3
	r := newAdminAuthLimitTestRouter(t, burst)

	var forbidden, limited atomic.Int32
	var wg sync.WaitGroup
	for i := 0; i < 50; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			switch code := adminAuthRequest(r, "203.0.113.9", "Bearer wrong"); code {
			case http.StatusForbidden:
				forbidden.Add(1)
			case http.StatusTooManyRequests:
				limited.Add(1)
			default:
				t.Errorf("unexpected status %d", code)
			}
		}()
	}
	wg.Wait()

	if got := forbidden.Load(); got != burst {
		t.Errorf("token comparisons performed: want %d, got %d (limited=%d)", burst, got, limited.Load())
	}
}

func TestAuthFailureLimiter_RefillAndRefund(t *testing.T) {
	clock := time.Unix(1_700_000_000, 0)
	l := newAuthFailureLimiter(60, 2) // 每秒回补 1 次
	l.now = func() time.Time { return clock }

	if !l.acquire("ip") || !l.acquire("ip") {
		t.Fatal("first two acquires should succeed")
	}
	if l.acquire("ip") {
		t.Fatal("third acquire should be rejected when burst is spent")
	}

	clock = clock.Add(time.Second)
	if !l.acquire("ip") {
		t.Fatal("one token should be refilled after one second")
	}
	if l.acquire("ip") {
		t.Fatal("only one token should have been refilled")
	}

	l.refund("ip")
	if !l.acquire("ip") {
		t.Fatal("refunded token should be usable")
	}

	// 回补与退回都不能超过 burst
	clock = clock.Add(time.Hour)
	l.refund("ip")
	for i := 0; i < 2; i++ {
		if !l.acquire("ip") {
			t.Fatalf("acquire %d after full refill should succeed", i+1)
		}
	}
	if l.acquire("ip") {
		t.Fatal("tokens must be capped at burst")
	}
}

// 桶数过阈值时只清理已回满的桶；仍在冷却的 IP 不能因清理重获额度。
func TestAuthFailureLimiter_SweepKeepsCoolingBuckets(t *testing.T) {
	clock := time.Unix(1_700_000_000, 0)
	l := newAuthFailureLimiter(1, 1)
	l.now = func() time.Time { return clock }

	if !l.acquire("attacker") {
		t.Fatal("first acquire should succeed")
	}
	for i := 0; len(l.buckets) < authFailureSweepThreshold; i++ {
		l.refund(fmt.Sprintf("idle-%d", i)) // 新建满桶
	}

	l.acquire("trigger-sweep")

	if _, ok := l.buckets["attacker"]; !ok {
		t.Fatal("cooling bucket must survive sweep")
	}
	if l.acquire("attacker") {
		t.Fatal("attacker must still be rate limited after sweep")
	}
	if n := len(l.buckets); n > 3 {
		t.Errorf("full buckets should be swept, %d remain", n)
	}
}

// 存活桶很多时，清理门槛要随之抬高，否则每个新 IP 都触发一次全表扫描。
func TestAuthFailureLimiter_SweepThresholdGrowsWithLiveBuckets(t *testing.T) {
	clock := time.Unix(1_700_000_000, 0)
	l := newAuthFailureLimiter(1, 1)
	l.now = func() time.Time { return clock }

	for i := 0; i <= authFailureSweepThreshold; i++ {
		l.acquire(fmt.Sprintf("live-%d", i)) // 扣光的桶都不会被清理
	}
	if want := 2 * authFailureSweepThreshold; l.sweepAt < want {
		t.Fatalf("sweepAt after sweeping only live buckets: want >= %d, got %d", want, l.sweepAt)
	}
}
