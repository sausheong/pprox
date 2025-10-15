package main

import (
	"context"
	"crypto/tls"
	"fmt"
	"io"
	"log"
	"net"
	"time"

	"github.com/jackc/pgproto3/v2"
)

// PreparedStatement stores information about a prepared statement
type PreparedStatement struct {
	name       string
	query      string
	paramOIDs  []uint32
	queryType  QueryType
}

// Portal stores information about a bound portal
type Portal struct {
	name      string
	stmtName  string
	params    [][]byte
	formats   []int16
}

// ClientHandler handles a single client connection
type ClientHandler struct {
	conn               net.Conn
	backend            *pgproto3.Backend
	router             *Router
	preparedStmts      map[string]*PreparedStatement
	portals            map[string]*Portal
	tlsState           *tls.ConnectionState
	authenticated      bool
	username           string
}

// NewClientHandler creates a new client handler
func NewClientHandler(conn net.Conn, router *Router) *ClientHandler {
	return &ClientHandler{
		conn:          conn,
		backend:       pgproto3.NewBackend(pgproto3.NewChunkReader(conn), conn),
		router:        router,
		preparedStmts: make(map[string]*PreparedStatement),
		portals:       make(map[string]*Portal),
	}
}

// Handle processes messages from the client
func (h *ClientHandler) Handle() {
	defer h.conn.Close()

	// Handle startup
	if err := h.handleStartup(); err != nil {
		log.Printf("Startup error: %v", err)
		return
	}

	// Main message loop
	for {
		msg, err := h.backend.Receive()
		if err != nil {
			if err != io.EOF {
				log.Printf("Error receiving message: %v", err)
			}
			return
		}

		switch msg := msg.(type) {
		case *pgproto3.Query:
			h.handleQuery(msg.String)
		case *pgproto3.Parse:
			h.handleParse(msg)
		case *pgproto3.Bind:
			h.handleBind(msg)
		case *pgproto3.Execute:
			h.handleExecute(msg)
		case *pgproto3.Describe:
			h.handleDescribe(msg)
		case *pgproto3.Close:
			h.handleClose(msg)
		case *pgproto3.Sync:
			h.sendReadyForQuery('I')
		case *pgproto3.Flush:
			// Nothing to do for flush
		case *pgproto3.Terminate:
			return
		default:
			log.Printf("Unsupported message type: %T", msg)
			h.sendError("unsupported message type")
		}
	}
}

// handleStartup handles the PostgreSQL startup sequence
func (h *ClientHandler) handleStartup() error {
	startupMsg, err := h.backend.ReceiveStartupMessage()
	if err != nil {
		return fmt.Errorf("failed to receive startup message: %w", err)
	}

	switch startupMsg.(type) {
	case *pgproto3.SSLRequest:
		// Handle SSL request
		if h.router.config.TLSConfig.Enabled {
			// Accept SSL
			_, err = h.conn.Write([]byte{'S'})
			if err != nil {
				return fmt.Errorf("failed to send SSL acceptance: %w", err)
			}

			// Upgrade to TLS
			tlsConn := tls.Server(h.conn, h.router.config.TLSConfig.TLS)
			if err := tlsConn.Handshake(); err != nil {
				return fmt.Errorf("TLS handshake failed: %w", err)
			}

			// Store TLS state for channel binding
			state := tlsConn.ConnectionState()
			h.tlsState = &state

			// Update connection and backend
			h.conn = tlsConn
			h.backend = pgproto3.NewBackend(pgproto3.NewChunkReader(tlsConn), tlsConn)

			log.Printf("TLS connection established from %s", h.conn.RemoteAddr())
		} else {
			// Deny SSL
			_, err = h.conn.Write([]byte{'N'})
			if err != nil {
				return fmt.Errorf("failed to send SSL denial: %w", err)
			}
		}

		// Receive the actual startup message after SSL negotiation
		startupMsg, err = h.backend.ReceiveStartupMessage()
		if err != nil {
			return fmt.Errorf("failed to receive startup message after SSL: %w", err)
		}
	}

	switch msg := startupMsg.(type) {
	case *pgproto3.StartupMessage:
		// Extract username
		username := ""
		for k, v := range msg.Parameters {
			if k == "user" {
				username = v
				break
			}
		}

		if username == "" {
			return fmt.Errorf("username not provided in startup message")
		}

		h.username = username

		// Check if authentication is required
		if len(h.router.config.AuthConfig.Users) == 0 {
			// Trust mode - no authentication
			h.authenticated = true
			return h.sendAuthenticationOk()
		}

		// Perform SCRAM-SHA-256 authentication
		return h.performSCRAMAuth(username)
	default:
		return fmt.Errorf("unexpected startup message type: %T", startupMsg)
	}
}

// handleQuery processes a query message
func (h *ClientHandler) handleQuery(sql string) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	queryType := ClassifyQuery(sql)

	if queryType == QueryTypeRead {
		h.handleReadQuery(ctx, sql)
	} else {
		h.handleWriteQuery(ctx, sql)
	}
}

// handleReadQuery executes a read query and returns results
func (h *ClientHandler) handleReadQuery(ctx context.Context, sql string) {
	// Connect to reader
	conn, err := h.router.connectToBackend(ctx, h.router.config.ReaderDSN)
	if err != nil {
		h.sendError(fmt.Sprintf("failed to connect to reader: %v", err))
		h.sendReadyForQuery('I')
		return
	}
	defer conn.Close(ctx)

	// Execute query
	rows, err := conn.Query(ctx, sql)
	if err != nil {
		h.sendError(fmt.Sprintf("failed to execute query: %v", err))
		h.sendReadyForQuery('I')
		return
	}
	defer rows.Close()

	// Send row description
	fieldDescs := rows.FieldDescriptions()
	if len(fieldDescs) > 0 {
		rowDesc := &pgproto3.RowDescription{
			Fields: make([]pgproto3.FieldDescription, len(fieldDescs)),
		}
		for i, fd := range fieldDescs {
			rowDesc.Fields[i] = pgproto3.FieldDescription{
				Name:                 []byte(fd.Name),
				TableOID:             fd.TableOID,
				TableAttributeNumber: fd.TableAttributeNumber,
				DataTypeOID:          fd.DataTypeOID,
				DataTypeSize:         fd.DataTypeSize,
				TypeModifier:         fd.TypeModifier,
				Format:               fd.Format,
			}
		}
		buf, err := rowDesc.Encode(nil)
		if err == nil {
			h.conn.Write(buf)
		}
	}

	// Send data rows
	rowCount := 0
	for rows.Next() {
		values, err := rows.Values()
		if err != nil {
			h.sendError(fmt.Sprintf("failed to get row values: %v", err))
			h.sendReadyForQuery('I')
			return
		}

		dataRow := &pgproto3.DataRow{
			Values: make([][]byte, len(values)),
		}
		for i, v := range values {
			if v == nil {
				dataRow.Values[i] = nil
			} else {
				dataRow.Values[i] = []byte(fmt.Sprintf("%v", v))
			}
		}
		buf, err := dataRow.Encode(nil)
		if err == nil {
			h.conn.Write(buf)
		}
		rowCount++
	}

	if rows.Err() != nil {
		h.sendError(fmt.Sprintf("error iterating rows: %v", rows.Err()))
		h.sendReadyForQuery('I')
		return
	}

	// Send command complete
	cmdComplete := &pgproto3.CommandComplete{
		CommandTag: []byte(fmt.Sprintf("SELECT %d", rowCount)),
	}
	buf, err := cmdComplete.Encode(nil)
	if err == nil {
		h.conn.Write(buf)
	}

	// Send ready for query
	h.sendReadyForQuery('I')
}

// handleWriteQuery executes a write query on all writers
func (h *ClientHandler) handleWriteQuery(ctx context.Context, sql string) {
	err := h.router.ExecuteWrite(ctx, sql)
	if err != nil {
		h.sendError(fmt.Sprintf("failed to execute write: %v", err))
		h.sendReadyForQuery('I')
		return
	}

	// Send command complete
	cmdComplete := &pgproto3.CommandComplete{
		CommandTag: []byte("OK"),
	}
	buf, err := cmdComplete.Encode(nil)
	if err == nil {
		h.conn.Write(buf)
	}

	// Send ready for query
	h.sendReadyForQuery('I')
}

// sendError sends an error response to the client
func (h *ClientHandler) sendError(message string) {
	errResp := &pgproto3.ErrorResponse{
		Severity: "ERROR",
		Code:     "XX000",
		Message:  message,
	}
	buf, err := errResp.Encode(nil)
	if err == nil {
		h.conn.Write(buf)
	}
}

// sendReadyForQuery sends a ready for query message
func (h *ClientHandler) sendReadyForQuery(status byte) {
	ready := &pgproto3.ReadyForQuery{
		TxStatus: status,
	}
	buf, err := ready.Encode(nil)
	if err == nil {
		h.conn.Write(buf)
	}
}

// handleParse handles Parse message for prepared statements
func (h *ClientHandler) handleParse(msg *pgproto3.Parse) {
	// Store the prepared statement
	stmt := &PreparedStatement{
		name:      msg.Name,
		query:     msg.Query,
		paramOIDs: msg.ParameterOIDs,
		queryType: ClassifyQuery(msg.Query),
	}
	h.preparedStmts[msg.Name] = stmt

	// Send ParseComplete
	parseComplete := &pgproto3.ParseComplete{}
	buf, err := parseComplete.Encode(nil)
	if err == nil {
		h.conn.Write(buf)
	}
}

// handleBind handles Bind message to create a portal
func (h *ClientHandler) handleBind(msg *pgproto3.Bind) {
	// Check if the statement exists
	stmt, exists := h.preparedStmts[msg.PreparedStatement]
	if !exists {
		h.sendError(fmt.Sprintf("prepared statement %s does not exist", msg.PreparedStatement))
		return
	}

	// Store the portal
	portal := &Portal{
		name:     msg.DestinationPortal,
		stmtName: msg.PreparedStatement,
		params:   msg.Parameters,
		formats:  msg.ResultFormatCodes,
	}
	h.portals[msg.DestinationPortal] = portal

	// For validation, we could connect and prepare on the backend here
	// but for simplicity, we'll defer that to Execute
	_ = stmt // Use stmt to avoid unused variable warning

	// Send BindComplete
	bindComplete := &pgproto3.BindComplete{}
	buf, err := bindComplete.Encode(nil)
	if err == nil {
		h.conn.Write(buf)
	}
}

// handleExecute handles Execute message to run a portal
func (h *ClientHandler) handleExecute(msg *pgproto3.Execute) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	// Get the portal
	portal, exists := h.portals[msg.Portal]
	if !exists {
		h.sendError(fmt.Sprintf("portal %s does not exist", msg.Portal))
		return
	}

	// Get the prepared statement
	stmt, exists := h.preparedStmts[portal.stmtName]
	if !exists {
		h.sendError(fmt.Sprintf("prepared statement %s does not exist", portal.stmtName))
		return
	}

	// Execute based on query type
	if stmt.queryType == QueryTypeRead {
		h.executeReadPortal(ctx, stmt, portal, msg.MaxRows)
	} else {
		h.executeWritePortal(ctx, stmt, portal)
	}
}

// executeReadPortal executes a read query from a portal
func (h *ClientHandler) executeReadPortal(ctx context.Context, stmt *PreparedStatement, portal *Portal, maxRows uint32) {
	// Connect to reader
	conn, err := h.router.connectToBackend(ctx, h.router.config.ReaderDSN)
	if err != nil {
		h.sendError(fmt.Sprintf("failed to connect to reader: %v", err))
		return
	}
	defer conn.Close(ctx)

	// Convert parameters to interface{} slice
	params := make([]interface{}, len(portal.params))
	for i, p := range portal.params {
		if p == nil {
			params[i] = nil
		} else {
			params[i] = string(p)
		}
	}

	// Execute query with parameters
	rows, err := conn.Query(ctx, stmt.query, params...)
	if err != nil {
		h.sendError(fmt.Sprintf("failed to execute query: %v", err))
		return
	}
	defer rows.Close()

	// Send row description
	fieldDescs := rows.FieldDescriptions()
	if len(fieldDescs) > 0 {
		rowDesc := &pgproto3.RowDescription{
			Fields: make([]pgproto3.FieldDescription, len(fieldDescs)),
		}
		for i, fd := range fieldDescs {
			rowDesc.Fields[i] = pgproto3.FieldDescription{
				Name:                 []byte(fd.Name),
				TableOID:             fd.TableOID,
				TableAttributeNumber: fd.TableAttributeNumber,
				DataTypeOID:          fd.DataTypeOID,
				DataTypeSize:         fd.DataTypeSize,
				TypeModifier:         fd.TypeModifier,
				Format:               fd.Format,
			}
		}
		buf, err := rowDesc.Encode(nil)
		if err == nil {
			h.conn.Write(buf)
		}
	}

	// Send data rows
	rowCount := uint32(0)
	for rows.Next() {
		if maxRows > 0 && rowCount >= maxRows {
			// Send PortalSuspended if we hit the row limit
			suspended := &pgproto3.PortalSuspended{}
			buf, err := suspended.Encode(nil)
			if err == nil {
				h.conn.Write(buf)
			}
			return
		}

		values, err := rows.Values()
		if err != nil {
			h.sendError(fmt.Sprintf("failed to get row values: %v", err))
			return
		}

		dataRow := &pgproto3.DataRow{
			Values: make([][]byte, len(values)),
		}
		for i, v := range values {
			if v == nil {
				dataRow.Values[i] = nil
			} else {
				dataRow.Values[i] = []byte(fmt.Sprintf("%v", v))
			}
		}
		buf, err := dataRow.Encode(nil)
		if err == nil {
			h.conn.Write(buf)
		}
		rowCount++
	}

	if rows.Err() != nil {
		h.sendError(fmt.Sprintf("error iterating rows: %v", rows.Err()))
		return
	}

	// Send command complete
	cmdComplete := &pgproto3.CommandComplete{
		CommandTag: []byte(fmt.Sprintf("SELECT %d", rowCount)),
	}
	buf, err := cmdComplete.Encode(nil)
	if err == nil {
		h.conn.Write(buf)
	}
}

// executeWritePortal executes a write query from a portal
func (h *ClientHandler) executeWritePortal(ctx context.Context, stmt *PreparedStatement, portal *Portal) {
	// Convert parameters to interface{} slice
	params := make([]interface{}, len(portal.params))
	for i, p := range portal.params {
		if p == nil {
			params[i] = nil
		} else {
			params[i] = string(p)
		}
	}

	// Execute on all writers
	err := h.router.ExecuteWriteWithParams(ctx, stmt.query, params...)
	if err != nil {
		h.sendError(fmt.Sprintf("failed to execute write: %v", err))
		return
	}

	// Send command complete
	cmdComplete := &pgproto3.CommandComplete{
		CommandTag: []byte("OK"),
	}
	buf, err := cmdComplete.Encode(nil)
	if err == nil {
		h.conn.Write(buf)
	}
}

// handleDescribe handles Describe message
func (h *ClientHandler) handleDescribe(msg *pgproto3.Describe) {
	if msg.ObjectType == 'S' {
		// Describe statement
		stmt, exists := h.preparedStmts[msg.Name]
		if !exists {
			h.sendError(fmt.Sprintf("prepared statement %s does not exist", msg.Name))
			return
		}

		// For simplicity, send ParameterDescription with no parameters
		// In a full implementation, we'd parse the SQL to determine parameter types
		paramDesc := &pgproto3.ParameterDescription{
			ParameterOIDs: stmt.paramOIDs,
		}
		buf, err := paramDesc.Encode(nil)
		if err == nil {
			h.conn.Write(buf)
		}

		// Send NoData for now (we'd need to execute to get row description)
		noData := &pgproto3.NoData{}
		buf, err = noData.Encode(nil)
		if err == nil {
			h.conn.Write(buf)
		}
	} else if msg.ObjectType == 'P' {
		// Describe portal
		_, exists := h.portals[msg.Name]
		if !exists {
			h.sendError(fmt.Sprintf("portal %s does not exist", msg.Name))
			return
		}

		// Send NoData for now
		noData := &pgproto3.NoData{}
		buf, err := noData.Encode(nil)
		if err == nil {
			h.conn.Write(buf)
		}
	}
}

// handleClose handles Close message
func (h *ClientHandler) handleClose(msg *pgproto3.Close) {
	if msg.ObjectType == 'S' {
		// Close statement
		delete(h.preparedStmts, msg.Name)
	} else if msg.ObjectType == 'P' {
		// Close portal
		delete(h.portals, msg.Name)
	}

	// Send CloseComplete
	closeComplete := &pgproto3.CloseComplete{}
	buf, err := closeComplete.Encode(nil)
	if err == nil {
		h.conn.Write(buf)
	}
}

// sendAuthenticationOk sends authentication OK message
func (h *ClientHandler) sendAuthenticationOk() error {
	authOK := &pgproto3.AuthenticationOk{}
	buf, err := authOK.Encode(nil)
	if err != nil {
		return fmt.Errorf("failed to encode auth OK: %w", err)
	}
	_, err = h.conn.Write(buf)
	if err != nil {
		return fmt.Errorf("failed to send auth OK: %w", err)
	}

	// Send ready for query
	h.sendReadyForQuery('I')
	return nil
}

// performSCRAMAuth performs SCRAM-SHA-256 authentication
func (h *ClientHandler) performSCRAMAuth(username string) error {
	// Get user credentials
	user, exists := h.router.config.AuthConfig.GetUser(username)
	if !exists {
		return fmt.Errorf("user %s not found", username)
	}

	// Send SASL authentication request
	saslAuth := &pgproto3.AuthenticationSASL{
		AuthMechanisms: []string{"SCRAM-SHA-256"},
	}
	buf, err := saslAuth.Encode(nil)
	if err != nil {
		return fmt.Errorf("failed to encode SASL auth: %w", err)
	}
	_, err = h.conn.Write(buf)
	if err != nil {
		return fmt.Errorf("failed to send SASL auth: %w", err)
	}

	// Create SCRAM server
	scramServer := NewSCRAMServer(user, h.tlsState)

	// Receive client-first-message
	msg, err := h.backend.Receive()
	if err != nil {
		return fmt.Errorf("failed to receive SASL initial response: %w", err)
	}

	saslInitial, ok := msg.(*pgproto3.SASLInitialResponse)
	if !ok {
		return fmt.Errorf("expected SASLInitialResponse, got %T", msg)
	}

	if saslInitial.AuthMechanism != "SCRAM-SHA-256" {
		return fmt.Errorf("unsupported auth mechanism: %s", saslInitial.AuthMechanism)
	}

	// Process client-first-message
	serverFirst, err := scramServer.HandleClientFirst(string(saslInitial.Data))
	if err != nil {
		return fmt.Errorf("SCRAM client-first failed: %w", err)
	}

	// Send server-first-message
	saslContinue := &pgproto3.AuthenticationSASLContinue{
		Data: []byte(serverFirst),
	}
	buf, err = saslContinue.Encode(nil)
	if err != nil {
		return fmt.Errorf("failed to encode SASL continue: %w", err)
	}
	_, err = h.conn.Write(buf)
	if err != nil {
		return fmt.Errorf("failed to send SASL continue: %w", err)
	}

	// Receive client-final-message
	msg, err = h.backend.Receive()
	if err != nil {
		return fmt.Errorf("failed to receive SASL response: %w", err)
	}

	saslResponse, ok := msg.(*pgproto3.SASLResponse)
	if !ok {
		return fmt.Errorf("expected SASLResponse, got %T", msg)
	}

	// Process client-final-message
	serverFinal, err := scramServer.HandleClientFinal(string(saslResponse.Data))
	if err != nil {
		// Authentication failed
		log.Printf("Authentication failed for user %s: %v", username, err)
		
		// Send authentication error
		errResp := &pgproto3.ErrorResponse{
			Severity: "FATAL",
			Code:     "28P01", // invalid_password
			Message:  "password authentication failed for user \"" + username + "\"",
		}
		buf, _ := errResp.Encode(nil)
		h.conn.Write(buf)
		
		return fmt.Errorf("authentication failed: %w", err)
	}

	// Send server-final-message
	saslFinal := &pgproto3.AuthenticationSASLFinal{
		Data: []byte(serverFinal),
	}
	buf, err = saslFinal.Encode(nil)
	if err != nil {
		return fmt.Errorf("failed to encode SASL final: %w", err)
	}
	_, err = h.conn.Write(buf)
	if err != nil {
		return fmt.Errorf("failed to send SASL final: %w", err)
	}

	// Authentication successful
	h.authenticated = true
	log.Printf("User %s authenticated successfully", username)

	// Send authentication OK
	return h.sendAuthenticationOk()
}
