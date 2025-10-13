package main

import (
	"fmt"
	"log"
	"net"
	"os"
	"os/signal"
	"syscall"
)

func main() {
	// Load configuration
	config, err := LoadConfig()
	if err != nil {
		log.Fatalf("Failed to load configuration: %v", err)
	}

	log.Printf("Starting pprox on %s", config.ProxyAddr)
	log.Printf("Reader DSN: %s", config.ReaderDSN)
	log.Printf("Writer DSNs: %v", config.WriterDSNs)

	// Create router
	router := NewRouter(config)

	// Start TCP listener
	listener, err := net.Listen("tcp", config.ProxyAddr)
	if err != nil {
		log.Fatalf("Failed to start listener: %v", err)
	}
	defer listener.Close()

	log.Printf("pprox listening on %s", config.ProxyAddr)

	// Handle graceful shutdown
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, os.Interrupt, syscall.SIGTERM)

	go func() {
		<-sigChan
		log.Println("Shutting down pprox...")
		listener.Close()
		os.Exit(0)
	}()

	// Accept connections
	for {
		conn, err := listener.Accept()
		if err != nil {
			log.Printf("Failed to accept connection: %v", err)
			continue
		}

		log.Printf("New connection from %s", conn.RemoteAddr())

		// Handle client in a goroutine
		handler := NewClientHandler(conn, router)
		go func() {
			defer func() {
				if r := recover(); r != nil {
					log.Printf("Panic in handler: %v", r)
				}
			}()
			handler.Handle()
			log.Printf("Connection closed from %s", conn.RemoteAddr())
		}()
	}
}

func init() {
	// Set up logging
	log.SetFlags(log.LstdFlags | log.Lshortfile)
	log.SetOutput(os.Stdout)

	// Print banner
	fmt.Println("╔═══════════════════════════════════════╗")
	fmt.Println("║         pprox - PostgreSQL Proxy      ║")
	fmt.Println("║   Read/Write Query Router & Fan-out   ║")
	fmt.Println("╚═══════════════════════════════════════╝")
	fmt.Println()
}
