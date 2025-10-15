package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"os"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
)

func main() {
	sourceHost := flag.String("source-host", "", "Source database endpoint")
	sourceUser := flag.String("source-user", "postgres", "Source database username")
	sourcePass := flag.String("source-password", "", "Source database password")
	sourceDB := flag.String("source-database", "postgres", "Source database name")

	targetHost := flag.String("target-host", "", "Target database endpoint")
	targetUser := flag.String("target-user", "postgres", "Target database username")
	targetPass := flag.String("target-password", "", "Target database password")
	targetDB := flag.String("target-database", "postgres", "Target database name")

	dryRun := flag.Bool("dry-run", false, "Show what would be synced without making changes")
	tables := flag.String("tables", "", "Comma-separated list of tables to sync (default: all)")
	truncate := flag.Bool("truncate", false, "Truncate target tables before copying (WARNING: destructive)")
	skipVerify := flag.Bool("skip-verify", false, "Skip post-sync verification")

	flag.Parse()

	if *sourceHost == "" || *sourcePass == "" || *targetHost == "" || *targetPass == "" {
		fmt.Println("Usage: pprox-sync-databases [options]")
		fmt.Println("\nRequired flags:")
		fmt.Println("  -source-host       Source database endpoint")
		fmt.Println("  -source-password   Source database password")
		fmt.Println("  -target-host       Target database endpoint")
		fmt.Println("  -target-password   Target database password")
		fmt.Println("\nOptional flags:")
		fmt.Println("  -source-user       Source database username (default: postgres)")
		fmt.Println("  -source-database   Source database name (default: postgres)")
		fmt.Println("  -target-user       Target database username (default: postgres)")
		fmt.Println("  -target-database   Target database name (default: postgres)")
		fmt.Println("  -tables            Comma-separated list of tables to sync")
		fmt.Println("  -truncate          Truncate target tables before copying")
		fmt.Println("  -dry-run           Show what would be synced without making changes")
		fmt.Println("  -skip-verify       Skip post-sync verification")
		fmt.Println("\nExample:")
		fmt.Println("  pprox-sync-databases \\")
		fmt.Println("    -source-host rds.amazonaws.com \\")
		fmt.Println("    -source-password secret1 \\")
		fmt.Println("    -target-host cloudsql.ip \\")
		fmt.Println("    -target-password secret2 \\")
		fmt.Println("    -truncate")
		os.Exit(1)
	}

	log.SetOutput(os.Stdout)
	log.SetFlags(log.LstdFlags)

	fmt.Println("🔄 PPROX DATABASE SYNCHRONIZATION")
	fmt.Println("=" + strings.Repeat("=", 50))
	fmt.Println()

	if *dryRun {
		fmt.Println("🔍 DRY RUN MODE - No changes will be made")
		fmt.Println()
	}

	// Connect to source database
	fmt.Println("Connecting to source database...")
	fmt.Printf("  Host: %s\n", *sourceHost)
	sourceDSN := fmt.Sprintf("postgresql://%s:%s@%s:5432/%s?sslmode=require",
		*sourceUser, *sourcePass, *sourceHost, *sourceDB)

	ctx := context.Background()
	sourceConn, err := pgx.Connect(ctx, sourceDSN)
	if err != nil {
		log.Fatalf("Failed to connect to source database: %v", err)
	}
	defer sourceConn.Close(ctx)
	fmt.Println("✅ Connected to source database")

	// Connect to target database
	fmt.Println("\nConnecting to target database...")
	fmt.Printf("  Host: %s\n", *targetHost)
	targetDSN := fmt.Sprintf("postgresql://%s:%s@%s:5432/%s?sslmode=require",
		*targetUser, *targetPass, *targetHost, *targetDB)

	targetConn, err := pgx.Connect(ctx, targetDSN)
	if err != nil {
		log.Fatalf("Failed to connect to target database: %v", err)
	}
	defer targetConn.Close(ctx)
	fmt.Println("✅ Connected to target database")

	// Get list of tables to sync
	var tablesToSync []string
	if *tables != "" {
		tablesToSync = strings.Split(*tables, ",")
		for i := range tablesToSync {
			tablesToSync[i] = strings.TrimSpace(tablesToSync[i])
		}
	} else {
		// Get all tables from source
		fmt.Println("\nGetting table list from source database...")
		rows, err := sourceConn.Query(ctx, `
			SELECT tablename 
			FROM pg_tables 
			WHERE schemaname='public' 
			ORDER BY tablename
		`)
		if err != nil {
			log.Fatalf("Failed to get table list: %v", err)
		}

		for rows.Next() {
			var table string
			if err := rows.Scan(&table); err != nil {
				continue
			}
			tablesToSync = append(tablesToSync, table)
		}
		rows.Close()
	}

	if len(tablesToSync) == 0 {
		fmt.Println("\n⚠️  No tables found to sync")
		os.Exit(0)
	}

	fmt.Printf("Found %d tables to sync: %v\n", len(tablesToSync), tablesToSync)

	if *dryRun {
		fmt.Println("\nWould perform the following actions:")
		for _, table := range tablesToSync {
			var count int64
			sourceConn.QueryRow(ctx, fmt.Sprintf("SELECT COUNT(*) FROM %s", table)).Scan(&count)
			if *truncate {
				fmt.Printf("  1. TRUNCATE %s on target\n", table)
			}
			fmt.Printf("  2. COPY %d rows from %s\n", count, table)
		}
		fmt.Println("\nRun without --dry-run to execute sync")
		return
	}

	// Confirm with user
	fmt.Println("\n⚠️  WARNING: This will modify the target database!")
	if *truncate {
		fmt.Println("   Tables will be TRUNCATED before copying (all existing data will be lost)")
	}
	fmt.Print("\nProceed with sync? (yes/no): ")

	var response string
	fmt.Scanln(&response)
	if strings.ToLower(response) != "yes" {
		fmt.Println("Sync cancelled")
		os.Exit(0)
	}

	// Start sync
	startTime := time.Now()
	fmt.Println("\n" + strings.Repeat("-", 50))
	fmt.Println("SYNCING TABLES")
	fmt.Println(strings.Repeat("-", 50))

	totalRows := int64(0)
	for _, table := range tablesToSync {
		fmt.Printf("\nSyncing table: %s\n", table)

		// Get row count
		var sourceCount int64
		err := sourceConn.QueryRow(ctx, fmt.Sprintf("SELECT COUNT(*) FROM %s", table)).Scan(&sourceCount)
		if err != nil {
			log.Printf("  ❌ Failed to count rows: %v", err)
			continue
		}
		fmt.Printf("  Source rows: %d\n", sourceCount)

		// Truncate target table if requested
		if *truncate {
			fmt.Printf("  Truncating target table...\n")
			_, err := targetConn.Exec(ctx, fmt.Sprintf("TRUNCATE TABLE %s CASCADE", table))
			if err != nil {
				log.Printf("  ❌ Failed to truncate: %v", err)
				continue
			}
		}

		// Get column names
		rows, err := sourceConn.Query(ctx, fmt.Sprintf(`
			SELECT column_name 
			FROM information_schema.columns 
			WHERE table_name='%s' AND table_schema='public'
			ORDER BY ordinal_position
		`, table))
		if err != nil {
			log.Printf("  ❌ Failed to get columns: %v", err)
			continue
		}

		var columns []string
		for rows.Next() {
			var col string
			if err := rows.Scan(&col); err != nil {
				continue
			}
			columns = append(columns, col)
		}
		rows.Close()

		if len(columns) == 0 {
			log.Printf("  ❌ No columns found")
			continue
		}

		// Copy data
		fmt.Printf("  Copying data...\n")
		columnList := strings.Join(columns, ", ")
		placeholders := make([]string, len(columns))
		for i := range placeholders {
			placeholders[i] = fmt.Sprintf("$%d", i+1)
		}
		insertSQL := fmt.Sprintf("INSERT INTO %s (%s) VALUES (%s)",
			table, columnList, strings.Join(placeholders, ", "))

		// Read from source and insert into target
		dataRows, err := sourceConn.Query(ctx, fmt.Sprintf("SELECT %s FROM %s", columnList, table))
		if err != nil {
			log.Printf("  ❌ Failed to read data: %v", err)
			continue
		}

		copiedRows := int64(0)
		batch := &pgx.Batch{}
		batchSize := 0

		for dataRows.Next() {
			values, err := dataRows.Values()
			if err != nil {
				log.Printf("  ⚠️  Failed to read row: %v", err)
				continue
			}

			batch.Queue(insertSQL, values...)
			batchSize++

			// Execute batch every 1000 rows
			if batchSize >= 1000 {
				br := targetConn.SendBatch(ctx, batch)
				for i := 0; i < batchSize; i++ {
					_, err := br.Exec()
					if err != nil {
						log.Printf("  ⚠️  Failed to insert row: %v", err)
					} else {
						copiedRows++
					}
				}
				br.Close()
				batch = &pgx.Batch{}
				batchSize = 0
				fmt.Printf("  Progress: %d rows copied\r", copiedRows)
			}
		}

		// Execute remaining batch
		if batchSize > 0 {
			br := targetConn.SendBatch(ctx, batch)
			for i := 0; i < batchSize; i++ {
				_, err := br.Exec()
				if err != nil {
					log.Printf("  ⚠️  Failed to insert row: %v", err)
				} else {
					copiedRows++
				}
			}
			br.Close()
		}

		dataRows.Close()

		fmt.Printf("  ✅ Copied %d rows\n", copiedRows)
		totalRows += copiedRows
	}

	duration := time.Since(startTime)

	// Verify sync
	if !*skipVerify {
		fmt.Println("\n" + strings.Repeat("-", 50))
		fmt.Println("VERIFYING SYNC")
		fmt.Println(strings.Repeat("-", 50))

		allMatch := true
		for _, table := range tablesToSync {
			var sourceCount, targetCount int64
			sourceConn.QueryRow(ctx, fmt.Sprintf("SELECT COUNT(*) FROM %s", table)).Scan(&sourceCount)
			targetConn.QueryRow(ctx, fmt.Sprintf("SELECT COUNT(*) FROM %s", table)).Scan(&targetCount)

			if sourceCount == targetCount {
				fmt.Printf("✅ %-30s %10d rows (match)\n", table, sourceCount)
			} else {
				fmt.Printf("❌ %-30s Source: %d, Target: %d (MISMATCH)\n", table, sourceCount, targetCount)
				allMatch = false
			}
		}

		if !allMatch {
			fmt.Println("\n⚠️  Warning: Some tables have mismatched row counts")
		}
	}

	// Summary
	fmt.Println("\n" + strings.Repeat("=", 50))
	fmt.Println("SYNC COMPLETED")
	fmt.Println(strings.Repeat("=", 50))
	fmt.Printf("\nTotal rows copied: %d\n", totalRows)
	fmt.Printf("Duration: %v\n", duration.Round(time.Second))
	fmt.Printf("Tables synced: %d\n", len(tablesToSync))
	fmt.Println("\n✅ Database synchronization complete!")
}
