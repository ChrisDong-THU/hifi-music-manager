package main

import (
	"errors"
	"sort"

	bolt "go.etcd.io/bbolt"
)

const historyLimit = 99

func (a *app) checkOperation() error {
	if a.ctx != nil {
		if err := a.ctx.Err(); err != nil {
			return err
		}
	}
	return a.checkStorage()
}

func storeOperationTx(tx *bolt.Tx, op *Operation) error {
	b := tx.Bucket([]byte("operations"))
	if op.Sequence == 0 {
		seq, err := b.NextSequence()
		if err != nil {
			return err
		}
		op.Sequence = seq
	}
	raw, err := marshal(op)
	if err != nil {
		return err
	}
	if err := b.Put([]byte(op.ID), raw); err != nil {
		return err
	}
	return trimOperationHistoryTx(tx)
}

func trimOperationHistoryTx(tx *bolt.Tx) error {
	ops, err := readAllTx[Operation](tx, "operations")
	if err != nil {
		return err
	}
	sort.Slice(ops, func(i, j int) bool { return ops[i].Sequence > ops[j].Sequence })
	b := tx.Bucket([]byte("operations"))
	for _, old := range ops[min(historyLimit, len(ops)):] {
		if err := b.Delete([]byte(old.ID)); err != nil {
			return err
		}
	}
	return nil
}

func (a *app) saveOperation(op *Operation) error {
	return a.db.Update(func(tx *bolt.Tx) error { return storeOperationTx(tx, op) })
}

func recentOperationsTx(tx *bolt.Tx) ([]Operation, error) {
	ops, err := readAllTx[Operation](tx, "operations")
	sort.Slice(ops, func(i, j int) bool { return ops[i].Sequence > ops[j].Sequence })
	return ops[:min(historyLimit, len(ops))], err
}

func (a *app) begin(kind, label string) (Operation, error) {
	op := Operation{ID: uid(), Kind: kind, Label: label, Created: now(), State: "working"}
	if err := a.checkOperation(); err != nil {
		return op, err
	}
	err := a.saveOperation(&op)
	return op, err
}

func (a *app) finish(op *Operation) error {
	op.State, op.CompletedAt = "done", now()
	return a.saveOperation(op)
}

func (a *app) fail(op *Operation, err error) error {
	op.State, op.CompletedAt, op.Error = "failed", now(), err.Error()
	return errors.Join(err, a.saveOperation(op))
}

// Commit each published file immediately; a later failure preserves preceding writes.
func storeTrackTx(tx *bolt.Tx, t Track) error {
	raw, err := marshal(t)
	if err != nil {
		return err
	}
	b := tx.Bucket([]byte("tracks"))
	if _, err = b.NextSequence(); err != nil {
		return err
	}
	return b.Put([]byte(t.ID), raw)
}

func (a *app) recordTrack(op *Operation, t *Track, deletedID string) error {
	return a.db.Update(func(tx *bolt.Tx) error {
		if t != nil {
			if err := storeTrackTx(tx, *t); err != nil {
				return err
			}
		} else {
			b := tx.Bucket([]byte("tracks"))
			if _, err := b.NextSequence(); err != nil {
				return err
			}
			if err := b.Delete([]byte(deletedID)); err != nil {
				return err
			}
		}
		return storeOperationTx(tx, op)
	})
}

func (a *app) wakeMaintenance() {
	select {
	case a.maintenanceWake <- struct{}{}:
	default:
	}
}
