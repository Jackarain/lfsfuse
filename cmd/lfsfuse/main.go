// LFSFuse - Git LFS 虚拟文件系统
//
// LFSFuse 是一个基于 FUSE 的用户空间文件系统，它将本地 Git 仓库挂载为一个虚拟目录，
// 自动解析 Git LFS 指针文件，并从远程 LFS 存储服务透明地获取实际文件内容。
// 这使得用户可以像操作普通文件一样访问 LFS 文件，无需手动下载。
//
// 使用方式:
//
//	lfsfuse <git_repo_path> <lfs_http_endpoint> <mount_point>
//
// 示例:
//
//	lfsfuse /path/to/repo https://lfs.example.com /mnt/lfs
package main

import (
	"log"
	"os"
	"strings"

	"lfsfuse/internal/lfs"

	"github.com/hanwen/go-fuse/v2/fs"
	"github.com/hanwen/go-fuse/v2/fuse"
)

func main() {
	if len(os.Args) < 4 {
		log.Fatalf("用法: %s <git_repo_path> <lfs_http_endpoint> <mount_point>\n", os.Args[0])
	}

	gitRepoPath := os.Args[1]
	lfsEndpoint := strings.TrimRight(os.Args[2], "/")
	mountPoint := os.Args[3]

	// 确保挂载点目录存在
	if err := os.MkdirAll(mountPoint, 0755); err != nil {
		log.Fatalf("创建挂载点目录失败: %v", err)
	}

	// 验证 Git 仓库路径存在
	if _, err := os.Stat(gitRepoPath); err != nil {
		log.Fatalf("Git 仓库路径无效: %v", err)
	}

	// 创建根节点
	rootNode := lfs.NewNode(gitRepoPath, lfsEndpoint, true, 0, "")

	// 配置 FUSE 挂载选项：只读模式
	server, err := fs.Mount(mountPoint, rootNode, &fs.Options{
		MountOptions: fuse.MountOptions{
			Options: []string{"ro"},
			FsName:  "git-lfs-virtual",
		},
	})
	if err != nil {
		log.Fatalf("FUSE 挂载失败: %v", err)
	}
	defer server.Unmount()

	// 扫描 Git 仓库并构建虚拟文件树
	log.Println("正在扫描 Git 仓库并建立虚拟文件树...")
	if err := rootNode.BuildTree(); err != nil {
		log.Fatalf("扫描仓库失败: %v", err)
	}

	log.Printf("Git LFS 虚拟文件系统已成功挂载至: %s", mountPoint)
	log.Println("按 Ctrl+C 卸载并退出。")

	// 等待 FUSE 服务结束
	server.Wait()
}
