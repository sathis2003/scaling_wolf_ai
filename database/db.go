package database

import (
    "context"
    "fmt"
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
    // Prefer IPv4 at DNS resolution time: resolve via IPv4-only public DNS and return A records only.
    cfg.ConnConfig.Config.LookupFunc = func(ctx context.Context, host string) ([]string, error) {
        resolvers := []string{"1.1.1.1:53", "8.8.8.8:53"}
        v4s := make([]string, 0, 4)
        for _, dns := range resolvers {
            r := &net.Resolver{
                PreferGo: true,
                Dial: func(ctx context.Context, network, address string) (net.Conn, error) {
                    d := net.Dialer{Timeout: 2 * time.Second}
                    return d.DialContext(ctx, "udp4", dns)
                },
            }
            ips, err := r.LookupIPAddr(ctx, host)
            if err != nil {
                continue
            }
            for _, ip := range ips {
                if v4 := ip.IP.To4(); v4 != nil {
                    v4s = append(v4s, v4.String())
                }
            }
            if len(v4s) > 0 {
                break
            }
        }
        if len(v4s) == 0 {
            return nil, fmt.Errorf("no IPv4 addresses found for host %s", host)
        }
        return v4s, nil
    }

    // Force IPv4 dialing only.
    cfg.ConnConfig.Config.DialFunc = func(ctx context.Context, _ string, addr string) (net.Conn, error) {
        d := &net.Dialer{Timeout: 5 * time.Second}
        return d.DialContext(ctx, "tcp4", addr)
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
