package commitqueue

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// fakeProcs decides which fake pids are alive. Every pid is alive until
// killed.
type fakeProcs struct {
	mu   sync.Mutex
	dead map[int]bool
}

func (p *fakeProcs) alive(pid int) bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	return pid > 0 && !p.dead[pid]
}

func (p *fakeProcs) kill(pid int) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.dead == nil {
		p.dead = map[int]bool{}
	}
	p.dead[pid] = true
}

// fakeClock moves only when told to, and ticks a nanosecond per reading so
// tickets taken in sequence always sort in that sequence.
type fakeClock struct {
	mu  sync.Mutex
	now time.Time
}

func (c *fakeClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.now = c.now.Add(time.Nanosecond)
	return c.now
}

func (c *fakeClock) advance(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.now = c.now.Add(d)
}

func newTestQueue(t *testing.T, slots int) (*Queue, *fakeProcs, *fakeClock) {
	t.Helper()
	procs := &fakeProcs{}
	clock := &fakeClock{now: time.Unix(1_800_000_000, 0)}
	q, err := New(Options{
		Dir:          t.TempDir(),
		Slots:        slots,
		Poll:         time.Millisecond,
		StaleAfter:   time.Hour,
		Now:          clock.Now,
		ProcessAlive: procs.alive,
	})
	if err != nil {
		t.Fatalf("New() = %v", err)
	}
	return q, procs, clock
}

func caller(name string, pid int) Caller {
	return Caller{Name: name, Repo: "/repo", PID: pid, WaiterPID: pid}
}

// mustAcquire takes a slot that is known to be free.
func mustAcquire(t *testing.T, q *Queue, c Caller) Slot {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	s, err := q.Acquire(ctx, c, nil, nil)
	if err != nil {
		t.Fatalf("Acquire(%s) = %v", c.Name, err)
	}
	return s
}

// startWaiting begins an Acquire that must queue, and returns once its
// ticket is written, so callers started in sequence queue in sequence. A
// waiter still waiting when the test ends is cancelled and awaited before
// the state directory is removed.
func startWaiting(t *testing.T, q *Queue, c Caller) <-chan Slot {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	queued := make(chan struct{})
	got := make(chan Slot, 1)
	exited := make(chan struct{})
	go func() {
		defer close(exited)
		s, err := q.Acquire(ctx, c, func(int) { close(queued) }, nil)
		if errors.Is(err, context.Canceled) {
			return
		}
		if err != nil {
			t.Errorf("Acquire(%s) = %v", c.Name, err)
			return
		}
		got <- s
	}()
	t.Cleanup(func() {
		cancel()
		<-exited
	})
	select {
	case <-queued:
	case <-time.After(5 * time.Second):
		t.Fatalf("%s never queued", c.Name)
	}
	return got
}

func receive(t *testing.T, ch <-chan Slot, who string) Slot {
	t.Helper()
	select {
	case s := <-ch:
		return s
	case <-time.After(5 * time.Second):
		t.Fatalf("%s was never granted a slot", who)
		return Slot{}
	}
}

func holders(t *testing.T, q *Queue) []string {
	t.Helper()
	snap, err := q.Snapshot()
	if err != nil {
		t.Fatalf("Snapshot() = %v", err)
	}
	var names []string
	for _, s := range snap.Held {
		names = append(names, s.Name)
	}
	return names
}

func TestAcquireTakesAFreeSlotAtOnce(t *testing.T) {
	q, _, _ := newTestQueue(t, 2)
	s := mustAcquire(t, q, caller("a", 101))
	if s.Number != 1 || s.Name != "a" || s.PID != 101 {
		t.Errorf("slot = %+v, want slot 1 held by a (pid 101)", s)
	}
}

func TestWaitersAreServedInArrivalOrder(t *testing.T) {
	q, _, _ := newTestQueue(t, 1)
	mustAcquire(t, q, caller("a", 101))
	gotB := startWaiting(t, q, caller("b", 102))
	gotC := startWaiting(t, q, caller("c", 103))

	if _, err := q.Release("a", "/repo"); err != nil {
		t.Fatalf("Release(a) = %v", err)
	}
	if s := receive(t, gotB, "b"); s.Number != 1 {
		t.Errorf("b got slot %d, want 1", s.Number)
	}
	// c polls as often as b did, but must not have been served first.
	if got := holders(t, q); len(got) != 1 || got[0] != "b" {
		t.Fatalf("holders = %v, want [b]", got)
	}

	if _, err := q.Release("b", "/repo"); err != nil {
		t.Fatalf("Release(b) = %v", err)
	}
	receive(t, gotC, "c")
}

func TestFreeSlotsServeTheOldestWaiters(t *testing.T) {
	q, _, _ := newTestQueue(t, 2)
	mustAcquire(t, q, caller("a", 101))
	mustAcquire(t, q, caller("b", 102))
	gotC := startWaiting(t, q, caller("c", 103))
	gotD := startWaiting(t, q, caller("d", 104))
	startWaiting(t, q, caller("e", 105))

	for _, name := range []string{"a", "b"} {
		if _, err := q.Release(name, "/repo"); err != nil {
			t.Fatalf("Release(%s) = %v", name, err)
		}
	}
	receive(t, gotC, "c")
	receive(t, gotD, "d")
	snap, err := q.Snapshot()
	if err != nil {
		t.Fatalf("Snapshot() = %v", err)
	}
	if len(snap.Waiting) != 1 || snap.Waiting[0].Name != "e" {
		t.Errorf("waiting = %+v, want only e", snap.Waiting)
	}
}

func TestDeadHolderIsReclaimed(t *testing.T) {
	q, procs, _ := newTestQueue(t, 1)
	mustAcquire(t, q, caller("crashed", 101))
	procs.kill(101)

	var reclaimed []Reclaimed
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if _, err := q.Acquire(ctx, caller("next", 102), nil, func(r Reclaimed) { reclaimed = append(reclaimed, r) }); err != nil {
		t.Fatalf("Acquire(next) = %v", err)
	}
	if len(reclaimed) != 1 || reclaimed[0].Kind != "slot" || reclaimed[0].Name != "crashed" {
		t.Errorf("reclaimed = %+v, want crashed's slot", reclaimed)
	}
}

func TestDeadWaiterDoesNotHoldUpTheLine(t *testing.T) {
	q, procs, _ := newTestQueue(t, 1)
	mustAcquire(t, q, caller("a", 101))
	if _, _, err := q.enqueue(caller("killed-while-waiting", 102)); err != nil {
		t.Fatalf("enqueue = %v", err)
	}
	gotC := startWaiting(t, q, caller("c", 103))
	procs.kill(102)

	if _, err := q.Release("a", "/repo"); err != nil {
		t.Fatalf("Release(a) = %v", err)
	}
	receive(t, gotC, "c")
}

func TestSlotHeldTooLongIsReclaimed(t *testing.T) {
	q, _, clock := newTestQueue(t, 1)
	mustAcquire(t, q, caller("stuck", 101))
	clock.advance(2 * time.Hour)
	if s := mustAcquire(t, q, caller("next", 102)); s.Name != "next" {
		t.Errorf("slot = %+v, want next to hold it", s)
	}
}

func TestSameNameCannotQueueTwice(t *testing.T) {
	q, _, _ := newTestQueue(t, 1)
	mustAcquire(t, q, caller("a", 101))
	_, err := q.Acquire(context.Background(), caller("a", 102), nil, nil)
	if err == nil {
		t.Fatal("Acquire(a) twice = nil, want an error")
	}
	// The same name in another repo is a different caller.
	other := Caller{Name: "a", Repo: "/other", PID: 103, WaiterPID: 103}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	if _, err := q.Acquire(ctx, other, nil, nil); !errors.Is(err, context.DeadlineExceeded) {
		t.Errorf("Acquire(a in /other) = %v, want it to queue and time out", err)
	}
}

func TestCancelledWaiterLeavesTheLine(t *testing.T) {
	q, _, _ := newTestQueue(t, 1)
	mustAcquire(t, q, caller("a", 101))
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		_, err := q.Acquire(ctx, caller("gives-up", 102), func(int) { cancel() }, nil)
		done <- err
	}()
	if err := <-done; !errors.Is(err, context.Canceled) {
		t.Fatalf("Acquire = %v, want context.Canceled", err)
	}
	snap, err := q.Snapshot()
	if err != nil {
		t.Fatalf("Snapshot() = %v", err)
	}
	if len(snap.Waiting) != 0 {
		t.Errorf("waiting = %+v, want the cancelled ticket gone", snap.Waiting)
	}
}

func TestNeverMoreHoldersThanSlots(t *testing.T) {
	const slots, callers = 3, 24
	q, _, _ := newTestQueue(t, slots)
	var inside, most atomic.Int32
	var wg sync.WaitGroup
	for i := 0; i < callers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			c := caller("c"+string(rune('a'+i)), 1000+i)
			ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
			defer cancel()
			if _, err := q.Acquire(ctx, c, nil, nil); err != nil {
				t.Errorf("Acquire(%s) = %v", c.Name, err)
				return
			}
			n := inside.Add(1)
			for {
				m := most.Load()
				if n <= m || most.CompareAndSwap(m, n) {
					break
				}
			}
			inside.Add(-1)
			if _, err := q.Release(c.Name, c.Repo); err != nil {
				t.Errorf("Release(%s) = %v", c.Name, err)
			}
		}()
	}
	wg.Wait()
	if got := most.Load(); got > slots {
		t.Errorf("at most %d callers held a slot at once, want <= %d", got, slots)
	}
}

func TestHoldsNeedsALiveHolderInTheSameRepo(t *testing.T) {
	q, procs, _ := newTestQueue(t, 2)
	mustAcquire(t, q, caller("a", 101))

	for _, tt := range []struct {
		name, repo string
		want       bool
	}{
		{"a", "/repo", true},
		{"a", "/other", false},
		{"b", "/repo", false},
	} {
		got, err := q.Holds(tt.name, tt.repo)
		if err != nil || got != tt.want {
			t.Errorf("Holds(%s, %s) = %v, %v; want %v", tt.name, tt.repo, got, err, tt.want)
		}
	}
	procs.kill(101)
	if got, _ := q.Holds("a", "/repo"); got {
		t.Error("Holds(a) = true after its holder exited")
	}
}
