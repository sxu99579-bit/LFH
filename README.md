<p align="center">
  <picture>
    <source media="(prefers-color-scheme: dark)" srcset="./web/logo-dark.png" />
    <source media="(prefers-color-scheme: light)" srcset="./web/logo-light.png" />
    <img src="./web/logo.png" width="112" alt="LFH Logo" />
  </picture>
</p>

<h1 align="center">LFH — LAN FileHub</h1>

<p align="center">
  轻量、零依赖的分布式局域网文件中心 · 单入口聚合多主机闲置空间 · 实时协作与在线预览
</p>

<p align="center">
  <a href="./README.md">简体中文</a> · <a href="./README.en.md">English</a> · <a href="./LICENSE">MIT License</a> · <a href="https://github.com/sxu99579-bit/LFH/discussions">讨论与留言</a> · <a href="https://github.com/sxu99579-bit/LFH/issues">问题反馈</a> · <code>V0.11.24.3</code>
</p>

---

**LFH（LAN FileHub）** 是一个面向个人工作区与小型局域网团队的自托管文件中心。它采用纯 Go 标准库编写，将多台接入同一局域网的 Windows / Linux / macOS 设备自动组网为统一的存储池——外部访问只需记住一个入口地址，文件会自动按节点剩余空间分配并支持跨节点透明预览、下载与在线解压。

单二进制开箱即用，无外部数据库或第三方运行时依赖；支持 Docker 容器部署与本地一键运行。

<p align="center">
  <img src="./assets/readme/hero-workspace.jpg" width="900" alt="LFH 桌面端与移动端工作台概览" />
</p>

## 核心特性

- **多智能体文件协作中枢（Agent File Bridge & 原生 MCP）**：
  - **一键免密接入、自主配对审批与原生 OAuth 2.1**：提供 `/agent/connect.ps1`（Windows）与 `/agent/connect.sh`（macOS / Linux）一条命令自动探测并写入 Cursor / Antigravity / Claude Code CLI / Claude Desktop 配置；Agent 可通过 `/api/agent-pairing/request` 自助申请配对，管理员在 Web 控制台实时接收 SSE 提醒后一键「批准 / 拒绝」（支持按最小权限裁剪作用域，拒绝越权），同时兼容原生 MCP OAuth 2.1 动态客户端注册与 PKCE S256 授权（`/.well-known/oauth-protected-resource`、`/.well-known/oauth-authorization-server`、`/oauth/*`）；
  - **Agent 实时控制台与零信任凭证**：设置页「Agent 协作」顶部提供实时控制台，聚合展示已授权密钥、活跃 MCP 会话、工作状态与当前任务/文件软租约，并支持一键断开与实时 SSE 状态刷新；签发的 `lfh_ag_` 密钥服务端仅存 SHA-256 哈希，细粒度控制 `read / write / share / verify / delete` 作用域、目录前缀白名单与请求速率限制；
  - **可写工作区挂载与高并发读写**：挂载目录可按需启用 `Writable` 写穿模式与 `Shadow` 自动版本备份，内置文件忽略规则与敏感路径（`.git`、`.ssh`、`.env` 等）硬拦截；提供分段文件锁（Striped Locks）、CAS 乐观版本校验（`expected_version`）、软编辑租约（`/api/files/{id}/lease`）及精准补丁 / 多文件事务提交（`/api/files/{id}/patch`、`/api/files/batch-commit`）；
  - **纯 Go 原生 Streamable HTTP `/mcp`（25 项工具）**：免外部桥接进程直接暴露 `/mcp` 端点，涵盖工作区发现、检索、分片/行号读取、补丁、事务提交、软租约、`wait_for_changes` 长轮询、`issue_transfer_ticket`、`trigger_workspace_verify`、实时状态汇报 `report_agent_status`，以及 AI 任务板协同工具 `list_tasks` / `claim_task` / `heartbeat_task` / `split_task` / `post_task_comment` / `complete_task` / `release_task`；
  - **AI 协同任务板、三层卡通工牌与零消耗后台值守**：内置结构化待办任务板（定时自动激活、多 Agent 原子 CAS 抢单、`@Agent` 定向指派、分块子任务 `subtasks`/`depends_on` 接力与群聊讨论楼轮次上限熔断）、8 套矢量卡通形象 + 归属成员外圈色环 + 运行设备角标定制、零 LLM Token 消耗轮询脚本 `/agent/worker.ps1` 与 `/agent/worker.sh`（基于 `/api/agent-tasks/poll` 长轮询，仅在有待办或 `@提及` 时唤醒本地 Agent），以及一键接入脚本的内网 `NO_PROXY` 诊断与空 `write_folders` 防坑提示；
  - **短链直传、Webhook 自动化、受控构建校验与 WAL 压缩**：支持 HMAC 签名的 `/s/ticket` 一次性上传与临时下载短链（局域网/Tailscale 下自动路由至 Direct-to-Worker 直传，跳过主节点中转）；管理员可配置按目录前缀与事件过滤的 Webhook（`X-LFH-Signature-256` 签名、`1s/4s/12s` 退避重试与在线测试投递）；挂载目录可配置白名单预设校验命令（无 Shell 注入、单飞合流、末尾 `128KB` 捕获与 `.lfh-logs/` 落盘）；底层元数据采用 `files.wal` 顺序日志与去抖原子快照压缩。
- **分布式算力插件系统与插件市场（Distributed Compute Plugins）**：
  - 支持将算力插件按节点安装与启停（内置 **OCR 图文识别**、**智能文件整理**、**PDF 转 Word (.docx)**，同时支持自定义外部命令行/Sidecar 插件标准清单 `plugin.json`）；
  - 各节点通过心跳实时汇总能力至主节点：**只要局域网内任意一台在线节点安装了某插件，全网所有终端均可触发使用**；主节点调度器自动按数据本地性、空闲并发槽位与 CPU 负载路由到最优节点计算并回传产物至 `插件产物/` 目录。
- **断点续传、传输队列与文件完整性巡检（Transfer Queue & Integrity Scrubbing）**：
  - 大文件自动启用 4 MB 并发分片上传（`/api/uploads/init` -> `chunk` -> `complete`），支持**同指纹断点续传**与**秒传去重**，页面右下角/底部提供可折叠**「传输队列」悬浮抽屉**（支持上传/下载队列、并发控制、实时速度与剩余时间、暂停/继续/重试/取消）；
  - 上传写入期流式计算并持久化整文件 SHA-256，后台定期与管理员按需执行 **Bit-Rot 静默损坏巡检**，发现异常自动标记并计入存储读错误告警；
  - **安全机制加固**：节点间 HMAC-SHA256 通信增加 `X-LFH-Nonce` 随机防重放保护，登录接口提供基于 IP 滑动窗口的防爆破限流与持久化**安全审计日志**。
- **本机零拷贝存储与目录虚拟挂载（Zero-Copy & Mounts）**：
  - 支持主/从节点管理员直接通过本机绝对路径导入文件或文件夹：**同盘分区自动创建操作系统硬链接（`os.Link`）**（0 额外磁盘空间，且原文件重命名/移动/删除后 LFH 仍 100% 完整可读），**跨盘自动转为只读外链引用**（后台自动检测源路径有效性，失效时返回提示而不误删文件）；
  - 支持将主机已有海量目录直接**挂载为只读共享库**（如 `D:\电影`、`/mnt/media`），不复制任何实体文件、不占用上传配额、后台每 15 秒增量同步索引，卸载挂载时严格保留原始文件。

- **异构局域网自动组网与网页端可视化探测接入（Entry / Worker）**：
  - 支持 UDP 局域网广播发现（默认 `53319/udp`），并可在管理员面板「设置 → 集群与入口」**主动扫描同网段局域网节点**或**手动输入对方 IP:端口探测**；
  - 无需命令行即可直接在网页端输入对方管理员账号或密钥**一键纳管远端节点**（或将本机主动接入其他主入口），实现多主机闲置空间实时并池、单一主入口统一访问；
  - **节点在线状态与在线文件联动**：顶部状态栏、左侧节点列表与集群管理表实时展示节点在线状态；**节点在线时汇总展示该节点文件，节点离线后自动隐藏其上的文件、上线后自动恢复显示**。
  - **主节点无损切换与节点退出控制**：支持一键将任意在线从节点设为新**主节点**（自动迁移账号权限并保留各节点文件），支持在主节点将成员**移出集群 / 删除离线节点**、**解散集群**，以及直接访问从节点 `/worker` 控制台自行退出集群。
- **超级剪贴板与新建 TXT 文本文件**：
  - 支持各部署节点本机手动开关（`-clipboard` 或 Web / 控制台切换）**超级剪贴板**，并由主节点在网页端集中控制各分节点是否开启；开启后自动监听本机复制的文本与图片，按时间戳自动命名并归档上传至 `超级剪贴板/YYYY-MM` 目录，跨设备秒级同步；
  - 工具栏支持一键**新建 `.txt` 文件**并在网页中直接编写内容即时入库。
- **拖拽上传与目录保留**：支持整文件夹拖拽直传与压缩包直接入库，实时显示传输进度，并在侧栏提供上传后缀白名单 / 黑名单策略。
- **Apple Finder / iPadOS 风格文件视图、真实封面缩略图与实时同步**：
  - 默认提供无边框 **Apple 图标视图**（支持目录聚合分层与面包屑导航、`小 / 中 / 大` 图标缩放与一键切换详细「列表视图」），采用中间省略命名保留首尾关键信息与拓展名；
  - 集成离线 `pdfjs-dist` + Range 按需渲染 **PDF 第 1 页真实白纸封面**、文本/代码/Office 首行微缩排版纸张预览（`?snippet=1`），以及纯 Go 标准库磁盘缓存图片缩略图接口（`GET /api/files/{id}/thumb`，支持 `ETag` 与透明通道保护）；
  - 桌面端支持苹果蓝底反白选中、悬停毛玻璃快捷操作胶囊、`Space` 空格 QuickLook 预览与右键毛玻璃上下文菜单；配合 SSE 事件总线，全集群新增/编辑/删除实时无感刷新。
- **在线预览、在线文本编辑与完整版本控制溯源**：
  - 支持常见图片、音视频、文本/Markdown、文档及压缩包在线浏览、搜索与解压；
  - **文本在线编辑**：支持对已上传的 `.txt`、`.json`、`.md`、`.yaml`、`.csv`、`.log`、`.ini`、`.conf`、`.sql` 及常见代码文件直接在浏览器中在线编辑（内置行号、JSON 语法校验与一键格式化/压缩、Markdown 实时分栏预览），保存后自动生成新版本；
  - **全量版本信息控制**：详细展示每个版本的**更新方式**（`初始上传`、`上传覆盖更新`、`在线编辑更新`、`手动上传新版本`、`历史版本回滚`）、**更新用户**、**更新设备**（设备名称、指纹与 IP）及**更新时间**，支持任意历史版本独立下载与一键回滚。
- **精细权限控制与角色治理**：
  - 首次初始化自动生成全局随机管理员账号（`lfh_xxxxxx` 与高强度随机密码），并写入数据目录 `initial-admin.txt`，避免多机部署产生默认弱口令冲突。
  - 管理员可在「用户管理」中随时为正常账号**授予或取消管理员权限**（内置防误删最后一名管理员与防自我降权保护）。
  - 支持**指定用户可见与下载**：上传时或上传后均可限定仅指定系统用户可下载，未授权用户在列表、统计图表、实时推送与下载接口中完全不可见；仅**管理员与原上传者**可删除文件或调整权限，其余被授权用户只读下载。
  - 普通成员仅展示外观主题与个人账号面板；节点存储磁盘选择、路径切换（含已有文件一键跨盘迁移）及真实存储路径对普通用户严格隐藏，仅限管理员管理。
- **深浅双主题与全端适配**：内置精致深/浅色模式切换，界面针对桌面端、平板及移动端重排优化。
- **GitHub 仓库版本检测与不停机升级**：管理面板可自动检查仓库新版本，支持后台下载校验并在热交接后完成不停机平滑更新。

<table>
  <tr>
    <td width="50%" align="center">
      <img src="./assets/readme/cluster-mesh.jpg" alt="分布式多节点统一存储池拓扑" />
      <br />
      <sub><b>多机分布式存储池</b> · Windows / Linux / macOS 与手机共享单一入口</sub>
    </td>
    <td width="50%" align="center">
      <img src="./assets/readme/compute-sync.jpg" alt="零拷贝存储分层与分布式算力调度" />
      <br />
      <sub><b>零拷贝分层 & 算力调度</b> · 同盘硬链、断点续传与全集群插件协同</sub>
    </td>
  </tr>
</table>

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
docker build -t lfh:V0.11.21.7 .

docker run -d \
  --name lfh \
  --restart unless-stopped \
  -p 8080:8080 \
  -p 53319:53319/udp \
  -v $(pwd)/lfh-data:/data \
  lfh:V0.11.21.7 -data /data -port 8080 -expose-ip 192.168.1.100
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
├── assets/                     # Logo 矢量/透明包与 README 视觉插图
├── frontend/                   # React 19 + TypeScript + Vite + Tailwind v4 前端源码（参考 shadcn-admin 风格）
├── web/                        # 预构建前端产物（内嵌至 Go 单文件：index.html / mobile.html / assets）
├── Dockerfile                  # 多阶段生产容器镜像构建文件
├── docker-compose.yml          # 容器编排示例配置
├── cluster-config.example.json # 节点配置模板（已过滤敏感配置）
└── LICENSE                     # MIT 开源许可证
```

欢迎直接 Fork 本项目、定制企业内网认证或扩展更多文件格式处理器。本地运行完整测试：

```bash
go test -count=1 ./...
```

## 反馈与交流

欢迎任何部署反馈、改进建议或功能点子：

- **自由留言与讨论区**：前往 [GitHub Discussions](https://github.com/sxu99579-bit/LFH/discussions) 发帖交流、晒出你的局域网部署方案或提问答疑。
- **Bug 反馈与功能建议**：前往 [GitHub Issues](https://github.com/sxu99579-bit/LFH/issues) 选择对应模板提交，附带「设置 -> 系统日志」导出日志能帮我们更快定位问题。

## 开源协议

本项目基于 [**MIT License**](./LICENSE) 开源发布——你可以自由地使用、修改、分发本软件，或将其用于个人及商业项目的二次开发，只需保留原始版权及许可声明。

## 开发致谢

- 感谢 [**satnaing/shadcn-admin**](https://github.com/satnaing/shadcn-admin)（MIT License）及 [**shadcn/ui**](https://ui.shadcn.com/) 提供的优秀开源后台 UI 架构与设计规范，本项目的桌面端与移动端工作台界面灵感及设计令牌均致敬并参考了该开源项目。
- 感谢 **Google Antigravity** 与 **Gemini** 在本项目架构设计、分布式组网方案演进、界面细节打磨及工程自动化构建过程中提供的重要支持与协助。
