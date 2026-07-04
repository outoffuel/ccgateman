package main

import (
	"log"
	"net/http"
)

func main() {
	log.Println("[GateMan] Starting Entry/Exit Management System...")

	if err := initDB(); err != nil {
		log.Fatalf("Fatal: Database initialization failed: %v", err)
	}
	log.Println("[DB] SQLite database initialized successfully.")

	if err := loadMasterData(); err != nil {
		log.Printf("[Master] Warning: Master data reload failed: %v", err)
	}

	http.Handle("/static/", http.StripPrefix("/static/", http.FileServer(http.Dir("static"))))

	http.HandleFunc("/", handleIndex)
	http.HandleFunc("/api/scan", handleScan)
	http.HandleFunc("/api/logs", handleLogs)
	http.HandleFunc("/api/users", handleUsers)
	http.HandleFunc("/api/users/reload", handleUsersReload)
	http.HandleFunc("/api/export", handleExport)
	http.HandleFunc("/api/nightly-force-exit", handleNightlyForcedExit)

	ips := getLocalIPs()
	port := ":8080"

	log.Println("[GateMan] Server initialized. Listening on port", port)
	log.Println("--------------------------------------------------")
	log.Println("Local Access (on Raspberry Pi):")
	log.Printf("  打刻画面: http://localhost%s\n", port)
	log.Println("Remote Access (from other PCs):")
	for _, ip := range ips {
		log.Printf("  打刻画面: http://%s%s\n", ip, port)
	}
	log.Println("--------------------------------------------------")

	if err := http.ListenAndServe(port, nil); err != nil {
		log.Fatalf("Fatal: Server failed to start: %v", err)
	}
}
