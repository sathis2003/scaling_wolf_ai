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
    // Parse DSN and configure IPv4-only DNS resolution (Render compatible)
    cfg, err := pgxpool.ParseConfig(databaseURL)
    if err != nil {
        log.Fatalf("db parse config error: %v", err)
    }

    // Prefer simple protocol for broader compatibility (e.g., proxies).
    cfg.ConnConfig.DefaultQueryExecMode = pgx.QueryExecModeSimpleProtocol
    // Prefer IPv4 at DNS resolution time: filter to A records only.
    cfg.ConnConfig.Config.LookupFunc = func(ctx context.Context, host string) ([]string, error) {
        r := net.DefaultResolver
        ips, err := r.LookupIPAddr(ctx, host)
        if err != nil {
            return nil, err
        }
        out := make([]string, 0, len(ips))
        for _, ip := range ips {
            if v4 := ip.IP.To4(); v4 != nil {
                out = append(out, v4.String())
            }
        }
        if len(out) == 0 {
            // Fallback to original host if no IPv4 found
            return []string{host}, nil
        }
        return out, nil
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
