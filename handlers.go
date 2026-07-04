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

func handleIndex(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/" {
		http.NotFound(w, r)
		return
	}
	http.ServeFile(w, r, "templates/index.html")
}

func handleAdmin(w http.ResponseWriter, r *http.Request) {
	http.ServeFile(w, r, "templates/admin.html")
}

func handleScan(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method Not Allowed", http.StatusMethodNotAllowed)
		return
	}

	_ = r.ParseForm()
	_ = r.ParseMultipartForm(32 << 20)

	rawID := strings.TrimSpace(r.FormValue("card_id"))
	cardID := cleanseID(rawID)

	if cardID == "" {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"success":false,"message":"カードIDが入力されていません。"}`))
		return
	}

	now := time.Now()
	nowStr := now.Format(TimeLayout)

	user, found := findUserByCardID(cardID)

	if !found {
		logEntry := AccessLog{
			Timestamp:    nowStr,
			CardID:       cardID,
			Name:         "未登録",
			Result:       "NG",
			Status:       "-",
			StayDuration: "-",
		}
		_ = insertLog(logEntry)

		w.Header().Set("Content-Type", "application/json")
		resp := ScanResponse{
			Success: false,
			Message: "利用登録を済ませてください",
			Name:    "未登録",
			Status:  "-",
		}
		_ = json.NewEncoder(w).Encode(resp)
		return
	}

	var lastStatus string
	var lastEnterTime string
	err := db.QueryRow(
		"SELECT status, timestamp FROM access_logs WHERE card_id = ? AND result = 'OK' ORDER BY id DESC LIMIT 1",
		user.CardID,
	).Scan(&lastStatus, &lastEnterTime)

	isEnter := true
	var stayDuration string

	if err == sql.ErrNoRows {
		isEnter = true
	} else if err != nil {
		log.Printf("[Scan] DB query error for last status: %v", err)
		isEnter = true
	} else {
		if lastStatus == "入室" {
			isEnter = false
			if t, parseErr := time.ParseInLocation(TimeLayout, lastEnterTime, time.Local); parseErr == nil {
				diff := now.Sub(t)
				if diff < 0 {
					diff = -diff
				}
				hours := int(diff.Hours())
				minutes := int(diff.Minutes()) % 60
				if hours > 0 {
					stayDuration = fmt.Sprintf("%d時間%d分", hours, minutes)
				} else {
					stayDuration = fmt.Sprintf("%d分", minutes)
				}
			}
		}
	}

	status := "入室"
	if !isEnter {
		status = "退室"
	}

	logEntry := AccessLog{
		Timestamp:    nowStr,
		CardID:       user.CardID,
		StudentID:    user.StudentID,
		Name:         user.Name,
		Result:       "OK",
		AttrCode:     user.AttrCode,
		AttrLabel:    attrCodeToLabel(user.AttrCode),
		Status:       status,
		StayDuration: stayDuration,
	}
	_ = insertLog(logEntry)

	message := fmt.Sprintf("%sさん、%sしました。", user.Name, status)
	if !isEnter && stayDuration != "" {
		message = fmt.Sprintf("%sさん、%sしました。（滞在時間: %s）", user.Name, status, stayDuration)
	}

	w.Header().Set("Content-Type", "application/json")
	resp := ScanResponse{
		Success:      true,
		Message:      message,
		Name:         user.Name,
		StudentID:    user.StudentID,
		AttrLabel:    logEntry.AttrLabel,
		Status:       status,
		StayDuration: stayDuration,
		Timestamp:    nowStr,
	}
	_ = json.NewEncoder(w).Encode(resp)
	log.Printf("[Scan] %s | ID=%s | Name=%s | Result=%s | Status=%s | Duration=%s",
		nowStr, user.CardID, user.Name, "OK", status, stayDuration)
}

func handleLogs(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method Not Allowed", http.StatusMethodNotAllowed)
		return
	}

	rows, err := db.Query(
		"SELECT id, timestamp, card_id, student_id, name, result, attr_code, attr_label, status, stay_duration FROM access_logs ORDER BY id DESC LIMIT 100",
	)
	if err != nil {
		log.Printf("[Logs] Query failed: %v", err)
		http.Error(w, "Database Query Error", http.StatusInternalServerError)
		return
	}
	defer rows.Close()

	type LogEntry struct {
		StudentID string `json:"StudentID"`
		Name      string `json:"Name"`
		Status    string `json:"Status"`
		Timestamp string `json:"Timestamp"`
	}

	logs := make([]LogEntry, 0)
	todayLogsCount := 0
	activeUsersCount := 0
	todayStart := time.Now().Format("2006-01-02") + " 00:00:00"

	for rows.Next() {
		var l struct {
			ID        int
			Timestamp string
			CardID    string
			StudentID string
			Name      string
			Result    string
			AttrCode  string
			AttrLabel string
			Status    string
			StayDuration string
		}
		if err := rows.Scan(&l.ID, &l.Timestamp, &l.CardID, &l.StudentID, &l.Name,
			&l.Result, &l.AttrCode, &l.AttrLabel, &l.Status, &l.StayDuration); err != nil {
			log.Printf("[Logs] Row scanning failed: %v", err)
			continue
		}

		logs = append(logs, LogEntry{
			StudentID: l.StudentID,
			Name:      l.Name,
			Status:    l.Status,
			Timestamp: l.Timestamp,
		})

		if l.Timestamp >= todayStart {
			todayLogsCount++
		}
		if l.Status == "入室" {
			activeUsersCount++
		}
	}

	w.Header().Set("Content-Type", "application/json")
	resp := map[string]interface{}{
		"logs":             logs,
		"todayLogsCount":   todayLogsCount,
		"activeUsersCount": activeUsersCount,
	}
	_ = json.NewEncoder(w).Encode(resp)
}

func insertLog(l AccessLog) error {
	_, err := db.Exec(
		`INSERT INTO access_logs (timestamp, card_id, student_id, name, result, attr_code, attr_label, status, stay_duration)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		l.Timestamp, l.CardID, l.StudentID, l.Name, l.Result,
		l.AttrCode, l.AttrLabel, l.Status, l.StayDuration,
	)
	return err
}

func handleNightlyForcedExit(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method Not Allowed", http.StatusMethodNotAllowed)
		return
	}

	now := time.Now()
	nowStr := now.Format(TimeLayout)

	rows, err := db.Query(
		`SELECT a.card_id, a.student_id, a.name, a.attr_code, a.timestamp
		 FROM access_logs a
		 INNER JOIN (
			 SELECT card_id, MAX(id) as max_id
			 FROM access_logs
			 WHERE result = 'OK'
			 GROUP BY card_id
		 ) b ON a.id = b.max_id
		 WHERE a.status = '入室'`,
	)
	if err != nil {
		log.Printf("[ForceExit] Query failed: %v", err)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"success":false,"message":"Database query error"}`))
		return
	}
	defer rows.Close()

	count := 0
	for rows.Next() {
		var cardID, studentID, name, attrCode, enterTime sql.NullString
		if err := rows.Scan(&cardID, &studentID, &name, &attrCode, &enterTime); err != nil {
			log.Printf("[ForceExit] Row scan error: %v", err)
			continue
		}

		cID := ""
		if cardID.Valid {
			cID = cardID.String
		}
		sID := ""
		if studentID.Valid {
			sID = studentID.String
		}
		n := ""
		if name.Valid {
			n = name.String
		}
		aCode := ""
		if attrCode.Valid {
			aCode = attrCode.String
		}
		eTime := ""
		if enterTime.Valid {
			eTime = enterTime.String
		}

		var stayDuration string
		if t, parseErr := time.ParseInLocation(TimeLayout, eTime, time.Local); parseErr == nil {
			diff := now.Sub(t)
			if diff < 0 {
				diff = -diff
			}
			hours := int(diff.Hours())
			minutes := int(diff.Minutes()) % 60
			if hours > 0 {
				stayDuration = fmt.Sprintf("%d時間%d分", hours, minutes)
			} else {
				stayDuration = fmt.Sprintf("%d分", minutes)
			}
		}

		logEntry := AccessLog{
			Timestamp:    nowStr,
			CardID:       cID,
			StudentID:    sID,
			Name:         n,
			Result:       "NG (自動処理)",
			AttrCode:     aCode,
			AttrLabel:    attrCodeToLabel(aCode),
			Status:       "強制退室",
			StayDuration: stayDuration,
		}
		if err := insertLog(logEntry); err != nil {
			log.Printf("[ForceExit] Insert error for %s: %v", cID, err)
			continue
		}
		count++
		log.Printf("[ForceExit] Forced exit: %s (%s)", n, sID)
	}

	w.Header().Set("Content-Type", "application/json")
	resp := map[string]interface{}{
		"success": true,
		"count":   count,
		"message": fmt.Sprintf("%d 名を強制退室させました。", count),
	}
	_ = json.NewEncoder(w).Encode(resp)
}

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

		furigana := strings.TrimSpace(r.FormValue("furigana"))
		if furigana != "" {
			furigana = strings.Map(func(r rune) rune {
				if r >= 0x3041 && r <= 0x3096 {
					return r + 0x60
				}
				return r
			}, furigana)
		}

		newUser := User{
			CardID:     strings.TrimSpace(r.FormValue("card_id")),
			StudentID:  strings.TrimSpace(r.FormValue("student_id")),
			Name:       strings.TrimSpace(r.FormValue("name")),
			AttrCode:   strings.TrimSpace(r.FormValue("attr_code")),
			Attribute:  attrCodeToLabel(strings.TrimSpace(r.FormValue("attr_code"))),
			Department: strings.TrimSpace(r.FormValue("department")),
			Furigana:   furigana,
		}

		if newUser.CardID == "" || newUser.StudentID == "" || newUser.Name == "" || newUser.AttrCode == "" {
			_, _ = w.Write([]byte(`{"success":false,"message":"磁気ID、学籍番号、名前、区分コードは必須項目です。"}`))
			return
		}

		if err := appendUserToExcel(newUser); err != nil {
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

func handleUsersReload(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method Not Allowed", http.StatusMethodNotAllowed)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	if err := loadMasterData(); err != nil {
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

func handleExport(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method Not Allowed", http.StatusMethodNotAllowed)
		return
	}

	rows, err := db.Query(
		"SELECT id, timestamp, card_id, student_id, name, result, attr_code, attr_label, status, stay_duration FROM access_logs ORDER BY id DESC",
	)
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

	headers := []string{"ログID", "日時", "カードID", "学籍番号", "氏名", "判定結果", "区分コード", "区分", "ステータス", "滞在時間"}
	for colIdx, h := range headers {
		cell, _ := excelize.CoordinatesToCellName(colIdx+1, 1)
		_ = f.SetCellValue(sheetName, cell, h)
	}

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
		_ = f.SetCellStyle(sheetName, "A1", "J1", headerStyle)
	}

	rowNum := 2
	for rows.Next() {
		var l AccessLog
		if err := rows.Scan(&l.ID, &l.Timestamp, &l.CardID, &l.StudentID, &l.Name,
			&l.Result, &l.AttrCode, &l.AttrLabel, &l.Status, &l.StayDuration); err != nil {
			continue
		}

		_ = f.SetCellValue(sheetName, fmt.Sprintf("A%d", rowNum), l.ID)
		_ = f.SetCellValue(sheetName, fmt.Sprintf("B%d", rowNum), l.Timestamp)
		_ = f.SetCellValue(sheetName, fmt.Sprintf("C%d", rowNum), l.CardID)
		_ = f.SetCellValue(sheetName, fmt.Sprintf("D%d", rowNum), l.StudentID)
		_ = f.SetCellValue(sheetName, fmt.Sprintf("E%d", rowNum), l.Name)
		_ = f.SetCellValue(sheetName, fmt.Sprintf("F%d", rowNum), l.Result)
		_ = f.SetCellValue(sheetName, fmt.Sprintf("G%d", rowNum), l.AttrCode)
		_ = f.SetCellValue(sheetName, fmt.Sprintf("H%d", rowNum), l.AttrLabel)
		_ = f.SetCellValue(sheetName, fmt.Sprintf("I%d", rowNum), l.Status)
		_ = f.SetCellValue(sheetName, fmt.Sprintf("J%d", rowNum), l.StayDuration)

		rowNum++
	}

	_ = f.DeleteSheet("Sheet1")

	_ = f.SetColWidth(sheetName, "A", "A", 8)
	_ = f.SetColWidth(sheetName, "B", "B", 22)
	_ = f.SetColWidth(sheetName, "C", "C", 15)
	_ = f.SetColWidth(sheetName, "D", "D", 12)
	_ = f.SetColWidth(sheetName, "E", "E", 20)
	_ = f.SetColWidth(sheetName, "F", "F", 16)
	_ = f.SetColWidth(sheetName, "G", "G", 10)
	_ = f.SetColWidth(sheetName, "H", "H", 10)
	_ = f.SetColWidth(sheetName, "I", "I", 12)
	_ = f.SetColWidth(sheetName, "J", "J", 12)

	var buf bytes.Buffer
	if err := f.Write(&buf); err != nil {
		log.Printf("[Export] Buffer write error: %v", err)
		http.Error(w, "Failed to build file response", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/vnd.openxmlformats-officedocument.spreadsheetml.sheet")
	w.Header().Set("Content-Disposition", "attachment; filename=access_log_export.xlsx")
	_, _ = w.Write(buf.Bytes())
	log.Printf("[Export] Log Excel sheet generated and exported successfully.")
}

func handleYearRollover(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method Not Allowed", http.StatusMethodNotAllowed)
		return
	}

	w.Header().Set("Content-Type", "application/json")

	archiveDir := getArchiveDir()
	exportPath, err := exportCurrentYearUsers(archiveDir)
	if err != nil {
		log.Printf("[YearRollover] Export failed: %v", err)
		_, _ = w.Write([]byte(fmt.Sprintf(`{"success":false,"message":"エクスポートに失敗しました: %s"}`, err.Error())))
		return
	}
	log.Printf("[YearRollover] Exported current year users to %s", exportPath)

	nextSheetName, err := createNextYearSheet()
	if err != nil {
		log.Printf("[YearRollover] Create next year sheet failed: %v", err)
		_, _ = w.Write([]byte(fmt.Sprintf(`{"success":false,"message":"次年度シート作成に失敗しました: %s"}`, err.Error())))
		return
	}
	log.Printf("[YearRollover] Created next year sheet '%s'", nextSheetName)

	resp := map[string]interface{}{
		"success":    true,
		"message":    fmt.Sprintf("%s のデータをアーカイブしました\n新年度シート '%s' を作成しました", getFiscalYearSheetName(), nextSheetName),
		"exportPath": exportPath,
		"nextSheet":  nextSheetName,
	}
	_ = json.NewEncoder(w).Encode(resp)
}
