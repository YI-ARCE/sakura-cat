// Package template 提供文件命名模板的解析与渲染功能。
package template

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// NamingVars 包含命名模板渲染所需的变量。
type NamingVars struct {
	Date      string // 消息日期 YYYYMMDD
	Channel   string // 频道名
	MessageID int64  // 消息ID
	Filename  string // 原始文件名（不含扩展名）
	MediaType string // 媒体类型: video/audio/photo/document
	// Episode 排序后的集数序号（1-based，从 download_records.sort_order 推导）。
	// 仅在任务配置了 EpisodeRegex 并完成排序后有意义；0 表示未启用集数排序。
	// 渲染时按 2 位 padding 输出（1→"01"、12→"12"），便于 S01E01 等格式使用。
	Episode int
}

// RenderTemplate 根据模板与变量渲染最终文件名。
// 支持变量：{date} {channel} {message_id} {filename} {media_type} {episode}
//   - {episode}：按 sort_order 渲染为 2 位 padding 字符串（1→"01"）
//
// 如果模板为空，默认使用 {date}_{filename}。
// 渲染后会对文件名做安全处理：替换非法字符（\ / : * ? " < > |）为下划线。
func RenderTemplate(tpl string, vars NamingVars) string {
	// 模板为空时使用默认模板
	if tpl == "" {
		tpl = "{date}_{filename}"
	}

	result := tpl
	result = strings.ReplaceAll(result, "{date}", vars.Date)
	result = strings.ReplaceAll(result, "{channel}", vars.Channel)
	result = strings.ReplaceAll(result, "{message_id}", strconv.FormatInt(vars.MessageID, 10))
	result = strings.ReplaceAll(result, "{filename}", vars.Filename)
	result = strings.ReplaceAll(result, "{media_type}", vars.MediaType)
	// {episode} 输出 2 位 padding（1→"01"）；未启用集数时为 0 → "00"（用户可选择是否使用此变量）
	result = strings.ReplaceAll(result, "{episode}", fmt.Sprintf("%02d", vars.Episode))

	// 对生成的文件名做安全处理
	return sanitizeFilename(result)
}

// sanitizeFilename 替换文件名中的非法字符为下划线。
// 非法字符：\ / : * ? " < > |
func sanitizeFilename(name string) string {
	illegalChars := []string{"\\", "/", ":", "*", "?", "\"", "<", ">", "|"}
	for _, ch := range illegalChars {
		name = strings.ReplaceAll(name, ch, "_")
	}
	return name
}

// ApplyVideoPrefix 在文件名前追加视频前缀。
// 如果 prefix 为空则原样返回。
// 注意：prefix 加在完整文件名之前，例如
// ApplyVideoPrefix("20240101_video.mp4", "VID_") 返回 "VID_20240101_video.mp4"。
//
// 调用顺序：先 RenderTemplate 生成文件名，再对视频类型调用 ApplyVideoPrefix。
func ApplyVideoPrefix(name string, prefix string) string {
	if prefix == "" {
		return name
	}
	return prefix + name
}

// ResolveConflict 检查 dir 目录下是否已存在 filename，
// 若存在则在文件名（不含扩展名）后追加 _1, _2, ... 直到不冲突。
// 例如：photo.jpg → photo_1.jpg → photo_2.jpg。
// 返回最终不冲突的文件名（仅文件名，不含目录）。
func ResolveConflict(dir string, filename string) (string, error) {
	// 检查原始文件名是否冲突
	target := filepath.Join(dir, filename)
	if _, err := os.Stat(target); err != nil {
		if os.IsNotExist(err) {
			// 文件不存在，直接使用原文件名
			return filename, nil
		}
		return "", err
	}

	// 拆分文件名与扩展名，依次追加序号
	ext := filepath.Ext(filename)
	base := strings.TrimSuffix(filename, ext)
	for i := 1; ; i++ {
		newName := fmt.Sprintf("%s_%d%s", base, i, ext)
		target = filepath.Join(dir, newName)
		if _, err := os.Stat(target); err != nil {
			if os.IsNotExist(err) {
				return newName, nil
			}
			return "", err
		}
	}
}
