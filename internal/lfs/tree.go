package lfs

import (
	"context"
	"log"
	"os"
	"path/filepath"
	"syscall"

	"github.com/hanwen/go-fuse/v2/fs"
)

// BuildTree 递归扫描本地 Git 仓库目录，为每个文件和子目录创建对应的 FUSE 节点。
//
// 扫描过程中：
//   - 跳过 .git 目录
//   - 对每个文件解析 LFS 指针信息
//   - 为 LFS 文件创建虚拟节点（显示原始大小，内容从远程获取）
//   - 为普通文件创建直接读取本地文件的节点
//
// BuildTree 必须在 FUSE 挂载完成后调用，以确保 NewPersistentInode 正常工作。
func (n *Node) BuildTree() error {
	entries, err := os.ReadDir(n.localPath)
	if err != nil {
		return err
	}

	for _, entry := range entries {
		name := entry.Name()
		if name == ".git" {
			continue
		}

		childLocalPath := filepath.Join(n.localPath, name)

		if entry.IsDir() {
			if err := n.buildDirNode(name, childLocalPath); err != nil {
				return err
			}
		} else {
			if err := n.buildFileNode(name, childLocalPath); err != nil {
				return err
			}
		}
	}
	return nil
}

// buildDirNode 为目录创建 FUSE 节点并递归构建子树。
func (n *Node) buildDirNode(name, localPath string) error {
	childNode := NewNode(localPath, n.lfsEndpoint, true, 0, "")
	child := n.NewPersistentInode(context.Background(), childNode, fs.StableAttr{Mode: syscall.S_IFDIR})
	n.AddChild(name, child, true)

	if err := childNode.BuildTree(); err != nil {
		return err
	}
	return nil
}

// buildFileNode 为文件创建 FUSE 节点，自动检测 LFS 指针文件。
func (n *Node) buildFileNode(name, localPath string) error {
	lfsInfo, err := ParsePointer(localPath)
	if err != nil {
		log.Printf("解析文件失败 %s: %v", localPath, err)
		return nil // 跳过无法解析的文件
	}

	var fileSize int64
	var oid string

	if lfsInfo.IsLFS {
		fileSize = lfsInfo.Size
		oid = lfsInfo.OID
		log.Printf("[LFS] 映射虚拟文件: %s -> %d bytes, OID: %s...",
			name, fileSize, oid[:8])
	} else {
		// 普通文件：读取本地文件大小
		fi, err := os.Stat(localPath)
		if err != nil {
			log.Printf("获取文件信息失败 %s: %v", localPath, err)
			return nil
		}
		fileSize = fi.Size()
	}

	childNode := NewNode(localPath, n.lfsEndpoint, false, fileSize, oid)
	child := n.NewPersistentInode(context.Background(), childNode, fs.StableAttr{Mode: syscall.S_IFREG})
	n.AddChild(name, child, true)
	return nil
}
