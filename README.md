# LFSFuse — Git LFS 虚拟文件系统

[![Go Version](https://img.shields.io/badge/Go-1.26+-00ADD8?logo=go)](https://go.dev/)
[![License: MIT](https://img.shields.io/badge/License-MIT-yellow.svg)](LICENSE)

**LFSFuse** 是一个基于 [FUSE](https://github.com/libfuse/libfuse) 的用户空间文件系统，它将本地 Git 仓库挂载为一个虚拟目录，自动解析 [Git LFS](https://git-lfs.com/)（Large File Storage）指针文件，并从远程 LFS 存储服务透明地获取实际文件内容。

> 🪄 挂载后，LFS 文件在文件系统中表现为直接包含实际内容，而非 LFS 指针文本。用户可以像操作普通文件一样 `cat`、`less`、`head` 等，无需手动 `git lfs pull`。

---

## 目录

- [工作原理](#工作原理)
- [功能特性](#功能特性)
- [安装](#安装)
  - [前置要求](#前置要求)
  - [从源码编译](#从源码编译)
  - [使用 Go 安装](#使用-go-安装)
- [搭建 LFS 存储服务（推荐）](#搭建-lfs-存储服务推荐)
- [使用指南](#使用指南)
  - [基本用法](#基本用法)
  - [完整示例](#完整示例)
- [开发指南](#开发指南)
  - [快速开始](#快速开始)
  - [测试](#测试)
  - [代码检查](#代码检查)
- [技术细节](#技术细节)
  - [LFS 指针解析](#lfs-指针解析)
  - [HTTP Range 请求](#http-range-请求)
  - [只读挂载](#只读挂载)
- [常见问题](#常见问题)
- [许可证](#许可证)

---

## 工作原理

Git LFS（Large File Storage）是 Git 的一个扩展，它将大文件替换为文本指针，实际文件内容存储在远程服务器上。在本地仓库中，LFS 文件仅包含一个指针文件，格式如下：

```
version https://git-lfs.github.com/spec/v1
oid sha256:4df7c5b3e3b3f2c0e5e6b7c8d9e0f1a2b3c4d5e6f7a8b9c0d1e2f3a4b5c6d7e8
size 12345678
```

LFSFuse 的工作原理如下：

1. **挂载阶段**：将 FUSE 文件系统挂载到指定目录。
2. **扫描阶段**：递归扫描本地 Git 仓库，为每个文件和目录创建对应的 FUSE 节点。
3. **解析阶段**：检测每个文件是否为 LFS 指针文件。若是，则解析出 OID 和原始大小。
4. **访问阶段**：当用户读取文件时：
   - **普通文件**：直接从本地磁盘读取。
   - **LFS 文件**：通过 HTTP `Range` 请求从远程 LFS 端点获取指定范围的数据，实现透明访问。

```
┌───────────────┐      ┌──────────────┐      ┌─────────────────┐
│  user op      │ ───▶ │  FUSE 挂载点 │ ───▶ │  LFSFuse(FUSE)  │
│  cat/less/vim │      │  /mnt/lfs    │      │  用户空间驱动   │
└───────────────┘      └──────────────┘      └────────┬────────┘
                                                      │
                          ┌───────────────────────────┼──────────┐
                          │                           │          │
                          ▼                           ▼          │
                   ┌────────────┐             ┌──────────────┐   │
                   │  普通文件  │             │  LFS 文件    │   │
                   │  本地读取  │             │  HTTP Range  │   │
                   └────────────┘             │  远程获取    │   │
                                              └──────────────┘   │
                          ┌──────────────────────────────────────┘
                          ▼
                   ┌──────────────┐
                   │ LFS 存储服务 │
                   │  httpd 服务  │
                   └──────────────┘
```

## 功能特性

- ✅ **透明访问** — LFS 文件在挂载点中表现为包含实际内容的普通文件
- ✅ **只读安全** — 文件系统以只读模式挂载，防止意外修改
- ✅ **部分读取** — 支持 HTTP Range 请求，适用于流式播放和大文件预览
- ✅ **零依赖部署** — 单二进制文件，无需安装 FUSE 库（Linux 内核内置支持）
- ✅ **内存高效** — 不缓存文件内容到内存，按需从远程获取
- ✅ **广泛兼容** — 兼容任何标准 Git LFS 存储服务

## 安装

### 前置要求

- **Linux** 操作系统（FUSE 需要内核支持，Linux 内核 2.6.14+ 内置）
- **Go 1.26+**（仅编译时需要）
- **FUSE 库**：大多数 Linux 发行版已默认安装，如未安装：

  ```bash
  # Debian/Ubuntu
  sudo apt-get install fuse3

  # CentOS/RHEL/Fedora
  sudo yum install fuse3
  ```

### 从源码编译

```bash
git clone https://github.com/Jackarain/lfsfuse.git
cd lfsfuse
make build
```

编译产物位于 `bin/lfsfuse`。

### 使用 Go 安装

```bash
go install github.com/Jackarain/lfsfuse/cmd/lfsfuse@latest
```

### 搭建 LFS 存储服务（推荐）

LFSFuse 需要配合一个支持 HTTP Range 请求的 LFS 对象存储服务使用。推荐使用 [httpd](https://github.com/avplayer/httpd) — 一个轻量级、高性能的 HTTP 文件存储服务器，专为 Git LFS 场景设计

httpd 的特点：
- **单二进制**，零外部依赖，部署简便
- **支持 HTTP Range**，完美适配 LFSFuse 的按需读取
- **高性能**，适合生产环境使用

启动 httpd 后，即可将其地址作为 LFSFuse 的 `<lfs_http_endpoint>` 参数使用。

## 使用指南

### 基本用法

```bash
lfsfuse <git_repo_path> <lfs_http_endpoint> <mount_point>
```

| 参数 | 说明 | 示例 |
|------|------|------|
| `git_repo_path` | 本地 Git 仓库路径 | `/home/user/my-repo`，存储指针文件即可，无需拉取 LFS 真实数据 |
| `lfs_http_endpoint` | LFS 存储服务的 HTTP 端点 URL | `https://lfs.example.com` |
| `mount_point` | 挂载点目录 | `/mnt/lfs` |

### 完整示例

以下演示从零搭建一个完整的 Git LFS 虚拟文件系统环境：

```bash
# 1. 启动 LFS 存储服务（终端 1）
httpd -listen "[::0]:8080" --path ./lfs-storage

# 2. 创建挂载点并启动 LFSFuse（终端 2）
mkdir -p /mnt/lfs
lfsfuse /path/to/git-repo http://localhost:8080 /mnt/lfs

# 3. 在另一个终端中访问文件（终端 3）
ls -la /mnt/lfs/
cat /mnt/lfs/path/to/large-file.bin
head -c 100 /mnt/lfs/path/to/large-file.bin

# 4. 卸载（在 LFSFuse 终端按 Ctrl+C）
#    或使用 fusermount 命令
fusermount -u /mnt/lfs
```

### 包说明

| 包 | 路径 | 职责 |
|----|------|------|
| `main` | `cmd/lfsfuse/` | 入口点：CLI 参数解析、FUSE 挂载初始化、信号处理 |
| `lfs` | `internal/lfs/` | 核心逻辑：LFS 指针解析、FUSE 节点实现、文件树构建 |

## 开发指南

### 快速开始

```bash
# 克隆项目
git clone https://github.com/Jackarain/lfsfuse.git
cd lfsfuse

# 编译
make build

# 运行（需要准备测试仓库和 LFS 端点）
./bin/lfsfuse /path/to/test-repo https://lfs.example.com /mnt/lfs
```

### 测试

```bash
# 运行所有测试
make test

# 直接使用 go test
go test -v -race ./...
```

### 代码检查

```bash
# 安装 golangci-lint（如未安装）
go install github.com/golangci/golangci-lint/cmd/golangci-lint@latest

# 运行代码检查
make lint

# 运行 go vet
make vet
```

## 技术细节

### LFS 指针解析

LFS 指针文件是一个文本文件，包含以下关键信息：

- **版本标识**：`version https://git-lfs.github.com/spec/v1` — 用于识别 LFS 指针文件
- **OID**：`oid sha256:<64位十六进制哈希>` — 文件内容的 SHA256 哈希值
- **原始大小**：`size <数字>` — 文件原始大小（字节）

LFSFuse 读取文件的前 1KB 进行解析，通过正则表达式提取上述信息。这种设计确保了：
- 快速识别 LFS 文件（仅需读取少量字节）
- 避免误判非 LFS 文件（大文件的前 1KB 几乎不可能匹配 LFS 格式）

### HTTP Range 请求

LFSFuse 利用 HTTP `Range` 请求头实现部分内容获取：

```
GET /<oid> HTTP/1.1
Range: bytes=<start>-<end>
```

这种方式的好处：
- **按需读取**：仅获取用户实际访问的数据范围，节省带宽
- **流式支持**：支持文件的随机访问和部分读取
- **兼容性强**：绝大多数 HTTP 存储服务（S3、MinIO、WebDAV 等）都支持 Range 请求

### 只读挂载

LFSFuse 使用 FUSE 的只读挂载选项，确保：

- 文件系统在挂载时指定 `ro`（read-only）参数
- `Open` 方法拒绝 `O_WRONLY` 和 `O_RDWR` 标志
- 这层保护防止用户意外修改 LFS 文件，因为修改本地指针文件不会同步到远程存储

## 常见问题

### Q: 为什么挂载后 LFS 文件大小为 0？

A: 请确认 LFS 端点 URL 正确，并且网络可以访问该端点。LFSFuse 解析指针文件后，文件大小来自指针文件中的 `size` 字段。

### Q: 支持写入文件吗？

A: 不支持。LFSFuse 设计为只读文件系统，用于浏览和读取 LFS 文件。如需修改 LFS 文件，请使用标准的 `git lfs` 工作流程。

### Q: 性能和内存占用如何？

A: LFSFuse 不缓存文件内容到内存，仅在用户读取时通过 HTTP Range 请求获取所需部分。因此，即使挂载包含大量 LFS 文件的仓库，内存占用也非常低。

### Q: 支持 macOS 或 Windows 吗？

A: 当前版本主要面向 Linux。macOS 可通过 osxfuse 使用，但未经过充分测试。Windows 不支持。

### Q: 为什么需要 FUSE？

A: FUSE（Filesystem in Userspace）允许在用户空间实现文件系统，无需修改内核代码。这使得 LFSFuse 可以像普通程序一样安装和运行。

## 许可证

本项目基于 [MIT 许可证](LICENSE) 开源。

---

<p align="center">Made with ❤️ for the Git LFS community</p>
