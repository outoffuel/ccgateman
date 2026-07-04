package main

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
