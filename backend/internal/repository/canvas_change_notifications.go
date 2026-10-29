package repository

import "gorm.io/gorm"

const (
	CanvasChangeActorAgent   = "agent"
	CanvasChangeActorBrowser = "browser"
)

// CanvasChange is published after the outer transaction commits. Revision is 0
// for changes that do not create a document revision (leases, deletion, tasks).
type CanvasChange struct {
	UserID, CanvasID string
	Revision         int64
	Actor            string
}

type canvasChangeKey struct{ userID, canvasID string }

// Transaction descendants collect invalidations until the outer commit succeeds.
func (r *Repository) WithCanvasChangeNotifier(notify func(CanvasChange)) *Repository {
	copy := *r
	copy.canvasChangeNotifier = notify
	return &copy
}

func (r *Repository) canvasChanged(userID, canvasID string) {
	r.recordCanvasChange(CanvasChange{UserID: userID, CanvasID: canvasID})
}

func (r *Repository) canvasRevisionChanged(userID, canvasID string, revision int64, actor string) {
	r.recordCanvasChange(CanvasChange{UserID: userID, CanvasID: canvasID, Revision: revision, Actor: actor})
}

func (r *Repository) recordCanvasChange(change CanvasChange) {
	if r.canvasChangeNotifier == nil {
		return
	}
	if r.canvasChanges != nil {
		key := canvasChangeKey{change.UserID, change.CanvasID}
		// One commit announces the newest revision it produced for each canvas.
		if previous, ok := r.canvasChanges[key]; !ok || change.Revision >= previous.Revision {
			r.canvasChanges[key] = change
		}
		return
	}
	r.canvasChangeNotifier(change)
}

// transact runs fn in a (possibly nested) transaction whose canvas changes are
// released only after it commits: to the notifier at the outermost level, or
// to the parent transaction's buffer when nested.
func (r *Repository) transact(db *gorm.DB, fn func(*Repository) error) error {
	changes := make(map[canvasChangeKey]CanvasChange)
	err := db.Transaction(func(tx *gorm.DB) error {
		writer := r.transactionRepository(tx)
		writer.canvasChanges = changes
		return fn(writer)
	})
	if err == nil {
		for _, change := range changes {
			r.recordCanvasChange(change)
		}
	}
	return err
}
