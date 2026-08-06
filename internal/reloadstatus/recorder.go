// Package reloadstatus 记录配置热更新未被应用的运行时状态，供只读健康端点（/ready）
// 信息化暴露。覆盖两条静默失败路径：配置加载/校验失败后保留旧配置（config.Watcher），
// 以及加载成功但被 fail-closed 运行时闸拒绝（main 的热更新回调）。二者都表现为
// "admin 保存返回 200、运行态却没变"。它是一个无反向依赖的叶子包：生产者与消费者
// （api 层）都依赖它，彼此不直接耦合。
package reloadstatus

import (
	"sync"
	"time"
)

// Status 描述进程生命周期内最近一次被跳过的配置热更新。
// 语义是「历史上最近一次跳过」，而非「当前仍被坏配置卡住」——一旦发生过跳过，
// 后续成功热更新不会清除本状态；计数与时间戳随进程重启归零。
type Status struct {
	LastSkipAt    time.Time // 最近一次跳过的时刻
	LastSkipError string    // 最近一次跳过的错误文本
	SkipCount     int64     // 进程生命周期内累计跳过次数
}

// Recorder 线程安全地记录热更新跳过状态。
// 写入来自 watcher 的 reload goroutine 与已被 runtimeMu 串行化的热更新回调（均低频），
// 读取来自并发的 /ready HTTP 处理器（高频），故用 RWMutex 读写分离。
type Recorder struct {
	mu     sync.RWMutex
	status Status
}

// New 创建一个空 Recorder（尚未发生过跳过）。
func New() *Recorder {
	return &Recorder{}
}

// RecordSkip 记录一次被跳过的配置热更新：打时间戳、累加计数、保存错误文本。
// err 为 nil 时错误文本留空但仍计数。
func (r *Recorder) RecordSkip(err error) {
	r.mu.Lock()
	defer r.mu.Unlock()

	r.status.LastSkipAt = time.Now()
	r.status.SkipCount++
	if err != nil {
		r.status.LastSkipError = err.Error()
	} else {
		r.status.LastSkipError = ""
	}
}

// Snapshot 在读锁内一次性拷贝出一致的状态快照，第二返回值表示是否曾发生过跳过
// （SkipCount > 0）。调用方据此决定是否在响应中暴露该信息。
func (r *Recorder) Snapshot() (Status, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()

	return r.status, r.status.SkipCount > 0
}
