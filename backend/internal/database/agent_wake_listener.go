package database

import (
	"context"
	"database/sql"
	"errors"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/stdlib"
)

func OpenAgentWakeListener(ctx context.Context, pool *sql.DB) (*pgx.Conn, error) {
	borrowed, err := pool.Conn(ctx)
	if err != nil {
		return nil, err
	}
	var config *pgx.ConnConfig
	err = borrowed.Raw(func(driverConn any) error {
		conn, ok := driverConn.(*stdlib.Conn)
		if !ok {
			return errors.New("wake listener requires a pgx pool")
		}
		config = conn.Conn().Config()
		return nil
	})
	_ = borrowed.Close()
	if err != nil {
		return nil, err
	}
	config.RuntimeParams["application_name"] = "canvas-agent-wake-listener"
	conn, err := pgx.ConnectConfig(ctx, config)
	if err != nil {
		return nil, err
	}
	if _, err := conn.Exec(ctx, "LISTEN canvas_agent_wake"); err != nil {
		_ = conn.Close(ctx)
		return nil, err
	}
	return conn, nil
}
