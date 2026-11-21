<p align="center">
  <img src="./web/logo.png" width="92" alt="LFH Logo" />
</p>

<h1 align="center">LFH — LAN FileHub</h1>

<p align="center">
  Lightweight, zero-dependency distributed LAN file hub · Unified entry URL across pooled host storage · Real-time sync & inline preview
</p>

<p align="center">
  <a href="./README.md">简体中文</a> · <a href="./README.en.md">English</a> · <a href="./LICENSE">MIT License</a> · <code>V0.11.21.7</code>
</p>

---

**LFH (LAN FileHub)** is a self-hosted distributed file hub built for personal workspaces and local teams. Written solely with the Go standard library and embedded frontend assets, it lets multiple Windows, Linux, and macOS machines on the same LAN form a unified storage pool while exposing a single clean entry address to users.

Ships as a single binary with zero database setup required—ready for both Docker containers and native local deployment.

## Key Features

- **Heterogeneous LAN Clustering & Web-Based Discovery / Join (`entry` / `worker`)**:
  - Combines passive UDP broadcast discovery (`53319/udp`) with **active LAN subnet scanning** and **manual IP:port probing** right inside **Settings -> Cluster & Entry**;
  - Join or adopt standalone LFH hosts directly from the Web UI using the peer's admin credentials or cluster secret—zero CLI restart needed, with customizable storage quotas (`500MB`, `10GB`, `1.5TB`) behind one unified entry URL;
  - **Live Node Status & Online-Only File Visibility**: Real-time node health indicators appear in the top bar, sidebar, and cluster table. Files on **online nodes appear normally**, while files stored on **offline nodes are automatically hidden** and seamlessly restored when the node comes back online.
  - **Lossless Primary Node Switching & Node Exit Control**: Promote any online worker node into the cluster's new **Primary/Entry** node in one click (migrating accounts/permissions while preserving files across all hosts), evict online nodes or delete offline entries cleanly, dissolve an entire cluster, or visit `/worker` on any worker node to leave the cluster independently.
- **Instant Drag-and-Drop & Folder Structure**: Drop individual files, entire directory trees, or archives directly into the workspace with real-time progress and configurable extension allow/deny policies.
- **Three Views & Live Auto-Refresh**: Switch cleanly between **List**, Windows Explorer-style **Grid**, and **Chart** views. Powered by Server-Sent Events (SSE), new uploads appear across connected clients automatically without page reloads.
- **Online Preview & Detailed File Properties**: Preview images, audio/video, text/Markdown, common document formats, and inspect/extract archive contents in-browser. Open **Properties** on any file to inspect exact byte size, MIME type, on-demand SHA-256 checksum (up to 256 MB), visibility scope, storage node location, and uploader device fingerprint—plus exact physical disk path for administrators.
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
├── web/                        # Dependency-free frontend (HTML, CSS, JS, logo.png)
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

Special thanks to **Google Antigravity** and **Gemini** for their assistance throughout architecture design, distributed LAN clustering iteration, UI polish, and engineering delivery.
