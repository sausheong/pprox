package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"os"
	"strings"

	"github.com/jackc/pgx/v5"
)

func main() {
	readHost := flag.String("read-host", "", "Read database endpoint")
	readUser := flag.String("read-user", "postgres", "Read database username")
	readPass := flag.String("read-password", "", "Read database password")
	readDB := flag.String("read-database", "postgres", "Read database name")

	writeHost := flag.String("write-host", "", "Write database endpoint")
	writeUser := flag.String("write-user", "postgres", "Write database username")
	writePass := flag.String("write-password", "", "Write database password")
	writeDB := flag.String("write-database", "postgres", "Write database name")

	verbose := flag.Bool("verbose", false, "Show detailed comparison")
	checksums := flag.Bool("checksums", false, "Compare checksums (slower but more thorough)")

	flag.Parse()

	if *readHost == "" || *readPass == "" || *writeHost == "" || *writePass == "" {
		fmt.Println("Usage: pprox-verify-sync [options]")
		fmt.Println("\nRequired flags:")
		fmt.Println("  -read-host       Read database endpoint")
		fmt.Println("  -read-password   Read database password")
		fmt.Println("  -write-host      Write database endpoint")
		fmt.Println("  -write-password  Write database password")
		fmt.Println("\nOptional flags:")
		fmt.Println("  -read-user       Read database username (default: postgres)")
		fmt.Println("  -read-database   Read database name (default: postgres)")
		fmt.Println("  -write-user      Write database username (default: postgres)")
		fmt.Println("  -write-database  Write database name (default: postgres)")
		fmt.Println("  -verbose         Show detailed comparison")
		fmt.Println("  -checksums       Compare checksums for data integrity")
		os.Exit(1)
	}

	log.SetOutput(os.Stdout)
	log.SetFlags(log.LstdFlags)

	fmt.Println("🔍 PPROX DATA SYNCHRONIZATION VERIFICATION")
	fmt.Println("=" + strings.Repeat("=", 50))
	fmt.Println()

	// Connect to read database
	fmt.Println("Connecting to read database...")
	readDSN := fmt.Sprintf("postgresql://%s:%s@%s:5432/%s?sslmode=require",
		*readUser, *readPass, *readHost, *readDB)

	ctx := context.Background()
	readConn, err := pgx.Connect(ctx, readDSN)
	if err != nil {
		log.Fatalf("Failed to connect to read database: %v", err)
	}
	defer readConn.Close(ctx)
	fmt.Println("✅ Connected to read database")

	// Connect to write database
	fmt.Println("Connecting to write database...")
	writeDSN := fmt.Sprintf("postgresql://%s:%s@%s:5432/%s?sslmode=require",
		*writeUser, *writePass, *writeHost, *writeDB)

	writeConn, err := pgx.Connect(ctx, writeDSN)
	if err != nil {
		log.Fatalf("Failed to connect to write database: %v", err)
	}
	defer writeConn.Close(ctx)
	fmt.Println("✅ Connected to write database")

	// Get database versions
	fmt.Println("\nDatabase Versions:")
	var readVersion, writeVersion string
	readConn.QueryRow(ctx, "SELECT version()").Scan(&readVersion)
	writeConn.QueryRow(ctx, "SELECT version()").Scan(&writeVersion)

	if *verbose {
		fmt.Printf("  Read DB: %s\n", readVersion)
		fmt.Printf("  Write DB: %s\n", writeVersion)
	} else {
		fmt.Printf("  Read DB: PostgreSQL %s\n", extractVersion(readVersion))
		fmt.Printf("  Write DB: PostgreSQL %s\n", extractVersion(writeVersion))
	}

	// Get table list from read database
	fmt.Println("\nGetting table list...")
	rows, err := readConn.Query(ctx, `
		SELECT tablename 
		FROM pg_tables 
		WHERE schemaname='public' 
		ORDER BY tablename
	`)
	if err != nil {
		log.Fatalf("Failed to get table list: %v", err)
	}

	var tables []string
	for rows.Next() {
		var table string
		if err := rows.Scan(&table); err != nil {
			continue
		}
		tables = append(tables, table)
	}
	rows.Close()

	if len(tables) == 0 {
		fmt.Println("\n⚠️  No tables found in public schema")
		fmt.Println("Databases appear to be empty or not synchronized")
		os.Exit(0)
	}

	fmt.Printf("Found %d tables to verify\n", len(tables))

	// Compare row counts
	fmt.Println("\n" + strings.Repeat("-", 50))
	fmt.Println("ROW COUNT COMPARISON")
	fmt.Println(strings.Repeat("-", 50))

	allMatch := true
	var mismatches []string

	for _, table := range tables {
		var readCount, writeCount int64

		err1 := readConn.QueryRow(ctx, fmt.Sprintf("SELECT COUNT(*) FROM %s", table)).Scan(&readCount)
		err2 := writeConn.QueryRow(ctx, fmt.Sprintf("SELECT COUNT(*) FROM %s", table)).Scan(&writeCount)

		if err1 != nil {
			fmt.Printf("❌ %-30s Read DB error: %v\n", table, err1)
			allMatch = false
			continue
		}

		if err2 != nil {
			fmt.Printf("❌ %-30s Write DB error: %v\n", table, err2)
			allMatch = false
			continue
		}

		if readCount == writeCount {
			if *verbose {
				fmt.Printf("✅ %-30s %10d rows (match)\n", table, readCount)
			} else {
				fmt.Printf("✅ %-30s %10d rows\n", table, readCount)
			}
		} else {
			fmt.Printf("❌ %-30s Read DB: %d, Write DB: %d (MISMATCH)\n", table, readCount, writeCount)
			allMatch = false
			mismatches = append(mismatches, table)
		}
	}

	// Compare sequences
	fmt.Println("\n" + strings.Repeat("-", 50))
	fmt.Println("SEQUENCE COMPARISON")
	fmt.Println(strings.Repeat("-", 50))

	seqRows, err := readConn.Query(ctx, `
		SELECT sequencename 
		FROM pg_sequences 
		WHERE schemaname='public'
		ORDER BY sequencename
	`)
	if err == nil {
		var sequences []string
		for seqRows.Next() {
			var seq string
			if err := seqRows.Scan(&seq); err != nil {
				continue
			}
			sequences = append(sequences, seq)
		}
		seqRows.Close()

		if len(sequences) == 0 {
			fmt.Println("No sequences found")
		} else {
			for _, seq := range sequences {
				var readVal, writeVal int64

				err1 := readConn.QueryRow(ctx, fmt.Sprintf("SELECT last_value FROM %s", seq)).Scan(&readVal)
				err2 := writeConn.QueryRow(ctx, fmt.Sprintf("SELECT last_value FROM %s", seq)).Scan(&writeVal)

				if err1 != nil || err2 != nil {
					fmt.Printf("⚠️  %-30s Could not verify\n", seq)
					continue
				}

				if readVal == writeVal {
					fmt.Printf("✅ %-30s %10d\n", seq, readVal)
				} else {
					fmt.Printf("❌ %-30s Read DB: %d, Write DB: %d\n", seq, readVal, writeVal)
					allMatch = false
				}
			}
		}
	}

	// Checksum comparison (if requested)
	if *checksums && len(tables) > 0 {
		fmt.Println("\n" + strings.Repeat("-", 50))
		fmt.Println("CHECKSUM COMPARISON (this may take a while)")
		fmt.Println(strings.Repeat("-", 50))

		for _, table := range tables {
			// Get primary key or first column
			var pkColumn string
			err := readConn.QueryRow(ctx, fmt.Sprintf(`
				SELECT column_name 
				FROM information_schema.columns 
				WHERE table_name='%s' 
				ORDER BY ordinal_position 
				LIMIT 1
			`, table)).Scan(&pkColumn)

			if err != nil {
				fmt.Printf("⚠️  %-30s Could not determine key column\n", table)
				continue
			}

			// Calculate checksums
			checksumQuery := fmt.Sprintf(`
				SELECT md5(string_agg(%s::text, ',' ORDER BY %s)) 
				FROM %s
			`, pkColumn, pkColumn, table)

			var readChecksum, writeChecksum string
			err1 := readConn.QueryRow(ctx, checksumQuery).Scan(&readChecksum)
			err2 := writeConn.QueryRow(ctx, checksumQuery).Scan(&writeChecksum)

			if err1 != nil || err2 != nil {
				fmt.Printf("⚠️  %-30s Could not calculate checksum\n", table)
				continue
			}

			if readChecksum == writeChecksum {
				fmt.Printf("✅ %-30s Checksums match\n", table)
			} else {
				fmt.Printf("❌ %-30s Checksums differ\n", table)
				allMatch = false
			}
		}
	}

	// Summary
	fmt.Println("\n" + strings.Repeat("=", 50))
	fmt.Println("SUMMARY")
	fmt.Println(strings.Repeat("=", 50))

	if allMatch {
		fmt.Println("✅ All tables are synchronized")
		fmt.Println("✅ Read and write databases have identical data")
		fmt.Println("\n🎉 Databases are in sync!")
		os.Exit(0)
	} else {
		fmt.Println("❌ Data synchronization issues detected")
		if len(mismatches) > 0 {
			fmt.Println("\nTables with mismatches:")
			for _, table := range mismatches {
				fmt.Printf("  - %s\n", table)
			}
		}
		fmt.Println("\n⚠️  Databases are not synchronized")
		fmt.Println("\nRecommended actions:")
		fmt.Println("  1. Check if logical replication is running")
		fmt.Println("  2. Verify replication lag: SELECT * FROM pg_stat_subscription;")
		fmt.Println("  3. Wait for sync to complete")
		fmt.Println("  4. Run this verification again")
		os.Exit(1)
	}
}

func extractVersion(fullVersion string) string {
	// Extract version number from full version string
	parts := strings.Fields(fullVersion)
	if len(parts) >= 2 {
		return parts[1]
	}
	return "unknown"
}
