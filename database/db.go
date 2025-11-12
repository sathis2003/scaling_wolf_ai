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

    // Prefer simple protocol for broader compatibility (e.g., proxies).
    cfg.ConnConfig.DefaultQueryExecMode = pgx.QueryExecModeSimpleProtocol
    // Force IPv4 first to avoid environments without IPv6 routing (e.g., some hosts)
    cfg.ConnConfig.Config.DialFunc = func(ctx context.Context, network, addr string) (net.Conn, error) {
        d := &net.Dialer{Timeout: 5 * time.Second}
        if conn, err := d.DialContext(ctx, "tcp4", addr); err == nil {
            return conn, nil
        }
        // Fallback to default tcp (which may try IPv6/IPv4 as available)
        return d.DialContext(ctx, "tcp", addr)
    }

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
