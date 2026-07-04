package main

import (
	"database/sql"
	"fmt"

	_ "modernc.org/sqlite"
)

var db *sql.DB

func initDB() error {
	var err error
	db, err = sql.Open("sqlite", "entry_log.db")
	if err != nil {
		return fmt.Errorf("failed to open database: %w", err)
	}

	if err = db.Ping(); err != nil {
		return fmt.Errorf("failed to ping database: %w", err)
	}

	query := `
	CREATE TABLE IF NOT EXISTS access_logs (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		timestamp TEXT NOT NULL,
		card_id TEXT,
		student_id TEXT,
		name TEXT NOT NULL,
		result TEXT NOT NULL,
		attr_code TEXT DEFAULT '',
		attr_label TEXT DEFAULT '',
		status TEXT NOT NULL,
		stay_duration TEXT DEFAULT '-'
	);`
	if _, err = db.Exec(query); err != nil {
		return fmt.Errorf("failed to create access_logs table: %w", err)
	}

	return nil
}
