<p align="center">
  <img src="./web/logo.png" width="92" alt="LFH Logo" />
</p>

<h1 align="center">LFH — LAN FileHub</h1>

<p align="center">
  轻量、零依赖的分布式局域网文件中心 · 单入口聚合多主机闲置空间 · 实时协作与在线预览
</p>

<p align="center">
  <a href="./README.md">简体中文</a> · <a href="./README.en.md">English</a> · <a href="./LICENSE">MIT License</a> · <code>V0.11.21.4</code>
</p>

---

**LFH（LAN FileHub）** 是一个面向个人工作区与小型局域网团队的自托管文件中心。它采用纯 Go 标准库编写，将多台接入同一局域网的 Windows / Linux / macOS 设备自动组网为统一的存储池——外部访问只需记住一个入口地址，文件会自动按节点剩余空间分配并支持跨节点透明预览、下载与在线解压。

单二进制开箱即用，无外部数据库或第三方运行时依赖；支持 Docker 容器部署与本地一键运行。

## 核心特性

- **异构局域网自动组网与网页端可视化探测接入（Entry / Worker）**：
  - 支持 UDP 局域网广播发现（默认 `53319/udp`），并可在管理员面板「设置 → 集群与入口」**主动扫描同网段局域网节点**或**手动输入对方 IP:端口探测**；
  - 无需命令行即可直接在网页端输入对方管理员账号或密钥**一键纳管远端节点**（或将本机主动接入其他主入口），实现多主机闲置空间实时并池、单一主入口统一访问；
  - **节点在线状态与在线文件联动**：顶部状态栏、左侧节点列表与集群管理表实时展示节点在线状态；**节点在线时汇总展示该节点文件，节点离线后自动隐藏其上的文件、上线后自动恢复显示**。
- **拖拽上传与目录保留**：支持整文件夹拖拽直传与压缩包直接入库，实时显示传输进度，并在侧栏提供上传后缀白名单 / 黑名单策略。
- **三视图文件浏览与实时同步**：内置**列表视图**、**网格图标视图**（Windows 风格文件/文件夹图标）及**统计图表视图**；基于 SSE（Server-Sent Events）实现多端上传自动无感刷新。
- **在线预览与文件详细属性**：支持常见图片、音视频、文本/Markdown、文档及压缩包在线浏览、搜索与解压；点击**属性**可查看精确字节大小、MIME 类型、实时 SHA-256 校验值、可见范围、所在节点及设备指纹来源；管理员还可查看文件的实际物理磁盘路径。
- **精细权限控制与角色治理**：
  - 首次初始化自动生成全局随机管理员账号（`lfh_xxxxxx` 与高强度随机密码），并写入数据目录 `initial-admin.txt`，避免多机部署产生默认弱口令冲突。
  - 管理员可在「用户管理」中随时为正常账号**授予或取消管理员权限**（内置防误删最后一名管理员与防自我降权保护）。
  - 支持**指定用户可见与下载**：上传时或上传后均可限定仅指定系统用户可下载，未授权用户在列表、统计图表、实时推送与下载接口中完全不可见；仅**管理员与原上传者**可删除文件或调整权限，其余被授权用户只读下载。
  - 普通成员仅展示外观主题与个人账号面板；节点存储磁盘选择、路径切换（含已有文件一键跨盘迁移）及真实存储路径对普通用户严格隐藏，仅限管理员管理。
- **深浅双主题与全端适配**：内置精致深/浅色模式切换，界面针对桌面端、平板及移动端重排优化。
- **GitHub 仓库版本检测与不停机升级**：管理面板可自动检查仓库新版本，支持后台下载校验并在热交接后完成不停机平滑更新。

---

## 快速部署

### 方式一：Docker 部署（推荐）

仓库根目录已提供多阶段构建的 [`Dockerfile`](./Dockerfile) 与 [`docker-compose.yml`](./docker-compose.yml)，镜像内置 Alpine 运行环境并将数据持久化挂载至 `./lfh-data`：

```bash
# 1. 克隆空白源码仓库
git clone https://github.com/sxu99579-bit/LFH.git
cd LFH

# 2. 启动服务（首次启动将自动构建镜像并初始化管理员凭据）
docker compose up -d

# 3. 查看首启生成的随机管理员账号与密码
docker compose logs lfh
```

首次生成的高随机度管理员凭据同时保存在宿主机 `./lfh-data/initial-admin.txt` 中。

> **局域网提示**：
> - 若在 Docker Bridge 网络下运行，建议修改 `docker-compose.yml` 的 `command` 增加 `-expose-ip 你的宿主机局域网IP`（例如 `192.168.1.100`），首页展示的访问入口即会自动对应宿主机地址。
> - 若在 Linux 宿主机上需要与同局域网其他物理机进行 UDP 自动组网发现（默认 `53319/udp`），可改为 `network_mode: host`。

也可以直接使用 `docker build` / `docker run` 手动运行：

```bash
docker build -t lfh:V0.11.21.4 .

docker run -d \
  --name lfh \
  --restart unless-stopped \
  -p 8080:8080 \
  -p 53319:53319/udp \
  -v $(pwd)/lfh-data:/data \
  lfh:V0.11.21.4 -data /data -port 8080 -expose-ip 192.168.1.100
```

---

### 方式二：本地部署与源码编译

环境要求：Go `1.23+`（若直接下载 GitHub Releases 中的预编译二进制则无需安装 Go）。

#### 1. 从源码构建运行

```bash
git clone https://github.com/sxu99579-bit/LFH.git
cd LFH

# 编译
go build -trimpath -ldflags="-s -w" -o lfh .      # Linux / macOS
go build -trimpath -ldflags="-s -w" -o lfh.exe .  # Windows

# 启动主入口节点（默认监听 8080 端口，数据存放在 ./data）
./lfh -data ./data -port 8080
```

启动后控制台与 `./data/initial-admin.txt` 会输出本次随机生成的管理员用户名和密码，浏览器访问控制台打印的局域网入口地址即可使用。

#### 2. 多主机组网接入（加入 Worker 节点）

在第二台电脑上复制配置模板 [`cluster-config.example.json`](./cluster-config.example.json) 为 `cluster-config.json`，或在设置中心查看主节点的集群密钥后，通过命令行一键加入：

```bash
# 示例：将第二台机器作为存储节点接入，贡献 50GB 空间
./lfh -role worker -quota 50GB -secret "主节点设置的集群密钥" -entry "http://192.168.1.100:8080"
```

---

## 常用启动参数

| 参数 | 默认值 | 说明 |
| :--- | :--- | :--- |
| `-port` | `8080` | HTTP 服务监听端口 |
| `-data` | `./data` | 用户数据、文件索引、缓存与上传目录 |
| `-role` | `entry` | 节点角色：`entry`（主入口）或 `worker`（扩展存储节点） |
| `-quota` | 自动 | 预留给 LFH 使用的最大空间配额（支持 `MB` / `GB` / `TB`） |
| `-storage-dir` | `<data>/uploads` | 自定义当前节点实际保存上传文件的磁盘盘符或目录路径（也可在界面「集群与入口」中由管理员在线切换并迁移） |
| `-expose-ip` | 自动探测 | 自定义首页展示的对外访问 IP 或主机名 |
| `-secret` | 随机/配置 | 集群节点互信密钥 |
| `-entry` | 空 | Worker 节点主动连接的主入口地址提示 |
| `-name` | 主机名 | 节点在界面中的展示名称 |
| `-config` | 空 | 指定初始化读取的 `cluster-config.json` 路径 |
| `-discovery-port` | `53319` | 局域网 UDP 自动发现端口（设为 `0` 关闭广播发现） |
| `-repo` | `sxu99579-bit/LFH` | 用于新版本检测的 GitHub 仓库（`owner/repo`） |
| `-init-user` / `-init-pass` | 随机生成 | 仅在首次初始化空数据目录时指定初始管理员账号密码 |
| `-version` | `false` | 输出当前版本号并退出 |

---

## 目录结构与二次开发

项目完全基于 Go 标准库实现核心后端与嵌入式前端资源，模块划分清晰，欢迎按需裁剪或扩展功能：

```text
.
├── main.go                     # 入口启动、集群初始化与平滑热重启交接
├── internal/
│   ├── auth/                   # 账号体系、邀请码、封禁状态、随机管理员初始化
│   ├── cluster/                # UDP 节点发现、心跳同步、容量配额与请求代理
│   ├── preview/                # 文本/Office/压缩包解析与实时字节流校验辅助
│   ├── server/                 # HTTP API、SSE 事件总线、权限校验与文件元数据接口
│   ├── storage/                # 本地文件写入、设备指纹追踪与聚合索引
│   ├── updater/                # GitHub Release 检查、校验与零停机替换升级
│   └── version/                # 应用标识与版本信息
├── web/                        # 无构建依赖原生前端（HTML / CSS / JS + 图标）
├── Dockerfile                  # 多阶段生产容器镜像构建文件
├── docker-compose.yml          # 容器编排示例配置
├── cluster-config.example.json # 节点配置模板（已过滤敏感配置）
└── LICENSE                     # MIT 开源许可证
```

欢迎直接 Fork 本项目、定制企业内网认证或扩展更多文件格式处理器。本地运行完整测试：

```bash
go test -count=1 ./...
```

## 开源协议

本项目基于 [**MIT License**](./LICENSE) 开源发布——你可以自由地使用、修改、分发本软件，或将其用于个人及商业项目的二次开发，只需保留原始版权及许可声明。

## 开发致谢

在此特别感谢 **Google Antigravity** 与 **Gemini** 在本项目架构设计、分布式组网方案演进、界面细节打磨及工程自动化构建过程中提供的重要支持与协助。
