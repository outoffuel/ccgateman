package main

import (
	"database/sql"
	"fmt"

	_ "modernc.org/sqlite"
)

var db *sql.DB

// initDB initializes SQLite database and creates the table.
func initDB() error {
	var err error
	db, err = sql.Open("sqlite", "entry_log.db")
	if err != nil {
		return fmt.Errorf("failed to open database: %w", err)
	}

	if err = db.Ping(); err != nil {
		return fmt.Errorf("failed to ping database: %w", err)
	}

	// Create table schema
	query := `
	CREATE TABLE IF NOT EXISTS entry_logs (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		student_id TEXT NOT NULL,
		name TEXT NOT NULL,
		enter_at TEXT,
		exit_at TEXT
	);`
	if _, err = db.Exec(query); err != nil {
		return fmt.Errorf("failed to create entry_logs table: %w", err)
	}

	return nil
}
