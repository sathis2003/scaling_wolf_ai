package database

import (
    "context"
    "log"
    "net"
    "time"

    "github.com/jackc/pgx/v5"
    "github.com/jackc/pgx/v5/pgxpool"
)

var Pool *pgxpool.Pool

func Connect(databaseURL string) {
    // Parse DSN and force IPv4 via custom dialer
    cfg, err := pgxpool.ParseConfig(databaseURL)
    if err != nil {
        log.Fatalf("db parse config error: %v", err)
    }

    // Allow IPv6 first with automatic IPv4 fallback (Happy Eyeballs)
    // and prefer the simple protocol for broader compatibility (e.g., proxies).
    cfg.ConnConfig.DefaultQueryExecMode = pgx.QueryExecModeSimpleProtocol
    cfg.ConnConfig.Config.DialFunc = (&net.Dialer{
        Timeout:   5 * time.Second,
        DualStack: true,
    }).DialContext

    ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
    defer cancel()

    pool, err := pgxpool.NewWithConfig(ctx, cfg)
    if err != nil {
        log.Fatalf("db connect error: %v", err)
    }
    if err := pool.Ping(ctx); err != nil {
        log.Fatalf("db ping error: %v", err)
    }
    Pool = pool
}
