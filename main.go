package main

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"syscall"

	"github.com/hanwen/go-fuse/v2/fs"
	"github.com/hanwen/go-fuse/v2/fuse"
)

// LFSFileInfo 存储解析出来的 LFS 指针信息
type LFSFileInfo struct {
	IsLFS bool
	OID   string
	Size  int64
}

// LFSNode 实现 FUSE 的节点接口
type LFSNode struct {
	fs.Inode
	localPath   string
	lfsEndpoint string
	isDir       bool
	size        int64
	lfsOID      string // 如果是 LFS 文件，存储其 OID
}

// parseLFSPointer 检查并解析 LFS 指针文件
func parseLFSPointer(path string) (*LFSFileInfo, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer file.Close()

	scanner := bufio.NewScanner(io.LimitReader(file, 1024))
	var isLFS bool
	var oid string
	var size int64

	oidRegex := regexp.MustCompile(`oid sha256:([a-f0-9]{64})`)
	sizeRegex := regexp.MustCompile(`size (\d+)`)

	for scanner.Scan() {
		line := scanner.Text()
		if strings.Contains(line, "version https://git-lfs.github.com/spec/v1") {
			isLFS = true
		}
		if match := oidRegex.FindStringSubmatch(line); len(match) > 1 {
			oid = match[1]
		}
		if match := sizeRegex.FindStringSubmatch(line); len(match) > 1 {
			parsedSize, _ := strconv.ParseInt(match[1], 10, 64)
			size = parsedSize
		}
	}

	if isLFS && oid != "" && size > 0 {
		return &LFSFileInfo{IsLFS: true, OID: oid, Size: size}, nil
	}
	return &LFSFileInfo{IsLFS: false}, nil
}

// BuildTree 递归扫描本地 Git 仓库并构建 FUSE 节点树
func (n *LFSNode) BuildTree() error {
	files, err := os.ReadDir(n.localPath)
	if err != nil {
		return err
	}

	for _, file := range files {
		name := file.Name()
		if name == ".git" {
			continue
		}

		childLocalPath := filepath.Join(n.localPath, name)

		if file.IsDir() {
			childNode := &LFSNode{
				localPath:   childLocalPath,
				lfsEndpoint: n.lfsEndpoint,
				isDir:       true,
			}
			// 此时根节点已被 Mount 绑定，这里可以安全调用 NewPersistentInode
			ch := n.NewPersistentInode(context.Background(), childNode, fs.StableAttr{Mode: syscall.S_IFDIR})
			n.AddChild(name, ch, true)
			childNode.BuildTree()
		} else {
			lfsInfo, err := parseLFSPointer(childLocalPath)
			if err != nil {
				log.Printf("解析文件失败 %s: %v", childLocalPath, err)
				continue
			}

			var fileSize int64
			var oid string

			if lfsInfo.IsLFS {
				fileSize = lfsInfo.Size
				oid = lfsInfo.OID
				log.Printf("[LFS 指针] 映射虚拟文件: %s -> Size: %d, OID: %s...", name, fileSize, oid[:8])
			} else {
				info, _ := file.Info()
				fileSize = info.Size()
			}

			childNode := &LFSNode{
				localPath:   childLocalPath,
				lfsEndpoint: n.lfsEndpoint,
				isDir:       false,
				size:        fileSize,
				lfsOID:      oid,
			}
			ch := n.NewPersistentInode(context.Background(), childNode, fs.StableAttr{Mode: syscall.S_IFREG})
			n.AddChild(name, ch, true)
		}
	}
	return nil
}

func (n *LFSNode) Getattr(ctx context.Context, f fs.FileHandle, out *fuse.AttrOut) syscall.Errno {
	if n.isDir {
		out.Mode = syscall.S_IFDIR | 0755
	} else {
		out.Mode = syscall.S_IFREG | 0644
		out.Size = uint64(n.size)
	}
	return 0
}

func (n *LFSNode) Open(ctx context.Context, flags uint32) (fh fs.FileHandle, fuseFlags uint32, errno syscall.Errno) {
	if flags&(syscall.O_WRONLY|syscall.O_RDWR) != 0 {
		return nil, 0, syscall.EACCES
	}
	return nil, 0, 0
}

func (n *LFSNode) Read(ctx context.Context, f fs.FileHandle, dest []byte, off int64) (fuse.ReadResult, syscall.Errno) {
	if n.lfsOID == "" {
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

	if off >= n.size {
		return fuse.ReadResultData([]byte{}), 0
	}

	end := off + int64(len(dest)) - 1
	if end >= n.size {
		end = n.size - 1
	}

	// 注意：根据目标服务器接口，我们把 URL 拼接成了标准规范格式
	url := fmt.Sprintf("%s/%s", n.lfsEndpoint, n.lfsOID)
	req, err := http.NewRequestWithContext(ctx, "GET", url, nil)
	if err != nil {
		return nil, syscall.EIO
	}

	req.Header.Set("Range", fmt.Sprintf("bytes=%d-%d", off, end))
	log.Printf("[HTTP Range] 发送请求: %s [Range: bytes=%d-%d]", filepath.Base(n.localPath), off, end)

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		log.Printf("网络请求失败: %v", err)
		return nil, syscall.EIO
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusPartialContent && resp.StatusCode != http.StatusOK {
		log.Printf("LFS 服务响应异常码: %d", resp.StatusCode)
		return nil, syscall.EIO
	}

	nBytes, err := io.ReadFull(resp.Body, dest[:end-off+1])
	if err != nil && err != io.ErrUnexpectedEOF && err != io.EOF {
		return nil, syscall.EIO
	}

	return fuse.ReadResultData(dest[:nBytes]), 0
}

func main() {
	if len(os.Args) < 4 {
		fmt.Printf("Usage: %s <git_repo_path> <lfs_http_endpoint> <mount_point>\n", os.Args[0])
		os.Exit(1)
	}

	gitRepoPath := os.Args[1]
	lfsEndpoint := strings.TrimRight(os.Args[2], "/")
	mountPoint := os.Args[3]

	os.MkdirAll(mountPoint, 0755)

	rootNode := &LFSNode{
		localPath:   gitRepoPath,
		lfsEndpoint: lfsEndpoint,
		isDir:       true,
	}

	opts := &fs.Options{}
	opts.Debug = false
	opts.MountOptions.Options = []string{"ro"}
	opts.MountOptions.FsName = "git-lfs-virtual"

	// 核心修复点 1：先挂载，初始化内部的 Bridge 绑定结构
	server, err := fs.Mount(mountPoint, rootNode, opts)
	if err != nil {
		log.Fatalf("Mount 失败: %v", err)
	}

	// 核心修复点 2：挂载完成后，再建立虚拟文件树
	log.Println("正在扫描 Git 仓库并建立虚拟文件树...")
	if err := rootNode.BuildTree(); err != nil {
		// 扫树失败时最好安全解挂，防止目录死锁
		server.Unmount()
		log.Fatalf("扫描仓库失败: %v", err)
	}

	log.Printf("虚拟 LFS 文件系统已成功挂载至: %s", mountPoint)
	log.Println("按 Ctrl+C 退出卸载。")

	server.Wait()
}

