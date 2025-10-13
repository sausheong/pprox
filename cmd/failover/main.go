package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"os"
	"os/exec"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
)

const (
	configPath       = "/etc/pprox/.env"
	backupPathPrefix = "/etc/pprox/.env.backup"
	logPath          = "/var/log/pprox-failover.log"
)

type FailoverConfig struct {
	BackupEndpoint    string
	BackupUsername    string
	BackupPassword    string
	BackupDatabase    string
	PrimaryReader     string
	PrimaryWriter     string
	PrimaryPassword   string
	ProxyAddr         string
	TLSEnabled        string
	TLSCertFile       string
	TLSKeyFile        string
	CredentialSource  string
	CredentialFile    string
	CredentialKey     string
	BackendTLSMode    string
}

func main() {
	dryRun := flag.Bool("dry-run", false, "Show what would be done without making changes")
	verify := flag.Bool("verify", false, "Verify backup database is accessible before failover")
	force := flag.Bool("force", false, "Force failover even if primary database appears accessible")
	flag.Parse()

	log.SetOutput(os.Stdout)
	log.SetFlags(log.LstdFlags | log.Lshortfile)

	fmt.Println("🚨 PPROX FAILOVER TO BACKUP DATABASE")
	fmt.Println("=" + strings.Repeat("=", 50))
	fmt.Println()

	// Load current configuration
	config, err := loadConfig()
	if err != nil {
		log.Fatalf("Failed to load configuration: %v", err)
	}

	// Verify primary database is actually down (unless forced)
	if !*force {
		fmt.Println("Checking primary database status...")
		if isAccessible(config.PrimaryWriter, "postgres", config.PrimaryPassword) {
			fmt.Println("⚠️  WARNING: Primary database appears to be accessible")
			fmt.Println("Use --force to failover anyway")
			os.Exit(1)
		}
		fmt.Println("✅ Confirmed: Primary database is not accessible")
	}

	// Verify backup database is accessible
	if *verify {
		fmt.Println("Verifying backup database accessibility...")
		if !isAccessible(config.BackupEndpoint, config.BackupUsername, config.BackupPassword) {
			log.Fatalf("❌ Backup database is not accessible - cannot failover")
		}
		fmt.Println("✅ Backup database is accessible")
	}

	if *dryRun {
		fmt.Println("\n🔍 DRY RUN MODE - No changes will be made")
		fmt.Println("\nWould perform the following actions:")
		fmt.Println("1. Backup current configuration")
		fmt.Println("2. Update configuration to use backup database for both reader and writers")
		fmt.Println("3. Restart pprox service")
		fmt.Println("4. Verify pprox is running")
		fmt.Println("5. Test database connectivity")
		fmt.Println("\nRun without --dry-run to execute failover")
		return
	}

	// Confirm with user
	fmt.Println("\n⚠️  This will:")
	fmt.Println("   - Backup current configuration")
	fmt.Println("   - Switch all traffic to backup database")
	fmt.Println("   - Restart pprox (brief downtime)")
	fmt.Print("\nProceed with failover? (yes/no): ")
	
	var response string
	fmt.Scanln(&response)
	if strings.ToLower(response) != "yes" {
		fmt.Println("Failover cancelled")
		os.Exit(0)
	}

	// Execute failover
	startTime := time.Now()
	
	// Step 1: Backup configuration
	fmt.Println("\n📝 Step 1: Backing up current configuration...")
	backupPath := fmt.Sprintf("%s.%s", backupPathPrefix, time.Now().Format("20060102_150405"))
	if err := backupConfig(configPath, backupPath); err != nil {
		log.Fatalf("Failed to backup configuration: %v", err)
	}
	fmt.Printf("✅ Configuration backed up to: %s\n", backupPath)

	// Step 2: Update configuration
	fmt.Println("\n📝 Step 2: Updating configuration to use backup database...")
	if err := writeFailoverConfig(config); err != nil {
		log.Fatalf("Failed to update configuration: %v", err)
	}
	fmt.Println("✅ Configuration updated")

	// Step 3: Restart pprox
	fmt.Println("\n🔄 Step 3: Restarting pprox service...")
	if err := restartPprox(); err != nil {
		log.Printf("Failed to restart pprox: %v", err)
		fmt.Println("\n❌ Failover failed - attempting to restore configuration...")
		if err := restoreConfig(backupPath); err != nil {
			log.Fatalf("Failed to restore configuration: %v", err)
		}
		if err := restartPprox(); err != nil {
			log.Fatalf("Failed to restart pprox after restore: %v", err)
		}
		log.Fatalf("Configuration restored, but failover failed")
	}
	
	// Wait for service to be ready
	time.Sleep(3 * time.Second)
	fmt.Println("✅ pprox restarted")

	// Step 4: Verify pprox is running
	fmt.Println("\n🔍 Step 4: Verifying pprox service...")
	if !isPproxRunning() {
		log.Fatalf("❌ pprox service is not running")
	}
	fmt.Println("✅ pprox service is running")

	// Step 5: Test connectivity
	fmt.Println("\n🔍 Step 5: Testing database connectivity through pprox...")
	if err := testPproxConnectivity(); err != nil {
		log.Printf("⚠️  Warning: Could not verify connectivity: %v", err)
	} else {
		fmt.Println("✅ Database connectivity verified")
	}

	duration := time.Since(startTime)
	
	// Log to file
	logFailover(backupPath, duration)

	fmt.Println("\n" + strings.Repeat("=", 50))
	fmt.Println("✅ FAILOVER COMPLETED SUCCESSFULLY")
	fmt.Println(strings.Repeat("=", 50))
	fmt.Printf("\nDuration: %v\n", duration.Round(time.Second))
	fmt.Println("\nAll traffic is now routed to backup database")
	fmt.Println("Primary database can be synced when it comes back online")
	fmt.Printf("\nConfiguration backup: %s\n", backupPath)
	fmt.Printf("Failover log: %s\n", logPath)
	fmt.Println("\nTo failback to primary database later, run:")
	fmt.Println("  sudo pprox-failback")
}

func loadConfig() (*FailoverConfig, error) {
	// Read environment file
	data, err := os.ReadFile(configPath)
	if err != nil {
		return nil, err
	}

	config := &FailoverConfig{}
	lines := strings.Split(string(data), "\n")
	
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		
		parts := strings.SplitN(line, "=", 2)
		if len(parts) != 2 {
			continue
		}
		
		key := strings.TrimSpace(parts[0])
		value := strings.Trim(strings.TrimSpace(parts[1]), "\"")
		
		switch key {
		case "PROXY_ADDR":
			config.ProxyAddr = value
		case "TLS_ENABLED":
			config.TLSEnabled = value
		case "TLS_CERT_FILE":
			config.TLSCertFile = value
		case "TLS_KEY_FILE":
			config.TLSKeyFile = value
		case "CREDENTIAL_SOURCE":
			config.CredentialSource = value
		case "CREDENTIAL_FILE":
			config.CredentialFile = value
		case "CREDENTIAL_ENCRYPTION_KEY":
			config.CredentialKey = value
		case "BACKEND_TLS_MODE":
			config.BackendTLSMode = value
		}
		
		// Parse DSNs to extract connection details
		if strings.HasPrefix(key, "PG_READER_DSN") || strings.HasPrefix(key, "PG_WRITERS_CSV") {
			// Check if this is the backup database (secondary DSN in writers list)
			if strings.Contains(key, "PG_WRITERS_CSV") && strings.Contains(value, ",") {
				// Extract backup database from second DSN in writers list
				dsns := strings.Split(value, ",")
				if len(dsns) > 1 {
					backupDSN := strings.TrimSpace(dsns[1])
					config.BackupEndpoint = extractHost(backupDSN)
					config.BackupUsername = extractUsername(backupDSN)
					config.BackupPassword = extractPassword(backupDSN)
					config.BackupDatabase = extractDatabase(backupDSN)
				}
				// Extract primary database from first DSN
				primaryDSN := strings.TrimSpace(dsns[0])
				config.PrimaryWriter = extractHost(primaryDSN)
				config.PrimaryPassword = extractPassword(primaryDSN)
			} else {
				// Extract primary database details
				if config.PrimaryWriter == "" {
					config.PrimaryWriter = extractHost(value)
					config.PrimaryPassword = extractPassword(value)
				}
				if strings.Contains(value, "replica") || strings.Contains(key, "READER") {
					config.PrimaryReader = extractHost(value)
				}
			}
		}
	}
	
	return config, nil
}

func extractHost(dsn string) string {
	// Extract host from postgresql://user:pass@host:port/db
	if idx := strings.Index(dsn, "@"); idx != -1 {
		rest := dsn[idx+1:]
		if idx := strings.Index(rest, ":"); idx != -1 {
			return rest[:idx]
		}
		if idx := strings.Index(rest, "/"); idx != -1 {
			return rest[:idx]
		}
	}
	return ""
}

func extractUsername(dsn string) string {
	if idx := strings.Index(dsn, "://"); idx != -1 {
		rest := dsn[idx+3:]
		if idx := strings.Index(rest, ":"); idx != -1 {
			return rest[:idx]
		}
	}
	return "postgres"
}

func extractPassword(dsn string) string {
	if idx := strings.Index(dsn, "://"); idx != -1 {
		rest := dsn[idx+3:]
		if idx := strings.Index(rest, ":"); idx != -1 {
			rest = rest[idx+1:]
			if idx := strings.Index(rest, "@"); idx != -1 {
				return rest[:idx]
			}
		}
	}
	return ""
}

func extractDatabase(dsn string) string {
	if idx := strings.LastIndex(dsn, "/"); idx != -1 {
		rest := dsn[idx+1:]
		if idx := strings.Index(rest, "?"); idx != -1 {
			return rest[:idx]
		}
		return rest
	}
	return "postgres"
}

func isAccessible(host, username, password string) bool {
	dsn := fmt.Sprintf("postgresql://%s:%s@%s:5432/postgres?sslmode=require&connect_timeout=5",
		username, password, host)
	
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	
	conn, err := pgx.Connect(ctx, dsn)
	if err != nil {
		return false
	}
	defer conn.Close(ctx)
	
	var result int
	err = conn.QueryRow(ctx, "SELECT 1").Scan(&result)
	return err == nil
}

func backupConfig(src, dst string) error {
	data, err := os.ReadFile(src)
	if err != nil {
		return err
	}
	return os.WriteFile(dst, data, 0600)
}

func writeFailoverConfig(config *FailoverConfig) error {
	backupDSN := fmt.Sprintf("postgresql://%s:%s@%s:5432/%s?sslmode=require",
		config.BackupUsername, config.BackupPassword, config.BackupEndpoint, config.BackupDatabase)
	
	content := fmt.Sprintf(`# FAILOVER MODE - Generated by pprox-failover at %s
# Original configuration backed up
# All traffic routed to backup database

PROXY_ADDR="%s"

# FAILOVER: Using backup database for both reads and writes
PG_READER_DSN="%s"
PG_WRITERS_CSV="%s"

# Client TLS
TLS_ENABLED="%s"
TLS_CERT_FILE="%s"
TLS_KEY_FILE="%s"

# Authentication
CREDENTIAL_SOURCE="%s"
CREDENTIAL_FILE="%s"
CREDENTIAL_ENCRYPTION_KEY="%s"
CREDENTIAL_RELOAD_INTERVAL="5m"

# Backend TLS
BACKEND_TLS_MODE="%s"
`,
		time.Now().Format(time.RFC3339),
		config.ProxyAddr,
		backupDSN,
		backupDSN,
		config.TLSEnabled,
		config.TLSCertFile,
		config.TLSKeyFile,
		config.CredentialSource,
		config.CredentialFile,
		config.CredentialKey,
		config.BackendTLSMode,
	)
	
	return os.WriteFile(configPath, []byte(content), 0600)
}

func restartPprox() error {
	cmd := exec.Command("systemctl", "restart", "pprox")
	return cmd.Run()
}

func restoreConfig(backupPath string) error {
	data, err := os.ReadFile(backupPath)
	if err != nil {
		return err
	}
	return os.WriteFile(configPath, data, 0600)
}

func isPproxRunning() bool {
	cmd := exec.Command("systemctl", "is-active", "pprox")
	output, _ := cmd.Output()
	return strings.TrimSpace(string(output)) == "active"
}

func testPproxConnectivity() error {
	// Try to connect through pprox
	dsn := "postgresql://localhost:54329/postgres?sslmode=require&connect_timeout=5"
	
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	
	conn, err := pgx.Connect(ctx, dsn)
	if err != nil {
		return err
	}
	defer conn.Close(ctx)
	
	var result int
	return conn.QueryRow(ctx, "SELECT 1").Scan(&result)
}

func logFailover(backupPath string, duration time.Duration) {
	logEntry := fmt.Sprintf("[%s] Failover to backup database completed in %v (backup: %s)\n",
		time.Now().Format(time.RFC3339), duration.Round(time.Second), backupPath)
	
	f, err := os.OpenFile(logPath, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
	if err != nil {
		log.Printf("Failed to write to log file: %v", err)
		return
	}
	defer f.Close()
	
	f.WriteString(logEntry)
}
