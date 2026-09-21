package main

import (
	"flag"
	"fmt"
	"log"
	"os"
	"os/signal"
	"syscall"

	"github.com/hazyhaar/c2db/pkg/c2db"
	"github.com/hazyhaar/c2db/pkg/c2web"
)

var (
	version = "1.0.0"
)

func main() {
	port := flag.Int("port", 8556, "HTTP port for web server and API")
	dbPath := flag.String("db-path", "/tmp/c2db_showcase", "Directory path for c2db data")
	v := flag.Bool("version", false, "Print version and exit")
	flag.Parse()

	if *v {
		fmt.Printf("c2db version %s (Go 1.27 SIMD, 0-CGO)\n", version)
		os.Exit(0)
	}

	log.Printf("[c2db] Starting storage engine on %s (port :%d)...", *dbPath, *port)

	// Ensure directory exists
	if err := os.MkdirAll(*dbPath, 0755); err != nil {
		log.Fatalf("[c2db] Failed to create data dir: %v", err)
	}

	// Initialize database engine
	var key [32]byte
	copy(key[:], "c2db-showcase-master-key-2026")
	db, err := c2db.OpenDB(*dbPath, key)
	if err != nil {
		log.Printf("[c2db] Note: Running in showcase mode: %v", err)
	} else {
		defer db.Close()
	}

	addr := fmt.Sprintf(":%d", *port)
	srv, err := c2web.NewServer(addr, db)
	if err != nil {
		log.Fatalf("[c2db] Failed to initialize web server: %v", err)
	}

	// Graceful shutdown
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, os.Interrupt, syscall.SIGTERM)

	go func() {
		log.Printf("[c2db] Web showcase & simulator listening on http://127.0.0.1:%d", *port)
		if err := srv.Start(); err != nil && err.Error() != "http: Server closed" {
			log.Fatalf("[c2db] Web server error: %v", err)
		}
	}()

	<-sigCh
	log.Println("[c2db] Shutting down gracefully...")
	_ = srv.Close()
	log.Println("[c2db] Server stopped.")
}
