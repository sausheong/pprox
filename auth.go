package main

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"crypto/tls"
	"encoding/base64"
	"fmt"
	"strings"

	"golang.org/x/crypto/pbkdf2"
)

// AuthConfig holds authentication configuration
type AuthConfig struct {
	Users map[string]*UserCredentials
}

// UserCredentials stores user authentication information
type UserCredentials struct {
	Username       string
	Password       string
	Salt           []byte
	StoredKey      []byte
	ServerKey      []byte
	IterationCount int
}

// SCRAMServer handles SCRAM-SHA-256 authentication
type SCRAMServer struct {
	user           *UserCredentials
	clientNonce    string
	serverNonce    string
	clientFirstMsg string
	serverFirstMsg string
	authMessage    string
	tlsState       *tls.ConnectionState
}

// NewAuthConfig creates a new authentication configuration
func NewAuthConfig() *AuthConfig {
	return &AuthConfig{
		Users: make(map[string]*UserCredentials),
	}
}

// AddUser adds a user with SCRAM-SHA-256 credentials
func (ac *AuthConfig) AddUser(username, password string) error {
	salt := make([]byte, 16)
	if _, err := rand.Read(salt); err != nil {
		return fmt.Errorf("failed to generate salt: %w", err)
	}

	iterationCount := 4096

	// Generate SCRAM credentials
	saltedPassword := pbkdf2.Key([]byte(password), salt, iterationCount, 32, sha256.New)
	
	clientKeyHMAC := hmac.New(sha256.New, saltedPassword)
	clientKeyHMAC.Write([]byte("Client Key"))
	clientKey := clientKeyHMAC.Sum(nil)
	
	storedKeyHash := sha256.Sum256(clientKey)
	storedKey := storedKeyHash[:]
	
	serverKeyHMAC := hmac.New(sha256.New, saltedPassword)
	serverKeyHMAC.Write([]byte("Server Key"))
	serverKey := serverKeyHMAC.Sum(nil)

	ac.Users[username] = &UserCredentials{
		Username:       username,
		Password:       password,
		Salt:           salt,
		StoredKey:      storedKey,
		ServerKey:      serverKey,
		IterationCount: iterationCount,
	}

	return nil
}

// GetUser retrieves user credentials
func (ac *AuthConfig) GetUser(username string) (*UserCredentials, bool) {
	user, exists := ac.Users[username]
	return user, exists
}

// NewSCRAMServer creates a new SCRAM authentication server
func NewSCRAMServer(user *UserCredentials, tlsState *tls.ConnectionState) *SCRAMServer {
	return &SCRAMServer{
		user:     user,
		tlsState: tlsState,
	}
}

// HandleClientFirst processes the client-first-message
func (s *SCRAMServer) HandleClientFirst(clientFirstMsg string) (string, error) {
	s.clientFirstMsg = clientFirstMsg

	// Parse client-first-message: n,,n=username,r=clientNonce
	parts := strings.Split(clientFirstMsg, ",")
	if len(parts) < 3 {
		return "", fmt.Errorf("invalid client-first-message format")
	}

	// Extract client nonce
	for _, part := range parts {
		if strings.HasPrefix(part, "r=") {
			s.clientNonce = strings.TrimPrefix(part, "r=")
			break
		}
	}

	if s.clientNonce == "" {
		return "", fmt.Errorf("client nonce not found")
	}

	// Generate server nonce
	serverNonceBytes := make([]byte, 18)
	if _, err := rand.Read(serverNonceBytes); err != nil {
		return "", fmt.Errorf("failed to generate server nonce: %w", err)
	}
	s.serverNonce = base64.StdEncoding.EncodeToString(serverNonceBytes)

	// Build server-first-message
	nonce := s.clientNonce + s.serverNonce
	salt := base64.StdEncoding.EncodeToString(s.user.Salt)
	s.serverFirstMsg = fmt.Sprintf("r=%s,s=%s,i=%d", nonce, salt, s.user.IterationCount)

	return s.serverFirstMsg, nil
}

// HandleClientFinal processes the client-final-message and returns server-final-message
func (s *SCRAMServer) HandleClientFinal(clientFinalMsg string) (string, error) {
	// Parse client-final-message: c=channelBinding,r=nonce,p=clientProof
	parts := strings.Split(clientFinalMsg, ",")
	if len(parts) < 3 {
		return "", fmt.Errorf("invalid client-final-message format")
	}

	var channelBinding, nonce, clientProofB64 string
	for _, part := range parts {
		if strings.HasPrefix(part, "c=") {
			channelBinding = strings.TrimPrefix(part, "c=")
		} else if strings.HasPrefix(part, "r=") {
			nonce = strings.TrimPrefix(part, "r=")
		} else if strings.HasPrefix(part, "p=") {
			clientProofB64 = strings.TrimPrefix(part, "p=")
		}
	}

	// Verify nonce
	expectedNonce := s.clientNonce + s.serverNonce
	if nonce != expectedNonce {
		return "", fmt.Errorf("nonce mismatch")
	}

	// Verify channel binding
	if err := s.verifyChannelBinding(channelBinding); err != nil {
		return "", fmt.Errorf("channel binding verification failed: %w", err)
	}

	// Build auth message
	clientFirstBare := s.getClientFirstBare()
	clientFinalWithoutProof := s.getClientFinalWithoutProof(clientFinalMsg)
	s.authMessage = fmt.Sprintf("%s,%s,%s", clientFirstBare, s.serverFirstMsg, clientFinalWithoutProof)

	// Verify client proof
	clientProof, err := base64.StdEncoding.DecodeString(clientProofB64)
	if err != nil {
		return "", fmt.Errorf("failed to decode client proof: %w", err)
	}

	// Calculate client signature
	clientSignatureHMAC := hmac.New(sha256.New, s.user.StoredKey)
	clientSignatureHMAC.Write([]byte(s.authMessage))
	clientSignature := clientSignatureHMAC.Sum(nil)

	// Recover client key
	clientKey := make([]byte, len(clientProof))
	for i := range clientKey {
		clientKey[i] = clientProof[i] ^ clientSignature[i]
	}

	// Verify stored key
	storedKeyHash := sha256.Sum256(clientKey)
	if !hmac.Equal(storedKeyHash[:], s.user.StoredKey) {
		return "", fmt.Errorf("authentication failed: invalid credentials")
	}

	// Calculate server signature
	serverSignatureHMAC := hmac.New(sha256.New, s.user.ServerKey)
	serverSignatureHMAC.Write([]byte(s.authMessage))
	serverSignature := serverSignatureHMAC.Sum(nil)

	// Build server-final-message
	serverFinal := fmt.Sprintf("v=%s", base64.StdEncoding.EncodeToString(serverSignature))
	return serverFinal, nil
}

// verifyChannelBinding verifies the channel binding data
func (s *SCRAMServer) verifyChannelBinding(channelBindingB64 string) error {
	channelBindingData, err := base64.StdEncoding.DecodeString(channelBindingB64)
	if err != nil {
		return fmt.Errorf("failed to decode channel binding: %w", err)
	}

	// Channel binding format: gs2-header + channel-binding-data
	// For tls-server-end-point: "p=tls-server-end-point,," + certificate-hash
	
	if len(channelBindingData) < 3 {
		return fmt.Errorf("channel binding data too short")
	}

	// Check for tls-server-end-point binding
	if s.tlsState != nil && len(s.tlsState.PeerCertificates) > 0 {
		// Extract the channel binding type from the data
		cbString := string(channelBindingData)
		
		if strings.HasPrefix(cbString, "p=tls-server-end-point,,") {
			// Calculate expected certificate hash
			cert := s.tlsState.PeerCertificates[0]
			certHash := sha256.Sum256(cert.Raw)
			
			// The channel binding data should contain the certificate hash
			expectedCB := append([]byte("p=tls-server-end-point,,"), certHash[:]...)
			
			if !hmac.Equal(channelBindingData, expectedCB) {
				return fmt.Errorf("channel binding hash mismatch")
			}
		} else if strings.HasPrefix(cbString, "n,,") {
			// Client doesn't support channel binding
			return nil
		}
	} else {
		// No TLS, expect "n,," (no channel binding)
		if string(channelBindingData) != "n,," {
			return fmt.Errorf("channel binding not supported without TLS")
		}
	}

	return nil
}

// getClientFirstBare extracts the bare part of client-first-message
func (s *SCRAMServer) getClientFirstBare() string {
	// Remove GS2 header (everything before first comma after "n")
	parts := strings.SplitN(s.clientFirstMsg, ",", 3)
	if len(parts) >= 3 {
		return parts[2]
	}
	return s.clientFirstMsg
}

// getClientFinalWithoutProof extracts client-final-message without proof
func (s *SCRAMServer) getClientFinalWithoutProof(clientFinal string) string {
	// Remove the proof part (p=...)
	idx := strings.LastIndex(clientFinal, ",p=")
	if idx != -1 {
		return clientFinal[:idx]
	}
	return clientFinal
}

// ParseSCRAMClientFirst parses the initial SCRAM client message to extract username
func ParseSCRAMClientFirst(data []byte) (string, error) {
	msg := string(data)
	parts := strings.Split(msg, ",")
	
	for _, part := range parts {
		if strings.HasPrefix(part, "n=") {
			username := strings.TrimPrefix(part, "n=")
			// Unescape username (SASLprep would be applied here in production)
			username = strings.ReplaceAll(username, "=3D", "=")
			username = strings.ReplaceAll(username, "=2C", ",")
			return username, nil
		}
	}
	
	return "", fmt.Errorf("username not found in SCRAM message")
}

// GetTLSServerEndPoint calculates the tls-server-end-point channel binding data
func GetTLSServerEndPoint(tlsState *tls.ConnectionState) []byte {
	if tlsState == nil || len(tlsState.PeerCertificates) == 0 {
		return nil
	}
	
	cert := tlsState.PeerCertificates[0]
	certHash := sha256.Sum256(cert.Raw)
	return certHash[:]
}
