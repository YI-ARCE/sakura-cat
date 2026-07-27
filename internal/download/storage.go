// Package download 提供文件下载的存储管理功能。
package download

import (
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"golang.org/x/sys/windows"
)

// 磁盘空间检查的预留阈值（100MB），下载后应至少保留此空间。
const diskSpaceThreshold = int64(100 * 1024 * 1024)

// StorageManager 管理下载目录的配置与校验，以及磁盘空间检查。
type StorageManager struct {
	db *sql.DB
}

// windowsReservedNames 是 Windows 上禁止用作文件/目录名的保留名（不带扩展名）。
// 创建此类目录会触发系统保留名冲突，因此需要替换。
var windowsReservedNames = map[string]bool{
	"CON": true, "PRN": true, "AUX": true, "NUL": true,
	"COM1": true, "COM2": true, "COM3": true, "COM4": true, "COM5": true,
	"COM6": true, "COM7": true, "COM8": true, "COM9": true,
	"LPT1": true, "LPT2": true, "LPT3": true, "LPT4": true, "LPT5": true,
	"LPT6": true, "LPT7": true, "LPT8": true, "LPT9": true,
}

// sanitizeDirName 将频道/对话名称净化为 Windows 合法目录名。
// 替换 Windows 文件系统非法字符（< > : " / \ | ? *）为下划线 _，
// 去除首尾空白与点（Windows 不允许目录名以点结尾），
// 处理保留名（如 CON/PRN/AUX/NUL/COM1-9/LPT1-9），
// 空字符串兜底为 "unnamed"。
func sanitizeDirName(name string) string {
	// 替换所有非法字符为下划线
	replacer := strings.NewReplacer(
		"<", "_",
		">", "_",
		":", "_",
		`"`, "_",
		"/", "_",
		`\`, "_",
		"|", "_",
		"?", "_",
		"*", "_",
	)
	s := replacer.Replace(name)
	// 去除首尾空白与点（Windows 不允许目录名以 . 结尾）
	s = strings.Trim(s, " .")
	// 处理保留名
	if windowsReservedNames[strings.ToUpper(s)] {
		s = "_" + s
	}
	// 空字符串兜底
	if s == "" {
		s = "unnamed"
	}
	return s
}

// NewStorageManager 创建一个新的 StorageManager。
func NewStorageManager(db *sql.DB) *StorageManager {
	return &StorageManager{db: db}
}

// GetDownloadDir 从 settings 表读取下载目录路径（key="download_dir"）。
// 若未配置则返回错误。
func (s *StorageManager) GetDownloadDir() (string, error) {
	var dir string
	err := s.db.QueryRow("SELECT value FROM settings WHERE key = ?", "download_dir").Scan(&dir)
	if err != nil {
		if err == sql.ErrNoRows {
			return "", fmt.Errorf("下载目录未配置")
		}
		return "", err
	}
	return dir, nil
}

// SetDownloadDir 设置下载目录：
//  1. 校验目录是否存在，不存在则创建；
//  2. 校验目录可写（创建临时文件测试）；
//  3. 保存到 settings 表。
func (s *StorageManager) SetDownloadDir(dir string) error {
	// 创建目录（如果不存在）
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("创建目录失败: %w", err)
	}
	// 测试目录可写
	if err := testWritable(dir); err != nil {
		return fmt.Errorf("目录不可写: %w", err)
	}
	// 保存到 settings 表（key 已存在则更新）
	_, err := s.db.Exec(
		"INSERT OR REPLACE INTO settings (key, value, updated_at) VALUES (?, ?, ?)",
		"download_dir", dir, time.Now(),
	)
	if err != nil {
		return fmt.Errorf("保存下载目录配置失败: %w", err)
	}
	return nil
}

// EnsureDownloadDir 确保下载目录存在且可写，否则返回错误。
func (s *StorageManager) EnsureDownloadDir() error {
	dir, err := s.GetDownloadDir()
	if err != nil {
		return err
	}
	if dir == "" {
		return fmt.Errorf("下载目录未配置")
	}
	// 确保目录存在
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("创建下载目录失败: %w", err)
	}
	// 测试目录可写
	if err := testWritable(dir); err != nil {
		return fmt.Errorf("下载目录不可写: %w", err)
	}
	return nil
}

// GetTaskDir 返回 <download_dir>/<taskDirName> 路径并创建该目录。
// taskDirName 通常取自任务配置的 SearchTerm（搜索词），这样每个任务的文件能按搜索主题分目录存放，
// 而不是按频道名（同一频道可能有多个不同主题的下载任务）。
// taskDirName 会被净化为 Windows 合法目录名（替换非法字符 | < > : " / \ ? * 等）。
func (s *StorageManager) GetTaskDir(taskDirName string) (string, error) {
	dir, err := s.GetDownloadDir()
	if err != nil {
		return "", err
	}
	safeName := sanitizeDirName(taskDirName)
	taskDir := filepath.Join(dir, safeName)
	if err := os.MkdirAll(taskDir, 0o755); err != nil {
		return "", fmt.Errorf("创建任务目录失败: %w", err)
	}
	return taskDir, nil
}

// CheckDiskSpace 检查下载目录所在磁盘的可用空间，返回可用字节数。
// 使用 Windows API GetDiskFreeSpaceEx 查询。
func (s *StorageManager) CheckDiskSpace(requiredBytes int64) (int64, error) {
	dir, err := s.GetDownloadDir()
	if err != nil {
		return 0, err
	}
	// 将路径转换为 UTF16 指针（Windows API 要求）
	pathPtr, err := windows.UTF16PtrFromString(dir)
	if err != nil {
		return 0, fmt.Errorf("路径转换失败: %w", err)
	}
	var freeBytesAvailable uint64
	var totalBytes uint64
	var totalFreeBytes uint64
	err = windows.GetDiskFreeSpaceEx(pathPtr, &freeBytesAvailable, &totalBytes, &totalFreeBytes)
	if err != nil {
		return 0, fmt.Errorf("查询磁盘空间失败: %w", err)
	}
	return int64(freeBytesAvailable), nil
}

// HasEnoughSpace 检查磁盘是否有足够空间容纳 requiredBytes，
// 同时保留 100MB 的预留阈值。
func (s *StorageManager) HasEnoughSpace(requiredBytes int64) (bool, error) {
	freeBytes, err := s.CheckDiskSpace(requiredBytes)
	if err != nil {
		return false, err
	}
	return freeBytes >= requiredBytes+diskSpaceThreshold, nil
}

// testWritable 通过在目录下创建并删除临时文件来测试目录是否可写。
func testWritable(dir string) error {
	tmpFile := filepath.Join(dir, ".tg_download_writable_test")
	f, err := os.Create(tmpFile)
	if err != nil {
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	return os.Remove(tmpFile)
}
