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

type HealthStatus struct {
	Component string
	Status    string
	Message   string
	Latency   time.Duration
}

func main() {
	pproxAddr := flag.String("pprox", "localhost:54329", "pprox address")
	readHost := flag.String("read-host", "", "Read database endpoint (optional)")
	readUser := flag.String("read-user", "postgres", "Read database username")
	readPass := flag.String("read-password", "", "Read database password")
	writeHost := flag.String("write-host", "", "Write database endpoint (optional)")
	writePass := flag.String("write-password", "", "Write database password")
	continuous := flag.Bool("watch", false, "Continuous monitoring (refresh every 10s)")
	json := flag.Bool("json", false, "Output in JSON format")
	
	flag.Parse()

	log.SetOutput(os.Stdout)
	log.SetFlags(0)

	if *continuous {
		for {
			runHealthCheck(*pproxAddr, *readHost, *readUser, *readPass, *writeHost, *writePass, *json)
			time.Sleep(10 * time.Second)
			if !*json {
				fmt.Print("\033[H\033[2J") // Clear screen
			}
		}
	} else {
		runHealthCheck(*pproxAddr, *readHost, *readUser, *readPass, *writeHost, *writePass, *json)
	}
}

func runHealthCheck(pproxAddr, readHost, readUser, readPass, writeHost, writePass string, jsonOutput bool) {
	results := []HealthStatus{}
	
	if !jsonOutput {
		fmt.Println("🏥 PPROX HEALTH CHECK")
		fmt.Println("=" + strings.Repeat("=", 50))
		fmt.Printf("Time: %s\n\n", time.Now().Format(time.RFC3339))
	}

	// Check pprox service
	if !jsonOutput {
		fmt.Println("Checking pprox service...")
	}
	pproxStatus := checkPproxService()
	results = append(results, pproxStatus)
	if !jsonOutput {
		printStatus(pproxStatus)
	}

	// Check pprox connectivity
	if !jsonOutput {
		fmt.Println("\nChecking pprox connectivity...")
	}
	pproxConnStatus := checkPproxConnectivity(pproxAddr)
	results = append(results, pproxConnStatus)
	if !jsonOutput {
		printStatus(pproxConnStatus)
	}

	// Check read database (if provided)
	if readHost != "" && readPass != "" {
		if !jsonOutput {
			fmt.Println("\nChecking read database...")
		}
		readStatus := checkDatabase("Read DB", readHost, readUser, readPass, "postgres")
		results = append(results, readStatus)
		if !jsonOutput {
			printStatus(readStatus)
		}
	}

	// Check write database (if provided)
	if writeHost != "" && writePass != "" {
		if !jsonOutput {
			fmt.Println("\nChecking write database...")
		}
		writeStatus := checkDatabase("Write DB", writeHost, "postgres", writePass, "postgres")
		results = append(results, writeStatus)
		if !jsonOutput {
			printStatus(writeStatus)
		}
	}

	// Overall status
	if !jsonOutput {
		fmt.Println("\n" + strings.Repeat("=", 50))
		overallHealthy := true
		for _, r := range results {
			if r.Status != "healthy" {
				overallHealthy = false
				break
			}
		}
		
		if overallHealthy {
			fmt.Println("✅ OVERALL STATUS: HEALTHY")
		} else {
			fmt.Println("❌ OVERALL STATUS: UNHEALTHY")
			fmt.Println("\nIssues detected:")
			for _, r := range results {
				if r.Status != "healthy" {
					fmt.Printf("  - %s: %s\n", r.Component, r.Message)
				}
			}
		}
	} else {
		// JSON output
		fmt.Println("{")
		fmt.Printf("  \"timestamp\": \"%s\",\n", time.Now().Format(time.RFC3339))
		fmt.Println("  \"checks\": [")
		for i, r := range results {
			fmt.Println("    {")
			fmt.Printf("      \"component\": \"%s\",\n", r.Component)
			fmt.Printf("      \"status\": \"%s\",\n", r.Status)
			fmt.Printf("      \"message\": \"%s\",\n", r.Message)
			fmt.Printf("      \"latency_ms\": %d\n", r.Latency.Milliseconds())
			if i < len(results)-1 {
				fmt.Println("    },")
			} else {
				fmt.Println("    }")
			}
		}
		fmt.Println("  ]")
		fmt.Println("}")
	}
}

func checkPproxService() HealthStatus {
	start := time.Now()
	cmd := exec.Command("systemctl", "is-active", "pprox")
	output, err := cmd.Output()
	latency := time.Since(start)
	
	status := strings.TrimSpace(string(output))
	
	if err != nil || status != "active" {
		return HealthStatus{
			Component: "pprox service",
			Status:    "unhealthy",
			Message:   "Service is not running",
			Latency:   latency,
		}
	}
	
	return HealthStatus{
		Component: "pprox service",
		Status:    "healthy",
		Message:   "Service is active",
		Latency:   latency,
	}
}

func checkPproxConnectivity(addr string) HealthStatus {
	dsn := fmt.Sprintf("postgresql://%s/postgres?sslmode=require&connect_timeout=5", addr)
	
	start := time.Now()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	
	conn, err := pgx.Connect(ctx, dsn)
	if err != nil {
		return HealthStatus{
			Component: "pprox connectivity",
			Status:    "unhealthy",
			Message:   fmt.Sprintf("Cannot connect: %v", err),
			Latency:   time.Since(start),
		}
	}
	defer conn.Close(ctx)
	
	var result int
	err = conn.QueryRow(ctx, "SELECT 1").Scan(&result)
	latency := time.Since(start)
	
	if err != nil {
		return HealthStatus{
			Component: "pprox connectivity",
			Status:    "unhealthy",
			Message:   fmt.Sprintf("Query failed: %v", err),
			Latency:   latency,
		}
	}
	
	return HealthStatus{
		Component: "pprox connectivity",
		Status:    "healthy",
		Message:   fmt.Sprintf("Connected successfully (%dms)", latency.Milliseconds()),
		Latency:   latency,
	}
}

func checkDatabase(name, host, user, password, database string) HealthStatus {
	dsn := fmt.Sprintf("postgresql://%s:%s@%s:5432/%s?sslmode=require&connect_timeout=5",
		user, password, host, database)
	
	start := time.Now()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	
	conn, err := pgx.Connect(ctx, dsn)
	if err != nil {
		return HealthStatus{
			Component: name,
			Status:    "unhealthy",
			Message:   fmt.Sprintf("Cannot connect: %v", err),
			Latency:   time.Since(start),
		}
	}
	defer conn.Close(ctx)
	
	var result int
	err = conn.QueryRow(ctx, "SELECT 1").Scan(&result)
	latency := time.Since(start)
	
	if err != nil {
		return HealthStatus{
			Component: name,
			Status:    "unhealthy",
			Message:   fmt.Sprintf("Query failed: %v", err),
			Latency:   latency,
		}
	}
	
	// Get connection count
	var connections int
	conn.QueryRow(ctx, "SELECT count(*) FROM pg_stat_activity").Scan(&connections)
	
	return HealthStatus{
		Component: name,
		Status:    "healthy",
		Message:   fmt.Sprintf("Connected (%dms, %d active connections)", latency.Milliseconds(), connections),
		Latency:   latency,
	}
}

func printStatus(status HealthStatus) {
	icon := "✅"
	if status.Status != "healthy" {
		icon = "❌"
	}
	fmt.Printf("%s %-20s %s\n", icon, status.Component+":", status.Message)
}
