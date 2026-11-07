package database

// AgentMigrationTable is the cross-database migration contract for state that
// participates in Agent execution. The copier must retain primary keys and
// source ordering so retries, leases, cursors and Pi history keep their
// identity after SQLite -> PostgreSQL migration.
type AgentMigrationTable struct {
	Name                string
	Role                string
	PreservePrimaryKeys bool
	PreserveOrdering    bool
}

// AgentServiceMigrationContract lists the Agent service tables whose rows are
// runtime facts. Keep this list explicit even though Models contains the whole
// application schema: it is the reviewable C-stage checklist for Session,
// work, operation, budget and Pi history continuity.
func AgentServiceMigrationContract() []AgentMigrationTable {
	return []AgentMigrationTable{
		{Name: "agent_sessions", Role: "session", PreservePrimaryKeys: true, PreserveOrdering: true},
		{Name: "agent_session_messages", Role: "message", PreservePrimaryKeys: true, PreserveOrdering: true},
		{Name: "agent_memory_notes", Role: "memory", PreservePrimaryKeys: true, PreserveOrdering: true},
		{Name: "agent_work_items", Role: "work", PreservePrimaryKeys: true, PreserveOrdering: true},
		{Name: "agent_assignments", Role: "assignment", PreservePrimaryKeys: true, PreserveOrdering: true},
		{Name: "agent_work_reports", Role: "report", PreservePrimaryKeys: true, PreserveOrdering: true},
		{Name: "agent_work_inputs", Role: "work_input", PreservePrimaryKeys: true, PreserveOrdering: true},
		{Name: "agent_outboxes", Role: "outbox", PreservePrimaryKeys: true, PreserveOrdering: true},
		{Name: "agent_approvals", Role: "approval", PreservePrimaryKeys: true, PreserveOrdering: true},
		{Name: "agent_turns", Role: "turn", PreservePrimaryKeys: true, PreserveOrdering: true},
		{Name: "agent_operations", Role: "operation_receipt", PreservePrimaryKeys: true, PreserveOrdering: true},
		{Name: "agent_turn_contracts", Role: "frozen_contract", PreservePrimaryKeys: true, PreserveOrdering: true},
		{Name: "agent_ecommerce_batches", Role: "business_batch", PreservePrimaryKeys: true, PreserveOrdering: true},
		{Name: "agent_tool_operations", Role: "operation", PreservePrimaryKeys: true, PreserveOrdering: true},
		{Name: "platform_budget_grants", Role: "budget", PreservePrimaryKeys: true, PreserveOrdering: true},
		{Name: "platform_budget_charges", Role: "charge", PreservePrimaryKeys: true, PreserveOrdering: true},
		{Name: "cloud_agent_executions", Role: "run", PreservePrimaryKeys: true, PreserveOrdering: true},
		{Name: "cloud_agent_event_records", Role: "event", PreservePrimaryKeys: true, PreserveOrdering: true},
		{Name: "cloud_agent_message_records", Role: "run_message", PreservePrimaryKeys: true, PreserveOrdering: true},
		{Name: "cloud_agent_canvas_mutations", Role: "resource_mutation", PreservePrimaryKeys: true, PreserveOrdering: true},
		{Name: "cloud_agent_pi_sessions", Role: "pi_session", PreservePrimaryKeys: true, PreserveOrdering: true},
		{Name: "cloud_agent_pi_entries", Role: "pi_entry", PreservePrimaryKeys: true, PreserveOrdering: true},
	}
}
