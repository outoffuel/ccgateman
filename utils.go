package main

import (
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"runtime"
	"time"
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
