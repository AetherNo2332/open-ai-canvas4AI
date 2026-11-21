BEGIN;
SET LOCAL lock_timeout = '2s';
DROP TRIGGER IF EXISTS canvas_agent_wake_signal ON agent_wake_events;
DROP FUNCTION IF EXISTS canvas_agent_wake_signal();
COMMIT;
