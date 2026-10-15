package commitqueue

import (
	"context"
	"fmt"
	"time"
)

// Reclaimed describes an entry removed because its owner was gone or it
// was held too long.
type Reclaimed struct {
	Kind   string // "ticket" or "slot"
	Name   string
	Repo   string
	Slot   int // 0 for a ticket
	Reason string
}

// Acquire queues the caller and blocks until it holds a slot, ctx is done,
// or an error occurs. Callers are served strictly in arrival order. onWait,
// if set, is called once with the caller's position (1 = next) when it has
// to wait at all. onReclaim, if set, is called for each dead or stale entry
// removed while waiting.
func (q *Queue) Acquire(ctx context.Context, c Caller, onWait func(position int), onReclaim func(Reclaimed)) (Slot, error) {
	if c.Name == "" {
		return Slot{}, fmt.Errorf("commitqueue: caller has no name")
	}
	ticket, reclaimed, err := q.enqueue(c)
	report := func(rs []Reclaimed) {
		if onReclaim == nil {
			return
		}
		for _, r := range rs {
			onReclaim(r)
		}
	}
	report(reclaimed)
	if err != nil {
		return Slot{}, err
	}
	waited := false
	for {
		slot, position, reclaimed, err := q.tryGrant(ticket, c)
		report(reclaimed)
		if err != nil {
			_ = q.dropTicket(ticket)
			return Slot{}, err
		}
		if slot != nil {
			return *slot, nil
		}
		if !waited && onWait != nil {
			onWait(position)
		}
		waited = true
		select {
		case <-ctx.Done():
			_ = q.dropTicket(ticket)
			return Slot{}, ctx.Err()
		case <-time.After(q.poll):
		}
	}
}

// enqueue writes the caller's ticket, refusing a name already waiting or
// holding a slot for the same repo. It reaps dead entries first so a name
// left behind by a crashed run can be queued again, and returns what it
// reaped.
func (q *Queue) enqueue(c Caller) (Ticket, []Reclaimed, error) {
	now := q.now().UnixNano()
	t := Ticket{
		ID:       fmt.Sprintf("%020d-%d", now, c.waiterPID()),
		Name:     c.Name,
		Repo:     c.Repo,
		PID:      c.waiterPID(),
		QueuedAt: now,
	}
	var reclaimed []Reclaimed
	err := q.withLock(func() error {
		var err error
		if reclaimed, err = q.reap(); err != nil {
			return err
		}
		if taken, err := q.nameTaken(c.Name, c.Repo); err != nil {
			return err
		} else if taken {
			return fmt.Errorf("commitqueue: %q is already queued or holding a slot for %s", c.Name, c.Repo)
		}
		return writeNewJSON(q.ticketPath(t.ID), t)
	})
	return t, reclaimed, err
}

// tryGrant gives the caller a slot if one is free and its ticket is among
// the oldest that the free slots can serve. It returns the slot, or nil and
// the caller's 1-based position in line.
func (q *Queue) tryGrant(t Ticket, c Caller) (*Slot, int, []Reclaimed, error) {
	var granted *Slot
	position := 0
	var reclaimed []Reclaimed
	err := q.withLock(func() error {
		var err error
		if reclaimed, err = q.reap(); err != nil {
			return err
		}
		held, err := q.heldSlots()
		if err != nil {
			return err
		}
		waiting, err := q.tickets()
		if err != nil {
			return err
		}
		position = indexOfTicket(waiting, t.ID) + 1
		if position == 0 {
			return fmt.Errorf("commitqueue: ticket for %q disappeared", c.Name)
		}
		free := freeSlotNumbers(held, q.slots)
		if position > len(free) {
			return nil
		}
		slot := Slot{
			Number:    free[position-1],
			Name:      c.Name,
			Repo:      c.Repo,
			PID:       c.slotPID(),
			QueuedAt:  t.QueuedAt,
			StartedAt: q.now().UnixNano(),
		}
		if err := writeNewJSON(q.slotPath(slot.Number), slot); err != nil {
			return err
		}
		granted = &slot
		return removeIfExists(q.ticketPath(t.ID))
	})
	return granted, position, reclaimed, err
}

// Release frees every slot held, and drops every ticket waiting, under this
// name and repo. It returns the slot numbers freed.
func (q *Queue) Release(name, repo string) ([]int, error) {
	var freed []int
	err := q.withLock(func() error {
		held, err := q.heldSlots()
		if err != nil {
			return err
		}
		for _, s := range held {
			if s.Name == name && s.Repo == repo {
				if err := removeIfExists(q.slotPath(s.Number)); err != nil {
					return err
				}
				freed = append(freed, s.Number)
			}
		}
		waiting, err := q.tickets()
		if err != nil {
			return err
		}
		for _, t := range waiting {
			if t.Name == name && t.Repo == repo {
				if err := removeIfExists(q.ticketPath(t.ID)); err != nil {
					return err
				}
			}
		}
		return nil
	})
	return freed, err
}

func (q *Queue) dropTicket(t Ticket) error {
	return q.withLock(func() error { return removeIfExists(q.ticketPath(t.ID)) })
}

// reap removes tickets whose waiter exited and slots whose owner exited or
// that were held longer than StaleAfter. Call it with the lock held.
func (q *Queue) reap() ([]Reclaimed, error) {
	var out []Reclaimed
	waiting, err := q.tickets()
	if err != nil {
		return nil, err
	}
	for _, t := range waiting {
		if q.processAlive(t.PID) {
			continue
		}
		if err := removeIfExists(q.ticketPath(t.ID)); err != nil {
			return nil, err
		}
		out = append(out, Reclaimed{Kind: "ticket", Name: t.Name, Repo: t.Repo, Reason: fmt.Sprintf("waiter pid %d exited", t.PID)})
	}
	held, err := q.heldSlots()
	if err != nil {
		return nil, err
	}
	now := q.now()
	for _, s := range held {
		reason := ""
		switch {
		case !q.processAlive(s.PID):
			reason = fmt.Sprintf("owner pid %d exited", s.PID)
		case now.Sub(time.Unix(0, s.StartedAt)) > q.staleAfter:
			reason = fmt.Sprintf("held longer than %s", q.staleAfter)
		default:
			continue
		}
		if err := removeIfExists(q.slotPath(s.Number)); err != nil {
			return nil, err
		}
		out = append(out, Reclaimed{Kind: "slot", Name: s.Name, Repo: s.Repo, Slot: s.Number, Reason: reason})
	}
	return out, nil
}

func (q *Queue) nameTaken(name, repo string) (bool, error) {
	held, err := q.heldSlots()
	if err != nil {
		return false, err
	}
	for _, s := range held {
		if s.Name == name && s.Repo == repo {
			return true, nil
		}
	}
	waiting, err := q.tickets()
	if err != nil {
		return false, err
	}
	for _, t := range waiting {
		if t.Name == name && t.Repo == repo {
			return true, nil
		}
	}
	return false, nil
}

// freeSlotNumbers lists the unheld slot numbers in 1..total, lowest first.
func freeSlotNumbers(held []Slot, total int) []int {
	taken := make(map[int]bool, len(held))
	for _, s := range held {
		taken[s.Number] = true
	}
	var free []int
	for n := 1; n <= total; n++ {
		if !taken[n] {
			free = append(free, n)
		}
	}
	return free
}

func indexOfTicket(ts []Ticket, id string) int {
	for i, t := range ts {
		if t.ID == id {
			return i
		}
	}
	return -1
}
