package balancer

import (
	"sync"
	"testing"

	"github.com/PortableBaka/gateway/internal/config"
)

// mkUpstreams builds equal-weight upstreams from a list of URLs.
func mkUpstreams(urls ...string) []*config.Upstream {
	ups := make([]*config.Upstream, len(urls))
	for i, u := range urls {
		ups[i] = &config.Upstream{URL: u, Weight: 1}
	}
	return ups
}

func TestRoundRobinDistribution(t *testing.T) {
	ups := mkUpstreams("a", "b", "c")
	rr := NewRoundRobin(ups)

	counts := map[string]int{}
	for i := 0; i < 9; i++ {
		counts[rr.Next().URL]++
	}
	for _, u := range ups {
		if counts[u.URL] != 3 {
			t.Errorf("upstream %s: got %d picks, want 3", u.URL, counts[u.URL])
		}
	}
}

func TestRoundRobinOrder(t *testing.T) {
	ups := mkUpstreams("a", "b", "c")
	rr := NewRoundRobin(ups)

	want := []string{"a", "b", "c", "a", "b", "c"}
	for i, w := range want {
		if got := rr.Next().URL; got != w {
			t.Errorf("call %d: got %s, want %s", i, got, w)
		}
	}
}

func TestWeightedDistribution(t *testing.T) {
	ups := []*config.Upstream{
		{URL: "a", Weight: 1},
		{URL: "b", Weight: 2},
	}
	w := NewWeighted(ups)

	counts := map[string]int{}
	const n = 300 // 100 full cycles of total weight 3
	for i := 0; i < n; i++ {
		counts[w.Next().URL]++
	}
	// Smooth WRR is exact over whole cycles: a=100, b=200 (a 1:2 ratio).
	if counts["a"] != 100 || counts["b"] != 200 {
		t.Errorf("got a=%d b=%d, want a=100 b=200", counts["a"], counts["b"])
	}
}

// TestWeightedOrder pins the smooth-WRR interleaving so a regression to naive
// "burst" weighting (b,b,a) would be caught.
func TestWeightedOrder(t *testing.T) {
	ups := []*config.Upstream{
		{URL: "a", Weight: 1},
		{URL: "b", Weight: 2},
	}
	w := NewWeighted(ups)

	want := []string{"b", "a", "b", "b", "a", "b"}
	for i, exp := range want {
		if got := w.Next().URL; got != exp {
			t.Errorf("call %d: got %s, want %s", i, got, exp)
		}
	}
}

func TestNewBalancer(t *testing.T) {
	ups := mkUpstreams("a")

	if _, err := NewBalancer(config.RoundRobin, ups); err != nil {
		t.Errorf("round_robin: unexpected error %v", err)
	}
	if _, err := NewBalancer(config.Weighted, ups); err != nil {
		t.Errorf("weighted: unexpected error %v", err)
	}
	if _, err := NewBalancer("banana", ups); err == nil {
		t.Error("expected error for unknown strategy, got nil")
	}
}

func TestEmptyUpstreams(t *testing.T) {
	if got := NewRoundRobin(nil).Next(); got != nil {
		t.Errorf("round robin: got %v, want nil", got)
	}
	if got := NewWeighted(nil).Next(); got != nil {
		t.Errorf("weighted: got %v, want nil", got)
	}
}

// TestConcurrentNext is the key one: run it with -race. If a balancer's state
// weren't guarded (atomic counter / mutex), the race detector would fail here.
func TestConcurrentNext(t *testing.T) {
	ups := mkUpstreams("a", "b", "c")
	balancers := map[string]Balancer{
		"round_robin": NewRoundRobin(ups),
		"weighted":    NewWeighted(ups),
	}

	for name, b := range balancers {
		t.Run(name, func(t *testing.T) {
			var wg sync.WaitGroup
			for g := 0; g < 100; g++ {
				wg.Add(1)
				go func() {
					defer wg.Done()
					for i := 0; i < 1000; i++ {
						if b.Next() == nil {
							t.Error("Next returned nil under concurrency")
							return
						}
					}
				}()
			}
			wg.Wait()
		})
	}
}
