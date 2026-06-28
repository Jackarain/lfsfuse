package lfs

import (
	"context"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"syscall"

	"github.com/hanwen/go-fuse/v2/fs"
	"github.com/hanwen/go-fuse/v2/fuse"
)

// Node 实现 FUSE 文件系统节点，支持普通文件和 LFS 虚拟文件的透明访问。
//
// 对于普通文件，Node 直接读取本地磁盘文件内容。
// 对于 LFS 指针文件，Node 通过 HTTP Range 请求从远程 LFS 存储中获取实际内容，
// 使得挂载点上的文件看起来包含实际内容而非 LFS 指针文本。
type Node struct {
	fs.Inode

	// localPath 是本地 Git 仓库中对应文件的绝对路径。
	localPath string

	// lfsURL 是 LFS 存储服务的 HTTP URL。
	lfsURL string

	// isDir 标识该节点是否为目录。
	isDir bool

	// size 是文件大小。对于普通文件，等于本地文件大小；
	// 对于 LFS 文件，等于指针文件中记录的原始大小。
	size int64

	// lfsOID 如果是 LFS 文件，存储其 SHA256 对象 ID；普通文件为空字符串。
	lfsOID string
}

// NewNode 创建一个新的 LFS 节点。
func NewNode(localPath, lfsURL string, isDir bool, size int64, lfsOID string) *Node {
	return &Node{
		localPath: localPath,
		lfsURL:    lfsURL,
		isDir:     isDir,
		size:      size,
		lfsOID:    lfsOID,
	}
}

// Getattr 返回文件或目录的属性（stat 信息）。
func (n *Node) Getattr(ctx context.Context, f fs.FileHandle, out *fuse.AttrOut) syscall.Errno {
	if n.isDir {
		out.Mode = syscall.S_IFDIR | 0755
	} else {
		out.Mode = syscall.S_IFREG | 0644
		out.Size = uint64(n.size)
	}
	return 0
}

// Open 打开文件。本文件系统为只读，拒绝写操作。
func (n *Node) Open(ctx context.Context, flags uint32) (fh fs.FileHandle, fuseFlags uint32, errno syscall.Errno) {
	if flags&(syscall.O_WRONLY|syscall.O_RDWR) != 0 {
		return nil, 0, syscall.EACCES
	}
	return nil, 0, 0
}

// Read 读取文件内容。
//
// 对于普通文件，直接从本地磁盘读取。
// 对于 LFS 文件，通过 HTTP Range 请求从远程 LFS 端点获取指定范围的数据，
// 支持部分读取（适用于流式播放、压缩归档等场景）。
func (n *Node) Read(ctx context.Context, f fs.FileHandle, dest []byte, off int64) (fuse.ReadResult, syscall.Errno) {
	if n.lfsOID == "" {
		return n.readLocalFile(dest, off)
	}
	return n.readLFSFile(ctx, dest, off)
}

// readLocalFile 从本地磁盘读取普通文件内容。
func (n *Node) readLocalFile(dest []byte, off int64) (fuse.ReadResult, syscall.Errno) {
	file, err := os.Open(n.localPath)
	if err != nil {
		return nil, syscall.ENOENT
	}
	defer file.Close()

	_, err = file.ReadAt(dest, off)
	if err != nil && err != io.EOF {
		return nil, syscall.EIO
	}
	return fuse.ReadResultData(dest), 0
}

// readLFSFile 通过 HTTP Range 请求从远程 LFS 端点读取数据。
func (n *Node) readLFSFile(ctx context.Context, dest []byte, off int64) (fuse.ReadResult, syscall.Errno) {
	if off >= n.size {
		return fuse.ReadResultData([]byte{}), 0
	}

	end := off + int64(len(dest)) - 1
	if end >= n.size {
		end = n.size - 1
	}

	url := fmt.Sprintf("%s/%s", n.lfsURL, n.lfsOID)
	req, err := http.NewRequestWithContext(ctx, "GET", url, nil)
	if err != nil {
		return nil, syscall.EIO
	}

	rangeHeader := fmt.Sprintf("bytes=%d-%d", off, end)
	req.Header.Set("Range", rangeHeader)
	log.Printf("[HTTP Range] %s -> %s", filepath.Base(n.localPath), rangeHeader)

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		log.Printf("LFS 网络请求失败 %s: %v", filepath.Base(n.localPath), err)
		return nil, syscall.EIO
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusPartialContent && resp.StatusCode != http.StatusOK {
		log.Printf("LFS 服务响应异常 %s: HTTP %d", filepath.Base(n.localPath), resp.StatusCode)
		return nil, syscall.EIO
	}

	nBytes, err := io.ReadFull(resp.Body, dest[:end-off+1])
	if err != nil && err != io.ErrUnexpectedEOF && err != io.EOF {
		return nil, syscall.EIO
	}

	return fuse.ReadResultData(dest[:nBytes]), 0
}
