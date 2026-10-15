// Package commitqueue limits how many expensive pre-commit runs happen at
// once and hands out the free places strictly in the order callers asked.
//
// State is plain files under one directory, so any process on the machine
// can join the same queue:
//
//	<dir>/queue.lock         held (flock / LockFileEx) while state changes
//	<dir>/tickets/<t>.json   one per waiter, named so they sort by arrival
//	<dir>/slots/slot-<n>.json one per held slot
//
// A waiter only takes a slot when its ticket is among the oldest N, where N
// is the number of free slots, so nobody is overtaken. Tickets and slots
// record the owning process id, and an entry whose process has exited is
// removed, so a crashed caller cannot block the queue.
package commitqueue

import (
	"fmt"
	"os"
	"path/filepath"
	"time"
)

// Default settings, used when Options leaves a field zero.
const (
	DefaultSlots      = 2
	DefaultStaleAfter = 60 * time.Minute
	DefaultPoll       = time.Second
)

// Options configures a Queue. Zero fields take the defaults above.
type Options struct {
	// Dir is the state directory shared by every caller of this queue.
	Dir string
	// Slots is how many holders may run at once.
	Slots int
	// StaleAfter is how long a slot may be held before it is reclaimed even
	// though its owner still looks alive (a backstop for a reused pid).
	StaleAfter time.Duration
	// Poll is how often a blocked waiter re-checks for a free slot.
	Poll time.Duration
	// Now and ProcessAlive are replaced in tests.
	Now          func() time.Time
	ProcessAlive func(pid int) bool
}

// Queue is a handle on the queue state in one directory.
type Queue struct {
	dir          string
	slots        int
	staleAfter   time.Duration
	poll         time.Duration
	now          func() time.Time
	processAlive func(pid int) bool
}

// Ticket is a caller waiting for a slot.
type Ticket struct {
	ID       string `json:"id"`
	Name     string `json:"name"`
	Repo     string `json:"repo"`
	PID      int    `json:"pid"`
	QueuedAt int64  `json:"queuedAt"` // unix nanoseconds
}

// Slot is a held place in the queue.
type Slot struct {
	Number    int    `json:"slot"`
	Name      string `json:"name"`
	Repo      string `json:"repo"`
	PID       int    `json:"pid"`
	QueuedAt  int64  `json:"queuedAt"`  // unix nanoseconds, from the ticket
	StartedAt int64  `json:"startedAt"` // unix nanoseconds
}

// Caller identifies who is asking for a slot. Name and Repo together must
// be unique among live callers; Release frees by that pair.
type Caller struct {
	Name string
	Repo string
	// PID owns the slot once granted. A caller that holds the slot across
	// several processes (acquire now, release later) passes the long-lived
	// one, such as its shell. Zero means this process.
	PID int
	// WaiterPID owns the ticket while waiting: the process blocked in
	// Acquire, so a waiter that is killed leaves a ticket that is reaped
	// rather than one that blocks the line. Zero means this process.
	WaiterPID int
}

func (c Caller) slotPID() int {
	if c.PID > 0 {
		return c.PID
	}
	return os.Getpid()
}

func (c Caller) waiterPID() int {
	if c.WaiterPID > 0 {
		return c.WaiterPID
	}
	return os.Getpid()
}

// New opens the queue in opts.Dir, creating its directories.
func New(opts Options) (*Queue, error) {
	if opts.Dir == "" {
		return nil, fmt.Errorf("commitqueue: no state directory")
	}
	q := &Queue{
		dir:          opts.Dir,
		slots:        opts.Slots,
		staleAfter:   opts.StaleAfter,
		poll:         opts.Poll,
		now:          opts.Now,
		processAlive: opts.ProcessAlive,
	}
	if q.slots <= 0 {
		q.slots = DefaultSlots
	}
	if q.staleAfter <= 0 {
		q.staleAfter = DefaultStaleAfter
	}
	if q.poll <= 0 {
		q.poll = DefaultPoll
	}
	if q.now == nil {
		q.now = time.Now
	}
	if q.processAlive == nil {
		q.processAlive = processAlive
	}
	for _, sub := range []string{q.ticketDir(), q.slotDir()} {
		if err := os.MkdirAll(sub, 0o755); err != nil {
			return nil, fmt.Errorf("commitqueue: create %s: %w", sub, err)
		}
	}
	return q, nil
}

// Dir is the state directory.
func (q *Queue) Dir() string { return q.dir }

// SlotCount is how many slots the queue hands out.
func (q *Queue) SlotCount() int { return q.slots }

func (q *Queue) ticketDir() string { return filepath.Join(q.dir, "tickets") }
func (q *Queue) slotDir() string   { return filepath.Join(q.dir, "slots") }
func (q *Queue) lockPath() string  { return filepath.Join(q.dir, "queue.lock") }

func (q *Queue) ticketPath(id string) string {
	return filepath.Join(q.ticketDir(), id+".json")
}

func (q *Queue) slotPath(n int) string {
	return filepath.Join(q.slotDir(), fmt.Sprintf("slot-%d.json", n))
}
