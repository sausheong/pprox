package main

import (
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strings"
	"sync"
	"time"
)

// CredentialProvider is an interface for different credential sources
type CredentialProvider interface {
	GetCredentials(ctx context.Context) (map[string]string, error)
	SupportsReload() bool
}

// CredentialManager manages user credentials from various sources
type CredentialManager struct {
	provider      CredentialProvider
	authConfig    *AuthConfig
	mu            sync.RWMutex
	reloadEnabled bool
	stopReload    chan struct{}
}

// NewCredentialManager creates a new credential manager
func NewCredentialManager(provider CredentialProvider) *CredentialManager {
	return &CredentialManager{
		provider:      provider,
		authConfig:    NewAuthConfig(),
		reloadEnabled: provider.SupportsReload(),
		stopReload:    make(chan struct{}),
	}
}

// LoadCredentials loads credentials from the provider
func (cm *CredentialManager) LoadCredentials(ctx context.Context) error {
	credentials, err := cm.provider.GetCredentials(ctx)
	if err != nil {
		return fmt.Errorf("failed to get credentials: %w", err)
	}

	cm.mu.Lock()
	defer cm.mu.Unlock()

	// Create new auth config
	newAuthConfig := NewAuthConfig()
	for username, password := range credentials {
		if err := newAuthConfig.AddUser(username, password); err != nil {
			return fmt.Errorf("failed to add user %s: %w", username, err)
		}
	}

	cm.authConfig = newAuthConfig
	return nil
}

// GetAuthConfig returns the current auth configuration
func (cm *CredentialManager) GetAuthConfig() *AuthConfig {
	cm.mu.RLock()
	defer cm.mu.RUnlock()
	return cm.authConfig
}

// StartAutoReload starts automatic credential reloading
func (cm *CredentialManager) StartAutoReload(ctx context.Context, interval time.Duration) {
	if !cm.reloadEnabled {
		return
	}

	go func() {
		ticker := time.NewTicker(interval)
		defer ticker.Stop()

		for {
			select {
			case <-ticker.C:
				if err := cm.LoadCredentials(ctx); err != nil {
					fmt.Printf("Failed to reload credentials: %v\n", err)
				} else {
					fmt.Println("Credentials reloaded successfully")
				}
			case <-cm.stopReload:
				return
			case <-ctx.Done():
				return
			}
		}
	}()
}

// StopAutoReload stops automatic credential reloading
func (cm *CredentialManager) StopAutoReload() {
	close(cm.stopReload)
}

// ========================================================================
// Environment Variable Provider (Original)
// ========================================================================

// EnvCredentialProvider loads credentials from environment variables
type EnvCredentialProvider struct{}

func NewEnvCredentialProvider() *EnvCredentialProvider {
	return &EnvCredentialProvider{}
}

func (p *EnvCredentialProvider) GetCredentials(ctx context.Context) (map[string]string, error) {
	credentials := make(map[string]string)

	usersEnv := os.Getenv("PG_USERS")
	if usersEnv == "" {
		return credentials, nil // Trust mode
	}

	userPairs := strings.Split(usersEnv, ",")
	for _, pair := range userPairs {
		parts := strings.SplitN(strings.TrimSpace(pair), ":", 2)
		if len(parts) != 2 {
			return nil, fmt.Errorf("invalid user format: %s", pair)
		}
		credentials[strings.TrimSpace(parts[0])] = strings.TrimSpace(parts[1])
	}

	return credentials, nil
}

func (p *EnvCredentialProvider) SupportsReload() bool {
	return false // Environment variables don't change at runtime
}

// ========================================================================
// File-Based Provider (JSON with optional encryption)
// ========================================================================

// FileCredentialProvider loads credentials from a JSON file
type FileCredentialProvider struct {
	filePath       string
	encryptionKey  []byte
	lastModified   time.Time
}

// CredentialFile represents the JSON structure for credentials
type CredentialFile struct {
	Users []struct {
		Username string `json:"username"`
		Password string `json:"password"`
	} `json:"users"`
}

func NewFileCredentialProvider(filePath string, encryptionKey string) *FileCredentialProvider {
	var key []byte
	if encryptionKey != "" {
		// Derive 32-byte key from provided key
		key = make([]byte, 32)
		copy(key, []byte(encryptionKey))
	}

	return &FileCredentialProvider{
		filePath:      filePath,
		encryptionKey: key,
	}
}

func (p *FileCredentialProvider) GetCredentials(ctx context.Context) (map[string]string, error) {
	data, err := os.ReadFile(p.filePath)
	if err != nil {
		return nil, fmt.Errorf("failed to read credential file: %w", err)
	}

	// Update last modified time
	if info, err := os.Stat(p.filePath); err == nil {
		p.lastModified = info.ModTime()
	}

	// Decrypt if encryption key is provided
	if p.encryptionKey != nil {
		data, err = p.decrypt(data)
		if err != nil {
			return nil, fmt.Errorf("failed to decrypt credentials: %w", err)
		}
	}

	var credFile CredentialFile
	if err := json.Unmarshal(data, &credFile); err != nil {
		return nil, fmt.Errorf("failed to parse credential file: %w", err)
	}

	credentials := make(map[string]string)
	for _, user := range credFile.Users {
		credentials[user.Username] = user.Password
	}

	return credentials, nil
}

func (p *FileCredentialProvider) SupportsReload() bool {
	return true
}

func (p *FileCredentialProvider) decrypt(ciphertext []byte) ([]byte, error) {
	block, err := aes.NewCipher(p.encryptionKey)
	if err != nil {
		return nil, err
	}

	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}

	nonceSize := gcm.NonceSize()
	if len(ciphertext) < nonceSize {
		return nil, fmt.Errorf("ciphertext too short")
	}

	nonce, ciphertext := ciphertext[:nonceSize], ciphertext[nonceSize:]
	plaintext, err := gcm.Open(nil, nonce, ciphertext, nil)
	if err != nil {
		return nil, err
	}

	return plaintext, nil
}

// EncryptCredentialFile encrypts a credential file (utility function)
func EncryptCredentialFile(inputPath, outputPath, encryptionKey string) error {
	data, err := os.ReadFile(inputPath)
	if err != nil {
		return fmt.Errorf("failed to read input file: %w", err)
	}

	// Derive 32-byte key
	key := make([]byte, 32)
	copy(key, []byte(encryptionKey))

	block, err := aes.NewCipher(key)
	if err != nil {
		return err
	}

	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return err
	}

	nonce := make([]byte, gcm.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return err
	}

	ciphertext := gcm.Seal(nonce, nonce, data, nil)
	
	if err := os.WriteFile(outputPath, ciphertext, 0600); err != nil {
		return fmt.Errorf("failed to write encrypted file: %w", err)
	}

	return nil
}

// ========================================================================
// HashiCorp Vault Provider
// ========================================================================

// VaultCredentialProvider loads credentials from HashiCorp Vault
type VaultCredentialProvider struct {
	address    string
	token      string
	secretPath string
}

func NewVaultCredentialProvider(address, token, secretPath string) *VaultCredentialProvider {
	return &VaultCredentialProvider{
		address:    address,
		token:      token,
		secretPath: secretPath,
	}
}

func (p *VaultCredentialProvider) GetCredentials(ctx context.Context) (map[string]string, error) {
	// This is a simplified implementation
	// In production, use the official Vault client library
	// github.com/hashicorp/vault/api
	
	// For now, return an error indicating this needs the Vault library
	return nil, fmt.Errorf("Vault provider requires hashicorp/vault/api library - see CREDENTIALS.md for setup")
}

func (p *VaultCredentialProvider) SupportsReload() bool {
	return true
}

// ========================================================================
// AWS Secrets Manager Provider
// ========================================================================

// AWSSecretsProvider loads credentials from AWS Secrets Manager
type AWSSecretsProvider struct {
	secretName string
	region     string
}

func NewAWSSecretsProvider(secretName, region string) *AWSSecretsProvider {
	return &AWSSecretsProvider{
		secretName: secretName,
		region:     region,
	}
}

func (p *AWSSecretsProvider) GetCredentials(ctx context.Context) (map[string]string, error) {
	// This is a simplified implementation
	// In production, use the official AWS SDK
	// github.com/aws/aws-sdk-go-v2/service/secretsmanager
	
	// For now, return an error indicating this needs the AWS SDK
	return nil, fmt.Errorf("AWS Secrets Manager provider requires aws-sdk-go-v2 - see CREDENTIALS.md for setup")
}

func (p *AWSSecretsProvider) SupportsReload() bool {
	return true
}

// ========================================================================
// Kubernetes Secret Provider
// ========================================================================

// K8sSecretProvider loads credentials from Kubernetes secrets
type K8sSecretProvider struct {
	secretPath string // Path where secret is mounted (e.g., /var/run/secrets/pprox)
}

func NewK8sSecretProvider(secretPath string) *K8sSecretProvider {
	return &K8sSecretProvider{
		secretPath: secretPath,
	}
}

func (p *K8sSecretProvider) GetCredentials(ctx context.Context) (map[string]string, error) {
	// Read credentials from mounted secret files
	// Kubernetes mounts secrets as files in the pod
	
	usersFile := p.secretPath + "/users"
	data, err := os.ReadFile(usersFile)
	if err != nil {
		return nil, fmt.Errorf("failed to read K8s secret: %w", err)
	}

	credentials := make(map[string]string)
	
	// Parse format: username:password (one per line)
	lines := strings.Split(string(data), "\n")
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		
		parts := strings.SplitN(line, ":", 2)
		if len(parts) != 2 {
			return nil, fmt.Errorf("invalid credential format in K8s secret: %s", line)
		}
		
		credentials[strings.TrimSpace(parts[0])] = strings.TrimSpace(parts[1])
	}

	return credentials, nil
}

func (p *K8sSecretProvider) SupportsReload() bool {
	return true // K8s can update mounted secrets
}

// ========================================================================
// HTTP API Provider (for custom credential services)
// ========================================================================

// HTTPCredentialProvider loads credentials from an HTTP API
type HTTPCredentialProvider struct {
	endpoint string
	apiKey   string
}

func NewHTTPCredentialProvider(endpoint, apiKey string) *HTTPCredentialProvider {
	return &HTTPCredentialProvider{
		endpoint: endpoint,
		apiKey:   apiKey,
	}
}

func (p *HTTPCredentialProvider) GetCredentials(ctx context.Context) (map[string]string, error) {
	// This would make an HTTP request to your credential service
	// For now, return an error indicating this needs implementation
	return nil, fmt.Errorf("HTTP provider requires net/http implementation - see CREDENTIALS.md for setup")
}

func (p *HTTPCredentialProvider) SupportsReload() bool {
	return true
}

// ========================================================================
// Helper Functions
// ========================================================================

// CreateCredentialProvider creates the appropriate provider based on configuration
func CreateCredentialProvider() (CredentialProvider, error) {
	// Check for credential source configuration
	source := os.Getenv("CREDENTIAL_SOURCE")
	
	switch source {
	case "file":
		filePath := os.Getenv("CREDENTIAL_FILE")
		if filePath == "" {
			return nil, fmt.Errorf("CREDENTIAL_FILE must be set when CREDENTIAL_SOURCE=file")
		}
		encryptionKey := os.Getenv("CREDENTIAL_ENCRYPTION_KEY")
		return NewFileCredentialProvider(filePath, encryptionKey), nil
		
	case "vault":
		address := os.Getenv("VAULT_ADDR")
		token := os.Getenv("VAULT_TOKEN")
		path := os.Getenv("VAULT_SECRET_PATH")
		if address == "" || token == "" || path == "" {
			return nil, fmt.Errorf("VAULT_ADDR, VAULT_TOKEN, and VAULT_SECRET_PATH must be set")
		}
		return NewVaultCredentialProvider(address, token, path), nil
		
	case "aws":
		secretName := os.Getenv("AWS_SECRET_NAME")
		region := os.Getenv("AWS_REGION")
		if secretName == "" || region == "" {
			return nil, fmt.Errorf("AWS_SECRET_NAME and AWS_REGION must be set")
		}
		return NewAWSSecretsProvider(secretName, region), nil
		
	case "k8s":
		secretPath := os.Getenv("K8S_SECRET_PATH")
		if secretPath == "" {
			secretPath = "/var/run/secrets/pprox"
		}
		return NewK8sSecretProvider(secretPath), nil
		
	case "http":
		endpoint := os.Getenv("CREDENTIAL_API_ENDPOINT")
		apiKey := os.Getenv("CREDENTIAL_API_KEY")
		if endpoint == "" {
			return nil, fmt.Errorf("CREDENTIAL_API_ENDPOINT must be set")
		}
		return NewHTTPCredentialProvider(endpoint, apiKey), nil
		
	case "env", "":
		// Default to environment variables
		return NewEnvCredentialProvider(), nil
		
	default:
		return nil, fmt.Errorf("unknown credential source: %s", source)
	}
}
