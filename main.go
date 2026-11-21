package main

import (
	"context"
	"embed"
	"errors"
	"flag"
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"time"

	"lfh/internal/auth"
	"lfh/internal/cluster"
	"lfh/internal/server"
	"lfh/internal/storage"
	"lfh/internal/updater"
	"lfh/internal/version"
)

//go:embed web/*
var webFS embed.FS

const (
	envUpgradeHandoff = "LFH_UPGRADE_HANDOFF"
	envReadyFile      = "LFH_READY_FILE"
)

type listenerManager struct {
	mu      sync.Mutex
	ln      net.Listener
	srv     *http.Server
	port    string
	handler http.Handler
}

func (lm *listenerManager) serve(httpSrv *http.Server, ln net.Listener) {
	if err := httpSrv.Serve(ln); err != nil && !errors.Is(err, http.ErrServerClosed) && !errors.Is(err, net.ErrClosed) {
		log.Printf("[http] serve stopped: %v", err)
	}
}

func (lm *listenerManager) CurrentPort() string {
	lm.mu.Lock()
	defer lm.mu.Unlock()
	return lm.port
}

func (lm *listenerManager) SwitchPort(newPort string) error {
	newLn, err := net.Listen("tcp", ":"+newPort)
	if err != nil {
		return err
	}
	newSrv := &http.Server{Handler: lm.handler}

	lm.mu.Lock()
	oldSrv := lm.srv
	oldLn := lm.ln
	lm.ln = newLn
	lm.srv = newSrv
	lm.port = newPort
	lm.mu.Unlock()

	go lm.serve(newSrv, newLn)
	if oldSrv != nil {
		go func(s *http.Server) {
			ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
			defer cancel()
			_ = s.Shutdown(ctx)
		}(oldSrv)
	} else if oldLn != nil {
		go func(l net.Listener) {
			time.Sleep(1500 * time.Millisecond)
			_ = l.Close()
		}(oldLn)
	}
	log.Printf("[http] listening on :%s", newPort)
	return nil
}

func (lm *listenerManager) Close() {
	lm.mu.Lock()
	srv := lm.srv
	ln := lm.ln
	lm.srv = nil
	lm.ln = nil
	lm.mu.Unlock()

	if srv != nil {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = srv.Shutdown(ctx)
	} else if ln != nil {
		_ = ln.Close()
	}
}

func listenWithRetry(port string, waitHandoff bool) (net.Listener, error) {
	deadline := time.Now().Add(15 * time.Second)
	for {
		ln, err := net.Listen("tcp", ":"+port)
		if err == nil || !waitHandoff || time.Now().After(deadline) {
			return ln, err
		}
		time.Sleep(25 * time.Millisecond)
	}
}

func filterUpgradeEnv(readyPath string) []string {
	out := make([]string, 0, len(os.Environ())+2)
	for _, kv := range os.Environ() {
		if strings.HasPrefix(kv, envUpgradeHandoff+"=") || strings.HasPrefix(kv, envReadyFile+"=") {
			continue
		}
		out = append(out, kv)
	}
	out = append(out, envUpgradeHandoff+"=1", envReadyFile+"="+readyPath)
	return out
}

func main() {
	showVersion := flag.Bool("version", false, "Print version and exit")
	port := flag.String("port", "8080", "HTTP listen port")
	dataDir := flag.String("data", "./data", "Data directory for users and uploaded files")
	role := flag.String("role", "", "Cluster role: entry or worker")
	quotaStr := flag.String("quota", "", "Reserved storage quota (e.g. 500MB, 10GB, 1.5TB)")
	exposeIP := flag.String("expose-ip", "", "Custom exposed IP or hostname (empty = auto primary LAN IP)")
	secret := flag.String("secret", "", "Shared cluster secret")
	entryHint := flag.String("entry", "", "Primary entry URL hint for worker mode")
	storageDir := flag.String("storage-dir", "", "Custom directory/drive path for storing uploaded files")
	nodeName := flag.String("name", "", "Custom display name for this node")
	configPath := flag.String("config", "", "Seed cluster-config.json path")
	discoveryPort := flag.Int("discovery-port", -1, "UDP discovery port (0 to disable)")
	repoFlag := flag.String("repo", "", "GitHub repository (owner/repo) for auto-update checks")
	initUserFlag := flag.String("init-user", "", "Optional initial admin username when initializing fresh data dir")
	initPassFlag := flag.String("init-pass", "", "Optional initial admin password when initializing fresh data dir")
	flag.Parse()

	if *showVersion {
		fmt.Printf("%s %s\n", version.AppName, version.Version)
		return
	}

	if exe, err := os.Executable(); err == nil {
		updater.CleanupOldBinary(exe)
	}

	var portSet, quotaSet, exposeIPSet, discoverySet, storageDirSet bool
	flag.Visit(func(f *flag.Flag) {
		switch f.Name {
		case "port":
			portSet = true
		case "quota":
			quotaSet = true
		case "expose-ip":
			exposeIPSet = true
		case "discovery-port":
			discoverySet = true
		case "storage-dir":
			storageDirSet = true
		}
	})

	absData, err := filepath.Abs(*dataDir)
	if err != nil {
		log.Fatalf("resolve data dir failed: %v", err)
	}
	if err := os.MkdirAll(absData, 0755); err != nil {
		log.Fatalf("create data dir failed: %v", err)
	}

	authMgr, err := auth.NewManagerWithOptions(absData, auth.InitOptions{
		AdminUser: *initUserFlag,
		AdminPass: *initPassFlag,
	})
	if err != nil {
		log.Fatalf("init auth manager failed: %v", err)
	}

	store, err := storage.NewStore(absData)
	if err != nil {
		log.Fatalf("init file store failed: %v", err)
	}

	var quotaBytes int64
	if quotaSet {
		quotaBytes, err = cluster.ParseQuotaBytes(*quotaStr)
		if err != nil {
			log.Fatalf("invalid -quota %q: %v", *quotaStr, err)
		}
	}

	seedPath := resolveSeedConfig(*configPath)
	opts := cluster.Options{
		ConfigSeedPath:   seedPath,
		Role:             *role,
		NodeName:         *nodeName,
		ClusterSecret:    *secret,
		QuotaBytes:       quotaBytes,
		QuotaSet:         quotaSet,
		ExposeIP:         *exposeIP,
		ExposeIPSet:      exposeIPSet,
		Port:             *port,
		PortSet:          portSet,
		EntryHint:        *entryHint,
		StorageDir:       *storageDir,
		StorageDirSet:    storageDirSet,
		DiscoveryPort:    *discoveryPort,
		DiscoveryPortSet: discoverySet,
	}

	clusterMgr, err := cluster.NewManager(absData, store, opts)
	if err != nil {
		log.Fatalf("init cluster manager failed: %v", err)
	}

	listenPort := clusterMgr.Port()
	selfURL := server.ResolveEntryURL(clusterMgr.ExposeIP(), listenPort)
	clusterMgr.SetSelfURL(selfURL)

	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()
	clusterMgr.Start(ctx)

	updaterMgr, err := updater.NewManager(absData, *repoFlag)
	if err != nil {
		log.Fatalf("init updater manager failed: %v", err)
	}

	srv, err := server.New(authMgr, store, clusterMgr, webFS, listenPort)
	if err != nil {
		log.Fatalf("init server failed: %v", err)
	}
	srv.SetUpdater(updaterMgr)
	clusterMgr.SetOnPeerChange(func(nodeName string, online bool) {
		srv.PublishEvent(server.FileChangeEvent{
			Kind:     "cluster_sync",
			NodeName: nodeName,
		})
	})

	isHandoff := os.Getenv(envUpgradeHandoff) == "1"
	readyEnvPath := os.Getenv(envReadyFile)

	initialLn, err := listenWithRetry(listenPort, isHandoff)
	if err != nil {
		log.Fatalf("listen :%s failed: %v", listenPort, err)
	}

	handler := srv.Handler()
	initialHTTP := &http.Server{Handler: handler}
	lm := &listenerManager{
		ln:      initialLn,
		srv:     initialHTTP,
		port:    listenPort,
		handler: handler,
	}
	srv.SetPortSwitcher(lm.SwitchPort)

	go lm.serve(initialHTTP, initialLn)

	if readyEnvPath != "" {
		_ = os.WriteFile(readyEnvPath, []byte("OK"), 0600)
	}

	gracefulHandoff := func(exePath, backupPath, targetVersion string) error {
		srv.PublishUpgrading(targetVersion)
		time.Sleep(150 * time.Millisecond)

		readyTmp, err := os.CreateTemp("", "lfh-ready-*")
		if err != nil {
			return err
		}
		readyPath := readyTmp.Name()
		_ = readyTmp.Close()
		_ = os.Remove(readyPath)
		defer os.Remove(readyPath)

		cmd := exec.Command(exePath, os.Args[1:]...)
		cmd.Env = filterUpgradeEnv(readyPath)
		cmd.Stdout = os.Stdout
		cmd.Stderr = os.Stderr
		cmd.Stdin = os.Stdin
		if err := cmd.Start(); err != nil {
			return err
		}

		childDone := make(chan error, 1)
		go func() {
			childDone <- cmd.Wait()
		}()

		lm.mu.Lock()
		curSrv := lm.srv
		curPort := lm.port
		lm.srv = nil
		lm.ln = nil
		lm.mu.Unlock()

		drainDone := make(chan struct{})
		go func() {
			defer close(drainDone)
			if curSrv != nil {
				shCtx, shCancel := context.WithTimeout(context.Background(), 30*time.Second)
				defer shCancel()
				_ = curSrv.Shutdown(shCtx)
			}
		}()

		deadline := time.Now().Add(10 * time.Second)
		for {
			if raw, rerr := os.ReadFile(readyPath); rerr == nil && strings.TrimSpace(string(raw)) == "OK" {
				srv.CloseEventStreams()
				go func() {
					<-drainDone
					cancel()
					os.Exit(0)
				}()
				return nil
			}
			select {
			case cerr := <-childDone:
				_ = lm.SwitchPort(curPort)
				return fmt.Errorf("新版本进程提前退出: %v", cerr)
			default:
			}
			if time.Now().After(deadline) {
				if cmd.Process != nil {
					_ = cmd.Process.Kill()
				}
				_ = lm.SwitchPort(curPort)
				return errors.New("等待新版本接管端口超时")
			}
			time.Sleep(25 * time.Millisecond)
		}
	}

	updaterMgr.SetCallbacks(
		gracefulHandoff,
		func(st updater.Status) {
			cp := st
			srv.PublishEvent(server.FileChangeEvent{
				Kind:         "update_available",
				NodeName:     st.LatestVersion,
				UpdateStatus: &cp,
			})
		},
		func(st updater.Status) {
			cp := st
			srv.PublishEvent(server.FileChangeEvent{
				Kind:         "update_progress",
				NodeName:     st.LatestVersion,
				UpdateStatus: &cp,
			})
		},
	)
	updaterMgr.Start(ctx)

	st := clusterMgr.SelfStatus()
	fmt.Printf("%s %s 已启动:\n", version.AppName, version.Version)
	fmt.Printf("  节点角色: %s (%s)\n", st.Role, st.NodeName)
	fmt.Printf("  预留空间: %s\n", formatQuotaText(st.QuotaBytes))
	fmt.Printf("  文件存储位置: %s\n", clusterMgr.StorageDir())
	if st.Role == cluster.RoleEntry {
		fmt.Printf("  唯一对外入口: %s\n", clusterMgr.EntryURL())
		fmt.Printf("  本机访问: http://localhost:%s\n", listenPort)
		if initUser, initPass, created := authMgr.InitialAdminCredentials(); created {
			fmt.Println("  ====================================================")
			fmt.Println("  首次部署已自动生成随机管理员账号（每台主机唯一）:")
			fmt.Printf("    管理员用户名: %s\n", initUser)
			fmt.Printf("    管理员密码:   %s\n", initPass)
			fmt.Printf("    凭据已备份至: %s\n", filepath.Join(absData, "initial-admin.txt"))
			fmt.Println("  ====================================================")
		} else {
			fmt.Printf("  已加载现有账号数据（共 %d 个注册账号）\n", len(authMgr.ListUsers()))
		}
	} else {
		fmt.Printf("  存储节点服务: %s\n", st.HTTPURL)
		fmt.Printf("  主入口地址: %s\n", clusterMgr.EntryURL())
	}
	fmt.Printf("  数据目录: %s\n", absData)

	<-ctx.Done()
	lm.Close()
}

func resolveSeedConfig(explicit string) string {
	if explicit != "" {
		return explicit
	}
	if _, err := os.Stat("cluster-config.json"); err == nil {
		return "cluster-config.json"
	}
	if exe, err := os.Executable(); err == nil {
		candidate := filepath.Join(filepath.Dir(exe), "cluster-config.json")
		if _, err := os.Stat(candidate); err == nil {
			return candidate
		}
	}
	return ""
}

func formatQuotaText(b int64) string {
	if b >= cluster.TB {
		return fmt.Sprintf("%.2f TB", float64(b)/float64(cluster.TB))
	}
	if b >= cluster.GB {
		gb := float64(b) / float64(cluster.GB)
		if gb == float64(int64(gb)) {
			return fmt.Sprintf("%d GB", int64(gb))
		}
		return fmt.Sprintf("%.2f GB", gb)
	}
	return fmt.Sprintf("%.2f MB", float64(b)/float64(cluster.MB))
}
