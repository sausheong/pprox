# pprox

You are to create a PostgreSQL-compatible proxy server that speaks the PostgreSQL Frontend/Backend protocol directly to clients (psql, applications, etc.) and routes SQL queries to different backend PostgreSQL servers based on whether they are read or write queries.

## Goal

Implement a proxy that:
	•	Accepts PostgreSQL client connections using the standard protocol on a configurable TCP port (e.g., :54329).
	•	Routes:
	•	SELECT / SHOW / EXPLAIN statements to one read replica server.
	•	INSERT / UPDATE / DELETE / DDL statements to two or more write servers, executing the same SQL on each one inside a transaction (fan-out).
	•	Returns the result or error back to the client as if it came from a single Postgres server.

## Functional Requirements

1. Protocol Handling
	•	Implement the PostgreSQL Frontend/Backend simple query protocol (StartupMessage, Query, ErrorResponse, CommandComplete, etc.).
	•	Support:
	•	Authentication OK (no real auth, just pass-through or “trust” mode for now)
	•	Simple Query messages (Q)
	•	Termination message (X)
	•	Basic ready state (ReadyForQuery)
	•	Optional: Add SSL negotiation (SSLRequest → respond N).

2. Query Routing Logic
	•	Parse or heuristically classify SQL statements:
	•	If starts with SELECT, SHOW, or EXPLAIN → read
	•	Otherwise → write
	•	Reads → forward to reader DSN
	•	Writes → fan-out to all writer DSNs
	•	Start a transaction on each
	•	Execute the same SQL
	•	Commit all if all succeed
	•	Roll back all if any fail

3. Connections
	•	Accept multiple client connections concurrently.
	•	Maintain one connection per backend per request (connection pooling optional later).
	•	Each backend connection uses standard PostgreSQL client (libpq/pgx).
	•	Environment variables for configuration:
	•	PROXY_ADDR (default :54329)
	•	PG_READER_DSN
	•	PG_WRITERS_CSV (comma-separated list of writer DSNs)

4. Error Handling
	•	If any writer fails during a fan-out:
	•	Rollback all open writer transactions.
	•	Return an aggregated ErrorResponse to the client.
	•	If reader query fails:
	•	Return an appropriate Postgres error message.

5. Result Serialization
	•	Use PostgreSQL protocol messages to relay:
	•	RowDescription
	•	DataRow
	•	CommandComplete
	•	ReadyForQuery
	•	Preserve column metadata from backend response.

6. Client Compatibility
	•	Must be compatible with:
	•	psql
	•	standard drivers (Python psycopg2, Go pgx, etc.)
	•	Client DSN example:

postgresql://user:pass@localhost:54329/dbname



## Technical Design
	•	Language: Go (preferred)
	•	Libraries:
	•	github.com/jackc/pgproto3/v2 — for raw PostgreSQL protocol encoding/decoding
	•	github.com/jackc/pgx/v5 — for backend connections to real PostgreSQL servers
	•	Architecture:
	•	Listener accepts TCP connections.
	•	Per client, a goroutine handles message parsing (using pgproto3.Backend).
	•	Router dispatches queries based on type.
	•	Results written back through the same protocol stream.

## Example Flow

### Read

Client → Proxy: "SELECT * FROM users;"
Proxy → Reader: "SELECT * FROM users;"
Reader → Proxy → Client: Result rows

### Write

Client → Proxy: "INSERT INTO users VALUES (1,'A');"
Proxy → Writers[0..n]:
  BEGIN
  INSERT INTO users VALUES (1,'A');
  COMMIT
All succeed → OK → Client
Any fail → rollback all → ErrorResponse

## Test Plan
	1.	Basic Read
	•	Run psql against the proxy.
	•	Execute SELECT 1; → routed to reader only.
	2.	Write Fan-out
	•	Run INSERT INTO test VALUES (1); → applied on both writers.
	3.	Error Propagation
	•	Induce an error on one writer → proxy returns Postgres ERROR message.
	4.	Concurrent Clients
	•	Connect multiple clients, ensure isolation.
	5.	Failover / Timeout
	•	If one writer is down, rollback and error out cleanly.

## Enhancements (optional, phase 2)
	•	Support Extended Query Protocol (Parse/Bind/Execute).
	•	Add connection pooling for writers/readers.
	•	Implement TLS and SCRAM authentication.
	•	Add metrics (Prometheus endpoint).
	•	Add config file instead of env vars.
	•	Support logical replication awareness for read consistency.

## Deliverables
	1.	main.go — full proxy implementation.
	2.	go.mod — module definition.
	3.	Example environment setup (.env).
	4.	README.md with:
	•	Architecture diagram
	•	Usage instructions
	•	Example DSNs
	•	Limitations & roadmap
