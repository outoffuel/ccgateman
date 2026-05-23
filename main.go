package main

import (
	"bytes"
	"database/sql"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"

	_ "modernc.org/sqlite"
	"github.com/xuri/excelize/v2"
)

// User represents a cached master user record.
type User struct {
	CardID     string `json:"CardID"`     // 磁気ID
	StudentID  string `json:"StudentID"`  // 学籍番号
	Name       string `json:"Name"`       // 名前
	Attribute  string `json:"Attribute"`  // 属性 (学生/学生スタッフ/教職員)
	Department string `json:"Department"` // 学部学科
}

// LogEntry represents a SQLite entry log.
type LogEntry struct {
	ID        int64   `json:"ID"`
	StudentID string  `json:"StudentID"`
	Name      string  `json:"Name"`
	EnterAt   *string `json:"EnterAt"`
	ExitAt    *string `json:"ExitAt"`
}

const (
	MasterPathDefault    = "/mnt/nas/entry_master.xlsx"
	LocalCopyPathDefault = "/tmp/local_master.xlsx"
	TimeLayout           = "2006-01-02 15:04:05"
)

var (
	db          *sql.DB
	userCache   = make(map[string]User)
	userCacheMu sync.RWMutex
)

// getMasterPath returns the appropriate master Excel file path.
// It falls back to a local file in development (e.g., Windows testing).
func getMasterPath() string {
	if runtime.GOOS == "windows" {
		// If testing on Windows, check if the typical mount path exists, otherwise fallback to local dir
		if _, err := os.Stat("C:\\mnt\\nas\\entry_master.xlsx"); err == nil {
			return "C:\\mnt\\nas\\entry_master.xlsx"
		}
		return "./entry_master.xlsx"
	}
	return MasterPathDefault
}

// getLocalCopyPath returns the path for the local master cache file to avoid locks on the NAS.
func getLocalCopyPath() string {
	if runtime.GOOS == "windows" {
		return "./local_master.xlsx"
	}
	return LocalCopyPathDefault
}

// getFiscalYearSheetName returns the Japanese fiscal year sheet name (e.g. "2026年度")
// based on Japanese academic year rules (April to March).
func getFiscalYearSheetName() string {
	now := time.Now()
	year := now.Year()
	if now.Month() < time.April {
		year--
	}
	return fmt.Sprintf("%d年度", year)
}

// copyFile copies a file from src to dst, creating the dst directory if necessary.
func copyFile(src, dst string) error {
	dstDir := filepath.Dir(dst)
	if err := os.MkdirAll(dstDir, 0755); err != nil {
		return fmt.Errorf("failed to create directory %s: %w", dstDir, err)
	}

	in, err := os.Open(src)
	if err != nil {
		return fmt.Errorf("failed to open source file %s: %w", src, err)
	}
	defer in.Close()

	out, err := os.Create(dst)
	if err != nil {
		return fmt.Errorf("failed to create destination file %s: %w", dst, err)
	}
	defer out.Close()

	if _, err = io.Copy(out, in); err != nil {
		return fmt.Errorf("failed to copy file contents: %w", err)
	}
	return nil
}

// ensureMasterFileExists creates a dummy master Excel file if it does not exist.
func ensureMasterFileExists(path string) error {
	if _, err := os.Stat(path); err == nil {
		return nil // Already exists
	}

	dir := filepath.Dir(path)
	if dir != "." && dir != "" {
		if err := os.MkdirAll(dir, 0755); err != nil {
			return fmt.Errorf("failed to create directory %s: %w", dir, err)
		}
	}

	f := excelize.NewFile()
	defer f.Close()

	sheetName := getFiscalYearSheetName()
	index, err := f.NewSheet(sheetName)
	if err != nil {
		return fmt.Errorf("failed to create sheet %s: %w", sheetName, err)
	}
	f.SetActiveSheet(index)

	// Set headers
	headers := []string{"磁気ID", "学籍番号", "名前", "属性", "学部学科"}
	for colIdx, h := range headers {
		cell, err := excelize.CoordinatesToCellName(colIdx+1, 1)
		if err == nil {
			_ = f.SetCellValue(sheetName, cell, h)
		}
	}

	// Add dummy seed data for demonstration
	dummyRows := [][]string{
		{"12345678", "S26001", "山田 太郎", "学生", "工学部情報工学科"},
		{"87654321", "S26002", "佐藤 美咲", "学生", "理学部物理学科"},
		{"11223344", "ST2601", "鈴木 一郎", "学生スタッフ", "工学部機械工学科"},
		{"55667788", "T26001", "田中 健二", "教職員", "事務局"},
	}
	for rowIdx, r := range dummyRows {
		for colIdx, val := range r {
			cell, err := excelize.CoordinatesToCellName(colIdx+1, rowIdx+2)
			if err == nil {
				_ = f.SetCellValue(sheetName, cell, val)
			}
		}
	}

	// Remove default sheet
	_ = f.DeleteSheet("Sheet1")

	if err := f.SaveAs(path); err != nil {
		return fmt.Errorf("failed to save Excel file to %s: %w", path, err)
	}

	log.Printf("[Master] Created initial template master file at %s", path)
	return nil
}

// readExcel reads Excel sheet for the current fiscal year and returns a map of Users.
func readExcel(path string) (map[string]User, error) {
	f, err := excelize.OpenFile(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	sheetName := getFiscalYearSheetName()
	sheets := f.GetSheetList()
	
	sheetExists := false
	for _, s := range sheets {
		if s == sheetName {
			sheetExists = true
			break
		}
	}
	
	// Fallback to first sheet if the current fiscal year sheet doesn't exist yet
	if !sheetExists {
		if len(sheets) == 0 {
			return nil, fmt.Errorf("no sheets found in Excel file")
		}
		sheetName = sheets[0]
		log.Printf("[Master] Warning: Fiscal year sheet '%s' not found. Falling back to '%s'", getFiscalYearSheetName(), sheetName)
	}

	rows, err := f.GetRows(sheetName)
	if err != nil {
		return nil, err
	}

	cache := make(map[string]User)
	if len(rows) == 0 {
		return cache, nil
	}

	// Default column mappings
	cardIdx := 0
	studentIdx := 1
	nameIdx := 2
	attrIdx := 3
	deptIdx := 4

	// Dynamically map column positions from header names
	headers := rows[0]
	for i, h := range headers {
		h = strings.TrimSpace(h)
		switch h {
		case "磁気ID", "カードID", "磁気カードID", "CardID", "Card ID":
			cardIdx = i
		case "学籍番号", "学籍", "StudentID", "Student ID":
			studentIdx = i
		case "名前", "氏名", "Name":
			nameIdx = i
		case "属性", "区分", "役職", "Attribute", "Attr":
			attrIdx = i
		case "学部学科", "学部", "学科", "Department", "Dept":
			deptIdx = i
		}
	}

	// Load rows into cache map
	for i := 1; i < len(rows); i++ {
		row := rows[i]
		
		getVal := func(idx int) string {
			if idx < len(row) {
				return strings.TrimSpace(row[idx])
			}
			return ""
		}

		cardID := getVal(cardIdx)
		if cardID == "" {
			continue
		}

		cache[cardID] = User{
			CardID:     cardID,
			StudentID:  getVal(studentIdx),
			Name:       getVal(nameIdx),
			Attribute:  getVal(attrIdx),
			Department: getVal(deptIdx),
		}
	}

	return cache, nil
}

// loadMasterData handles copying the master file to cache directory and reloading memory cache.
func loadMasterData() error {
	master := getMasterPath()
	local := getLocalCopyPath()

	log.Printf("[Master] Copying master file from %s to %s...", master, local)
	if err := ensureMasterFileExists(master); err != nil {
		log.Printf("[Master] Warning: Failed to ensure master file exists: %v", err)
	}

	err := copyFile(master, local)
	if err != nil {
		log.Printf("[Master] Warning: Copy failed: %v. Checking for pre-existing local copy...", err)
		if _, statErr := os.Stat(local); statErr != nil {
			return fmt.Errorf("master excel copying failed and no local copy found: %w", err)
		}
		log.Printf("[Master] Using pre-existing local copy at %s", local)
	} else {
		log.Printf("[Master] Master file copied successfully to %s", local)
	}

	cache, err := readExcel(local)
	if err != nil {
		return fmt.Errorf("failed to parse local master excel: %w", err)
	}

	userCacheMu.Lock()
	userCache = cache
	userCacheMu.Unlock()

	log.Printf("[Master] Loaded %d records into memory cache.", len(cache))
	return nil
}

// appendUserToExcel appends a new user to the master Excel file, copies it locally, and updates cache.
func appendUserToExcel(user User) error {
	master := getMasterPath()

	if err := ensureMasterFileExists(master); err != nil {
		return err
	}

	f, err := excelize.OpenFile(master)
	if err != nil {
		return fmt.Errorf("failed to open master Excel: %w", err)
	}
	defer f.Close()

	sheetName := getFiscalYearSheetName()
	sheets := f.GetSheetList()
	
	sheetExists := false
	for _, s := range sheets {
		if s == sheetName {
			sheetExists = true
			break
		}
	}
	
	if !sheetExists {
		index, err := f.NewSheet(sheetName)
		if err != nil {
			return fmt.Errorf("failed to create sheet: %w", err)
		}
		// Write headers for new sheet
		headers := []string{"磁気ID", "学籍番号", "名前", "属性", "学部学科"}
		for colIdx, h := range headers {
			cell, _ := excelize.CoordinatesToCellName(colIdx+1, 1)
			_ = f.SetCellValue(sheetName, cell, h)
		}
		f.SetActiveSheet(index)
	}

	rows, err := f.GetRows(sheetName)
	if err != nil {
		return fmt.Errorf("failed to read rows: %w", err)
	}

	// Column indices mapped dynamically
	cardIdx := 0
	studentIdx := 1
	nameIdx := 2
	attrIdx := 3
	deptIdx := 4

	if len(rows) > 0 {
		headers := rows[0]
		for i, h := range headers {
			h = strings.TrimSpace(h)
			switch h {
			case "磁気ID", "カードID", "磁気カードID", "CardID", "Card ID":
				cardIdx = i
			case "学籍番号", "学籍", "StudentID", "Student ID":
				studentIdx = i
			case "名前", "氏名", "Name":
				nameIdx = i
			case "属性", "区分", "役職", "Attribute", "Attr":
				attrIdx = i
			case "学部学科", "学部", "学科", "Department", "Dept":
				deptIdx = i
			}
		}
	}

	// Verify duplicate ID
	for i := 1; i < len(rows); i++ {
		row := rows[i]
		if cardIdx < len(row) && strings.TrimSpace(row[cardIdx]) == user.CardID {
			return fmt.Errorf("磁気ID %s は既に登録されています", user.CardID)
		}
	}

	// Append row
	newRowIdx := len(rows) + 1

	setCellVal := func(colIdx int, val string) {
		cell, err := excelize.CoordinatesToCellName(colIdx+1, newRowIdx)
		if err == nil {
			_ = f.SetCellValue(sheetName, cell, val)
		}
	}

	setCellVal(cardIdx, user.CardID)
	setCellVal(studentIdx, user.StudentID)
	setCellVal(nameIdx, user.Name)
	setCellVal(attrIdx, user.Attribute)
	setCellVal(deptIdx, user.Department)

	if err := f.SaveAs(master); err != nil {
		return fmt.Errorf("failed to save master Excel: %w", err)
	}

	// Reload master data to update cache and local copy
	if err := loadMasterData(); err != nil {
		return fmt.Errorf("failed to reload master data after registration: %w", err)
	}

	return nil
}

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

// getGreeting gets the greeting depending on the time of day.
// 5:00 - 11:00 (exclusive) -> おはようございます
// 11:00 - 17:00 (exclusive) -> こんにちは
// Else -> こんばんは
func getGreeting() string {
	hour := time.Now().Hour()
	if hour >= 5 && hour < 11 {
		return "おはようございます"
	} else if hour >= 11 && hour < 17 {
		return "こんにちは"
	}
	return "こんばんは"
}

// findUserByStudentID is a helper that returns a User searching by student ID in the cache.
func findUserByStudentID(studentID string) (User, bool) {
	userCacheMu.RLock()
	defer userCacheMu.RUnlock()
	for _, u := range userCache {
		if u.StudentID == studentID {
			return u, true
		}
	}
	return User{}, false
}

// getLocalIPs returns local IPv4 addresses (useful for showing URL on startup).
func getLocalIPs() []string {
	var ips []string
	addrs, err := net.InterfaceAddrs()
	if err != nil {
		return nil
	}
	for _, addr := range addrs {
		ipnet, ok := addr.(*net.IPNet)
		if ok && !ipnet.IP.IsLoopback() && ipnet.IP.To4() != nil {
			ips = append(ips, ipnet.IP.String())
		}
	}
	return ips
}

func main() {
	log.Println("[GateMan] Starting Room Access Management System...")

	// 1. Initialize SQLite Database
	if err := initDB(); err != nil {
		log.Fatalf("Fatal: Database initialization failed: %v", err)
	}
	log.Println("[DB] SQLite database initialized successfully.")

	// 2. Load Master Excel into Cache
	if err := loadMasterData(); err != nil {
		log.Printf("[Master] Warning: Master data reload failed: %v", err)
	}

	// 3. Serve Frontend Pages
	http.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/" {
			http.NotFound(w, r)
			return
		}
		http.ServeFile(w, r, "templates/index.html")
	})

	http.HandleFunc("/admin", func(w http.ResponseWriter, r *http.Request) {
		http.ServeFile(w, r, "templates/admin.html")
	})

	// 4. API Endpoints

	// POST /api/scan - Card scan action
	http.HandleFunc("/api/scan", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "Method Not Allowed", http.StatusMethodNotAllowed)
			return
		}

		_ = r.ParseForm()
		_ = r.ParseMultipartForm(32 << 20)

		cardID := strings.TrimSpace(r.FormValue("card_id"))
		mode := strings.TrimSpace(r.FormValue("mode")) // "enter" or "exit"

		if cardID == "" {
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"success":false,"message":"カードIDが指定されていません。"}`))
			return
		}

		userCacheMu.RLock()
		user, found := userCache[cardID]
		userCacheMu.RUnlock()

		if !found {
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"success":false,"message":"利用登録を済ませてください"}`))
			return
		}

		greeting := getGreeting()
		nowStr := time.Now().Format(TimeLayout)
		timeOnlyStr := time.Now().Format("15:04:05")

		w.Header().Set("Content-Type", "application/json")

		if mode == "exit" {
			// Find open entry (exit_at is NULL) for this student
			var logID int64
			err := db.QueryRow("SELECT id FROM entry_logs WHERE student_id = ? AND exit_at IS NULL ORDER BY id DESC LIMIT 1", user.StudentID).Scan(&logID)
			
			if err == nil {
				// Record found, update with exit timestamp
				_, err = db.Exec("UPDATE entry_logs SET exit_at = ? WHERE id = ?", nowStr, logID)
				if err != nil {
					log.Printf("[DB] Error updating exit stamp: %v", err)
					_, _ = w.Write([]byte(`{"success":false,"message":"データベースの更新に失敗しました。"}`))
					return
				}
				
				resp := map[string]interface{}{
					"success":    true,
					"message":    fmt.Sprintf("%sさん、お疲れ様でした。\n%s", user.Name, user.Attribute),
					"student_id": user.StudentID,
					"name":       user.Name,
					"attribute":  user.Attribute,
					"department": user.Department,
					"time":       timeOnlyStr,
				}
				_ = json.NewEncoder(w).Encode(resp)
				log.Printf("[Scan] Exit stamp recorded for %s (%s)", user.Name, user.StudentID)
			} else if err == sql.ErrNoRows {
				// No open entry log found. Record as exit-only (enter_at = NULL)
				_, err = db.Exec("INSERT INTO entry_logs (student_id, name, enter_at, exit_at) VALUES (?, ?, NULL, ?)", user.StudentID, user.Name, nowStr)
				if err != nil {
					log.Printf("[DB] Error inserting exit-only stamp: %v", err)
					_, _ = w.Write([]byte(`{"success":false,"message":"データベースへの登録に失敗しました。"}`))
					return
				}

				resp := map[string]interface{}{
					"success":    true,
					"message":    fmt.Sprintf("%sさん、お疲れ様でした。(※入室打刻なし)\n%s", user.Name, user.Attribute),
					"student_id": user.StudentID,
					"name":       user.Name,
					"attribute":  user.Attribute,
					"department": user.Department,
					"time":       timeOnlyStr,
				}
				_ = json.NewEncoder(w).Encode(resp)
				log.Printf("[Scan] Exit-only stamp recorded (no entry found) for %s (%s)", user.Name, user.StudentID)
			} else {
				log.Printf("[DB] Error querying active entry log: %v", err)
				_, _ = w.Write([]byte(`{"success":false,"message":"データベースの検索エラーが発生しました。"}`))
			}

		} else {
			// Mode is "enter"
			_, err := db.Exec("INSERT INTO entry_logs (student_id, name, enter_at, exit_at) VALUES (?, ?, ?, NULL)", user.StudentID, user.Name, nowStr)
			if err != nil {
				log.Printf("[DB] Error writing enter stamp: %v", err)
				_, _ = w.Write([]byte(`{"success":false,"message":"データベースへの書込に失敗しました。"}`))
				return
			}

			resp := map[string]interface{}{
				"success":    true,
				"message":    fmt.Sprintf("%sさん、%s\n%s", user.Name, greeting, user.Attribute),
				"student_id": user.StudentID,
				"name":       user.Name,
				"attribute":  user.Attribute,
				"department": user.Department,
				"time":       timeOnlyStr,
			}
			_ = json.NewEncoder(w).Encode(resp)
			log.Printf("[Scan] Enter stamp recorded for %s (%s)", user.Name, user.StudentID)
		}
	})

	// GET /api/users - Returns list of registered users
	// POST /api/users - Registers a new user
	http.HandleFunc("/api/users", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		
		if r.Method == http.MethodGet {
			userCacheMu.RLock()
			users := make([]User, 0, len(userCache))
			for _, u := range userCache {
				users = append(users, u)
			}
			userCacheMu.RUnlock()

			_ = json.NewEncoder(w).Encode(users)
			return
		}

		if r.Method == http.MethodPost {
			_ = r.ParseForm()
			_ = r.ParseMultipartForm(32 << 20)

			newUser := User{
				CardID:     strings.TrimSpace(r.FormValue("card_id")),
				StudentID:  strings.TrimSpace(r.FormValue("student_id")),
				Name:       strings.TrimSpace(r.FormValue("name")),
				Attribute:  strings.TrimSpace(r.FormValue("attribute")),
				Department: strings.TrimSpace(r.FormValue("department")),
			}

			if newUser.CardID == "" || newUser.StudentID == "" || newUser.Name == "" || newUser.Attribute == "" {
				_, _ = w.Write([]byte(`{"success":false,"message":"磁気ID、学籍番号、名前、属性は必須項目です。"}`))
				return
			}

			if err := appendUserToExcel(newUser); err != nil {
				log.Printf("[Registration] Error appending user: %v", err)
				resp := map[string]interface{}{
					"success": false,
					"message": err.Error(),
				}
				_ = json.NewEncoder(w).Encode(resp)
				return
			}

			log.Printf("[Registration] Registered user %s (%s)", newUser.Name, newUser.StudentID)
			_, _ = w.Write([]byte(`{"success":true}`))
			return
		}

		http.Error(w, "Method Not Allowed", http.StatusMethodNotAllowed)
	})

	// POST /api/users/reload - Recopies NAS file and reloads memory cache
	http.HandleFunc("/api/users/reload", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "Method Not Allowed", http.StatusMethodNotAllowed)
			return
		}

		w.Header().Set("Content-Type", "application/json")
		if err := loadMasterData(); err != nil {
			log.Printf("[Master] Reload API error: %v", err)
			resp := map[string]interface{}{
				"success": false,
				"message": err.Error(),
			}
			_ = json.NewEncoder(w).Encode(resp)
			return
		}

		userCacheMu.RLock()
		count := len(userCache)
		userCacheMu.RUnlock()

		resp := map[string]interface{}{
			"success": true,
			"count":   count,
		}
		_ = json.NewEncoder(w).Encode(resp)
	})

	// GET /api/logs - Fetches recent logs and log statistics
	http.HandleFunc("/api/logs", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			http.Error(w, "Method Not Allowed", http.StatusMethodNotAllowed)
			return
		}

		// Fetch last 50 logs
		rows, err := db.Query("SELECT id, student_id, name, enter_at, exit_at FROM entry_logs ORDER BY id DESC LIMIT 50")
		if err != nil {
			log.Printf("[DB] Logs query failed: %v", err)
			http.Error(w, "Database Query Error", http.StatusInternalServerError)
			return
		}
		defer rows.Close()

		logs := make([]LogEntry, 0)
		for rows.Next() {
			var l LogEntry
			var enter, exit sql.NullString
			if err := rows.Scan(&l.ID, &l.StudentID, &l.Name, &enter, &exit); err != nil {
				log.Printf("[DB] Row scanning failed: %v", err)
				continue
			}
			if enter.Valid {
				l.EnterAt = &enter.String
			}
			if exit.Valid {
				l.ExitAt = &exit.String
			}
			logs = append(logs, l)
		}

		// Statistics
		todayStart := time.Now().Format("2006-01-02") + " 00:00:00"
		var todayLogsCount int
		_ = db.QueryRow("SELECT COUNT(*) FROM entry_logs WHERE enter_at >= ? OR exit_at >= ?", todayStart, todayStart).Scan(&todayLogsCount)

		var activeUsersCount int
		_ = db.QueryRow("SELECT COUNT(*) FROM entry_logs WHERE exit_at IS NULL").Scan(&activeUsersCount)

		w.Header().Set("Content-Type", "application/json")
		resp := map[string]interface{}{
			"logs":             logs,
			"todayLogsCount":   todayLogsCount,
			"activeUsersCount": activeUsersCount,
		}
		_ = json.NewEncoder(w).Encode(resp)
	})

	// GET /api/export - Exports database logs into Excel sheet and serves for download
	http.HandleFunc("/api/export", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			http.Error(w, "Method Not Allowed", http.StatusMethodNotAllowed)
			return
		}

		rows, err := db.Query("SELECT id, student_id, name, enter_at, exit_at FROM entry_logs ORDER BY id DESC")
		if err != nil {
			log.Printf("[Export] DB query error: %v", err)
			http.Error(w, "Database read error", http.StatusInternalServerError)
			return
		}
		defer rows.Close()

		f := excelize.NewFile()
		defer f.Close()

		sheetName := "入退室ログ"
		index, err := f.NewSheet(sheetName)
		if err != nil {
			log.Printf("[Export] New sheet error: %v", err)
			http.Error(w, "Failed to create Excel sheet", http.StatusInternalServerError)
			return
		}
		f.SetActiveSheet(index)

		// Set header row
		headers := []string{"ログID", "学籍番号", "氏名", "属性", "学部学科 / 所属", "入室時刻", "退室時刻", "滞在時間"}
		for colIdx, h := range headers {
			cell, _ := excelize.CoordinatesToCellName(colIdx+1, 1)
			_ = f.SetCellValue(sheetName, cell, h)
		}

		// Header styling
		headerStyle, err := f.NewStyle(&excelize.Style{
			Font: &excelize.Font{Bold: true, Color: "FFFFFF", Size: 11},
			Fill: excelize.Fill{Type: "pattern", Color: []string{"1F4E78"}, Pattern: 1},
			Alignment: &excelize.Alignment{
				Horizontal: "center",
				Vertical:   "center",
			},
		})
		if err == nil {
			_ = f.SetRowHeight(sheetName, 1, 26)
			_ = f.SetCellStyle(sheetName, "A1", "H1", headerStyle)
		}

		rowNum := 2
		for rows.Next() {
			var id int64
			var studentID, name string
			var enter, exit sql.NullString
			if err := rows.Scan(&id, &studentID, &name, &enter, &exit); err != nil {
				continue
			}

			// Lookup details from cache
			attribute := "-"
			department := "-"
			user, ok := findUserByStudentID(studentID)
			if ok {
				attribute = user.Attribute
				department = user.Department
				if department == "" {
					department = "-"
				}
			}

			// Format timestamps
			enterTimeStr := "-"
			exitTimeStr := "-"
			durationStr := "-"

			if enter.Valid {
				enterTimeStr = enter.String
			}
			if exit.Valid {
				exitTimeStr = exit.String
			}

			// Calculate stay duration
			if enter.Valid && exit.Valid {
				t1, err1 := time.Parse(TimeLayout, enter.String)
				t2, err2 := time.Parse(TimeLayout, exit.String)
				if err1 == nil && err2 == nil {
					diff := t2.Sub(t1)
					hours := int(diff.Hours())
					minutes := int(diff.Minutes()) % 60
					if hours > 0 {
						durationStr = fmt.Sprintf("%d時間%d分", hours, minutes)
					} else {
						durationStr = fmt.Sprintf("%d分", minutes)
					}
				}
			}

			_ = f.SetCellValue(sheetName, fmt.Sprintf("A%d", rowNum), id)
			_ = f.SetCellValue(sheetName, fmt.Sprintf("B%d", rowNum), studentID)
			_ = f.SetCellValue(sheetName, fmt.Sprintf("C%d", rowNum), name)
			_ = f.SetCellValue(sheetName, fmt.Sprintf("D%d", rowNum), attribute)
			_ = f.SetCellValue(sheetName, fmt.Sprintf("E%d", rowNum), department)
			_ = f.SetCellValue(sheetName, fmt.Sprintf("F%d", rowNum), enterTimeStr)
			_ = f.SetCellValue(sheetName, fmt.Sprintf("G%d", rowNum), exitTimeStr)
			_ = f.SetCellValue(sheetName, fmt.Sprintf("H%d", rowNum), durationStr)

			rowNum++
		}

		// Delete default sheet
		_ = f.DeleteSheet("Sheet1")

		// Adjust columns widths
		_ = f.SetColWidth(sheetName, "A", "A", 10)
		_ = f.SetColWidth(sheetName, "B", "B", 15)
		_ = f.SetColWidth(sheetName, "C", "C", 20)
		_ = f.SetColWidth(sheetName, "D", "D", 15)
		_ = f.SetColWidth(sheetName, "E", "E", 25)
		_ = f.SetColWidth(sheetName, "F", "G", 22)
		_ = f.SetColWidth(sheetName, "H", "H", 15)

		var buf bytes.Buffer
		if err := f.Write(&buf); err != nil {
			log.Printf("[Export] Buffer write error: %v", err)
			http.Error(w, "Failed to build file response", http.StatusInternalServerError)
			return
		}

		w.Header().Set("Content-Type", "application/vnd.openxmlformats-officedocument.spreadsheetml.sheet")
		w.Header().Set("Content-Disposition", "attachment; filename=entry_log_export.xlsx")
		_, _ = w.Write(buf.Bytes())
		log.Printf("[Export] Log Excel sheet generated and exported successfully.")
	})

	// 5. Start HTTP Server
	ips := getLocalIPs()
	port := ":8080"
	
	log.Println("[GateMan] Server initialized. Listening on port", port)
	log.Println("--------------------------------------------------")
	log.Println("Local Access (on Raspberry Pi):")
	log.Printf("  打刻画面: http://localhost%s\n", port)
	log.Printf("  管理画面: http://localhost%s/admin\n", port)
	log.Println("Remote Access (from other PCs):")
	for _, ip := range ips {
		log.Printf("  打刻画面: http://%s%s\n", ip, port)
		log.Printf("  管理画面: http://%s%s/admin\n", ip, port)
	}
	log.Println("--------------------------------------------------")

	if err := http.ListenAndServe(port, nil); err != nil {
		log.Fatalf("Fatal: Server failed to start: %v", err)
	}
}
