package main

import (
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"time"
	"unicode"
)

var (
	spaceRegex = regexp.MustCompile(`[\s\x{3000}]+`)
)

func getMasterPath() string {
	if runtime.GOOS == "windows" {
		if _, err := os.Stat("C:\\mnt\\nas\\entry_master.xlsx"); err == nil {
			return "C:\\mnt\\nas\\entry_master.xlsx"
		}
		return "./entry_master.xlsx"
	}
	return MasterPathDefault
}

func getLocalCopyPath() string {
	if runtime.GOOS == "windows" {
		return "./local_master.xlsx"
	}
	return LocalCopyPathDefault
}

func getFiscalYearSheetName() string {
	now := time.Now()
	year := now.Year()
	if now.Month() < time.April {
		year--
	}
	return fmt.Sprintf("%d年度", year)
}

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

func getGreeting() string {
	hour := time.Now().Hour()
	if hour >= 5 && hour < 11 {
		return "おはようございます"
	} else if hour >= 11 && hour < 17 {
		return "こんにちは"
	}
	return "こんばんは"
}

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

func cleanseID(raw string) string {
	s := raw
	s = strings.NewReplacer(
		"０", "0", "１", "1", "２", "2", "３", "3", "４", "4",
		"５", "5", "６", "6", "７", "7", "８", "8", "９", "9",
		"Ａ", "A", "Ｂ", "B", "Ｃ", "C", "Ｄ", "D", "Ｅ", "E",
		"Ｆ", "F", "Ｇ", "G", "Ｈ", "H", "Ｉ", "I", "Ｊ", "J",
		"Ｋ", "K", "Ｌ", "L", "Ｍ", "M", "Ｎ", "N", "Ｏ", "O",
		"Ｐ", "P", "Ｑ", "Q", "Ｒ", "R", "Ｓ", "S", "Ｔ", "T",
		"Ｕ", "U", "Ｖ", "V", "Ｗ", "W", "Ｘ", "X", "Ｙ", "Y",
		"Ｚ", "Z", "ａ", "a", "ｂ", "b", "ｃ", "c", "ｄ", "d",
		"ｅ", "e", "ｆ", "f", "ｇ", "g", "ｈ", "h", "ｉ", "i",
		"ｊ", "j", "ｋ", "k", "ｌ", "l", "ｍ", "m", "ｎ", "n",
		"ｏ", "o", "ｐ", "p", "ｑ", "q", "ｒ", "r", "ｓ", "s",
		"ｔ", "t", "ｕ", "u", "ｖ", "v", "ｗ", "w", "ｘ", "x",
		"ｙ", "y", "ｚ", "z",
		"－", "-", "ー", "-", "―", "-",
		"（", "(", "）", ")",
		"［", "[", "］", "]",
		"｛", "{", "｝", "}",
		"！", "!", "？", "?",
		"＠", "@", "＃", "#",
		"＄", "$", "％", "%",
		"＾", "^", "＆", "&",
		"＊", "*", "～", "~",
		"＿", "_", "＋", "+",
		"＝", "=", "｜", "|",
		"：", ":", "；", ";",
		"＂", "\"", "＇", "'",
		"＜", "<", "＞", ">",
		"、", ",", "。", ".",
		"・", ".", "＼", "\\",
		"／", "/",
	).Replace(s)

	s = spaceRegex.ReplaceAllString(s, "")
	s = strings.TrimSpace(s)
	return s
}

func hiraganaToKatakana(s string) string {
	var b strings.Builder
	for _, r := range s {
		if unicode.In(r, unicode.Hiragana) {
			b.WriteRune(r + 0x60)
		} else {
			b.WriteRune(r)
		}
	}
	return b.String()
}
