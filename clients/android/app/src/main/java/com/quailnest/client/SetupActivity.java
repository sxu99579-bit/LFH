package com.quailnest.client;

import android.app.Activity;
import android.content.Intent;
import android.graphics.Insets;
import android.os.Build;
import android.os.Bundle;
import android.os.Handler;
import android.os.Looper;
import android.view.LayoutInflater;
import android.view.View;
import android.view.WindowInsets;
import android.widget.Button;
import android.widget.EditText;
import android.widget.LinearLayout;
import android.widget.ScrollView;
import android.widget.Switch;
import android.widget.TextView;
import android.widget.Toast;

import java.util.ArrayList;
import java.util.List;
import java.util.concurrent.ExecutorService;
import java.util.concurrent.Executors;
import java.util.concurrent.atomic.AtomicBoolean;

public class SetupActivity extends Activity {
    public static final String EXTRA_EDIT_MODE = "extra_edit_mode";
    public static final String EXTRA_OFFLINE_URL = "extra_offline_url";
    public static final String EXTRA_OFFLINE_REASON = "extra_offline_reason";

    private ServerConfigStore store;
    private ExecutorService executor;
    private Handler mainHandler;
    private AtomicBoolean activeScanCancel;

    private String selectedScheme = "http";
    private Button btnScanLan;
    private TextView tvScanStatus;
    private LinearLayout discoveredContainer;

    private Button btnSchemeHttp;
    private Button btnSchemeHttps;
    private EditText etHost;
    private EditText etPort;
    private EditText etLabel;
    private Switch swAutoConnect;
    private Switch swMobileView;
    private TextView tvProbeStatus;
    private Button btnTest;
    private Button btnSaveConnect;
    private LinearLayout offlineBanner;
    private TextView tvBannerTitle;
    private TextView tvBannerDetail;
    private LinearLayout historyContainer;
    private TextView tvHistoryEmpty;

    private TextView tvClientVersion;
    private TextView tvUpdateStatus;
    private Button btnCheckUpdate;

    @Override
    protected void onCreate(Bundle savedInstanceState) {
        super.onCreate(savedInstanceState);
        store = new ServerConfigStore(this);
        executor = Executors.newCachedThreadPool();
        mainHandler = new Handler(Looper.getMainLooper());

        boolean editMode = getIntent().getBooleanExtra(EXTRA_EDIT_MODE, false);
        String offlineUrl = getIntent().getStringExtra(EXTRA_OFFLINE_URL);
        String offlineReason = getIntent().getStringExtra(EXTRA_OFFLINE_REASON);

        ServerConfigStore.ServerProfile current = store.loadCurrent();
        if (!editMode && offlineUrl == null && store.isAutoConnect() && current != null) {
            openWorkspace();
            return;
        }

        setContentView(R.layout.activity_setup);
        bindViews();
        applySafeInsets();
        hydrateFromConfig(current, offlineUrl, offlineReason);
        renderHistory();

        if (!editMode && offlineUrl == null && current == null) {
            startLanScan(true);
        }
    }

    private void bindViews() {
        btnScanLan = findViewById(R.id.btn_scan_lan);
        tvScanStatus = findViewById(R.id.tv_scan_status);
        discoveredContainer = findViewById(R.id.discovered_container);

        btnSchemeHttp = findViewById(R.id.btn_scheme_http);
        btnSchemeHttps = findViewById(R.id.btn_scheme_https);
        etHost = findViewById(R.id.et_host);
        etPort = findViewById(R.id.et_port);
        etLabel = findViewById(R.id.et_label);
        swAutoConnect = findViewById(R.id.sw_auto_connect);
        swMobileView = findViewById(R.id.sw_mobile_view);
        tvProbeStatus = findViewById(R.id.tv_probe_status);
        btnTest = findViewById(R.id.btn_test);
        btnSaveConnect = findViewById(R.id.btn_save_connect);
        offlineBanner = findViewById(R.id.offline_banner);
        tvBannerTitle = findViewById(R.id.tv_banner_title);
        tvBannerDetail = findViewById(R.id.tv_banner_detail);
        historyContainer = findViewById(R.id.history_container);
        tvHistoryEmpty = findViewById(R.id.tv_history_empty);

        tvClientVersion = findViewById(R.id.tv_client_version);
        tvUpdateStatus = findViewById(R.id.tv_update_status);
        btnCheckUpdate = findViewById(R.id.btn_check_update);

        if (tvClientVersion != null) {
            tvClientVersion.setText("当前客户端版本：" + ClientUpdater.CLIENT_VERSION);
        }
        if (btnScanLan != null) {
            btnScanLan.setOnClickListener(v -> startLanScan(false));
        }
        if (btnCheckUpdate != null) {
            btnCheckUpdate.setOnClickListener(v -> runClientUpdateCheck());
        }

        btnSchemeHttp.setOnClickListener(v -> setScheme("http", true));
        btnSchemeHttps.setOnClickListener(v -> setScheme("https", true));

        etHost.setOnFocusChangeListener((v, hasFocus) -> {
            if (!hasFocus) {
                tryAutoSplitHostField();
            }
        });

        btnTest.setOnClickListener(v -> runTestConnection());
        btnSaveConnect.setOnClickListener(v -> saveAndEnterWorkspace());
    }

    private void applySafeInsets() {
        final ScrollView root = findViewById(R.id.root_setup);
        if (root == null) return;
        root.setOnApplyWindowInsetsListener((v, insets) -> {
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

    private void hydrateFromConfig(
            ServerConfigStore.ServerProfile current,
            String offlineUrl,
            String offlineReason
    ) {
        swAutoConnect.setChecked(store.isAutoConnect());
        swMobileView.setChecked(store.isMobileView());
        if (current != null) {
            setScheme(current.scheme, false);
            etHost.setText(current.host);
            etPort.setText(String.valueOf(current.port));
            etLabel.setText(current.label);
        } else {
            setScheme("http", false);
            etPort.setText("8080");
        }

        if (offlineUrl != null && !offlineUrl.trim().isEmpty()) {
            offlineBanner.setVisibility(View.VISIBLE);
            tvBannerTitle.setText("无法连接服务端：" + offlineUrl);
            tvBannerDetail.setText(
                    (offlineReason == null || offlineReason.trim().isEmpty())
                            ? "请检查局域网连接、防火墙放行规则，或扫描局域网 / 修改 IP 与端口重试"
                            : offlineReason
            );
        } else {
            offlineBanner.setVisibility(View.GONE);
        }
    }

    private void startLanScan(final boolean autoEnterSingle) {
        if (activeScanCancel != null) {
            activeScanCancel.set(true);
        }
        final AtomicBoolean cancel = new AtomicBoolean(false);
        activeScanCancel = cancel;

        if (btnScanLan != null) {
            btnScanLan.setEnabled(false);
            btnScanLan.setText("扫描中...");
        }
        if (tvScanStatus != null) {
            tvScanStatus.setText("正在扫描局域网所有子网 8080 端口与节点 UDP 广播...");
            tvScanStatus.setTextColor(getColor(R.color.status_info_text));
        }
        if (discoveredContainer != null) {
            discoveredContainer.removeAllViews();
        }

        final List<String> hints = new ArrayList<>();
        ServerConfigStore.ParseResult inputParsed = ServerConfigStore.parseSmartInput(
                selectedScheme, etHost.getText().toString(), etPort.getText().toString(), etLabel.getText().toString());
        if (inputParsed.profile != null) {
            hints.add(inputParsed.profile.baseUrl());
        }

        executor.execute(() -> {
            final List<LanScanner.DiscoveredCluster> found = LanScanner.scanLan(SetupActivity.this, hints, null, cancel);
            mainHandler.post(() -> {
                if (isFinishing() || isDestroyed() || cancel.get()) return;
                if (btnScanLan != null) {
                    btnScanLan.setEnabled(true);
                    btnScanLan.setText(R.string.btn_scan_lan);
                }

                if (found.isEmpty()) {
                    if (tvScanStatus != null) {
                        tvScanStatus.setText("未在局域网 8080 端口扫描到在线服务端，请确认设备在同一 Wi-Fi 或在下方手动填写 IP 与端口。");
                        tvScanStatus.setTextColor(getColor(R.color.text_secondary));
                    }
                    return;
                }

                renderDiscoveredClusters(found);
                if (found.size() == 1 && autoEnterSingle) {
                    if (tvScanStatus != null) {
                        tvScanStatus.setText("已自动发现唯一集群「" + found.get(0).displayTitle() + "」，正在同步节点地址表并进入...");
                        tvScanStatus.setTextColor(getColor(R.color.status_ok_text));
                    }
                    connectDiscoveredCluster(found.get(0));
                } else {
                    if (tvScanStatus != null) {
                        tvScanStatus.setText("发现 " + found.size() + " 个 QuailNest 集群（已合并多网卡与子节点），请点击「接入」进入：");
                        tvScanStatus.setTextColor(getColor(R.color.status_ok_text));
                    }
                }
            });
        });
    }

    private void renderDiscoveredClusters(List<LanScanner.DiscoveredCluster> clusters) {
        if (discoveredContainer == null) return;
        discoveredContainer.removeAllViews();
        LayoutInflater inflater = LayoutInflater.from(this);
        for (final LanScanner.DiscoveredCluster c : clusters) {
            View row = inflater.inflate(R.layout.item_discovered_cluster, discoveredContainer, false);
            TextView tvTitle = row.findViewById(R.id.tv_discovered_title);
            TextView tvUrl = row.findViewById(R.id.tv_discovered_url);
            TextView tvMeta = row.findViewById(R.id.tv_discovered_meta);
            Button btnConnect = row.findViewById(R.id.btn_discovered_connect);

            String title = c.displayTitle();
            if (c.version != null && !c.version.isEmpty()) {
                title = title + " (" + c.version + ")";
            }
            tvTitle.setText(title);
            tvUrl.setText(c.primaryUrl);
            tvMeta.setText(
                    ("entry".equalsIgnoreCase(c.role) ? "主入口" : "子节点")
                            + " · 在线 " + c.onlineNodes + "/" + c.totalNodes
                            + " · 可选路径 " + c.urls.size()
                            + " · " + c.latencyMs + " ms"
            );

            View.OnClickListener enterAction = v -> connectDiscoveredCluster(c);
            row.findViewById(R.id.discovered_info_area).setOnClickListener(v -> {
                ServerConfigStore.ServerProfile p = ServerConfigStore.ServerProfile.fromUrl(
                        c.primaryUrl, c.displayTitle(), c.version);
                if (p != null) {
                    setScheme(p.scheme, false);
                    etHost.setText(p.host);
                    etPort.setText(String.valueOf(p.port));
                    etLabel.setText(p.label);
                }
            });
            btnConnect.setOnClickListener(enterAction);
            discoveredContainer.addView(row);
        }
    }

    private void connectDiscoveredCluster(final LanScanner.DiscoveredCluster cluster) {
        if (cluster == null || cluster.primaryUrl == null || cluster.primaryUrl.isEmpty()) return;
        store.setAutoConnect(swAutoConnect.isChecked());
        store.setMobileView(swMobileView.isChecked());

        executor.execute(() -> {
            List<String> candidates = new ArrayList<>();
            candidates.add(cluster.primaryUrl);
            candidates.addAll(cluster.urls);
            ServerConfigStore.ProbeResult best = ServerConfigStore.pickFastestEntry(candidates, 1800);
            String chosenUrl = (best != null && best.ok) ? best.url : cluster.primaryUrl;
            String ver = (best != null && best.ok && !best.version.isEmpty()) ? best.version : cluster.version;

            ServerConfigStore.SavedTopology topo = ServerConfigStore.fetchTopology(chosenUrl, 1800);
            if (topo != null) {
                store.saveTopology(topo);
                if (topo.activeEntryUrls != null && !topo.activeEntryUrls.isEmpty()) {
                    ServerConfigStore.ProbeResult entryHit = ServerConfigStore.pickFastestEntry(topo.activeEntryUrls, 1500);
                    if (entryHit != null && entryHit.ok) {
                        chosenUrl = entryHit.url;
                        if (!entryHit.version.isEmpty()) ver = entryHit.version;
                    }
                }
            }

            final ServerConfigStore.ServerProfile profile = ServerConfigStore.ServerProfile.fromUrl(
                    chosenUrl, cluster.displayTitle(), ver);
            mainHandler.post(() -> {
                if (isFinishing() || isDestroyed()) return;
                if (profile != null) {
                    store.saveAndSelect(profile);
                    openWorkspace();
                } else {
                    Toast.makeText(SetupActivity.this, "解析选中集群地址失败", Toast.LENGTH_SHORT).show();
                }
            });
        });
    }

    private void runClientUpdateCheck() {
        String candidateServer = null;
        ServerConfigStore.ParseResult parsed = ServerConfigStore.parseSmartInput(
                selectedScheme, etHost.getText().toString(), etPort.getText().toString(), etLabel.getText().toString());
        if (parsed.profile != null) {
            candidateServer = parsed.profile.baseUrl();
        } else if (store.loadCurrent() != null) {
            candidateServer = store.loadCurrent().baseUrl();
        } else {
            List<ServerConfigStore.ServerProfile> hist = store.getHistory();
            if (!hist.isEmpty()) {
                candidateServer = hist.get(0).baseUrl();
            }
        }

        ClientUpdater.startInteractiveUpdateCheck(this, candidateServer, (text, isErr) -> {
            if (tvUpdateStatus != null) {
                tvUpdateStatus.setVisibility(View.VISIBLE);
                tvUpdateStatus.setText(text);
                tvUpdateStatus.setTextColor(getColor(isErr ? R.color.status_err_text : R.color.status_info_text));
            }
        });
    }

    private void setScheme(String scheme, boolean adjustDefaultPort) {
        selectedScheme = "https".equalsIgnoreCase(scheme) ? "https" : "http";
        if ("https".equals(selectedScheme)) {
            btnSchemeHttp.setBackgroundResource(R.drawable.bg_btn_outline);
            btnSchemeHttp.setTextColor(getColor(R.color.text_secondary));
            btnSchemeHttps.setBackgroundResource(R.drawable.bg_btn_primary);
            btnSchemeHttps.setTextColor(0xFFFFFFFF);
            if (adjustDefaultPort && "8080".equals(etPort.getText().toString().trim())) {
                etPort.setText("443");
            }
        } else {
            btnSchemeHttp.setBackgroundResource(R.drawable.bg_btn_primary);
            btnSchemeHttp.setTextColor(0xFFFFFFFF);
            btnSchemeHttps.setBackgroundResource(R.drawable.bg_btn_outline);
            btnSchemeHttps.setTextColor(getColor(R.color.text_secondary));
            if (adjustDefaultPort && "443".equals(etPort.getText().toString().trim())) {
                etPort.setText("8080");
            }
        }
    }

    private void tryAutoSplitHostField() {
        String raw = etHost.getText().toString().trim();
        if (!raw.contains("://") && !raw.matches("^[^:\\s]+:\\d+$")) {
            return;
        }
        ServerConfigStore.ParseResult parsed = ServerConfigStore.parseSmartInput(
                selectedScheme, raw, etPort.getText().toString(), etLabel.getText().toString());
        if (parsed.profile != null) {
            setScheme(parsed.profile.scheme, false);
            etHost.setText(parsed.profile.host);
            etPort.setText(String.valueOf(parsed.profile.port));
        }
    }

    private void showProbeStatus(String kind, String text) {
        tvProbeStatus.setVisibility(View.VISIBLE);
        tvProbeStatus.setText(text);
        if ("ok".equals(kind)) {
            tvProbeStatus.setBackgroundResource(R.drawable.bg_probe_ok);
            tvProbeStatus.setTextColor(getColor(R.color.status_ok_text));
        } else if ("err".equals(kind)) {
            tvProbeStatus.setBackgroundResource(R.drawable.bg_banner_error);
            tvProbeStatus.setTextColor(getColor(R.color.status_err_text));
        } else {
            tvProbeStatus.setBackgroundResource(R.drawable.bg_probe_loading);
            tvProbeStatus.setTextColor(getColor(R.color.status_info_text));
        }
    }

    private void runTestConnection() {
        tryAutoSplitHostField();
        ServerConfigStore.ParseResult parsed = ServerConfigStore.parseSmartInput(
                selectedScheme,
                etHost.getText().toString(),
                etPort.getText().toString(),
                etLabel.getText().toString()
        );
        if (parsed.error != null || parsed.profile == null) {
            showProbeStatus("err", parsed.error != null ? parsed.error : "服务器地址无效");
            return;
        }
        final String baseUrl = parsed.profile.baseUrl();
        btnTest.setEnabled(false);
        showProbeStatus("loading", "正在探测 " + baseUrl + "/api/info ...");

        executor.execute(() -> {
            final ServerConfigStore.ProbeResult res = ServerConfigStore.probeServer(baseUrl, 4000);
            if (res.ok) {
                ServerConfigStore.SavedTopology topo = ServerConfigStore.fetchTopology(baseUrl, 2000);
                if (topo != null) store.saveTopology(topo);
            }
            mainHandler.post(() -> {
                if (isFinishing() || isDestroyed()) return;
                btnTest.setEnabled(true);
                if (res.ok) {
                    String ver = res.version.isEmpty() ? "" : (" " + res.version);
                    showProbeStatus(
                            "ok",
                            "连接成功：" + res.appName + ver
                                    + " · 在线节点 " + res.onlineNodes + "/" + res.totalNodes
                                    + " · 延迟 " + res.latencyMs + " ms"
                    );
                } else {
                    showProbeStatus("err", res.error);
                }
            });
        });
    }

    private void saveAndEnterWorkspace() {
        tryAutoSplitHostField();
        ServerConfigStore.ParseResult parsed = ServerConfigStore.parseSmartInput(
                selectedScheme,
                etHost.getText().toString(),
                etPort.getText().toString(),
                etLabel.getText().toString()
        );
        if (parsed.error != null || parsed.profile == null) {
            showProbeStatus("err", parsed.error != null ? parsed.error : "服务器地址无效");
            Toast.makeText(this, parsed.error, Toast.LENGTH_SHORT).show();
            return;
        }
        final ServerConfigStore.ServerProfile target = parsed.profile;
        store.setAutoConnect(swAutoConnect.isChecked());
        store.setMobileView(swMobileView.isChecked());
        store.saveAndSelect(target);

        // Pre-sync topology quickly before entering workspace if reachable
        btnSaveConnect.setEnabled(false);
        executor.execute(() -> {
            ServerConfigStore.SavedTopology topo = ServerConfigStore.fetchTopology(target.baseUrl(), 1200);
            if (topo != null) {
                store.saveTopology(topo);
            }
            mainHandler.post(() -> {
                if (isFinishing() || isDestroyed()) return;
                btnSaveConnect.setEnabled(true);
                openWorkspace();
            });
        });
    }

    private void renderHistory() {
        historyContainer.removeAllViews();
        List<ServerConfigStore.ServerProfile> history = store.getHistory();
        if (history.isEmpty()) {
            tvHistoryEmpty.setVisibility(View.VISIBLE);
            return;
        }
        tvHistoryEmpty.setVisibility(View.GONE);
        LayoutInflater inflater = LayoutInflater.from(this);
        for (final ServerConfigStore.ServerProfile item : history) {
            View row = inflater.inflate(R.layout.item_server_history, historyContainer, false);
            TextView tvTitle = row.findViewById(R.id.tv_history_title);
            TextView tvUrl = row.findViewById(R.id.tv_history_url);
            View infoArea = row.findViewById(R.id.history_info_area);
            Button btnConnect = row.findViewById(R.id.btn_history_connect);
            Button btnDelete = row.findViewById(R.id.btn_history_delete);

            String title = item.displayName();
            if (item.version != null && !item.version.isEmpty()) {
                title = title + "  (" + item.version + ")";
            }
            tvTitle.setText(title);
            tvUrl.setText(item.baseUrl());

            infoArea.setOnClickListener(v -> {
                setScheme(item.scheme, false);
                etHost.setText(item.host);
                etPort.setText(String.valueOf(item.port));
                etLabel.setText(item.label);
            });

            btnConnect.setOnClickListener(v -> {
                store.setAutoConnect(swAutoConnect.isChecked());
                store.setMobileView(swMobileView.isChecked());
                store.saveAndSelect(item);
                openWorkspace();
            });

            btnDelete.setOnClickListener(v -> {
                store.removeHistory(item.baseUrl());
                renderHistory();
            });

            historyContainer.addView(row);
        }
    }

    private void openWorkspace() {
        Intent intent = new Intent(this, WorkspaceActivity.class);
        intent.addFlags(Intent.FLAG_ACTIVITY_CLEAR_TOP);
        startActivity(intent);
        finish();
    }

    @Override
    protected void onDestroy() {
        if (activeScanCancel != null) {
            activeScanCancel.set(true);
        }
        super.onDestroy();
        if (executor != null) {
            executor.shutdownNow();
        }
    }
}
