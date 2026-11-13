// Package previs owns shared pure scene editing rules. Authentication,
// transactions, CAS, approvals and events remain in the two controlled adapters.
package previs

type IDPolicy int

const (
	AgentIDs IDPolicy = iota
	CommandIDs
)

type SceneUpdate struct {
	ID                   string
	Title                *string
	Background           *string
	EnvironmentIntensity *float64
	GridVisible          *bool
	ActiveShotID         *string
}

type EditResult struct {
	Operation string
	ID        string
	Title     string
	Summary   string
}

type ValidationError struct {
	Message string
	Plain   bool
}

func (e *ValidationError) Error() string { return e.Message }
