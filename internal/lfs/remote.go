// Package lfs 提供 Git LFS 远程访问支持，包括 HTTP 和 SSH 两种传输协议。
package lfs

import (
	"context"
	"fmt"
	"log"
	"os/exec"
	"path/filepath"
	"strings"
)

// RemoteType 表示远程 LFS 存储的访问协议类型。
type RemoteType int

const (
	// RemoteHTTP 表示通过 HTTP/HTTPS 协议访问 LFS 对象。
	RemoteHTTP RemoteType = iota
	// RemoteSSH 表示通过 SSH 协议访问远程服务器上的 LFS 对象。
	RemoteSSH
)

// SSHConfig 存储 SSH 连接参数。
type SSHConfig struct {
	// User 是 SSH 登录用户名。
	User string
	// Host 是 SSH 服务器地址。
	Host string
	// Port 是 SSH 端口号（默认 22）。
	Port string
	// RemotePath 是远程服务器上 Git 仓库或 LFS 存储的根路径。
	RemotePath string
}

// RemoteConfig 存储远程 LFS 存储的访问配置。
type RemoteConfig struct {
	// Type 指示远程访问协议类型。
	Type RemoteType
	// HTTPURL 是 HTTP/HTTPS 访问时的基础 URL。
	HTTPURL string
	// SSH 是 SSH 访问时的连接参数。
	SSH SSHConfig
}

// ParseURL 将 URL 字符串解析为 RemoteConfig。
// 支持 HTTP/HTTPS URL 和 SSH URL（ssh:// 或 [user@]host:path 格式）。
func ParseURL(rawURL string) RemoteConfig {
	rawURL = strings.TrimRight(rawURL, "/")

	if isHTTPURL(rawURL) {
		return RemoteConfig{
			Type:    RemoteHTTP,
			HTTPURL: rawURL,
		}
	}

	user, host, port, path := parseSSHURL(rawURL)
	return RemoteConfig{
		Type: RemoteSSH,
		SSH: SSHConfig{
			User:       user,
			Host:       host,
			Port:       port,
			RemotePath: path,
		},
	}
}

// IsHTTPURL 检查 URL 是否为 HTTP/HTTPS 协议。
func IsHTTPURL(url string) bool {
	return strings.HasPrefix(url, "http://") || strings.HasPrefix(url, "https://")
}

// IsSSHURL 检查 URL 是否为 SSH 协议。
// 支持 ssh:// 格式和 SCP 风格的 [user@]host:path 格式。
func IsSSHURL(url string) bool {
	if strings.HasPrefix(url, "ssh://") {
		return true
	}
	// SCP 风格: [user@]host:path — 不含协议前缀且包含 @ 或 :
	// 但需要排除 HTTP URL
	if !IsHTTPURL(url) && !strings.HasPrefix(url, "/") && !strings.HasPrefix(url, ".") {
		if strings.Contains(url, "@") || strings.Contains(url, ":") {
			return true
		}
	}
	return false
}

// isHTTPURL 是 IsHTTPURL 的非导出版本。
func isHTTPURL(url string) bool {
	return IsHTTPURL(url)
}

// parseSSHURL 解析 SSH URL 的各组成部分。
// 支持的格式:
//
//	ssh://[user@]host[:port]/path
//	[user@]host:path                    (SCP 风格)
//	[user@]host:/absolute/path          (SCP 风格，绝对路径)
func parseSSHURL(rawURL string) (user, host, port, path string) {
	port = "22"

	// ---- ssh:// 协议格式 ----
	if strings.HasPrefix(rawURL, "ssh://") {
		remainder := rawURL[6:]

		// 提取 user@ 部分
		atIdx := strings.LastIndex(remainder, "@")
		if atIdx >= 0 {
			user = remainder[:atIdx]
			remainder = remainder[atIdx+1:]
		}

		// 找到第一个 : 或 / 来分离 host、port、path
		colonIdx := strings.Index(remainder, ":")
		slashIdx := strings.Index(remainder, "/")

		if colonIdx >= 0 && (slashIdx < 0 || colonIdx < slashIdx) {
			// host:port/path 或 host:path
			host = remainder[:colonIdx]
			rest := remainder[colonIdx+1:]
			if strings.Contains(rest, "/") {
				// host:port/path
				parts := strings.SplitN(rest, "/", 2)
				port = parts[0]
				path = "/" + parts[1]
			} else {
				// host:path（没有端口，整个 rest 是路径）
				path = "/" + rest
			}
		} else if slashIdx >= 0 {
			// host/path
			host = remainder[:slashIdx]
			path = remainder[slashIdx:]
		} else {
			// 只有 host
			host = remainder
			path = "/"
		}

		if path == "" {
			path = "/"
		}
		return
	}

	// ---- SCP 风格: [user@]host:path ----
	atIdx := strings.LastIndex(rawURL, "@")
	if atIdx >= 0 {
		user = rawURL[:atIdx]
		rawURL = rawURL[atIdx+1:]
	} else {
		user = ""
	}

	colonIdx := strings.Index(rawURL, ":")
	if colonIdx >= 0 {
		host = rawURL[:colonIdx]
		path = rawURL[colonIdx+1:]
		// 如果路径不以 / 开头，则为相对路径（相对于 remote 用户 home）
		if !strings.HasPrefix(path, "/") {
			path = "/" + path
		}
	} else {
		// 没有冒号，整个字符串是 host
		host = rawURL
		path = "/"
	}

	return
}

// BuildLFSObjectPath 根据 OID 构建远程 LFS 对象的存储路径。
// 标准 Git LFS 布局: {remotePath}/lfs/objects/{oid[0:2]}/{oid[2:4]}/{oid}
func BuildLFSObjectPath(remotePath, oid string) string {
	return filepath.Join(remotePath, "lfs", "objects", oid[:2], oid[2:4], oid)
}

// ReadLFSFileSSH 通过 SSH 从远程服务器读取 LFS 对象的指定字节范围。
// 使用 dd 命令实现远程范围读取。
func ReadLFSFileSSH(ctx context.Context, cfg SSHConfig, oid string, dest []byte, off int64, fileSize int64) ([]byte, error) {
	objectPath := BuildLFSObjectPath(cfg.RemotePath, oid)

	// 构造 SSH 目标地址
	sshDest := cfg.Host
	if cfg.User != "" {
		sshDest = cfg.User + "@" + cfg.Host
	}

	// 计算读取范围
	end := off + int64(len(dest))
	if end > fileSize {
		end = fileSize
	}
	count := end - off
	if count <= 0 {
		return []byte{}, nil
	}

	// 构建 ssh 命令: ssh [user@]host [-p port] "dd if=path bs=1 skip=OFF count=COUNT 2>/dev/null"
	args := make([]string, 0)
	if cfg.Port != "" && cfg.Port != "22" {
		args = append(args, "-p", cfg.Port)
	}
	remoteCmd := fmt.Sprintf("dd if=%s bs=1 skip=%d count=%d 2>/dev/null", objectPath, off, count)
	args = append(args, sshDest, remoteCmd)

	log.Printf("[SSH] 读取 %s 从 %s (偏移=%d, 大小=%d)", oid[:8], sshDest, off, count)

	cmd := exec.CommandContext(ctx, "ssh", args...)
	output, err := cmd.Output()
	if err != nil {
		log.Printf("SSH 读取失败 %s: %v", oid[:8], err)
		return nil, fmt.Errorf("SSH 读取失败: %w", err)
	}

	return output, nil
}

// FetchLFSSSH 通过 SSH 完整获取远程 LFS 对象的内容。
func FetchLFSSSH(ctx context.Context, cfg SSHConfig, oid string) ([]byte, error) {
	objectPath := BuildLFSObjectPath(cfg.RemotePath, oid)

	sshDest := cfg.Host
	if cfg.User != "" {
		sshDest = cfg.User + "@" + cfg.Host
	}

	args := make([]string, 0)
	if cfg.Port != "" && cfg.Port != "22" {
		args = append(args, "-p", cfg.Port)
	}
	args = append(args, sshDest, "cat", objectPath)

	log.Printf("[SSH] 完整获取 %s 从 %s", oid[:8], sshDest)

	cmd := exec.CommandContext(ctx, "ssh", args...)
	output, err := cmd.Output()
	if err != nil {
		log.Printf("SSH 获取失败 %s: %v", oid[:8], err)
		return nil, fmt.Errorf("SSH 获取失败: %w", err)
	}

	return output, nil
}

// DetectGitRemoteURL 从 Git 仓库中检测 remote origin 的 URL，
// 用于在未配置 lfs.url 时推断 SSH 远程地址。
func DetectGitRemoteURL(repoPath string) (string, error) {
	cmd := exec.Command("git", "remote", "get-url", "origin")
	cmd.Dir = repoPath
	output, err := cmd.Output()
	if err != nil {
		return "", fmt.Errorf("无法获取 Git remote origin URL: %w", err)
	}
	url := strings.TrimSpace(string(output))
	if url == "" {
		return "", fmt.Errorf("Git remote origin URL 为空")
	}
	return url, nil
}
