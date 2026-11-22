<p align="center">
  <picture>
    <source media="(prefers-color-scheme: dark)" srcset="./web/logo-dark.png" />
    <source media="(prefers-color-scheme: light)" srcset="./web/logo-light.png" />
    <img src="./web/logo.png" width="112" alt="LFH Logo" />
  </picture>
</p>

<h1 align="center">LFH — LAN FileHub</h1>

<p align="center">
  Lightweight, zero-dependency distributed LAN file hub · Unified entry URL across pooled host storage · Real-time sync & inline preview
</p>

<p align="center">
  <a href="./README.md">简体中文</a> · <a href="./README.en.md">English</a> · <a href="./LICENSE">MIT License</a> · <code>V0.11.22.6</code>
</p>

---

**LFH (LAN FileHub)** is a self-hosted distributed file hub built for personal workspaces and local teams. Written solely with the Go standard library and embedded frontend assets, it lets multiple Windows, Linux, and macOS machines on the same LAN form a unified storage pool while exposing a single clean entry address to users.

Ships as a single binary with zero database setup required—ready for both Docker containers and native local deployment.

## Key Features

- **Distributed Compute Plugin System & Marketplace**:
  - Install and toggle plugins per cluster node (built-in **OCR Vision**, **Auto Organizer**, and **PDF to Word (.docx)**, plus extensible `plugin.json` external command runners);
  - Heartbeats automatically aggregate node capabilities cluster-wide so any user can trigger plugins hosted anywhere on the LAN, while the scheduler routes jobs by data locality, concurrency slots, and CPU load and saves output artifacts into `插件产物/`.
- **Resumable Chunked Transfers, Queue Drawer & Integrity Scrubbing**:
  - Automatic 4 MB parallel chunked uploads (`/api/uploads/init` -> `chunk` -> `complete`) with fingerprint-based resume and SHA-256 instant deduplication, paired with Range-segmented queued downloads inside a collapsible **Transfer Queue** drawer (pause/resume/retry/cancel, live speed & ETA);
  - Persists streaming SHA-256 checksums on write and runs scheduled/on-demand **Bit-Rot Integrity Scrubbing** to detect silent disk corruption;
  - Hardened with per-IP login brute-force rate limiting, anti-replay HMAC `X-LFH-Nonce` signatures across cluster RPCs, `SameSite=Lax` cookies, and a persistent security **Audit Log**.
- **Zero-Copy Local Import & Virtual Directory Mounts**:
  - Administrators can index existing local files/folders on any online node by host path without duplicating bytes: **same-volume imports create OS hard links (`os.Link`)** (zero extra disk usage, surviving source rename/move/deletion), while **cross-volume imports fall back to read-only external references** with background missing-source detection;
  - Mount large existing host directories (e.g., `D:\Movies`, `/mnt/media`) directly as auto-synced read-only virtual libraries with zero disk duplication, zero upload quota consumption, and non-destructive unmounting.

- **Heterogeneous LAN Clustering & Web-Based Discovery / Join (`entry` / `worker`)**:
  - Combines passive UDP broadcast discovery (`53319/udp`) with **active LAN subnet scanning** and **manual IP:port probing** right inside **Settings -> Cluster & Entry**;
  - Join or adopt standalone LFH hosts directly from the Web UI using the peer's admin credentials or cluster secret—zero CLI restart needed, with customizable storage quotas (`500MB`, `10GB`, `1.5TB`) behind one unified entry URL;
  - **Live Node Status & Online-Only File Visibility**: Real-time node health indicators appear in the top bar, sidebar, and cluster table. Files on **online nodes appear normally**, while files stored on **offline nodes are automatically hidden** and seamlessly restored when the node comes back online.
  - **Lossless Primary Node Switching & Node Exit Control**: Promote any online worker node into the cluster's new **Primary/Entry** node in one click (migrating accounts/permissions while preserving files across all hosts), evict online nodes or delete offline entries cleanly, dissolve an entire cluster, or visit `/worker` on any worker node to leave the cluster independently.
- **Super Clipboard & Instant Create `.txt`**:
  - Opt-in **Super Clipboard** per node (`-clipboard`, local `/worker` toggle, or centralized Primary-to-Worker remote toggle in Cluster Settings): automatically watches copied text/images on enabled machines, auto-names them with timestamps, and uploads into `超级剪贴板/YYYY-MM` for cross-device access;
  - Create `.txt` files directly from the Web toolbar with an inline editor and immediate versioning.
- **Instant Drag-and-Drop & Folder Structure**: Drop individual files, entire directory trees, or archives directly into the workspace with real-time progress and configurable extension allow/deny policies.
- **Three Views & Live Auto-Refresh**: Switch cleanly between **List**, Windows Explorer-style **Grid**, and **Chart** views. Powered by Server-Sent Events (SSE), new uploads appear across connected clients automatically without page reloads.
- **Online Preview, In-Browser Text Editor & Full Version Provenance**:
  - Preview images, audio/video, text/Markdown, common document formats, and inspect/extract archive contents in-browser;
  - **Online Text / JSON / Markdown / Code Editor**: Edit uploaded `.txt`, `.json`, `.md`, `.yaml`, `.csv`, `.log`, `.ini`, `.conf`, `.sql`, and source code files directly in the browser with line numbers, JSON formatting/minifying/validation, and live split Markdown preview—saving creates a new version automatically;
  - **Detailed File Version Control**: Every version records and displays its **update method** (`Initial Upload`, `Upload Overwrite`, `Online Edit`, `Manual Version Upload`, `Historical Version Restore`), **updating user**, **updating device** (device name, fingerprint & IP), and **update timestamp**, with one-click download or rollback for any historical version.
- **Fine-Grained Access Control & Role Governance**:
  - Generates unique random initial admin credentials (`lfh_xxxxxx` + 12-character password) per fresh installation and saves a copy to `initial-admin.txt`, avoiding duplicate default passwords across hosts.
  - Administrators can **grant or revoke admin privileges** for active accounts directly in User Management (with built-in self-modification and last-admin protection).
  - **Designated User Visibility & Owner/Admin Deletion**: Restrict uploaded files to selected system users—unauthorized users never see them in file lists, charts, SSE notifications, or download/preview endpoints. Only **admins and the original uploader** may delete files or update visibility; other authorized recipients have read/download access only.
  - Standard users only see Appearance and Account settings. Storage drive selection, physical directory migration across disks, and server disk paths stay strictly hidden and accessible to administrators only.
- **Light & Dark Themes across All Screens**: Polished light and dark themes with responsive layouts tuned for desktop, tablet, and mobile viewports.
- **Zero-Downtime GitHub Self-Updater**: Checks the configured GitHub repository (`sxu99579-bit/LFH`) for new releases, verifies downloads, and swaps the running binary via graceful handoff.

---

## Quick Deployment

### Option 1: Docker (Recommended)

This repository includes a multi-stage [`Dockerfile`](./Dockerfile) and [`docker-compose.yml`](./docker-compose.yml) with persistent storage mounted at `./lfh-data`:

```bash
# 1. Clone the repository
git clone https://github.com/sxu99579-bit/LFH.git
cd LFH

# 2. Build and start in background
docker compose up -d

# 3. View the auto-generated initial admin username and password
docker compose logs lfh
```

Your generated initial credentials are also written to `./lfh-data/initial-admin.txt` on the host.

> **LAN Networking Notes**:
> - Under Docker bridge mode, add `-expose-ip YOUR_HOST_LAN_IP` (for example `192.168.1.100`) to `command` in `docker-compose.yml` so the UI shows your host's reachable LAN URL rather than the container bridge IP.
> - On Linux hosts using multi-node UDP auto-discovery (`53319/udp`), switching to `network_mode: host` enables zero-config LAN peer discovery.

Manual `docker build` / `docker run` equivalent:

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

### Option 2: Local Binary / Build from Source

Requires Go `1.23+` (or download a prebuilt binary from GitHub Releases).

#### 1. Build & Start Entry Node

```bash
git clone https://github.com/sxu99579-bit/LFH.git
cd LFH

# Build binary
go build -trimpath -ldflags="-s -w" -o lfh .      # Linux / macOS
go build -trimpath -ldflags="-s -w" -o lfh.exe .  # Windows

# Start entry server (default port 8080, data in ./data)
./lfh -data ./data -port 8080
```

Open the LAN URL printed in the terminal (or `http://localhost:8080`) and sign in with the random admin credentials shown on first launch (also saved in `./data/initial-admin.txt`).

#### 2. Join Additional Hosts as Worker Nodes

Copy [`cluster-config.example.json`](./cluster-config.example.json) to `cluster-config.json` on a peer machine, or pass CLI flags directly using the shared cluster secret shown in Settings:

```bash
# Example: contribute 50GB from a second machine to the cluster pool
./lfh -role worker -quota 50GB -secret "your-cluster-secret" -entry "http://192.168.1.100:8080"
```

---

## CLI Flags Reference

| Flag | Default | Description |
| :--- | :--- | :--- |
| `-port` | `8080` | HTTP server listen port |
| `-data` | `./data` | Data directory for users, metadata, uploads, and credentials |
| `-role` | `entry` | Cluster node role: `entry` or `worker` |
| `-quota` | auto | Reserved storage quota on this node (e.g. `500MB`, `10GB`, `1.5TB`) |
| `-storage-dir` | `<data>/uploads` | Custom disk drive or directory path where this node stores uploaded files (also switchable live with migration by admins in Settings) |
| `-expose-ip` | auto-detect | Custom IP or hostname displayed on the entry landing page |
| `-secret` | auto/config | Shared cluster authentication secret |
| `-entry` | empty | Entry node URL hint used by worker nodes |
| `-name` | hostname | Custom node display name |
| `-config` | empty | Path to seed `cluster-config.json` |
| `-discovery-port` | `53319` | UDP broadcast discovery port (`0` to disable) |
| `-repo` | `sxu99579-bit/LFH` | GitHub repository (`owner/repo`) checked for updates |
| `-init-user` / `-init-pass` | random | Optional initial admin username/password for a fresh data directory |
| `-version` | `false` | Print current version and exit |

---

## Architecture & Secondary Development

All backend packages use pure Go stdlib with embedded frontend static assets (`//go:embed web/*`):

```text
.
├── main.go                     # CLI flags, cluster bootstrap & graceful handoff
├── internal/
│   ├── auth/                   # Users, invites, bans & random initial admin generator
│   ├── cluster/                # UDP discovery, peer heartbeats, quotas & reverse proxy
│   ├── preview/                # Text/Markdown/Office/archive inspection helpers
│   ├── server/                 # REST API, SSE event hub, RBAC & file metadata endpoint
│   ├── storage/                # File store, device fingerprinting & query facets
│   ├── updater/                # GitHub Release check, verification & live swap
│   └── version/                # App name, release tag & default repo
├── frontend/                   # React 19 + TypeScript + Vite + Tailwind v4 frontend source (inspired by shadcn-admin)
├── web/                        # Compiled frontend bundle embedded via Go (index.html, mobile.html, assets)
├── Dockerfile                  # Multi-stage Alpine container build
├── docker-compose.yml          # Ready-to-run Compose definition
├── cluster-config.example.json # Sanitized cluster node config template
└── LICENSE                     # MIT License
```

Forks, custom integrations, and secondary development are warmly encouraged. Run the test suite locally with:

```bash
go test -count=1 ./...
```

## License

Released under the [**MIT License**](./LICENSE) — free to use, modify, redistribute, and build commercial or private derivative projects upon, provided the copyright notice and license terms are retained.

## Acknowledgements

- Special thanks to [**satnaing/shadcn-admin**](https://github.com/satnaing/shadcn-admin) (MIT License) and [**shadcn/ui**](https://ui.shadcn.com/) for the open-source admin UI architecture and design system that inspired LFH's desktop and mobile workspaces.
- Thanks to **Google Antigravity** and **Gemini** for assistance throughout architecture design, distributed LAN clustering iteration, UI polish, and engineering delivery.
