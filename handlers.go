package main

import (
	"bytes"
	"database/sql"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"strings"
	"time"

	"github.com/xuri/excelize/v2"
)

// handleIndex serves the scan interface page.
func handleIndex(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/" {
		http.NotFound(w, r)
		return
	}
	http.ServeFile(w, r, "templates/index.html")
}

// handleAdmin serves the admin dashboard page.
func handleAdmin(w http.ResponseWriter, r *http.Request) {
	http.ServeFile(w, r, "templates/admin.html")
}

// handleScan processes card swipe requests.
func handleScan(w http.ResponseWriter, r *http.Request) {
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
}

// handleUsers lists or registers users.
func handleUsers(w http.ResponseWriter, r *http.Request) {
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
}

// handleUsersReload re-downloads the NAS excel and repopulates the local cache.
func handleUsersReload(w http.ResponseWriter, r *http.Request) {
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
}

// handleLogs returns historical access records.
func handleLogs(w http.ResponseWriter, r *http.Request) {
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
}

// handleExport generates and exports Excel logs.
func handleExport(w http.ResponseWriter, r *http.Request) {
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
}
