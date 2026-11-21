package database

import "gorm.io/gorm"

func init() {
	schemaMigrations = append(schemaMigrations, migration{
		version: 110, name: "agent_wake_notification", checksum: "agent-wake-notification-v110-20261121",
		apply: migrateAgentWakeNotification,
	})
}

func migrateAgentWakeNotification(tx *gorm.DB) error {
	if tx.Dialector.Name() != "postgres" {
		return nil
	}
	if err := tx.Exec(`CREATE OR REPLACE FUNCTION canvas_agent_wake_signal() RETURNS trigger
LANGUAGE plpgsql AS $$
BEGIN
  PERFORM pg_notify('canvas_agent_wake', '');
  RETURN NULL;
END;
$$`).Error; err != nil {
		return err
	}
	return tx.Exec(`CREATE OR REPLACE TRIGGER canvas_agent_wake_signal
AFTER INSERT ON agent_wake_events
FOR EACH STATEMENT EXECUTE FUNCTION canvas_agent_wake_signal()`).Error
}
