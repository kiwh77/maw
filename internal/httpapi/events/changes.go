package events

import (
	"context"
	"sync"
	"time"

	"github.com/kiwh77/maw/internal/query"
)

// MemoryChanges 是测试与单进程用的内存失效源。
type MemoryChanges struct {
	mu      sync.Mutex
	subs    map[chan ChangeSignal]struct{}
	closed  bool
}

// NewMemoryChanges 构造可广播的变化源。
func NewMemoryChanges() *MemoryChanges {
	return &MemoryChanges{subs: make(map[chan ChangeSignal]struct{})}
}

// Subscribe 实现 ChangeSource。
func (m *MemoryChanges) Subscribe(ctx context.Context) (<-chan ChangeSignal, func()) {
	ch := make(chan ChangeSignal, 8)
	m.mu.Lock()
	if m.closed {
		m.mu.Unlock()
		close(ch)
		return ch, func() {}
	}
	m.subs[ch] = struct{}{}
	m.mu.Unlock()

	var once sync.Once
	cancel := func() {
		once.Do(func() {
			m.mu.Lock()
			defer m.mu.Unlock()
			if _, ok := m.subs[ch]; ok {
				delete(m.subs, ch)
				close(ch)
			}
		})
	}
	go func() {
		<-ctx.Done()
		cancel()
	}()
	return ch, cancel
}

// Publish 向所有订阅者投递一次失效（慢客户端丢帧，靠补拉恢复）。
func (m *MemoryChanges) Publish(sig ChangeSignal) {
	if sig.At.IsZero() {
		sig.At = time.Now().UTC()
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	for ch := range m.subs {
		select {
		case ch <- sig:
		default:
		}
	}
}

// Close 结束所有订阅。
func (m *MemoryChanges) Close() {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.closed = true
	for ch := range m.subs {
		close(ch)
		delete(m.subs, ch)
	}
}

// PollingChanges 按间隔读取水位，变化时发失效信号（对齐旧 board eventHub 轮询语义）。
type PollingChanges struct {
	Interval time.Duration
	Peek     func(ctx context.Context) (query.Watermark, int64, error)
}

// Subscribe 实现 ChangeSource。
func (p *PollingChanges) Subscribe(ctx context.Context) (<-chan ChangeSignal, func()) {
	interval := p.Interval
	if interval <= 0 {
		interval = time.Second
	}
	out := make(chan ChangeSignal, 4)
	ctx, cancel := context.WithCancel(ctx)
	go func() {
		defer close(out)
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		var last query.Watermark
		var lastRev int64
		valid := false
		check := func() {
			if p.Peek == nil {
				return
			}
			wm, rev, err := p.Peek(ctx)
			if err != nil {
				return
			}
			if valid && wm == last && rev == lastRev {
				return
			}
			last, lastRev, valid = wm, rev, true
			sig := ChangeSignal{Watermark: wm, Revision: rev, At: time.Now().UTC()}
			select {
			case out <- sig:
			default:
			}
		}
		check()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				check()
			}
		}
	}()
	return out, cancel
}
