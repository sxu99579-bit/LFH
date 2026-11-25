package com.quailnest.client;

import android.content.Context;
import android.content.SharedPreferences;

import org.json.JSONArray;
import org.json.JSONObject;

import java.io.BufferedReader;
import java.io.InputStream;
import java.io.InputStreamReader;
import java.net.HttpURLConnection;
import java.net.URI;
import java.net.URL;
import java.nio.charset.StandardCharsets;
import java.util.ArrayList;
import java.util.Collections;
import java.util.LinkedHashSet;
import java.util.List;
import java.util.Locale;
import java.util.Set;
import java.util.concurrent.Callable;
import java.util.concurrent.ExecutorService;
import java.util.concurrent.Executors;
import java.util.concurrent.Future;
import java.util.concurrent.TimeUnit;
import java.util.concurrent.atomic.AtomicBoolean;

public class ServerConfigStore {
    private static final String PREF_NAME = "quailnest_client_prefs";
    private static final String KEY_CURRENT = "current_server";
    private static final String KEY_HISTORY = "server_history";
    private static final String KEY_TOPOLOGY = "saved_topology";
    private static final String KEY_AUTO_CONNECT = "auto_connect";
    private static final String KEY_MOBILE_VIEW = "mobile_view";
    private static final int MAX_HISTORY = 10;

    public static class ServerProfile {
        public String scheme = "http";
        public String host = "";
        public int port = 8080;
        public String label = "";
        public String version = "";
        public long lastUsedAt = 0L;

        public String baseUrl() {
            String safeHost = (host.contains(":") && !host.startsWith("["))
                    ? "[" + host + "]"
                    : host;
            return scheme + "://" + safeHost + ":" + port;
        }

        public String entryUrl(boolean mobileView) {
            return baseUrl() + "/?view=" + (mobileView ? "mobile" : "desktop");
        }

        public String displayName() {
            if (label != null && !label.trim().isEmpty()) {
                return label.trim();
            }
            return host + ":" + port;
        }

        public JSONObject toJson() {
            JSONObject obj = new JSONObject();
            try {
                obj.put("scheme", scheme);
                obj.put("host", host);
                obj.put("port", port);
                obj.put("label", label == null ? "" : label);
                obj.put("version", version == null ? "" : version);
                obj.put("lastUsedAt", lastUsedAt);
            } catch (Exception ignored) {
            }
            return obj;
        }

        public static ServerProfile fromJson(JSONObject obj) {
            if (obj == null) return null;
            String host = obj.optString("host", "").trim();
            if (host.isEmpty()) return null;
            ServerProfile p = new ServerProfile();
            String s = obj.optString("scheme", "http").trim().toLowerCase(Locale.US);
            p.scheme = "https".equals(s) ? "https" : "http";
            p.host = host;
            int port = obj.optInt("port", 8080);
            if (port < 1 || port > 65535) port = 8080;
            p.port = port;
            p.label = obj.optString("label", "").trim();
            p.version = obj.optString("version", "").trim();
            p.lastUsedAt = obj.optLong("lastUsedAt", 0L);
            return p;
        }

        public static ServerProfile fromUrl(String urlStr, String fallbackLabel, String version) {
            if (urlStr == null || urlStr.trim().isEmpty()) return null;
            try {
                URI uri = URI.create(urlStr.trim());
                String scheme = "https".equalsIgnoreCase(uri.getScheme()) ? "https" : "http";
                String host = uri.getHost();
                if (host == null || host.isEmpty()) return null;
                if (host.startsWith("[") && host.endsWith("]")) {
                    host = host.substring(1, host.length() - 1);
                }
                int port = uri.getPort();
                if (port < 1 || port > 65535) {
                    port = "https".equals(scheme) ? 443 : 8080;
                }
                ServerProfile p = new ServerProfile();
                p.scheme = scheme;
                p.host = host;
                p.port = port;
                p.label = fallbackLabel == null ? "" : fallbackLabel.trim();
                p.version = version == null ? "" : version.trim();
                return p;
            } catch (Exception e) {
                return null;
            }
        }
    }

    public static class TopologyNode {
        public String nodeId = "";
        public String nodeName = "";
        public String role = "";
        public boolean isHomePrimary;
        public boolean online;
        public List<String> urls = new ArrayList<>();
        public double score;
        public String version = "";

        public JSONObject toJson() {
            JSONObject obj = new JSONObject();
            try {
                obj.put("node_id", nodeId);
                obj.put("node_name", nodeName);
                obj.put("role", role);
                obj.put("is_home_primary", isHomePrimary);
                obj.put("online", online);
                obj.put("score", score);
                obj.put("version", version);
                JSONArray arr = new JSONArray();
                for (String u : urls) arr.put(u);
                obj.put("urls", arr);
            } catch (Exception ignored) {
            }
            return obj;
        }

        public static TopologyNode fromJson(JSONObject obj) {
            if (obj == null) return null;
            TopologyNode n = new TopologyNode();
            n.nodeId = obj.optString("node_id", "").trim();
            n.nodeName = obj.optString("node_name", "").trim();
            n.role = obj.optString("role", "").trim();
            n.isHomePrimary = obj.optBoolean("is_home_primary", false);
            n.online = obj.optBoolean("online", false);
            n.score = obj.optDouble("score", 0.0);
            n.version = obj.optString("version", "").trim();
            JSONArray arr = obj.optJSONArray("urls");
            if (arr != null) {
                for (int i = 0; i < arr.length(); i++) {
                    String u = LanScanner.normalizeBaseUrl(arr.optString(i, ""));
                    if (!u.isEmpty() && !n.urls.contains(u)) {
                        n.urls.add(u);
                    }
                }
            }
            return n;
        }
    }

    public static class SavedTopology {
        public String clusterId = "";
        public long epoch = 0L;
        public String homePrimaryNodeId = "";
        public List<String> homePrimaryUrls = new ArrayList<>();
        public String activeEntryNodeId = "";
        public List<String> activeEntryUrls = new ArrayList<>();
        public boolean failoverActive;
        public boolean autoFailover = true;
        public String preferredStandbyNodeId = "";
        public List<String> standbyOrder = new ArrayList<>();
        public List<TopologyNode> nodes = new ArrayList<>();
        public long savedAtMs = 0L;

        public List<String> getAllCandidateUrls(String excludeUrl) {
            String ex = LanScanner.normalizeBaseUrl(excludeUrl);
            Set<String> out = new LinkedHashSet<>();
            for (String u : activeEntryUrls) {
                if (!u.equals(ex)) out.add(u);
            }
            for (String u : homePrimaryUrls) {
                if (!u.equals(ex)) out.add(u);
            }
            for (String id : standbyOrder) {
                for (TopologyNode n : nodes) {
                    if (id.equals(n.nodeId)) {
                        for (String u : n.urls) {
                            if (!u.equals(ex)) out.add(u);
                        }
                    }
                }
            }
            for (TopologyNode n : nodes) {
                for (String u : n.urls) {
                    if (!u.equals(ex)) out.add(u);
                }
            }
            return new ArrayList<>(out);
        }

        public JSONObject toJson() {
            JSONObject obj = new JSONObject();
            try {
                obj.put("cluster_id", clusterId);
                obj.put("epoch", epoch);
                obj.put("home_primary_node_id", homePrimaryNodeId);
                obj.put("home_primary_urls", toJsonStringArray(homePrimaryUrls));
                obj.put("active_entry_node_id", activeEntryNodeId);
                obj.put("active_entry_urls", toJsonStringArray(activeEntryUrls));
                obj.put("failover_active", failoverActive);
                obj.put("auto_failover", autoFailover);
                obj.put("preferred_standby_node_id", preferredStandbyNodeId);
                obj.put("standby_order", toJsonStringArray(standbyOrder));
                JSONArray nArr = new JSONArray();
                for (TopologyNode n : nodes) nArr.put(n.toJson());
                obj.put("nodes", nArr);
                obj.put("saved_at_ms", savedAtMs);
            } catch (Exception ignored) {
            }
            return obj;
        }

        public static SavedTopology fromJson(JSONObject obj) {
            if (obj == null) return null;
            SavedTopology t = new SavedTopology();
            t.clusterId = obj.optString("cluster_id", "").trim();
            t.epoch = obj.optLong("epoch", 0L);
            t.homePrimaryNodeId = obj.optString("home_primary_node_id", "").trim();
            t.homePrimaryUrls = readUrlArray(obj.optJSONArray("home_primary_urls"));
            t.activeEntryNodeId = obj.optString("active_entry_node_id", "").trim();
            t.activeEntryUrls = readUrlArray(obj.optJSONArray("active_entry_urls"));
            t.failoverActive = obj.optBoolean("failover_active", false);
            t.autoFailover = obj.optBoolean("auto_failover", true);
            t.preferredStandbyNodeId = obj.optString("preferred_standby_node_id", "").trim();
            JSONArray ord = obj.optJSONArray("standby_order");
            if (ord != null) {
                for (int i = 0; i < ord.length(); i++) {
                    String s = ord.optString(i, "").trim();
                    if (!s.isEmpty()) t.standbyOrder.add(s);
                }
            }
            JSONArray nArr = obj.optJSONArray("nodes");
            if (nArr != null) {
                for (int i = 0; i < nArr.length(); i++) {
                    TopologyNode n = TopologyNode.fromJson(nArr.optJSONObject(i));
                    if (n != null) t.nodes.add(n);
                }
            }
            t.savedAtMs = obj.optLong("saved_at_ms", System.currentTimeMillis());
            return t;
        }

        private static JSONArray toJsonStringArray(List<String> list) {
            JSONArray arr = new JSONArray();
            if (list != null) {
                for (String s : list) arr.put(s);
            }
            return arr;
        }

        private static List<String> readUrlArray(JSONArray arr) {
            List<String> list = new ArrayList<>();
            if (arr == null) return list;
            for (int i = 0; i < arr.length(); i++) {
                String u = LanScanner.normalizeBaseUrl(arr.optString(i, ""));
                if (!u.isEmpty() && !list.contains(u)) list.add(u);
            }
            return list;
        }
    }

    public static class ParseResult {
        public final ServerProfile profile;
        public final String error;

        public ParseResult(ServerProfile profile, String error) {
            this.profile = profile;
            this.error = error;
        }
    }

    public static class ProbeResult {
        public boolean ok;
        public String url = "";
        public String appName = "";
        public String version = "";
        public String role = "";
        public String clusterId = "";
        public String nodeId = "";
        public String nodeName = "";
        public String entryUrl = "";
        public List<String> entryUrls = new ArrayList<>();
        public long epoch = 0L;
        public boolean failoverActive;
        public int onlineNodes;
        public int totalNodes;
        public long latencyMs;
        public String error = "";
    }

    public static class FailoverResolution {
        public ServerProfile profile;
        public ProbeResult probe;
        public SavedTopology topology;
        public String stageLabel = "";
    }

    private final SharedPreferences prefs;

    public ServerConfigStore(Context context) {
        this.prefs = context.getApplicationContext().getSharedPreferences(PREF_NAME, Context.MODE_PRIVATE);
    }

    public boolean isAutoConnect() {
        return prefs.getBoolean(KEY_AUTO_CONNECT, true);
    }

    public void setAutoConnect(boolean enabled) {
        prefs.edit().putBoolean(KEY_AUTO_CONNECT, enabled).apply();
    }

    public boolean isMobileView() {
        return prefs.getBoolean(KEY_MOBILE_VIEW, true);
    }

    public void setMobileView(boolean mobileView) {
        prefs.edit().putBoolean(KEY_MOBILE_VIEW, mobileView).apply();
    }

    public ServerProfile loadCurrent() {
        String raw = prefs.getString(KEY_CURRENT, null);
        if (raw == null || raw.trim().isEmpty()) return null;
        try {
            return ServerProfile.fromJson(new JSONObject(raw));
        } catch (Exception e) {
            return null;
        }
    }

    public SavedTopology loadTopology() {
        String raw = prefs.getString(KEY_TOPOLOGY, null);
        if (raw == null || raw.trim().isEmpty()) return null;
        try {
            return SavedTopology.fromJson(new JSONObject(raw));
        } catch (Exception e) {
            return null;
        }
    }

    public void saveTopology(SavedTopology topo) {
        if (topo == null) return;
        topo.savedAtMs = System.currentTimeMillis();
        prefs.edit().putString(KEY_TOPOLOGY, topo.toJson().toString()).apply();
    }

    public List<ServerProfile> getHistory() {
        List<ServerProfile> out = new ArrayList<>();
        String raw = prefs.getString(KEY_HISTORY, null);
        if (raw == null || raw.trim().isEmpty()) return out;
        try {
            JSONArray arr = new JSONArray(raw);
            for (int i = 0; i < arr.length(); i++) {
                ServerProfile p = ServerProfile.fromJson(arr.optJSONObject(i));
                if (p != null) out.add(p);
            }
        } catch (Exception ignored) {
        }
        return out;
    }

    public void saveAndSelect(ServerProfile profile) {
        if (profile == null) return;
        profile.lastUsedAt = System.currentTimeMillis();
        String targetKey = profile.baseUrl();

        List<ServerProfile> oldList = getHistory();
        if (profile.version == null || profile.version.isEmpty()) {
            for (ServerProfile prev : oldList) {
                if (targetKey.equals(prev.baseUrl())) {
                    if (prev.version != null && !prev.version.isEmpty()) {
                        profile.version = prev.version;
                    }
                    if ((profile.label == null || profile.label.isEmpty()) && prev.label != null) {
                        profile.label = prev.label;
                    }
                    break;
                }
            }
        }

        List<ServerProfile> nextList = new ArrayList<>();
        nextList.add(profile);
        for (ServerProfile item : oldList) {
            if (!targetKey.equals(item.baseUrl())) {
                nextList.add(item);
            }
            if (nextList.size() >= MAX_HISTORY) break;
        }

        JSONArray arr = new JSONArray();
        for (ServerProfile item : nextList) {
            arr.put(item.toJson());
        }

        prefs.edit()
                .putString(KEY_CURRENT, profile.toJson().toString())
                .putString(KEY_HISTORY, arr.toString())
                .apply();
    }

    public void updateCurrentVersion(String baseUrl, String version) {
        if (baseUrl == null || version == null || version.trim().isEmpty()) return;
        ServerProfile cur = loadCurrent();
        if (cur != null && baseUrl.equals(cur.baseUrl()) && !version.trim().equals(cur.version)) {
            cur.version = version.trim();
            saveAndSelect(cur);
        }
    }

    public void removeHistory(String baseUrl) {
        if (baseUrl == null) return;
        List<ServerProfile> list = getHistory();
        List<ServerProfile> kept = new ArrayList<>();
        for (ServerProfile item : list) {
            if (!baseUrl.equals(item.baseUrl())) {
                kept.add(item);
            }
        }
        JSONArray arr = new JSONArray();
        for (ServerProfile item : kept) {
            arr.put(item.toJson());
        }
        SharedPreferences.Editor ed = prefs.edit().putString(KEY_HISTORY, arr.toString());
        ServerProfile cur = loadCurrent();
        if (cur != null && baseUrl.equals(cur.baseUrl())) {
            if (kept.isEmpty()) {
                ed.remove(KEY_CURRENT);
            } else {
                ed.putString(KEY_CURRENT, kept.get(0).toJson().toString());
            }
        }
        ed.apply();
    }

    public static ParseResult parseSmartInput(String schemeInput, String rawHostInput, String rawPortInput, String labelInput) {
        String scheme = "https".equalsIgnoreCase(schemeInput == null ? "" : schemeInput.trim()) ? "https" : "http";
        String raw = rawHostInput == null ? "" : rawHostInput.trim();
        if (raw.isEmpty()) {
            return new ParseResult(null, "请输入服务器 IP 地址或域名");
        }

        String lower = raw.toLowerCase(Locale.US);
        if (lower.startsWith("http://")) {
            scheme = "http";
            raw = raw.substring(7);
        } else if (lower.startsWith("https://")) {
            scheme = "https";
            raw = raw.substring(8);
        }

        int slashIdx = indexOfAny(raw, '/', '?', '#');
        if (slashIdx >= 0) {
            raw = raw.substring(0, slashIdx);
        }
        raw = raw.trim();
        if (raw.isEmpty()) {
            return new ParseResult(null, "服务器地址格式无效");
        }
        for (int i = 0; i < raw.length(); i++) {
            if (Character.isWhitespace(raw.charAt(i))) {
                return new ParseResult(null, "服务器地址不能包含空格");
            }
        }

        Integer parsedPort = null;
        String cleanHost;
        if (raw.startsWith("[")) {
            int endBracket = raw.indexOf(']');
            if (endBracket <= 1) {
                return new ParseResult(null, "IPv6 地址格式无效");
            }
            cleanHost = raw.substring(1, endBracket);
            String after = raw.substring(endBracket + 1);
            if (after.startsWith(":")) {
                String pStr = after.substring(1).trim();
                if (!pStr.isEmpty()) {
                    try {
                        int p = Integer.parseInt(pStr);
                        if (p < 1 || p > 65535) {
                            return new ParseResult(null, "端口号必须在 1 到 65535 之间");
                        }
                        parsedPort = p;
                    } catch (NumberFormatException e) {
                        return new ParseResult(null, "地址中的端口号无效");
                    }
                }
            } else if (!after.isEmpty()) {
                return new ParseResult(null, "服务器地址格式无效");
            }
        } else {
            int firstColon = raw.indexOf(':');
            int lastColon = raw.lastIndexOf(':');
            if (firstColon > 0 && firstColon == lastColon) {
                cleanHost = raw.substring(0, lastColon).trim();
                String pStr = raw.substring(lastColon + 1).trim();
                if (cleanHost.isEmpty()) {
                    return new ParseResult(null, "服务器 IP 或域名不能为空");
                }
                if (!pStr.isEmpty()) {
                    try {
                        int p = Integer.parseInt(pStr);
                        if (p < 1 || p > 65535) {
                            return new ParseResult(null, "端口号必须在 1 到 65535 之间");
                        }
                        parsedPort = p;
                    } catch (NumberFormatException e) {
                        return new ParseResult(null, "地址中的端口号无效");
                    }
                }
            } else {
                cleanHost = raw;
            }
        }

        int finalPort;
        String portTrim = rawPortInput == null ? "" : rawPortInput.trim();
        if (parsedPort != null) {
            finalPort = parsedPort;
        } else if (!portTrim.isEmpty()) {
            try {
                int p = Integer.parseInt(portTrim);
                if (p < 1 || p > 65535) {
                    return new ParseResult(null, "端口号必须在 1 到 65535 之间");
                }
                finalPort = p;
            } catch (NumberFormatException e) {
                return new ParseResult(null, "端口必须为 1 到 65535 的数字");
            }
        } else {
            finalPort = "https".equals(scheme) ? 443 : 8080;
        }

        ServerProfile p = new ServerProfile();
        p.scheme = scheme;
        p.host = cleanHost;
        p.port = finalPort;
        String cleanLabel = labelInput == null ? "" : labelInput.trim();
        if (cleanLabel.length() > 48) {
            cleanLabel = cleanLabel.substring(0, 48);
        }
        p.label = cleanLabel;
        return new ParseResult(p, null);
    }

    private static int indexOfAny(String s, char... chars) {
        int best = -1;
        for (char c : chars) {
            int idx = s.indexOf(c);
            if (idx >= 0 && (best < 0 || idx < best)) {
                best = idx;
            }
        }
        return best;
    }

    public static ProbeResult probeServer(String baseUrl, int timeoutMs) {
        ProbeResult res = new ProbeResult();
        String cleanBase = baseUrl == null ? "" : baseUrl.replaceAll("/+$", "");
        res.url = cleanBase;
        long start = System.currentTimeMillis();
        HttpURLConnection conn = null;
        try {
            URL url = new URL(cleanBase + "/api/info");
            conn = (HttpURLConnection) url.openConnection();
            conn.setRequestMethod("GET");
            conn.setConnectTimeout(timeoutMs);
            conn.setReadTimeout(timeoutMs);
            conn.setInstanceFollowRedirects(true);
            int status = conn.getResponseCode();
            res.latencyMs = Math.max(1L, System.currentTimeMillis() - start);
            if (status < 200 || status >= 300) {
                res.ok = false;
                res.error = "服务端返回异常状态码 HTTP " + status;
                return res;
            }
            InputStream is = conn.getInputStream();
            BufferedReader reader = new BufferedReader(new InputStreamReader(is, StandardCharsets.UTF_8));
            StringBuilder sb = new StringBuilder();
            String line;
            while ((line = reader.readLine()) != null) {
                sb.append(line);
            }
            reader.close();

            JSONObject json = new JSONObject(sb.toString());
            String version = json.optString("version", "").trim();
            String appName = json.optString("app_name", "").trim();
            if (version.isEmpty() && appName.isEmpty()) {
                res.ok = false;
                res.error = "目标地址返回的不是 QuailNest / LFH 的 /api/info 数据";
                return res;
            }
            res.ok = true;
            res.appName = appName.isEmpty() ? "QuailNest" : appName;
            res.version = version;
            res.role = json.optString("role", "").trim();
            res.clusterId = json.optString("cluster_id", "").trim();
            res.nodeId = json.optString("node_id", "").trim();
            res.nodeName = json.optString("node_name", "").trim();
            res.entryUrl = LanScanner.normalizeBaseUrl(json.optString("entry_url", ""));
            res.epoch = json.optLong("epoch", 0L);
            res.failoverActive = json.optBoolean("failover_active", false);
            JSONArray eArr = json.optJSONArray("entry_urls");
            if (eArr != null) {
                for (int i = 0; i < eArr.length(); i++) {
                    String eu = LanScanner.normalizeBaseUrl(eArr.optString(i, ""));
                    if (!eu.isEmpty() && !res.entryUrls.contains(eu)) res.entryUrls.add(eu);
                }
            }
            JSONObject pool = json.optJSONObject("pool");
            if (pool != null) {
                res.onlineNodes = pool.optInt("online_nodes", 1);
                JSONArray nodes = pool.optJSONArray("nodes");
                res.totalNodes = nodes != null ? nodes.length() : 1;
            } else {
                res.onlineNodes = 1;
                res.totalNodes = 1;
            }
            return res;
        } catch (Exception e) {
            res.ok = false;
            res.latencyMs = Math.max(1L, System.currentTimeMillis() - start);
            String msg = e.getMessage() == null ? e.getClass().getSimpleName() : e.getMessage();
            String low = msg.toLowerCase(Locale.US);
            if (low.contains("timeout") || low.contains("timed out")) {
                res.error = "连接超时，请确认手机与电脑在同一局域网且防火墙已放行端口";
            } else if (low.contains("econnrefused") || low.contains("refused")) {
                res.error = "连接被拒绝，请确认 QuailNest 服务端已启动且端口填写正确";
            } else if (low.contains("unknownhost")) {
                res.error = "无法解析地址，请检查输入的 IP 或域名";
            } else if (low.contains("cleartext")) {
                res.error = "HTTP 明文访问受限，请检查系统或网络配置";
            } else {
                res.error = "连接失败：" + msg;
            }
            return res;
        } finally {
            if (conn != null) {
                conn.disconnect();
            }
        }
    }

    public static SavedTopology fetchTopology(String baseUrl, int timeoutMs) {
        String cleanBase = LanScanner.normalizeBaseUrl(baseUrl);
        if (cleanBase.isEmpty()) return null;
        HttpURLConnection conn = null;
        try {
            URL url = new URL(cleanBase + "/api/client/topology");
            conn = (HttpURLConnection) url.openConnection();
            conn.setRequestMethod("GET");
            conn.setConnectTimeout(timeoutMs);
            conn.setReadTimeout(timeoutMs);
            conn.setInstanceFollowRedirects(true);
            int code = conn.getResponseCode();
            if (code < 200 || code >= 300) return null;
            StringBuilder sb = new StringBuilder();
            try (BufferedReader br = new BufferedReader(new InputStreamReader(conn.getInputStream(), StandardCharsets.UTF_8))) {
                String line;
                while ((line = br.readLine()) != null) sb.append(line);
            }
            return SavedTopology.fromJson(new JSONObject(sb.toString()));
        } catch (Exception e) {
            return null;
        } finally {
            if (conn != null) conn.disconnect();
        }
    }

    public static ProbeResult pickFastestEntry(List<String> urls, int timeoutMs) {
        if (urls == null || urls.isEmpty()) return null;
        List<String> clean = new ArrayList<>();
        for (String u : urls) {
            String n = LanScanner.normalizeBaseUrl(u);
            if (!n.isEmpty() && !clean.contains(n)) clean.add(n);
        }
        if (clean.isEmpty()) return null;

        ExecutorService pool = Executors.newFixedThreadPool(Math.min(8, clean.size()));
        List<Future<ProbeResult>> futures = new ArrayList<>(clean.size());
        for (final String u : clean) {
            futures.add(pool.submit((Callable<ProbeResult>) () -> probeServer(u, timeoutMs)));
        }
        ProbeResult bestEntry = null;
        ProbeResult bestAny = null;
        for (Future<ProbeResult> f : futures) {
            try {
                ProbeResult pr = f.get(timeoutMs + 400L, TimeUnit.MILLISECONDS);
                if (pr == null || !pr.ok) continue;
                boolean isEntry = "entry".equalsIgnoreCase(pr.role) || pr.role.isEmpty();
                if (isEntry) {
                    if (bestEntry == null || pr.epoch > bestEntry.epoch
                            || (pr.epoch == bestEntry.epoch && pr.latencyMs < bestEntry.latencyMs)) {
                        bestEntry = pr;
                    }
                } else if (bestAny == null || pr.latencyMs < bestAny.latencyMs) {
                    bestAny = pr;
                }
            } catch (Exception ignored) {
            }
        }
        pool.shutdownNow();
        return bestEntry != null ? bestEntry : bestAny;
    }

    /**
     * 4-level automatic failover / primary restoration discovery:
     * L1 retry current -> L2 primary alternate NICs -> L3 standby peer nodes -> L4 LAN scan.
     */
    public FailoverResolution fourLevelResolve(Context context, ServerProfile current, AtomicBoolean cancel) {
        String currentUrl = current != null ? current.baseUrl() : "";
        String labelHint = current != null ? current.label : "";
        SavedTopology savedTopo = loadTopology();
        String expectedClusterId = savedTopo != null ? savedTopo.clusterId : "";

        // L1: Retry current address (may have momentarily blipped)
        if (!currentUrl.isEmpty() && (cancel == null || !cancel.get())) {
            ProbeResult curProbe = probeServer(currentUrl, 1600);
            if (curProbe.ok && ("entry".equalsIgnoreCase(curProbe.role) || curProbe.role.isEmpty())) {
                SavedTopology fresh = fetchTopology(currentUrl, 1600);
                if (fresh != null) saveTopology(fresh);
                FailoverResolution res = new FailoverResolution();
                res.probe = curProbe;
                res.topology = fresh != null ? fresh : savedTopo;
                res.profile = ServerProfile.fromUrl(curProbe.url, labelHint, curProbe.version);
                res.stageLabel = "L1: 当前节点已恢复响应";
                return res;
            }
            if (curProbe.ok && curProbe.entryUrls != null && !curProbe.entryUrls.isEmpty()) {
                ProbeResult promoted = pickFastestEntry(curProbe.entryUrls, 1800);
                if (promoted != null && promoted.ok) {
                    return finishResolution(promoted, labelHint, "L2: 已跟随最新入口节点");
                }
            }
        }

        // L2: Alternate enabled NICs of Home Primary / Active Entry
        if (savedTopo != null && (cancel == null || !cancel.get())) {
            List<String> primaryAltUrls = new ArrayList<>();
            for (String u : savedTopo.homePrimaryUrls) {
                if (!u.equals(currentUrl) && !primaryAltUrls.contains(u)) primaryAltUrls.add(u);
            }
            for (String u : savedTopo.activeEntryUrls) {
                if (!u.equals(currentUrl) && !primaryAltUrls.contains(u)) primaryAltUrls.add(u);
            }
            ProbeResult altHit = pickFastestEntry(primaryAltUrls, 1800);
            if (altHit != null && altHit.ok && ("entry".equalsIgnoreCase(altHit.role) || altHit.role.isEmpty())) {
                return finishResolution(altHit, labelHint, "L2: 已自动切换至主服务器备用网卡 IP");
            }
        }

        // L3: Standby peer nodes in deterministic priority order
        if (savedTopo != null && (cancel == null || !cancel.get())) {
            List<String> standbyUrls = savedTopo.getAllCandidateUrls(currentUrl);
            ProbeResult standbyHit = pickFastestEntry(standbyUrls, 2000);
            if (standbyHit != null && standbyHit.ok) {
                if (!"entry".equalsIgnoreCase(standbyHit.role) && !standbyHit.role.isEmpty()) {
                    // Standby knows promoted Entry (or has not finished promotion wait yet)
                    List<String> refs = new ArrayList<>(standbyHit.entryUrls);
                    if (!standbyHit.entryUrl.isEmpty() && !refs.contains(standbyHit.entryUrl)) {
                        refs.add(0, standbyHit.entryUrl);
                    }
                    SavedTopology peerTopo = fetchTopology(standbyHit.url, 1500);
                    if (peerTopo != null) {
                        for (String eu : peerTopo.activeEntryUrls) {
                            if (!refs.contains(eu)) refs.add(eu);
                        }
                    }
                    ProbeResult entryRef = pickFastestEntry(refs, 1800);
                    if (entryRef != null && entryRef.ok) {
                        return finishResolution(entryRef, labelHint, "L3: 已自动切换至集群接管节点");
                    }
                }
                return finishResolution(standbyHit, labelHint, "L3: 已自动切换至备援子节点");
            }
        }

        // L4: Full LAN subnet sweep matching cluster_id (or any single cluster)
        if (cancel == null || !cancel.get()) {
            List<LanScanner.DiscoveredCluster> clusters = LanScanner.scanLan(
                    context,
                    savedTopo != null ? savedTopo.getAllCandidateUrls("") : Collections.emptyList(),
                    expectedClusterId,
                    cancel
            );
            for (LanScanner.DiscoveredCluster c : clusters) {
                List<String> tryUrls = new ArrayList<>();
                if (!c.primaryUrl.isEmpty()) tryUrls.add(c.primaryUrl);
                tryUrls.addAll(c.urls);
                ProbeResult hit = pickFastestEntry(tryUrls, 1800);
                if (hit != null && hit.ok) {
                    return finishResolution(hit, labelHint.isEmpty() ? c.displayTitle() : labelHint, "L4: 已通过局域网重扫找回节点");
                }
            }
        }

        return null;
    }

    private FailoverResolution finishResolution(ProbeResult pr, String fallbackLabel, String stageLabel) {
        SavedTopology fresh = fetchTopology(pr.url, 1800);
        if (fresh != null) saveTopology(fresh);
        String chosenLabel = (fallbackLabel != null && !fallbackLabel.trim().isEmpty())
                ? fallbackLabel
                : (pr.nodeName != null && !pr.nodeName.isEmpty() ? pr.nodeName : "QuailNest");
        ServerProfile profile = ServerProfile.fromUrl(pr.url, chosenLabel, pr.version);
        if (profile != null) {
            saveAndSelect(profile);
        }
        FailoverResolution res = new FailoverResolution();
        res.probe = pr;
        res.topology = fresh != null ? fresh : loadTopology();
        res.profile = profile;
        res.stageLabel = stageLabel;
        return res;
    }
}
