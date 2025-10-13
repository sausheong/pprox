package main

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"os"
	"strings"
	"time"
)

// Config holds the proxy configuration
type Config struct {
	ProxyAddr     string
	ReaderDSN     string
	WriterDSNs    []string
	TLSConfig     *TLSConfig
	AuthConfig    *AuthConfig
	BackendTLS    *BackendTLSConfig
}

// TLSConfig holds TLS configuration for client connections
type TLSConfig struct {
	Enabled  bool
	CertFile string
	KeyFile  string
	TLS      *tls.Config
}

// BackendTLSConfig holds TLS configuration for backend database connections
type BackendTLSConfig struct {
	Enabled            bool
	Mode               string // disable, require, verify-ca, verify-full
	RootCAFile         string
	ClientCertFile     string
	ClientKeyFile      string
	TLS                *tls.Config
}

// LoadConfig loads configuration from environment variables
func LoadConfig() (*Config, error) {
	proxyAddr := os.Getenv("PROXY_ADDR")
	if proxyAddr == "" {
		proxyAddr = ":54329"
	}

	readerDSN := os.Getenv("PG_READER_DSN")
	if readerDSN == "" {
		return nil, fmt.Errorf("PG_READER_DSN environment variable is required")
	}

	writersCSV := os.Getenv("PG_WRITERS_CSV")
	if writersCSV == "" {
		return nil, fmt.Errorf("PG_WRITERS_CSV environment variable is required")
	}

	writerDSNs := strings.Split(writersCSV, ",")
	for i := range writerDSNs {
		writerDSNs[i] = strings.TrimSpace(writerDSNs[i])
	}

	// Load TLS configuration
	tlsConfig, err := loadTLSConfig()
	if err != nil {
		return nil, fmt.Errorf("failed to load TLS config: %w", err)
	}

	// Load authentication configuration
	authConfig, err := loadAuthConfig()
	if err != nil {
		return nil, fmt.Errorf("failed to load auth config: %w", err)
	}

	// Load backend TLS configuration
	backendTLS, err := loadBackendTLSConfig()
	if err != nil {
		return nil, fmt.Errorf("failed to load backend TLS config: %w", err)
	}

	return &Config{
		ProxyAddr:  proxyAddr,
		ReaderDSN:  readerDSN,
		WriterDSNs: writerDSNs,
		TLSConfig:  tlsConfig,
		AuthConfig: authConfig,
		BackendTLS: backendTLS,
	}, nil
}

// loadTLSConfig loads TLS configuration from environment
func loadTLSConfig() (*TLSConfig, error) {
	tlsEnabled := os.Getenv("TLS_ENABLED")
	if tlsEnabled != "true" && tlsEnabled != "1" {
		return &TLSConfig{Enabled: false}, nil
	}

	certFile := os.Getenv("TLS_CERT_FILE")
	keyFile := os.Getenv("TLS_KEY_FILE")

	if certFile == "" || keyFile == "" {
		return nil, fmt.Errorf("TLS_CERT_FILE and TLS_KEY_FILE are required when TLS is enabled")
	}

	// Load certificate
	cert, err := tls.LoadX509KeyPair(certFile, keyFile)
	if err != nil {
		return nil, fmt.Errorf("failed to load TLS certificate: %w", err)
	}

	tlsConf := &tls.Config{
		Certificates: []tls.Certificate{cert},
		MinVersion:   tls.VersionTLS12,
		CipherSuites: []uint16{
			tls.TLS_ECDHE_RSA_WITH_AES_128_GCM_SHA256,
			tls.TLS_ECDHE_RSA_WITH_AES_256_GCM_SHA384,
			tls.TLS_ECDHE_ECDSA_WITH_AES_128_GCM_SHA256,
			tls.TLS_ECDHE_ECDSA_WITH_AES_256_GCM_SHA384,
		},
	}

	return &TLSConfig{
		Enabled:  true,
		CertFile: certFile,
		KeyFile:  keyFile,
		TLS:      tlsConf,
	}, nil
}

// loadAuthConfig loads authentication configuration using credential providers
func loadAuthConfig() (*AuthConfig, error) {
	// Create credential provider based on configuration
	provider, err := CreateCredentialProvider()
	if err != nil {
		return nil, fmt.Errorf("failed to create credential provider: %w", err)
	}

	// Create credential manager
	credManager := NewCredentialManager(provider)
	
	// Load initial credentials
	if err := credManager.LoadCredentials(context.Background()); err != nil {
		return nil, fmt.Errorf("failed to load credentials: %w", err)
	}

	// Start auto-reload if supported and enabled
	reloadInterval := os.Getenv("CREDENTIAL_RELOAD_INTERVAL")
	if reloadInterval != "" && provider.SupportsReload() {
		// Parse interval (e.g., "5m", "1h")
		// For now, default to 5 minutes if not parseable
		// TODO: Add proper duration parsing
		credManager.StartAutoReload(context.Background(), 5*time.Minute)
	}

	return credManager.GetAuthConfig(), nil
}

// loadBackendTLSConfig loads backend TLS configuration from environment
func loadBackendTLSConfig() (*BackendTLSConfig, error) {
	mode := os.Getenv("BACKEND_TLS_MODE")
	if mode == "" || mode == "disable" {
		return &BackendTLSConfig{
			Enabled: false,
			Mode:    "disable",
		}, nil
	}

	// Validate mode
	validModes := map[string]bool{
		"require":     true,
		"verify-ca":   true,
		"verify-full": true,
	}
	if !validModes[mode] {
		return nil, fmt.Errorf("invalid BACKEND_TLS_MODE: %s (must be: disable, require, verify-ca, verify-full)", mode)
	}

	config := &BackendTLSConfig{
		Enabled: true,
		Mode:    mode,
	}

	// Load root CA certificate for verify-ca and verify-full modes
	if mode == "verify-ca" || mode == "verify-full" {
		rootCAFile := os.Getenv("BACKEND_TLS_ROOT_CA")
		if rootCAFile == "" {
			return nil, fmt.Errorf("BACKEND_TLS_ROOT_CA is required for mode %s", mode)
		}
		config.RootCAFile = rootCAFile
	}

	// Load client certificate and key (optional, for mutual TLS)
	clientCertFile := os.Getenv("BACKEND_TLS_CLIENT_CERT")
	clientKeyFile := os.Getenv("BACKEND_TLS_CLIENT_KEY")
	
	if clientCertFile != "" && clientKeyFile != "" {
		config.ClientCertFile = clientCertFile
		config.ClientKeyFile = clientKeyFile
	} else if clientCertFile != "" || clientKeyFile != "" {
		return nil, fmt.Errorf("both BACKEND_TLS_CLIENT_CERT and BACKEND_TLS_CLIENT_KEY must be set together")
	}

	// Build TLS config
	tlsConfig, err := buildBackendTLSConfig(config)
	if err != nil {
		return nil, fmt.Errorf("failed to build backend TLS config: %w", err)
	}
	config.TLS = tlsConfig

	return config, nil
}

// buildBackendTLSConfig creates a tls.Config for backend connections
func buildBackendTLSConfig(config *BackendTLSConfig) (*tls.Config, error) {
	tlsConf := &tls.Config{
		MinVersion: tls.VersionTLS12,
		CipherSuites: []uint16{
			tls.TLS_ECDHE_RSA_WITH_AES_128_GCM_SHA256,
			tls.TLS_ECDHE_RSA_WITH_AES_256_GCM_SHA384,
			tls.TLS_ECDHE_ECDSA_WITH_AES_128_GCM_SHA256,
			tls.TLS_ECDHE_ECDSA_WITH_AES_256_GCM_SHA384,
		},
	}

	// Configure certificate verification based on mode
	switch config.Mode {
	case "require":
		// Accept any certificate (encrypted but not verified)
		tlsConf.InsecureSkipVerify = true
		
	case "verify-ca", "verify-full":
		// Load root CA certificate
		if config.RootCAFile != "" {
			caCert, err := os.ReadFile(config.RootCAFile)
			if err != nil {
				return nil, fmt.Errorf("failed to read root CA file: %w", err)
			}
			
			caCertPool := x509.NewCertPool()
			if !caCertPool.AppendCertsFromPEM(caCert) {
				return nil, fmt.Errorf("failed to parse root CA certificate")
			}
			tlsConf.RootCAs = caCertPool
		}
		
		// For verify-ca, we skip hostname verification
		if config.Mode == "verify-ca" {
			tlsConf.InsecureSkipVerify = true
			// But we still verify the certificate chain in the connection
		}
		// For verify-full, hostname verification is enabled by default
	}

	// Load client certificate for mutual TLS
	if config.ClientCertFile != "" && config.ClientKeyFile != "" {
		cert, err := tls.LoadX509KeyPair(config.ClientCertFile, config.ClientKeyFile)
		if err != nil {
			return nil, fmt.Errorf("failed to load client certificate: %w", err)
		}
		tlsConf.Certificates = []tls.Certificate{cert}
	}

	return tlsConf, nil
}
