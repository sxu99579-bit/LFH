package com.quailnest.client;

import android.annotation.SuppressLint;
import android.app.Activity;
import android.app.AlertDialog;
import android.app.DownloadManager;
import android.content.ActivityNotFoundException;
import android.content.ClipData;
import android.content.ContentResolver;
import android.content.ContentValues;
import android.content.Intent;
import android.content.pm.PackageManager;
import android.graphics.Insets;
import android.net.Uri;
import android.os.Build;
import android.os.Bundle;
import android.os.Environment;
import android.os.Handler;
import android.os.Looper;
import android.provider.MediaStore;
import android.util.Base64;
import android.view.MotionEvent;
import android.view.View;
import android.view.ViewConfiguration;
import android.view.ViewGroup;
import android.view.WindowInsets;
import android.webkit.CookieManager;
import android.webkit.JavascriptInterface;
import android.webkit.URLUtil;
import android.webkit.WebChromeClient;
import android.webkit.WebResourceError;
import android.webkit.WebResourceRequest;
import android.webkit.WebSettings;
import android.webkit.WebView;
import android.webkit.WebViewClient;
import android.widget.Button;
import android.widget.FrameLayout;
import android.widget.LinearLayout;
import android.widget.TextView;
import android.widget.Toast;

import org.json.JSONArray;
import org.json.JSONObject;

import java.io.File;
import java.io.FileOutputStream;
import java.io.OutputStream;
import java.net.URI;
import java.util.ArrayList;
import java.util.Collections;
import java.util.List;
import java.util.concurrent.ExecutorService;
import java.util.concurrent.Executors;
import java.util.concurrent.ScheduledExecutorService;
import java.util.concurrent.TimeUnit;
import java.util.concurrent.atomic.AtomicBoolean;
import java.util.concurrent.atomic.AtomicInteger;

public class WorkspaceActivity extends Activity {
    private static final int REQ_FILE_CHOOSER = 1001;

    private ServerConfigStore store;
    private volatile ServerConfigStore.ServerProfile activeProfile;
    private WebView webView;
    private FrameLayout rootLayout;
    private LinearLayout switchingBanner;
    private TextView tvSwitchingStatus;
    private FrameLayout offlineOverlay;
    private TextView tvOfflineReason;
    private FrameLayout fullscreenContainer;
    private LinearLayout floatingBtn;

    private ValueCallbackBridge fileChooserBridge;
    private View customView;
    private WebChromeClient.CustomViewCallback customViewCallback;
    private ExecutorService ioExecutor;
    private ScheduledExecutorService topologyScheduler;
    private Handler mainHandler;

    private final AtomicBoolean failoverInFlight = new AtomicBoolean(false);
    private final AtomicBoolean failoverCancel = new AtomicBoolean(false);
    private final AtomicBoolean destroyed = new AtomicBoolean(false);
    private final AtomicInteger consecutiveHealthFails = new AtomicInteger(0);
    private volatile long lastSwitchAtMs = 0L;

    private boolean mainFrameFailed = false;
    private long lastBackPressedMs = 0L;

    private static class ValueCallbackBridge {
        android.webkit.ValueCallback<Uri[]> callback;
    }

    @SuppressLint({"SetJavaScriptEnabled", "ClickableViewAccessibility"})
    @Override
    protected void onCreate(Bundle savedInstanceState) {
        super.onCreate(savedInstanceState);
        store = new ServerConfigStore(this);
        activeProfile = store.loadCurrent();
        if (activeProfile == null) {
            launchSetup(true, null, null);
            return;
        }

        ioExecutor = Executors.newCachedThreadPool();
        mainHandler = new Handler(Looper.getMainLooper());
        fileChooserBridge = new ValueCallbackBridge();
        setContentView(R.layout.activity_workspace);

        rootLayout = findViewById(R.id.root_workspace);
        webView = findViewById(R.id.webview);
        switchingBanner = findViewById(R.id.switching_banner);
        tvSwitchingStatus = findViewById(R.id.tv_switching_status);
        offlineOverlay = findViewById(R.id.offline_overlay);
        tvOfflineReason = findViewById(R.id.tv_offline_reason);
        fullscreenContainer = findViewById(R.id.fullscreen_container);
        floatingBtn = findViewById(R.id.btn_floating_server);

        Button btnAutoReconnect = findViewById(R.id.btn_offline_auto_reconnect);
        Button btnRetry = findViewById(R.id.btn_offline_retry);
        Button btnEdit = findViewById(R.id.btn_offline_edit);
        if (btnAutoReconnect != null) {
            btnAutoReconnect.setOnClickListener(v -> triggerFourLevelFailover("正在主动搜寻多网卡 / 备援节点 / 局域网可用入口..."));
        }
        btnRetry.setOnClickListener(v -> reloadWorkspace());
        btnEdit.setOnClickListener(v -> launchSetup(true, null, null));

        applySafeInsets();
        configureWebView();
        setupDraggableFloatingButton();

        webView.loadUrl(activeProfile.entryUrl(store.isMobileView()));
        startTopologyWatcher();
    }

    private void startTopologyWatcher() {
        refreshServerAndTopologyOnce();
        topologyScheduler = Executors.newSingleThreadScheduledExecutor();
        topologyScheduler.scheduleWithFixedDelay(this::syncTopologyAndHealth, 10, 10, TimeUnit.SECONDS);
    }

    private void applySafeInsets() {
        rootLayout.setOnApplyWindowInsetsListener((v, insets) -> {
            if (Build.VERSION.SDK_INT >= Build.VERSION_CODES.R) {
                Insets bars = insets.getInsets(WindowInsets.Type.systemBars() | WindowInsets.Type.ime());
                v.setPadding(bars.left, bars.top, bars.right, bars.bottom);
            } else {
                @SuppressWarnings("deprecation")
                int top = insets.getSystemWindowInsetTop();
                @SuppressWarnings("deprecation")
                int bottom = insets.getSystemWindowInsetBottom();
                @SuppressWarnings("deprecation")
                int left = insets.getSystemWindowInsetLeft();
                @SuppressWarnings("deprecation")
                int right = insets.getSystemWindowInsetRight();
                v.setPadding(left, top, right, bottom);
            }
            return insets;
        });
    }

    @SuppressLint("SetJavaScriptEnabled")
    private void configureWebView() {
        WebSettings ws = webView.getSettings();
        ws.setJavaScriptEnabled(true);
        ws.setDomStorageEnabled(true);
        ws.setDatabaseEnabled(true);
        ws.setAllowFileAccess(true);
        ws.setAllowContentAccess(true);
        ws.setMixedContentMode(WebSettings.MIXED_CONTENT_ALWAYS_ALLOW);
        ws.setMediaPlaybackRequiresUserGesture(false);
        ws.setUseWideViewPort(true);
        ws.setLoadWithOverviewMode(true);
        ws.setSupportZoom(true);
        ws.setBuiltInZoomControls(true);
        ws.setDisplayZoomControls(false);

        CookieManager cm = CookieManager.getInstance();
        cm.setAcceptCookie(true);
        cm.setAcceptThirdPartyCookies(webView, true);

        webView.addJavascriptInterface(new AndroidBridge(), "QuailNestAndroid");

        webView.setWebViewClient(new WebViewClient() {
            @Override
            public boolean shouldOverrideUrlLoading(WebView view, WebResourceRequest request) {
                if (request == null || request.getUrl() == null) return false;
                return handleUriNavigation(request.getUrl());
            }

            @Override
            public void onPageStarted(WebView view, String url, android.graphics.Bitmap favicon) {
                super.onPageStarted(view, url, favicon);
                mainFrameFailed = false;
                offlineOverlay.setVisibility(View.GONE);
            }

            @Override
            public void onPageFinished(WebView view, String url) {
                super.onPageFinished(view, url);
                CookieManager.getInstance().flush();
                if (!mainFrameFailed) {
                    offlineOverlay.setVisibility(View.GONE);
                }
            }

            @Override
            public void onReceivedError(WebView view, WebResourceRequest request, WebResourceError error) {
                super.onReceivedError(view, request, error);
                if (request != null && request.isForMainFrame()) {
                    mainFrameFailed = true;
                    String desc = error != null && error.getDescription() != null
                            ? error.getDescription().toString()
                            : "网络不可达或服务端已离线";
                    if (store.isAutoConnect()) {
                        triggerFourLevelFailover("主入口访问中断（" + desc + "），正在自动切换可用地址...");
                    } else {
                        ServerConfigStore.ServerProfile cur = activeProfile;
                        showOfflineOverlay("目标地址：" + (cur != null ? cur.baseUrl() : "") + "\n原因：" + desc);
                    }
                }
            }
        });

        webView.setWebChromeClient(new WebChromeClient() {
            @Override
            public boolean onShowFileChooser(
                    WebView webView,
                    android.webkit.ValueCallback<Uri[]> filePathCallback,
                    FileChooserParams fileChooserParams
            ) {
                if (fileChooserBridge.callback != null) {
                    fileChooserBridge.callback.onReceiveValue(null);
                    fileChooserBridge.callback = null;
                }
                fileChooserBridge.callback = filePathCallback;

                Intent intent = new Intent(Intent.ACTION_GET_CONTENT);
                intent.addCategory(Intent.CATEGORY_OPENABLE);
                intent.setType("*/*");
                intent.putExtra(Intent.EXTRA_ALLOW_MULTIPLE, true);

                if (fileChooserParams != null && fileChooserParams.getAcceptTypes() != null) {
                    List<String> validMimes = new ArrayList<>();
                    for (String type : fileChooserParams.getAcceptTypes()) {
                        if (type != null) {
                            String trimmed = type.trim();
                            if (!trimmed.isEmpty() && trimmed.contains("/") && !"*/*".equals(trimmed)) {
                                validMimes.add(trimmed);
                            }
                        }
                    }
                    if (!validMimes.isEmpty()) {
                        intent.putExtra(Intent.EXTRA_MIME_TYPES, validMimes.toArray(new String[0]));
                    }
                }

                try {
                    startActivityForResult(Intent.createChooser(intent, "选择上传文件"), REQ_FILE_CHOOSER);
                    return true;
                } catch (ActivityNotFoundException e) {
                    if (fileChooserBridge.callback != null) {
                        fileChooserBridge.callback.onReceiveValue(null);
                        fileChooserBridge.callback = null;
                    }
                    Toast.makeText(WorkspaceActivity.this, "未找到可用的文件管理器", Toast.LENGTH_SHORT).show();
                    return false;
                }
            }

            @Override
            public void onShowCustomView(View view, CustomViewCallback callback) {
                if (customView != null) {
                    callback.onCustomViewHidden();
                    return;
                }
                customView = view;
                customViewCallback = callback;
                fullscreenContainer.removeAllViews();
                fullscreenContainer.addView(
                        view,
                        new FrameLayout.LayoutParams(
                                ViewGroup.LayoutParams.MATCH_PARENT,
                                ViewGroup.LayoutParams.MATCH_PARENT
                        )
                );
                fullscreenContainer.setVisibility(View.VISIBLE);
                floatingBtn.setVisibility(View.GONE);
            }

            @Override
            public void onHideCustomView() {
                hideCustomFullscreen();
            }
        });

        webView.setDownloadListener((url, userAgent, contentDisposition, mimetype, contentLength) -> {
            if (url == null || url.trim().isEmpty()) return;
            String fileName = URLUtil.guessFileName(url, contentDisposition, mimetype);
            if (url.startsWith("blob:")) {
                bridgeBlobDownload(url, fileName, mimetype);
            } else {
                enqueueSystemDownload(url, fileName, userAgent, mimetype);
            }
        });
    }

    private boolean isKnownClusterHost(String host) {
        if (host == null || host.trim().isEmpty()) return false;
        ServerConfigStore.ServerProfile cur = activeProfile;
        if (cur != null && host.equalsIgnoreCase(cur.host)) return true;
        ServerConfigStore.SavedTopology topo = store.loadTopology();
        if (topo != null) {
            for (String u : topo.getAllCandidateUrls("")) {
                try {
                    String h = URI.create(u).getHost();
                    if (h != null && h.equalsIgnoreCase(host)) return true;
                } catch (Exception ignored) {
                }
            }
        }
        return false;
    }

    private boolean handleUriNavigation(Uri uri) {
        String scheme = uri.getScheme();
        if (scheme == null) return false;
        if ("http".equalsIgnoreCase(scheme) || "https".equalsIgnoreCase(scheme)) {
            String host = uri.getHost();
            if (isKnownClusterHost(host)) {
                return false;
            }
            try {
                startActivity(new Intent(Intent.ACTION_VIEW, uri));
            } catch (Exception ignored) {
            }
            return true;
        }
        try {
            startActivity(new Intent(Intent.ACTION_VIEW, uri));
        } catch (Exception ignored) {
        }
        return true;
    }

    @SuppressLint("ClickableViewAccessibility")
    private void setupDraggableFloatingButton() {
        final float touchSlop = ViewConfiguration.get(this).getScaledTouchSlop();
        final float[] downRaw = new float[2];
        final float[] startTrans = new float[2];
        final boolean[] dragging = new boolean[1];

        floatingBtn.setOnTouchListener((v, event) -> {
            switch (event.getActionMasked()) {
                case MotionEvent.ACTION_DOWN:
                    downRaw[0] = event.getRawX();
                    downRaw[1] = event.getRawY();
                    startTrans[0] = v.getTranslationX();
                    startTrans[1] = v.getTranslationY();
                    dragging[0] = false;
                    return true;
                case MotionEvent.ACTION_MOVE:
                    float dx = event.getRawX() - downRaw[0];
                    float dy = event.getRawY() - downRaw[1];
                    if (!dragging[0] && Math.hypot(dx, dy) > touchSlop) {
                        dragging[0] = true;
                    }
                    if (dragging[0]) {
                        float nextX = startTrans[0] + dx;
                        float nextY = startTrans[1] + dy;
                        float minX = -(v.getLeft());
                        float maxX = rootLayout.getWidth() - v.getRight();
                        float minY = -(v.getTop());
                        float maxY = rootLayout.getHeight() - v.getBottom();
                        v.setTranslationX(Math.max(minX, Math.min(maxX, nextX)));
                        v.setTranslationY(Math.max(minY, Math.min(maxY, nextY)));
                    }
                    return true;
                case MotionEvent.ACTION_UP:
                    if (!dragging[0]) {
                        showServerQuickMenu();
                    }
                    return true;
                default:
                    return false;
            }
        });
    }

    private void showServerQuickMenu() {
        ServerConfigStore.ServerProfile cur = activeProfile;
        if (cur == null) {
            launchSetup(true, null, null);
            return;
        }
        ServerConfigStore.SavedTopology topo = store.loadTopology();
        String topoSummary = cur.baseUrl();
        if (topo != null && !topo.nodes.isEmpty()) {
            topoSummary += " · 已同步 " + topo.nodes.size() + " 节点";
        }
        final boolean isMobile = store.isMobileView();
        String[] options = new String[]{
                "扫描局域网服务器并切换",
                "手动修改 IP 与端口 / 切换历史",
                "自动搜寻可用节点（故障切换重试）",
                "检查客户端更新（当前 " + ClientUpdater.CLIENT_VERSION + "）",
                "刷新当前页面",
                isMobile ? "切换为桌面版视图" : "切换为移动版视图",
                "清除缓存并重新加载"
        };
        new AlertDialog.Builder(this)
                .setTitle("当前服务：" + cur.displayName() + "\n" + topoSummary)
                .setItems(options, (dialog, which) -> {
                    if (which == 0) {
                        showQuickLanSwitchDialog();
                    } else if (which == 1) {
                        launchSetup(true, null, null);
                    } else if (which == 2) {
                        triggerFourLevelFailover("正在检索主服务器多网卡、备援节点及局域网...");
                    } else if (which == 3) {
                        ClientUpdater.startInteractiveUpdateCheck(this, cur.baseUrl(), null);
                    } else if (which == 4) {
                        reloadWorkspace();
                    } else if (which == 5) {
                        boolean nextMobile = !isMobile;
                        store.setMobileView(nextMobile);
                        webView.loadUrl(cur.entryUrl(nextMobile));
                    } else if (which == 6) {
                        webView.clearCache(true);
                        reloadWorkspace();
                    }
                })
                .setNegativeButton("关闭", null)
                .show();
    }

    private void showQuickLanSwitchDialog() {
        showSwitchingBanner("正在扫描局域网可用 QuailNest 集群...");
        ioExecutor.execute(() -> {
            final List<LanScanner.DiscoveredCluster> clusters = LanScanner.scanLan(
                    WorkspaceActivity.this, Collections.emptyList(), null, null);
            runOnUiThread(() -> {
                if (destroyed.get() || isFinishing() || isDestroyed()) return;
                hideSwitchingBanner();
                if (clusters.isEmpty()) {
                    Toast.makeText(this, "局域网未扫描到其他在线 8080 节点", Toast.LENGTH_SHORT).show();
                    return;
                }
                String[] labels = new String[clusters.size()];
                for (int i = 0; i < clusters.size(); i++) {
                    LanScanner.DiscoveredCluster c = clusters.get(i);
                    labels[i] = c.displayTitle() + " (" + c.primaryUrl + " · "
                            + c.onlineNodes + "/" + c.totalNodes + " 节点 · " + c.latencyMs + "ms)";
                }
                new AlertDialog.Builder(this)
                        .setTitle("选择要接入的局域网集群")
                        .setItems(labels, (d, idx) -> {
                            LanScanner.DiscoveredCluster picked = clusters.get(idx);
                            ServerConfigStore.ServerProfile p = ServerConfigStore.ServerProfile.fromUrl(
                                    picked.primaryUrl, picked.displayTitle(), picked.version);
                            if (p != null) {
                                switchToResolvedProfile(p, null, "已手动切换至：" + p.baseUrl());
                            }
                        })
                        .setNegativeButton("取消", null)
                        .show();
            });
        });
    }

    private void reloadWorkspace() {
        mainFrameFailed = false;
        offlineOverlay.setVisibility(View.GONE);
        ServerConfigStore.ServerProfile cur = activeProfile;
        if (webView.getUrl() != null && !webView.getUrl().isEmpty()) {
            webView.reload();
        } else if (cur != null) {
            webView.loadUrl(cur.entryUrl(store.isMobileView()));
        }
    }

    private void showOfflineOverlay(String reason) {
        tvOfflineReason.setText(reason);
        offlineOverlay.setVisibility(View.VISIBLE);
    }

    private void showSwitchingBanner(String text) {
        if (switchingBanner != null && tvSwitchingStatus != null) {
            tvSwitchingStatus.setText(text);
            switchingBanner.setVisibility(View.VISIBLE);
        }
    }

    private void hideSwitchingBanner() {
        if (switchingBanner != null) {
            switchingBanner.setVisibility(View.GONE);
        }
    }

    private void refreshServerAndTopologyOnce() {
        ServerConfigStore.ServerProfile cur = activeProfile;
        if (cur == null || ioExecutor == null || destroyed.get()) return;
        final String base = cur.baseUrl();
        ioExecutor.execute(() -> {
            ServerConfigStore.ProbeResult res = ServerConfigStore.probeServer(base, 2500);
            if (res.ok) {
                if (res.version != null && !res.version.isEmpty()) {
                    store.updateCurrentVersion(base, res.version);
                }
                ServerConfigStore.SavedTopology topo = ServerConfigStore.fetchTopology(base, 2500);
                if (topo != null) {
                    store.saveTopology(topo);
                    checkEntryRedirectFromTopology(res, topo, "连接已优化至集群活动主入口");
                }
            }
        });
    }

    private void syncTopologyAndHealth() {
        if (destroyed.get() || failoverInFlight.get()) return;
        ServerConfigStore.ServerProfile cur = activeProfile;
        if (cur == null) return;

        final String curUrl = cur.baseUrl();
        ServerConfigStore.ProbeResult res = ServerConfigStore.probeServer(curUrl, 2200);
        if (res.ok) {
            consecutiveHealthFails.set(0);
            if (res.version != null && !res.version.isEmpty()) {
                store.updateCurrentVersion(curUrl, res.version);
            }
            ServerConfigStore.SavedTopology topo = ServerConfigStore.fetchTopology(curUrl, 2200);
            if (topo != null) {
                store.saveTopology(topo);
                checkEntryRedirectFromTopology(res, topo, "集群入口已更新，正在切至：" + "%s");
            } else if (!res.role.isEmpty() && !"entry".equalsIgnoreCase(res.role) && !res.entryUrls.isEmpty()) {
                ServerConfigStore.ProbeResult entryHit = ServerConfigStore.pickFastestEntry(res.entryUrls, 1800);
                if (entryHit != null && entryHit.ok && !entryHit.url.equals(curUrl)) {
                    ServerConfigStore.ServerProfile next = ServerConfigStore.ServerProfile.fromUrl(
                            entryHit.url, cur.label, entryHit.version);
                    if (next != null) {
                        switchToResolvedProfile(next, null, "当前节点为子节点，已自动切换至主入口：" + next.baseUrl());
                    }
                }
            }
            return;
        }

        int fails = consecutiveHealthFails.incrementAndGet();
        if (fails >= 2 || store.isAutoConnect()) {
            // Confirm once immediately to avoid switching on transient Wi-Fi micro-jitter
            ServerConfigStore.ProbeResult confirm = ServerConfigStore.probeServer(curUrl, 1500);
            if (confirm.ok) {
                consecutiveHealthFails.set(0);
                return;
            }
            if (store.isAutoConnect()) {
                triggerFourLevelFailover("当前连接不可达，正在自动切换至主服务器其他 IP 或备援节点...");
            }
        }
    }

    private void checkEntryRedirectFromTopology(
            ServerConfigStore.ProbeResult probe,
            ServerConfigStore.SavedTopology topo,
            String bannerFormat
    ) {
        if (topo == null || topo.activeEntryUrls == null || topo.activeEntryUrls.isEmpty()) return;
        ServerConfigStore.ServerProfile cur = activeProfile;
        if (cur == null) return;

        String curUrl = cur.baseUrl();
        boolean curIsActiveEntry = "entry".equalsIgnoreCase(probe.role) || topo.activeEntryUrls.contains(curUrl);
        if (curIsActiveEntry) {
            return;
        }
        // Only follow if within reasonable switch interval
        if (System.currentTimeMillis() - lastSwitchAtMs < 3000L) {
            return;
        }
        ServerConfigStore.ProbeResult best = ServerConfigStore.pickFastestEntry(topo.activeEntryUrls, 1800);
        if (best != null && best.ok && !best.url.equals(curUrl)) {
            ServerConfigStore.ServerProfile next = ServerConfigStore.ServerProfile.fromUrl(
                    best.url, cur.label, best.version);
            if (next != null) {
                String msg = bannerFormat.contains("%s")
                        ? String.format(bannerFormat, next.baseUrl())
                        : bannerFormat;
                switchToResolvedProfile(next, topo, msg);
            }
        }
    }

    private void triggerFourLevelFailover(final String bannerHint) {
        if (destroyed.get() || isFinishing() || isDestroyed()) return;
        if (!failoverInFlight.compareAndSet(false, true)) return;

        final ServerConfigStore.ServerProfile snapshot = activeProfile;
        runOnUiThread(() -> {
            if (!destroyed.get()) {
                showSwitchingBanner(bannerHint);
            }
        });

        ioExecutor.execute(() -> {
            try {
                ServerConfigStore.FailoverResolution resolved = store.fourLevelResolve(
                        WorkspaceActivity.this, snapshot, failoverCancel);
                if (destroyed.get()) return;

                if (resolved != null && resolved.profile != null) {
                    consecutiveHealthFails.set(0);
                    switchToResolvedProfile(
                            resolved.profile,
                            resolved.topology,
                            resolved.stageLabel + "（" + resolved.profile.baseUrl() + "）"
                    );
                } else {
                    runOnUiThread(() -> {
                        if (destroyed.get()) return;
                        hideSwitchingBanner();
                        String target = snapshot != null ? snapshot.baseUrl() : "";
                        showOfflineOverlay(
                                "目标地址：" + target
                                        + "\n已依次尝试重连主节点、主服务器其他网卡 IP、已保存备援节点及局域网 8080 扫描，均未发现可接入的集群节点。"
                        );
                    });
                }
            } finally {
                failoverInFlight.set(false);
            }
        });
    }

    private void switchToResolvedProfile(
            final ServerConfigStore.ServerProfile nextProfile,
            final ServerConfigStore.SavedTopology nextTopo,
            final String bannerNotice
    ) {
        if (nextProfile == null || destroyed.get()) return;
        if (nextTopo != null) {
            store.saveTopology(nextTopo);
        }
        store.saveAndSelect(nextProfile);
        activeProfile = nextProfile;
        lastSwitchAtMs = System.currentTimeMillis();

        runOnUiThread(() -> {
            if (destroyed.get() || isFinishing() || isDestroyed()) return;
            mainFrameFailed = false;
            offlineOverlay.setVisibility(View.GONE);
            if (bannerNotice != null && !bannerNotice.isEmpty()) {
                showSwitchingBanner(bannerNotice);
                Toast.makeText(WorkspaceActivity.this, bannerNotice, Toast.LENGTH_SHORT).show();
                mainHandler.postDelayed(() -> {
                    if (!failoverInFlight.get()) {
                        hideSwitchingBanner();
                    }
                }, 3600L);
            } else {
                hideSwitchingBanner();
            }
            webView.loadUrl(nextProfile.entryUrl(store.isMobileView()));
        });
    }

    private void bridgeBlobDownload(String blobUrl, String fallbackName, String mimetype) {
        String safeUrlLiteral = org.json.JSONObject.quote(blobUrl);
        String safeNameLiteral = org.json.JSONObject.quote(fallbackName);
        String safeMimeLiteral = org.json.JSONObject.quote(mimetype == null ? "" : mimetype);
        String script =
                "(async function(){"
                        + "try{"
                        + "const res=await fetch(" + safeUrlLiteral + ");"
                        + "const blob=await res.blob();"
                        + "const reader=new FileReader();"
                        + "reader.onloadend=function(){"
                        + "const dataUrl=String(reader.result||'');"
                        + "const idx=dataUrl.indexOf(',');"
                        + "const b64=idx>=0?dataUrl.slice(idx+1):dataUrl;"
                        + "if(window.QuailNestAndroid){window.QuailNestAndroid.saveBase64File(b64," + safeNameLiteral + "," + safeMimeLiteral + ");}"
                        + "};"
                        + "reader.readAsDataURL(blob);"
                        + "}catch(e){}"
                        + "})();";
        webView.evaluateJavascript(script, null);
    }

    private void enqueueSystemDownload(String urlOrPath, String fileName, String userAgent, String mimetype) {
        try {
            ServerConfigStore.ServerProfile cur = activeProfile;
            String resolved = urlOrPath;
            if (urlOrPath.startsWith("/") && cur != null) {
                resolved = cur.baseUrl() + urlOrPath;
            } else if (!urlOrPath.startsWith("http://") && !urlOrPath.startsWith("https://") && cur != null) {
                resolved = URI.create(cur.baseUrl() + "/").resolve(urlOrPath).toString();
            }

            String safeName = sanitizeFileName(fileName);
            DownloadManager dm = (DownloadManager) getSystemService(DOWNLOAD_SERVICE);
            if (dm == null) {
                Toast.makeText(this, "系统下载服务不可用", Toast.LENGTH_SHORT).show();
                return;
            }
            DownloadManager.Request req = new DownloadManager.Request(Uri.parse(resolved));
            String cookie = CookieManager.getInstance().getCookie(resolved);
            if (cookie != null && !cookie.isEmpty()) {
                req.addRequestHeader("Cookie", cookie);
            }
            if (userAgent != null && !userAgent.isEmpty()) {
                req.addRequestHeader("User-Agent", userAgent);
            }
            if (mimetype != null && !mimetype.trim().isEmpty()) {
                req.setMimeType(mimetype);
            }
            req.setTitle(safeName);
            req.setDescription("QuailNest 文件下载");
            req.setNotificationVisibility(DownloadManager.Request.VISIBILITY_VISIBLE_NOTIFY_COMPLETED);

            boolean canPublicDir = Build.VERSION.SDK_INT >= Build.VERSION_CODES.Q
                    || checkSelfPermission(android.Manifest.permission.WRITE_EXTERNAL_STORAGE)
                    == PackageManager.PERMISSION_GRANTED;
            if (canPublicDir) {
                req.setDestinationInExternalPublicDir(Environment.DIRECTORY_DOWNLOADS, "QuailNest/" + safeName);
            } else {
                req.setDestinationInExternalFilesDir(this, Environment.DIRECTORY_DOWNLOADS, safeName);
            }
            dm.enqueue(req);
            runOnUiThread(() -> Toast.makeText(this, "已加入系统下载任务：" + safeName, Toast.LENGTH_SHORT).show());
        } catch (Exception e) {
            runOnUiThread(() -> Toast.makeText(this, "启动下载失败：" + e.getMessage(), Toast.LENGTH_LONG).show());
        }
    }

    private static String sanitizeFileName(String raw) {
        String s = raw == null ? "" : raw.trim();
        if (s.isEmpty()) return "download.bin";
        s = s.replaceAll("[\\\\/:*?\"<>|]", "_");
        return s.isEmpty() ? "download.bin" : s;
    }

    private class AndroidBridge {
        @JavascriptInterface
        public void downloadUrl(String urlOrPath, String fileName) {
            runOnUiThread(() -> enqueueSystemDownload(urlOrPath, fileName, webView.getSettings().getUserAgentString(), ""));
        }

        @JavascriptInterface
        public void saveBase64File(String base64Data, String fileName, String mimeType) {
            if (ioExecutor == null) return;
            ioExecutor.execute(() -> {
                try {
                    byte[] bytes = Base64.decode(base64Data, Base64.DEFAULT);
                    String safeName = sanitizeFileName(fileName);
                    String mime = (mimeType == null || mimeType.trim().isEmpty())
                            ? "application/octet-stream"
                            : mimeType.trim();

                    if (Build.VERSION.SDK_INT >= Build.VERSION_CODES.Q) {
                        ContentValues cv = new ContentValues();
                        cv.put(MediaStore.Downloads.DISPLAY_NAME, safeName);
                        cv.put(MediaStore.Downloads.MIME_TYPE, mime);
                        cv.put(MediaStore.Downloads.RELATIVE_PATH, Environment.DIRECTORY_DOWNLOADS + "/QuailNest");
                        cv.put(MediaStore.Downloads.IS_PENDING, 1);

                        ContentResolver resolver = getContentResolver();
                        Uri uri = resolver.insert(MediaStore.Downloads.EXTERNAL_CONTENT_URI, cv);
                        if (uri == null) {
                            throw new IllegalStateException("无法创建下载目录文件");
                        }
                        try (OutputStream os = resolver.openOutputStream(uri)) {
                            if (os == null) throw new IllegalStateException("无法写入目标文件");
                            os.write(bytes);
                        }
                        cv.clear();
                        cv.put(MediaStore.Downloads.IS_PENDING, 0);
                        resolver.update(uri, cv, null, null);
                        final String savedLabel = Environment.DIRECTORY_DOWNLOADS + "/QuailNest/" + safeName;
                        runOnUiThread(() -> Toast.makeText(
                                WorkspaceActivity.this,
                                "文件已保存：" + savedLabel,
                                Toast.LENGTH_LONG
                        ).show());
                    } else {
                        File dir = Environment.getExternalStoragePublicDirectory(Environment.DIRECTORY_DOWNLOADS);
                        File subDir = new File(dir, "QuailNest");
                        if (!subDir.exists()) {
                            //noinspection ResultOfMethodCallIgnored
                            subDir.mkdirs();
                        }
                        File outFile = new File(subDir, safeName);
                        try (FileOutputStream fos = new FileOutputStream(outFile)) {
                            fos.write(bytes);
                        }
                        final String savedPath = outFile.getAbsolutePath();
                        runOnUiThread(() -> Toast.makeText(
                                WorkspaceActivity.this,
                                "文件已保存：" + savedPath,
                                Toast.LENGTH_LONG
                        ).show());
                    }
                } catch (Exception e) {
                    runOnUiThread(() -> Toast.makeText(
                            WorkspaceActivity.this,
                            "保存文件失败：" + e.getMessage(),
                            Toast.LENGTH_LONG
                    ).show());
                }
            });
        }

        @JavascriptInterface
        public void openServerSettings() {
            runOnUiThread(() -> launchSetup(true, null, null));
        }

        @JavascriptInterface
        public void onServerEvent(final String rawJson) {
            if (rawJson == null || rawJson.trim().isEmpty() || ioExecutor == null || destroyed.get()) {
                return;
            }
            ioExecutor.execute(() -> {
                try {
                    JSONObject ev = new JSONObject(rawJson);
                    String kind = ev.optString("kind", "").trim();
                    String reason = ev.optString("reason", "").trim();
                    JSONObject topoObj = ev.optJSONObject("topology");
                    final ServerConfigStore.SavedTopology nextTopo = ServerConfigStore.SavedTopology.fromJson(topoObj);
                    if (nextTopo != null) {
                        store.saveTopology(nextTopo);
                    }

                    if (!"entry_switched".equals(kind)) {
                        return;
                    }
                    final List<String> candidates = new ArrayList<>();
                    JSONArray eArr = ev.optJSONArray("entry_urls");
                    if (eArr != null) {
                        for (int i = 0; i < eArr.length(); i++) {
                            String u = LanScanner.normalizeBaseUrl(eArr.optString(i, ""));
                            if (!u.isEmpty() && !candidates.contains(u)) candidates.add(u);
                        }
                    }
                    String single = LanScanner.normalizeBaseUrl(ev.optString("entry_url", ""));
                    if (!single.isEmpty() && !candidates.contains(single)) {
                        candidates.add(0, single);
                    }
                    if (candidates.isEmpty()) return;

                    if (failoverInFlight.compareAndSet(false, true)) {
                        try {
                            ServerConfigStore.ProbeResult hit = ServerConfigStore.pickFastestEntry(candidates, 1800);
                            if (hit != null && hit.ok) {
                                ServerConfigStore.ServerProfile cur = activeProfile;
                                String label = cur != null ? cur.label : hit.nodeName;
                                ServerConfigStore.ServerProfile next = ServerConfigStore.ServerProfile.fromUrl(
                                        hit.url, label, hit.version);
                                if (next != null) {
                                    String msg = "primary_restored".equals(reason)
                                            ? "主服务器已恢复，已自动切回主入口：" + next.baseUrl()
                                            : "集群入口已切换至：" + next.baseUrl();
                                    switchToResolvedProfile(next, nextTopo, msg);
                                }
                            }
                        } finally {
                            failoverInFlight.set(false);
                        }
                    }
                } catch (Exception ignored) {
                }
            });
        }
    }

    @Override
    protected void onActivityResult(int requestCode, int resultCode, Intent data) {
        super.onActivityResult(requestCode, resultCode, data);
        if (requestCode == REQ_FILE_CHOOSER && fileChooserBridge != null && fileChooserBridge.callback != null) {
            Uri[] results = null;
            if (resultCode == RESULT_OK && data != null) {
                ClipData clip = data.getClipData();
                if (clip != null && clip.getItemCount() > 0) {
                    results = new Uri[clip.getItemCount()];
                    for (int i = 0; i < clip.getItemCount(); i++) {
                        results[i] = clip.getItemAt(i).getUri();
                    }
                } else if (data.getData() != null) {
                    results = new Uri[]{data.getData()};
                }
            }
            fileChooserBridge.callback.onReceiveValue(results);
            fileChooserBridge.callback = null;
        }
    }

    private void hideCustomFullscreen() {
        if (customView == null) return;
        fullscreenContainer.setVisibility(View.GONE);
        fullscreenContainer.removeAllViews();
        customView = null;
        if (customViewCallback != null) {
            customViewCallback.onCustomViewHidden();
            customViewCallback = null;
        }
        floatingBtn.setVisibility(View.VISIBLE);
    }

    @SuppressWarnings("deprecation")
    @Override
    public void onBackPressed() {
        if (customView != null) {
            hideCustomFullscreen();
            return;
        }
        if (webView != null && webView.canGoBack()) {
            webView.goBack();
            return;
        }
        long now = System.currentTimeMillis();
        if (now - lastBackPressedMs < 2000L) {
            super.onBackPressed();
        } else {
            lastBackPressedMs = now;
            Toast.makeText(this, "再按一次返回键退出 QuailNest", Toast.LENGTH_SHORT).show();
        }
    }

    private void launchSetup(boolean editMode, String failedUrl, String failedReason) {
        Intent intent = new Intent(this, SetupActivity.class);
        intent.putExtra(SetupActivity.EXTRA_EDIT_MODE, editMode);
        if (failedUrl != null) {
            intent.putExtra(SetupActivity.EXTRA_OFFLINE_URL, failedUrl);
        }
        if (failedReason != null) {
            intent.putExtra(SetupActivity.EXTRA_OFFLINE_REASON, failedReason);
        }
        startActivity(intent);
        finish();
    }

    @Override
    protected void onDestroy() {
        destroyed.set(true);
        failoverCancel.set(true);
        if (topologyScheduler != null) {
            topologyScheduler.shutdownNow();
        }
        if (webView != null) {
            webView.destroy();
        }
        if (ioExecutor != null) {
            ioExecutor.shutdownNow();
        }
        super.onDestroy();
    }
}
