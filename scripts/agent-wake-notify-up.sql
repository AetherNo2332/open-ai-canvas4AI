BEGIN;
SET LOCAL lock_timeout = '2s';
CREATE OR REPLACE FUNCTION canvas_agent_wake_signal() RETURNS trigger
LANGUAGE plpgsql AS $$
BEGIN
  PERFORM pg_notify('canvas_agent_wake', '');
  RETURN NULL;
END;
$$;
CREATE OR REPLACE TRIGGER canvas_agent_wake_signal
AFTER INSERT ON agent_wake_events
FOR EACH STATEMENT EXECUTE FUNCTION canvas_agent_wake_signal();
COMMIT;
