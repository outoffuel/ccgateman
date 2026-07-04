package main

type User struct {
	CardID     string `json:"CardID"`
	StudentID  string `json:"StudentID"`
	Name       string `json:"Name"`
	Attribute  string `json:"Attribute"`
	Department string `json:"Department"`
	AttrCode   string `json:"AttrCode"`
	Furigana   string `json:"Furigana"`
}

type AccessLog struct {
	ID           int64  `json:"id"`
	Timestamp    string `json:"timestamp"`
	CardID       string `json:"card_id"`
	StudentID    string `json:"student_id"`
	Name         string `json:"name"`
	Result       string `json:"result"`
	AttrCode     string `json:"attr_code"`
	AttrLabel    string `json:"attr_label"`
	Status       string `json:"status"`
	StayDuration string `json:"stay_duration"`
}

type ScanResponse struct {
	Success      bool   `json:"success"`
	Message      string `json:"message"`
	Name         string `json:"name"`
	StudentID    string `json:"student_id"`
	AttrLabel    string `json:"attr_label"`
	Status       string `json:"status"`
	StayDuration string `json:"stay_duration"`
	Timestamp    string `json:"timestamp"`
}

const (
	MasterPathDefault    = "/mnt/nas/entry_master.xlsx"
	LocalCopyPathDefault = "/tmp/local_master.xlsx"
	ArchiveDirDefault    = "/mnt/nas/archives"
	TimeLayout           = "2006-01-02 15:04:05"
)

func attrCodeToLabel(code string) string {
	if len(code) == 0 {
		return "職員"
	}
	switch code[0] {
	case '1':
		return "学生"
	case '9':
		return "スタッフ"
	default:
		return "職員"
	}
}
