package main

import (
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/xuri/excelize/v2"
)

var (
	userCache   = make(map[string]User)
	userCacheMu sync.RWMutex
)

func ensureMasterFileExists(path string) error {
	if _, err := os.Stat(path); err == nil {
		return nil
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

	headers := []string{"磁気ID", "学籍番号", "名前", "フリガナ", "区分コード", "学部学科"}
	for colIdx, h := range headers {
		cell, err := excelize.CoordinatesToCellName(colIdx+1, 1)
		if err == nil {
			_ = f.SetCellValue(sheetName, cell, h)
		}
	}

	dummyRows := [][]string{
		{"12345678", "S26001", "山田 太郎", "ヤマダ タロウ", "1", "工学部情報工学科"},
		{"87654321", "S26002", "佐藤 美咲", "サトウ ミサキ", "1", "理学部物理学科"},
		{"11223344", "ST2601", "鈴木 一郎", "スズキ イチロウ", "9", "工学部機械工学科"},
		{"55667788", "T26001", "田中 健二", "タナカ ケンジ", "5", "事務局"},
	}
	for rowIdx, r := range dummyRows {
		for colIdx, val := range r {
			cell, err := excelize.CoordinatesToCellName(colIdx+1, rowIdx+2)
			if err == nil {
				_ = f.SetCellValue(sheetName, cell, val)
			}
		}
	}

	_ = f.DeleteSheet("Sheet1")

	if err := f.SaveAs(path); err != nil {
		return fmt.Errorf("failed to save Excel file to %s: %w", path, err)
	}

	log.Printf("[Master] Created initial template master file at %s", path)
	return nil
}

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

	cardIdx, studentIdx, nameIdx := 0, 1, 2
	attrCodeIdx, deptIdx, furiganaIdx := 3, 4, -1

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
		case "区分コード", "区分", "属性コード", "AttrCode", "attr_code":
			attrCodeIdx = i
		case "学部学科", "学部", "学科", "Department", "Dept":
			deptIdx = i
		case "フリガナ", "ふりがな", "Furigana":
			furiganaIdx = i
		}
	}

	for i := 1; i < len(rows); i++ {
		row := rows[i]

		getVal := func(idx int) string {
			if idx >= 0 && idx < len(row) {
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
			AttrCode:   getVal(attrCodeIdx),
			Attribute:  attrCodeToLabel(getVal(attrCodeIdx)),
			Department: getVal(deptIdx),
			Furigana:   getVal(furiganaIdx),
		}
	}

	return cache, nil
}

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
		headers := []string{"磁気ID", "学籍番号", "名前", "区分コード", "学部学科"}
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

	cardIdx, studentIdx, nameIdx := 0, 1, 2
	attrCodeIdx, deptIdx := 3, 4

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
			case "区分コード", "区分", "属性コード", "AttrCode", "attr_code":
				attrCodeIdx = i
			case "学部学科", "学部", "学科", "Department", "Dept":
				deptIdx = i
			}
		}
	}

	for i := 1; i < len(rows); i++ {
		row := rows[i]
		if cardIdx < len(row) && strings.TrimSpace(row[cardIdx]) == user.CardID {
			return fmt.Errorf("磁気ID %s は既に登録されています", user.CardID)
		}
	}

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
	setCellVal(attrCodeIdx, user.AttrCode)
	setCellVal(deptIdx, user.Department)

	if err := f.SaveAs(master); err != nil {
		return fmt.Errorf("failed to save master Excel: %w", err)
	}

	if err := loadMasterData(); err != nil {
		return fmt.Errorf("failed to reload master data after registration: %w", err)
	}

	return nil
}

func findUserByCardID(cardID string) (User, bool) {
	userCacheMu.RLock()
	defer userCacheMu.RUnlock()
	u, ok := userCache[cardID]
	return u, ok
}

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
