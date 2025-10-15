package main

import (
	"context"
	"fmt"
	"log"
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
// If a writer fails or times out, it is skipped and the operation continues with remaining writers
func (r *Router) ExecuteWriteWithParams(ctx context.Context, sql string, params ...interface{}) error {
	if len(r.config.WriterDSNs) == 0 {
		return fmt.Errorf("no writer backends configured")
	}

	type writerConn struct {
		conn *pgx.Conn
		tx   pgx.Tx
		dsn  string
	}

	var successfulWriters []writerConn
	var failedWriters []string

	// Cleanup function for successful writers
	cleanup := func(rollback bool) {
		for _, w := range successfulWriters {
			if rollback && w.tx != nil {
				w.tx.Rollback(ctx)
			}
			if w.conn != nil {
				w.conn.Close(ctx)
			}
		}
	}

	// Try to connect to each writer
	for _, dsn := range r.config.WriterDSNs {
		conn, err := r.connectToBackend(ctx, dsn)
		if err != nil {
			// Log and skip this writer
			log.Printf("Warning: failed to connect to writer %s: %v", dsn, err)
			failedWriters = append(failedWriters, dsn)
			continue
		}
		successfulWriters = append(successfulWriters, writerConn{conn: conn, dsn: dsn})
	}

	// Check if at least one writer is available
	if len(successfulWriters) == 0 {
		return fmt.Errorf("no writer backends available (all %d writers failed)", len(r.config.WriterDSNs))
	}

	// Begin transactions on successful writers
	writersWithTx := make([]writerConn, 0, len(successfulWriters))
	for i := range successfulWriters {
		tx, err := successfulWriters[i].conn.Begin(ctx)
		if err != nil {
			log.Printf("Warning: failed to begin transaction on writer %s: %v", successfulWriters[i].dsn, err)
			// Close this connection
			successfulWriters[i].conn.Close(ctx)
			failedWriters = append(failedWriters, successfulWriters[i].dsn)
			continue
		}
		successfulWriters[i].tx = tx
		writersWithTx = append(writersWithTx, successfulWriters[i])
	}
	successfulWriters = writersWithTx

	// Check again if we have any writers left
	if len(successfulWriters) == 0 {
		return fmt.Errorf("no writer backends available (failed to begin transactions)")
	}

	// Execute query on all successful writers
	for i := range successfulWriters {
		_, err := successfulWriters[i].tx.Exec(ctx, sql, params...)
		if err != nil {
			log.Printf("Warning: failed to execute on writer %s: %v", successfulWriters[i].dsn, err)
			cleanup(true)
			return fmt.Errorf("failed to execute on writer %s: %w", successfulWriters[i].dsn, err)
		}
	}

	// Commit all transactions
	for i := range successfulWriters {
		err := successfulWriters[i].tx.Commit(ctx)
		if err != nil {
			log.Printf("Warning: failed to commit on writer %s: %v", successfulWriters[i].dsn, err)
			cleanup(true)
			return fmt.Errorf("failed to commit on writer %s: %w", successfulWriters[i].dsn, err)
		}
	}

	// Close all connections
	cleanup(false)

	// Log summary
	if len(failedWriters) > 0 {
		log.Printf("Write completed on %d/%d writers (failed: %v)", 
			len(successfulWriters), len(r.config.WriterDSNs), failedWriters)
	}

	return nil
}
