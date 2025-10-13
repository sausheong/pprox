package main

import (
	"context"
	"fmt"
	"strings"

	"github.com/jackc/pgx/v5"
)

// QueryType represents the type of SQL query
type QueryType int

const (
	QueryTypeRead QueryType = iota
	QueryTypeWrite
)

// ClassifyQuery determines if a query is a read or write operation
func ClassifyQuery(sql string) QueryType {
	trimmed := strings.TrimSpace(sql)
	upper := strings.ToUpper(trimmed)

	// Check for read operations
	if strings.HasPrefix(upper, "SELECT") ||
		strings.HasPrefix(upper, "SHOW") ||
		strings.HasPrefix(upper, "EXPLAIN") {
		return QueryTypeRead
	}

	// Everything else is considered a write
	return QueryTypeWrite
}

// Router handles query routing to appropriate backends
type Router struct {
	config *Config
}

// NewRouter creates a new Router instance
func NewRouter(config *Config) *Router {
	return &Router{config: config}
}

// connectToBackend creates a connection to a backend database with TLS if configured
func (r *Router) connectToBackend(ctx context.Context, dsn string) (*pgx.Conn, error) {
	config, err := pgx.ParseConfig(dsn)
	if err != nil {
		return nil, fmt.Errorf("failed to parse DSN: %w", err)
	}

	// Apply backend TLS configuration if enabled
	if r.config.BackendTLS.Enabled {
		config.TLSConfig = r.config.BackendTLS.TLS
	}

	conn, err := pgx.ConnectConfig(ctx, config)
	if err != nil {
		return nil, err
	}

	return conn, nil
}

// ExecuteRead executes a read query on the reader backend
func (r *Router) ExecuteRead(ctx context.Context, sql string) (pgx.Rows, error) {
	conn, err := r.connectToBackend(ctx, r.config.ReaderDSN)
	if err != nil {
		return nil, fmt.Errorf("failed to connect to reader: %w", err)
	}
	defer conn.Close(ctx)

	rows, err := conn.Query(ctx, sql)
	if err != nil {
		return nil, fmt.Errorf("failed to execute read query: %w", err)
	}

	return rows, nil
}

// ExecuteWrite executes a write query on all writer backends in a transaction
func (r *Router) ExecuteWrite(ctx context.Context, sql string) error {
	return r.ExecuteWriteWithParams(ctx, sql)
}

// ExecuteWriteWithParams executes a write query with parameters on all writer backends in a transaction
func (r *Router) ExecuteWriteWithParams(ctx context.Context, sql string, params ...interface{}) error {
	if len(r.config.WriterDSNs) == 0 {
		return fmt.Errorf("no writer backends configured")
	}

	// Connect to all writers
	conns := make([]*pgx.Conn, 0, len(r.config.WriterDSNs))
	txs := make([]pgx.Tx, 0, len(r.config.WriterDSNs))

	// Cleanup function
	cleanup := func() {
		for i := range txs {
			if txs[i] != nil {
				txs[i].Rollback(ctx)
			}
		}
		for i := range conns {
			if conns[i] != nil {
				conns[i].Close(ctx)
			}
		}
	}

	// Connect to all writers
	for _, dsn := range r.config.WriterDSNs {
		conn, err := r.connectToBackend(ctx, dsn)
		if err != nil {
			cleanup()
			return fmt.Errorf("failed to connect to writer %s: %w", dsn, err)
		}
		conns = append(conns, conn)
	}

	// Begin transactions on all writers
	for i, conn := range conns {
		tx, err := conn.Begin(ctx)
		if err != nil {
			cleanup()
			return fmt.Errorf("failed to begin transaction on writer %d: %w", i, err)
		}
		txs = append(txs, tx)
	}

	// Execute query on all writers
	for i, tx := range txs {
		_, err := tx.Exec(ctx, sql, params...)
		if err != nil {
			cleanup()
			return fmt.Errorf("failed to execute on writer %d: %w", i, err)
		}
	}

	// Commit all transactions
	for i, tx := range txs {
		err := tx.Commit(ctx)
		if err != nil {
			cleanup()
			return fmt.Errorf("failed to commit on writer %d: %w", i, err)
		}
	}

	// Close all connections
	for _, conn := range conns {
		conn.Close(ctx)
	}

	return nil
}
