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

type FailbackConfig struct {
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
	skipVerify := flag.Bool("skip-verify", false, "Skip primary database accessibility verification")
	skipSync := flag.Bool("skip-sync-check", false, "Skip data synchronization verification")
	flag.Parse()

	log.SetOutput(os.Stdout)
	log.SetFlags(log.LstdFlags | log.Lshortfile)

	fmt.Println("🔄 PPROX FAILBACK TO PRIMARY DATABASE")
	fmt.Println("=" + strings.Repeat("=", 50))
	fmt.Println()

	// Load current configuration
	config, err := loadConfig()
	if err != nil {
		log.Fatalf("Failed to load configuration: %v", err)
	}

	// Verify primary database is accessible
	if !*skipVerify {
		fmt.Println("Checking primary database status...")
		if !isAccessible(config.PrimaryWriter, "postgres", config.PrimaryPassword) {
			log.Fatalf("❌ Primary database is not accessible - cannot failback")
		}
		fmt.Println("✅ Primary database is accessible")

		if config.PrimaryReader != "" {
			if !isAccessible(config.PrimaryReader, "postgres", config.PrimaryPassword) {
				log.Printf("⚠️  Warning: Primary read replica is not accessible")
			} else {
				fmt.Println("✅ Primary read replica is accessible")
			}
		}
	}

	// Verify data synchronization
	if !*skipSync {
		fmt.Println("\nVerifying data synchronization...")
		if err := verifyDataSync(config); err != nil {
			log.Printf("⚠️  Warning: Data sync verification failed: %v", err)
			fmt.Print("\nContinue with failback anyway? (yes/no): ")
			var response string
			fmt.Scanln(&response)
			if strings.ToLower(response) != "yes" {
				fmt.Println("Failback cancelled")
				os.Exit(0)
			}
		} else {
			fmt.Println("✅ Data appears to be synchronized")
		}
	}

	if *dryRun {
		fmt.Println("\n🔍 DRY RUN MODE - No changes will be made")
		fmt.Println("\nWould perform the following actions:")
		fmt.Println("1. Backup current configuration")
		fmt.Println("2. Restore configuration to use primary database")
		fmt.Println("3. Restart pprox service")
		fmt.Println("4. Verify pprox is running")
		fmt.Println("5. Test database connectivity")
		fmt.Println("\nRun without --dry-run to execute failback")
		return
	}

	// Confirm with user
	fmt.Println("\n⚠️  This will:")
	fmt.Println("   - Backup current configuration")
	fmt.Println("   - Switch traffic back to primary database")
	fmt.Println("   - Restart pprox (brief downtime)")
	fmt.Print("\nProceed with failback? (yes/no): ")
	
	var response string
	fmt.Scanln(&response)
	if strings.ToLower(response) != "yes" {
		fmt.Println("Failback cancelled")
		os.Exit(0)
	}

	// Execute failback
	startTime := time.Now()
	
	// Step 1: Backup configuration
	fmt.Println("\n📝 Step 1: Backing up current configuration...")
	backupPath := fmt.Sprintf("%s.%s", backupPathPrefix, time.Now().Format("20060102_150405"))
	if err := backupConfig(configPath, backupPath); err != nil {
		log.Fatalf("Failed to backup configuration: %v", err)
	}
	fmt.Printf("✅ Configuration backed up to: %s\n", backupPath)

	// Step 2: Update configuration
	fmt.Println("\n📝 Step 2: Restoring configuration to use primary database...")
	if err := writeFailbackConfig(config); err != nil {
		log.Fatalf("Failed to update configuration: %v", err)
	}
	fmt.Println("✅ Configuration updated")

	// Step 3: Restart pprox
	fmt.Println("\n🔄 Step 3: Restarting pprox service...")
	if err := restartPprox(); err != nil {
		log.Printf("Failed to restart pprox: %v", err)
		fmt.Println("\n❌ Failback failed - attempting to restore configuration...")
		if err := restoreConfig(backupPath); err != nil {
			log.Fatalf("Failed to restore configuration: %v", err)
		}
		if err := restartPprox(); err != nil {
			log.Fatalf("Failed to restart pprox after restore: %v", err)
		}
		log.Fatalf("Configuration restored, but failback failed")
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
	logFailback(backupPath, duration)

	fmt.Println("\n" + strings.Repeat("=", 50))
	fmt.Println("✅ FAILBACK COMPLETED SUCCESSFULLY")
	fmt.Println(strings.Repeat("=", 50))
	fmt.Printf("\nDuration: %v\n", duration.Round(time.Second))
	fmt.Println("\nTraffic is now routed to:")
	fmt.Println("  - Reader: Primary database (replica)")
	fmt.Println("  - Writers: Primary database + Backup database")
	fmt.Printf("\nConfiguration backup: %s\n", backupPath)
	fmt.Printf("Failback log: %s\n", logPath)
	fmt.Println("\nNormal operation restored!")
}

func loadConfig() (*FailbackConfig, error) {
	// Read environment file
	data, err := os.ReadFile(configPath)
	if err != nil {
		return nil, err
	}

	config := &FailbackConfig{}
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
	
	// Try to find primary database details from backup files if not in current config
	if config.PrimaryWriter == "" {
		if err := loadFromBackup(config); err != nil {
			return nil, fmt.Errorf("could not find primary database configuration: %w", err)
		}
	}
	
	return config, nil
}

func loadFromBackup(config *FailbackConfig) error {
	// Find most recent non-failover backup
	files, err := os.ReadDir("/etc/pprox")
	if err != nil {
		return err
	}
	
	var latestBackup string
	for _, file := range files {
		if strings.HasPrefix(file.Name(), ".env.backup") && !strings.Contains(file.Name(), "failover") {
			if latestBackup == "" || file.Name() > latestBackup {
				latestBackup = file.Name()
			}
		}
	}
	
	if latestBackup == "" {
		return fmt.Errorf("no backup configuration found")
	}
	
	data, err := os.ReadFile("/etc/pprox/" + latestBackup)
	if err != nil {
		return err
	}
	
	lines := strings.Split(string(data), "\n")
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "PG_READER_DSN") || strings.HasPrefix(line, "PG_WRITERS_CSV") {
			parts := strings.SplitN(line, "=", 2)
			if len(parts) == 2 {
				value := strings.Trim(strings.TrimSpace(parts[1]), "\"")
				// Extract primary database from first DSN
				if strings.Contains(value, ",") {
					dsns := strings.Split(value, ",")
					if len(dsns) > 0 {
						primaryDSN := strings.TrimSpace(dsns[0])
						if config.PrimaryWriter == "" {
							config.PrimaryWriter = extractHost(primaryDSN)
							config.PrimaryPassword = extractPassword(primaryDSN)
						}
					}
				} else {
					if config.PrimaryWriter == "" {
						config.PrimaryWriter = extractHost(value)
						config.PrimaryPassword = extractPassword(value)
					}
					if strings.Contains(value, "replica") || strings.Contains(parts[0], "READER") {
						config.PrimaryReader = extractHost(value)
					}
				}
			}
		}
	}
	
	return nil
}

func extractHost(dsn string) string {
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

func verifyDataSync(config *FailbackConfig) error {
	// Connect to backup database
	backupDSN := fmt.Sprintf("postgresql://%s:%s@%s:5432/%s?sslmode=require&connect_timeout=5",
		config.BackupUsername, config.BackupPassword, config.BackupEndpoint, config.BackupDatabase)
	
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	
	backupConn, err := pgx.Connect(ctx, backupDSN)
	if err != nil {
		return fmt.Errorf("failed to connect to backup database: %w", err)
	}
	defer backupConn.Close(ctx)
	
	// Connect to primary database
	primaryDSN := fmt.Sprintf("postgresql://postgres:%s@%s:5432/postgres?sslmode=require&connect_timeout=5",
		config.PrimaryPassword, config.PrimaryWriter)
	
	primaryConn, err := pgx.Connect(ctx, primaryDSN)
	if err != nil {
		return fmt.Errorf("failed to connect to primary database: %w", err)
	}
	defer primaryConn.Close(ctx)
	
	// Get table list from backup database
	rows, err := backupConn.Query(ctx, "SELECT tablename FROM pg_tables WHERE schemaname='public'")
	if err != nil {
		return fmt.Errorf("failed to get table list: %w", err)
	}
	defer rows.Close()
	
	var tables []string
	for rows.Next() {
		var table string
		if err := rows.Scan(&table); err != nil {
			continue
		}
		tables = append(tables, table)
	}
	
	if len(tables) == 0 {
		fmt.Println("ℹ️  No tables found to verify")
		return nil
	}
	
	// Compare row counts for each table
	fmt.Println("\nComparing row counts:")
	mismatch := false
	for _, table := range tables {
		var backupCount, primaryCount int64
		
		err1 := backupConn.QueryRow(ctx, fmt.Sprintf("SELECT COUNT(*) FROM %s", table)).Scan(&backupCount)
		err2 := primaryConn.QueryRow(ctx, fmt.Sprintf("SELECT COUNT(*) FROM %s", table)).Scan(&primaryCount)
		
		if err1 != nil || err2 != nil {
			fmt.Printf("  ⚠️  %s: Could not verify\n", table)
			continue
		}
		
		if backupCount == primaryCount {
			fmt.Printf("  ✅ %s: %d rows (match)\n", table, backupCount)
		} else {
			fmt.Printf("  ❌ %s: Backup=%d, Primary=%d (MISMATCH)\n", table, backupCount, primaryCount)
			mismatch = true
		}
	}
	
	if mismatch {
		return fmt.Errorf("data mismatch detected between backup and primary databases")
	}
	
	return nil
}

func backupConfig(src, dst string) error {
	data, err := os.ReadFile(src)
	if err != nil {
		return err
	}
	return os.WriteFile(dst, data, 0600)
}

func writeFailbackConfig(config *FailbackConfig) error {
	readerDSN := fmt.Sprintf("postgresql://postgres:%s@%s:5432/postgres?sslmode=require",
		config.PrimaryPassword, config.PrimaryReader)
	
	writerDSNs := fmt.Sprintf("postgresql://postgres:%s@%s:5432/postgres?sslmode=require,postgresql://%s:%s@%s:5432/%s?sslmode=require",
		config.PrimaryPassword, config.PrimaryWriter,
		config.BackupUsername, config.BackupPassword, config.BackupEndpoint, config.BackupDatabase)
	
	content := fmt.Sprintf(`# NORMAL MODE - Restored by pprox-failback at %s
# Traffic routed to primary database (reader + writer) and backup database (writer)

PROXY_ADDR="%s"

# Normal configuration: Primary database for reads, Primary + Backup databases for writes
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
		readerDSN,
		writerDSNs,
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

func logFailback(backupPath string, duration time.Duration) {
	logEntry := fmt.Sprintf("[%s] Failback to primary database completed in %v (backup: %s)\n",
		time.Now().Format(time.RFC3339), duration.Round(time.Second), backupPath)
	
	f, err := os.OpenFile(logPath, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
	if err != nil {
		log.Printf("Failed to write to log file: %v", err)
		return
	}
	defer f.Close()
	
	f.WriteString(logEntry)
}
