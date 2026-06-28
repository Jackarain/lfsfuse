// LFSFuse - Git LFS 虚拟文件系统
//
// LFSFuse 是一个基于 FUSE 的用户空间文件系统，它将本地 Git 仓库挂载为一个虚拟目录，
// 自动解析 Git LFS 指针文件，并从远程 LFS 存储服务透明地获取实际文件内容。
// 这使得用户可以像操作普通文件一样访问 LFS 文件，无需手动下载。
//
// 使用方式:
//
//	lfsfuse [flags]
//
// 示例:
//
//	lfsfuse --repo /path/to/repo --endpoint https://lfs.example.com --mount /mnt/lfs
//	lfsfuse -r /path/to/repo -e https://lfs.example.com -m /mnt/lfs
//	lfsfuse --config ./config.yaml
package main

import (
	"bytes"
	"fmt"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"lfsfuse/internal/lfs"

	"github.com/hanwen/go-fuse/v2/fs"
	"github.com/hanwen/go-fuse/v2/fuse"
	"github.com/spf13/pflag"
	"github.com/spf13/viper"
)

const appVersion = "1.0.0"

// Config 存储所有配置项，支持配置文件、环境变量和命令行参数。
type Config struct {
	Repo     string `mapstructure:"repo"`
	Endpoint string `mapstructure:"endpoint"`
	Mount    string `mapstructure:"mount"`
}

// resolveLFSEndpoint 从 Git 仓库的多种配置文件中自动读取 lfs.url。
// 优先级：
//  1. 使用 `git config --get lfs.url` 命令（覆盖 .git/config、.lfsconfig、全局配置等）
//  2. 直接解析 .lfsconfig 文件
//  3. 直接解析 .git/config 文件
func resolveLFSEndpoint(repoPath string) (string, error) {
	// 方法1：使用 git config 命令读取（最全面，覆盖所有配置文件）
	cmd := exec.Command("git", "config", "--get", "lfs.url")
	cmd.Dir = repoPath
	var out bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = nil

	if err := cmd.Run(); err == nil {
		if url := strings.TrimSpace(out.String()); url != "" {
			return url, nil
		}
	}

	// 方法2：直接解析 .lfsconfig 文件（LFS 专用配置）
	lfsConfigPath := filepath.Join(repoPath, ".lfsconfig")
	if url, err := parseConfigKey(lfsConfigPath, "lfs", "url"); err == nil && url != "" {
		return url, nil
	}

	// 方法3：直接解析 .git/config 文件
	gitConfigPath := filepath.Join(repoPath, ".git", "config")
	if url, err := parseConfigKey(gitConfigPath, "lfs", "url"); err == nil && url != "" {
		return url, nil
	}

	return "", fmt.Errorf("在所有 Git 配置文件中均未找到 lfs.url")
}

// parseConfigKey 解析类 INI 格式的配置文件，查找指定 section 下的 key 值。
// 支持格式:
//
//	[section]
//	    key = value
//	    key=value
func parseConfigKey(configPath, section, key string) (string, error) {
	data, err := os.ReadFile(configPath)
	if err != nil {
		return "", err
	}

	sectionHeader := "[" + section + "]"
	inSection := false

	for _, line := range strings.Split(string(data), "\n") {
		trimmed := strings.TrimSpace(line)

		// 跳过空行和注释
		if trimmed == "" || strings.HasPrefix(trimmed, ";") || strings.HasPrefix(trimmed, "#") {
			continue
		}

		// 检查 section 头: [section]
		if strings.HasPrefix(trimmed, "[") && strings.HasSuffix(trimmed, "]") {
			inSection = trimmed == sectionHeader
			continue
		}

		if inSection {
			// 匹配 "key" 或 "key = value" 或 "key=value"
			if strings.HasPrefix(trimmed, key+" ") || strings.HasPrefix(trimmed, key+"=") || trimmed == key {
				eqIdx := strings.Index(trimmed, "=")
				if eqIdx >= 0 {
					return strings.TrimSpace(trimmed[eqIdx+1:]), nil
				}
				// 只有 key 没有值
				return "", fmt.Errorf("配置项 %s.%s 没有值", section, key)
			}
		}
	}

	return "", fmt.Errorf("未找到配置项 %s.%s", section, key)
}

func main() {
	// ============================================================
	// 第一步：定义命令行标志
	// ============================================================
	var (
		cfgFile  = pflag.StringP("config", "c", "", "配置文件路径（支持 YAML、JSON、TOML 格式）")
		showVer  = pflag.BoolP("version", "v", false, "显示版本信息")
		showHelp = pflag.BoolP("help", "h", false, "显示帮助信息")
	)
	pflag.StringP("repo", "r", "", "Git 仓库路径 (必需)")
	pflag.StringP("endpoint", "e", "", "LFS 存储服务 HTTP 端点 URL（可选，默认从 Git 仓库配置自动读取）")
	pflag.StringP("mount", "m", "", "挂载点目录 (必需)")

	// 自定义帮助信息
	pflag.Usage = func() {
		fmt.Fprintf(os.Stderr, `LFSFuse — Git LFS 虚拟文件系统 v%s

将本地 Git 仓库挂载为虚拟目录，透明访问 LFS 文件。
使用方式: %s [flags]

Flags:
  -r, --repo PATH         Git 仓库路径（必需）
  -e, --endpoint URL      LFS 存储服务 HTTP 端点 URL（可选，默认从 Git 仓库配置读取）
  -m, --mount PATH        挂载点目录（必需）
  -c, --config FILE       配置文件路径（支持 YAML、JSON、TOML 格式）
  -v, --version           显示版本信息
  -h, --help              显示帮助信息

配置文件示例 (config.yaml):
  repo: /path/to/repo
  endpoint: https://lfs.example.com（可选）
  mount: /mnt/lfs

环境变量:
  LFSFUSE_REPO, LFSFUSE_ENDPOINT, LFSFUSE_MOUNT

`,
			appVersion,
			filepath.Base(os.Args[0]))
	}

	pflag.Parse()

	// 处理 --help
	if *showHelp {
		pflag.Usage()
		return
	}

	// 处理 --version
	if *showVer {
		fmt.Printf("LFSFuse v%s\n", appVersion)
		return
	}

	// ============================================================
	// 第二步：初始化 Viper 配置管理
	// 优先级（从高到低）:
	//   1. 命令行标志 (pflag)
	//   2. 环境变量 (LFSFUSE_*)
	//   3. 配置文件
	//   4. 默认值
	// ============================================================
	v := viper.New()

	// 设置默认值
	v.SetDefault("repo", "")
	v.SetDefault("endpoint", "")
	v.SetDefault("mount", "")

	// 绑定环境变量（LFSFUSE_REPO, LFSFUSE_ENDPOINT, LFSFUSE_MOUNT）
	v.SetEnvPrefix("LFSFUSE")
	v.AutomaticEnv()

	// 加载配置文件
	if *cfgFile != "" {
		// 用户显式指定配置文件
		v.SetConfigFile(*cfgFile)
		if err := v.ReadInConfig(); err != nil {
			log.Fatalf("读取配置文件失败: %v", err)
		}
		log.Printf("已加载配置文件: %s", *cfgFile)
	} else {
		// 自动查找配置文件（非必需）
		v.SetConfigName(".lfsfuserc")
		v.AddConfigPath(".")
		v.AddConfigPath("$HOME")
		v.AddConfigPath("/etc/lfsfuse")
		if err := v.ReadInConfig(); err == nil {
			log.Printf("已加载配置文件: %s", v.ConfigFileUsed())
		}
	}

	// 绑定命令行标志（优先级最高）
	if err := v.BindPFlag("repo", pflag.Lookup("repo")); err != nil {
		log.Fatalf("绑定标志失败: %v", err)
	}
	if err := v.BindPFlag("endpoint", pflag.Lookup("endpoint")); err != nil {
		log.Fatalf("绑定标志失败: %v", err)
	}
	if err := v.BindPFlag("mount", pflag.Lookup("mount")); err != nil {
		log.Fatalf("绑定标志失败: %v", err)
	}

	// ============================================================
	// 第三步：解析配置
	// ============================================================
	var cfg Config
	if err := v.Unmarshal(&cfg); err != nil {
		log.Fatalf("解析配置失败: %v", err)
	}

	// ============================================================
	// 第四步：验证必需参数
	// ============================================================
	if cfg.Repo == "" || cfg.Mount == "" {
		pflag.Usage()
		log.Fatalf("错误: 缺少必需参数。请指定 --repo 和 --mount。")
	}

	// 清理 Endpoint 尾部斜杠
	cfg.Endpoint = strings.TrimRight(cfg.Endpoint, "/")

	// 如果未指定 Endpoint，尝试从 Git 仓库配置自动读取 lfs.url
	if cfg.Endpoint == "" {
		endpoint, err := resolveLFSEndpoint(cfg.Repo)
		if err != nil {
			log.Fatalf("错误: 未指定 --endpoint 且无法从 Git 仓库配置中自动读取 lfs.url: %v", err)
		}
		cfg.Endpoint = endpoint
		log.Printf("已从 Git 仓库配置自动读取 LFS 端点: %s", cfg.Endpoint)
	}

	// 确保挂载点目录存在
	if err := os.MkdirAll(cfg.Mount, 0755); err != nil {
		log.Fatalf("创建挂载点目录失败: %v", err)
	}

	// 验证 Git 仓库路径存在
	if _, err := os.Stat(cfg.Repo); err != nil {
		log.Fatalf("Git 仓库路径无效: %v", err)
	}

	// ============================================================
	// 第五步：FUSE 挂载
	// ============================================================
	rootNode := lfs.NewNode(cfg.Repo, cfg.Endpoint, true, 0, "")

	server, err := fs.Mount(cfg.Mount, rootNode, &fs.Options{
		MountOptions: fuse.MountOptions{
			Options: []string{"ro"},
			FsName:  "git-lfs-virtual",
		},
	})
	if err != nil {
		log.Fatalf("FUSE 挂载失败: %v", err)
	}
	defer server.Unmount()

	log.Println("正在扫描 Git 仓库并建立虚拟文件树...")
	if err := rootNode.BuildTree(); err != nil {
		log.Fatalf("扫描仓库失败: %v", err)
	}

	log.Printf("Git LFS 虚拟文件系统已成功挂载至: %s", cfg.Mount)
	log.Println("按 Ctrl+C 卸载并退出。")

	server.Wait()
}
