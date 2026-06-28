// Package lfs 提供 Git LFS (Large File Storage) 相关功能，
// 包括 LFS 指针文件的解析和 LFS 文件的虚拟访问。
package lfs

import (
	"bufio"
	"io"
	"os"
	"regexp"
	"strconv"
)

// FileInfo 存储解析出来的 LFS 指针文件信息。
type FileInfo struct {
	// IsLFS 标识该文件是否为 LFS 指针文件。
	IsLFS bool
	// OID 是 LFS 对象的 SHA256 哈希值。
	OID string
	// Size 是 LFS 对象的原始大小（字节）。
	Size int64
}

// lfsVersionRegex 匹配 LFS 规范版本标识行。
var lfsVersionRegex = regexp.MustCompile(`version https://git-lfs\.github\.com/spec/v1`)

// oidRegex 匹配 LFS 指针中的 OID 行，格式: oid sha256:<64位十六进制哈希>
var oidRegex = regexp.MustCompile(`oid sha256:([a-f0-9]{64})`)

// sizeRegex 匹配 LFS 指针中的 size 行，格式: size <数字>
var sizeRegex = regexp.MustCompile(`size (\d+)`)

// maxPointerSize 是 LFS 指针文件的最大读取字节数。
// LFS 指针文件通常很小（<1KB），这里设置 1KB 上限以避免误读大文件。
const maxPointerSize = 1024

// ParsePointer 检查并解析 LFS 指针文件。
// 它读取文件的前 maxPointerSize 字节，检查是否为标准的 Git LFS 指针格式。
// 如果是 LFS 指针文件，返回包含 OID 和原始大小的 FileInfo；
// 否则返回 IsLFS=false 的 FileInfo。
func ParsePointer(path string) (*FileInfo, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer file.Close()

	scanner := bufio.NewScanner(io.LimitReader(file, maxPointerSize))
	var info FileInfo

	for scanner.Scan() {
		line := scanner.Text()
		if lfsVersionRegex.MatchString(line) {
			info.IsLFS = true
		}
		if match := oidRegex.FindStringSubmatch(line); len(match) > 1 {
			info.OID = match[1]
		}
		if match := sizeRegex.FindStringSubmatch(line); len(match) > 1 {
			size, _ := strconv.ParseInt(match[1], 10, 64)
			info.Size = size
		}
	}

	if err := scanner.Err(); err != nil {
		return nil, err
	}

	if info.IsLFS && info.OID != "" && info.Size > 0 {
		return &info, nil
	}
	return &FileInfo{IsLFS: false}, nil
}

// IsPointer 快速判断一个文件是否为 LFS 指针文件，
// 通过检查文件开头是否包含 LFS 版本标识。
func IsPointer(path string) (bool, error) {
	info, err := ParsePointer(path)
	if err != nil {
		return false, err
	}
	return info.IsLFS, nil
}
