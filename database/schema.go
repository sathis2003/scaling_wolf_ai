package database

import (
    "context"
    "log"
)

// EnsureSchema creates required extensions and tables if they do not exist.
func EnsureSchema() {
    if Pool == nil { return }
    ctx := context.Background()

    stmts := []string{
        `CREATE EXTENSION IF NOT EXISTS vector`,
        `CREATE TABLE IF NOT EXISTS chats (
            id BIGSERIAL PRIMARY KEY,
            user_id BIGINT NOT NULL,
            title TEXT,
            created_at TIMESTAMPTZ NOT NULL DEFAULT now()
        )`,
        `CREATE TABLE IF NOT EXISTS chat_messages (
            id BIGSERIAL PRIMARY KEY,
            chat_id BIGINT NOT NULL REFERENCES chats(id) ON DELETE CASCADE,
            role TEXT NOT NULL,
            content TEXT NOT NULL,
            created_at TIMESTAMPTZ NOT NULL DEFAULT now()
        )`,
        `CREATE TABLE IF NOT EXISTS sales_metrics (
            id BIGSERIAL PRIMARY KEY,
            user_id BIGINT NOT NULL,
            source_type TEXT NOT NULL, -- 'file' or 'text'
            payload JSONB NOT NULL,
            total_sales NUMERIC,
            bill_row_count INT,
            unique_bill_count INT,
            created_at TIMESTAMPTZ NOT NULL DEFAULT now()
        )`,
        `CREATE TABLE IF NOT EXISTS rag_documents (
            id BIGSERIAL PRIMARY KEY,
            user_id BIGINT NOT NULL,
            content TEXT NOT NULL,
            metadata JSONB NOT NULL DEFAULT '{}'::jsonb,
            embedding vector(768) NOT NULL,
            created_at TIMESTAMPTZ NOT NULL DEFAULT now()
        )`,
        `CREATE INDEX IF NOT EXISTS rag_documents_user_id_idx ON rag_documents(user_id)`,
        `CREATE INDEX IF NOT EXISTS rag_documents_embedding_idx ON rag_documents USING ivfflat (embedding vector_l2_ops) WITH (lists = 100)`,
        `CREATE TABLE IF NOT EXISTS column_mappings (
            id BIGSERIAL PRIMARY KEY,
            user_id BIGINT NOT NULL,
            signature TEXT NOT NULL,
            header_row INT NOT NULL,
            sales_column TEXT NOT NULL,
            bill_column TEXT NOT NULL,
            created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
            UNIQUE(user_id, signature)
        )`,
        `CREATE TABLE IF NOT EXISTS bep_results (
            id BIGSERIAL PRIMARY KEY,
            user_id BIGINT NOT NULL,
            source_metrics_id BIGINT NULL REFERENCES sales_metrics(id) ON DELETE SET NULL,
            fixed_cost NUMERIC NOT NULL,
            variable_cost_rate NUMERIC NULL,
            variable_cost_per_bill NUMERIC NULL,
            gross_margin_rate NUMERIC NULL,
            avg_revenue_per_bill NUMERIC NOT NULL,
            contribution_per_bill NUMERIC NOT NULL,
            bep_bills INT NOT NULL,
            bep_sales NUMERIC NOT NULL,
            created_at TIMESTAMPTZ NOT NULL DEFAULT now()
        )`,
        `CREATE INDEX IF NOT EXISTS bep_results_user_id_idx ON bep_results(user_id, created_at DESC)`,
        `CREATE TABLE IF NOT EXISTS token_quotas (
            user_id BIGINT PRIMARY KEY,
            token_quota BIGINT NOT NULL DEFAULT 50000, -- default 5 points = 50k
            token_used  BIGINT NOT NULL DEFAULT 0,
            updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
        )`,
    }

    for _, s := range stmts {
        if _, err := Pool.Exec(ctx, s); err != nil {
            log.Printf("schema ensure error: %v in stmt: %s", err, s)
        }
    }
}
