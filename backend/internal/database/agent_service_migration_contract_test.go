package database

import "testing"

func TestAgentServiceMigrationContractCoversRuntimeIdentity(t *testing.T) {
	contract := AgentServiceMigrationContract()
	if len(contract) == 0 {
		t.Fatal("agent service migration contract is empty")
	}

	want := map[string]string{
		"agent_sessions":              "session",
		"agent_session_messages":      "message",
		"agent_memory_notes":          "memory",
		"agent_work_items":            "work",
		"agent_assignments":           "assignment",
		"agent_work_reports":          "report",
		"agent_work_inputs":           "work_input",
		"agent_outboxes":              "outbox",
		"agent_approvals":             "approval",
		"agent_turns":                 "turn",
		"agent_operations":            "operation_receipt",
		"agent_turn_contracts":        "frozen_contract",
		"agent_ecommerce_batches":     "business_batch",
		"agent_tool_operations":       "operation",
		"cloud_agent_executions":      "run",
		"cloud_agent_event_records":   "event",
		"cloud_agent_message_records": "run_message",
		"cloud_agent_pi_sessions":     "pi_session",
		"cloud_agent_pi_entries":      "pi_entry",
		"platform_budget_grants":      "budget",
		"platform_budget_charges":     "charge",
	}

	got := make(map[string]AgentMigrationTable, len(contract))
	for _, table := range contract {
		if _, exists := got[table.Name]; exists {
			t.Fatalf("duplicate migration contract table %q", table.Name)
		}
		got[table.Name] = table
	}
	for name, role := range want {
		table, ok := got[name]
		if !ok {
			t.Errorf("migration contract is missing %q", name)
			continue
		}
		if table.Role != role {
			t.Errorf("migration contract role for %q = %q, want %q", name, table.Role, role)
		}
		if !table.PreservePrimaryKeys || !table.PreserveOrdering {
			t.Errorf("migration contract for %q must preserve IDs and ordering: %+v", name, table)
		}
	}
}
