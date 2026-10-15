package commitqueue

// Snapshot is the queue's state at one moment: who holds which slot and who
// is waiting, in the order they will be served.
type Snapshot struct {
	Slots   int      `json:"slots"`
	Held    []Slot   `json:"held"`
	Waiting []Ticket `json:"waiting"`
}

// Snapshot reads the current state without changing it. Dead entries are
// still listed; the next Acquire removes them.
func (q *Queue) Snapshot() (Snapshot, error) {
	var snap Snapshot
	err := q.withLock(func() error {
		var err error
		if snap.Held, err = q.heldSlots(); err != nil {
			return err
		}
		snap.Waiting, err = q.tickets()
		return err
	})
	snap.Slots = q.slots
	return snap, err
}
