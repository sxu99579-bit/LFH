package com.quailnest.client;

import android.content.Context;

import org.json.JSONObject;

import java.net.DatagramPacket;
import java.net.DatagramSocket;
import java.net.Inet4Address;
import java.net.InetAddress;
import java.net.InetSocketAddress;
import java.net.InterfaceAddress;
import java.net.NetworkInterface;
import java.net.Socket;
import java.nio.charset.StandardCharsets;
import java.util.ArrayList;
import java.util.Collections;
import java.util.Enumeration;
import java.util.HashSet;
import java.util.LinkedHashMap;
import java.util.LinkedHashSet;
import java.util.List;
import java.util.Locale;
import java.util.Map;
import java.util.Set;
import java.util.concurrent.Callable;
import java.util.concurrent.ExecutorService;
import java.util.concurrent.Executors;
import java.util.concurrent.Future;
import java.util.concurrent.TimeUnit;
import java.util.concurrent.atomic.AtomicBoolean;

/**
 * Pure Java LAN discovery combining UDP beacon listen (port 53319) and
 * concurrent /24 subnet TCP:8080 sweep + /api/info verification.
 */
public class LanScanner {
    public static final int DEFAULT_PORT = 8080;
    public static final int BEACON_PORT = 53319;
    private static final int TCP_CONNECT_TIMEOUT_MS = 380;
    private static final int HTTP_PROBE_TIMEOUT_MS = 1200;

    public static class DiscoveredCluster {
        public String clusterId = "";
        public String clusterName = "";
        public String nodeId = "";
        public String nodeName = "";
        public String role = "";
        public String version = "";
        public String primaryUrl = "";
        public List<String> urls = new ArrayList<>();
        public int onlineNodes = 1;
        public int totalNodes = 1;
        public long latencyMs = 9999L;
        public boolean viaBeacon = false;

        public String displayTitle() {
            if (clusterName != null && !clusterName.trim().isEmpty()) {
                return clusterName.trim();
            }
            if (nodeName != null && !nodeName.trim().isEmpty()) {
                return nodeName.trim();
            }
            return primaryUrl;
        }
    }

    public static List<DiscoveredCluster> scanLan(
            Context context,
            List<String> extraUrls,
            String filterClusterId,
            AtomicBoolean cancel
    ) {
        Set<String> candidateUrls = new LinkedHashSet<>();
        Set<String> beaconUrls = new HashSet<>();

        // 1. Always inspect saved / explicit candidate URLs first
        if (extraUrls != null) {
            for (String u : extraUrls) {
                String norm = normalizeBaseUrl(u);
                if (!norm.isEmpty()) candidateUrls.add(norm);
            }
        }
        if (context != null) {
            ServerConfigStore store = new ServerConfigStore(context);
            for (ServerConfigStore.ServerProfile h : store.getHistory()) {
                candidateUrls.add(h.baseUrl());
            }
            ServerConfigStore.SavedTopology saved = store.loadTopology();
            if (saved != null) {
                for (String u : saved.getAllCandidateUrls("")) {
                    String norm = normalizeBaseUrl(u);
                    if (!norm.isEmpty()) candidateUrls.add(norm);
                }
            }
        }

        // 2. Listen briefly for UDP presence beacons + sweep local /24 subnets in parallel
        ExecutorService bgExec = Executors.newFixedThreadPool(2);
        Future<List<String>> beaconFuture = bgExec.submit(() -> listenUdpBeacons(950, cancel));
        Future<List<String>> subnetFuture = bgExec.submit(() -> sweepLocalSubnetsFor8080(cancel));

        try {
            List<String> bHits = beaconFuture.get(1500, TimeUnit.MILLISECONDS);
            if (bHits != null) {
                for (String u : bHits) {
                    String norm = normalizeBaseUrl(u);
                    if (!norm.isEmpty()) {
                        candidateUrls.add(norm);
                        beaconUrls.add(norm);
                    }
                }
            }
        } catch (Exception ignored) {
        }
        try {
            List<String> sHits = subnetFuture.get(6000, TimeUnit.MILLISECONDS);
            if (sHits != null) {
                for (String u : sHits) {
                    String norm = normalizeBaseUrl(u);
                    if (!norm.isEmpty()) candidateUrls.add(norm);
                }
            }
        } catch (Exception ignored) {
        }
        bgExec.shutdownNow();

        if (candidateUrls.isEmpty() || (cancel != null && cancel.get())) {
            return Collections.emptyList();
        }

        // 3. Probe /api/info in parallel on all open/known candidates
        List<ServerConfigStore.ProbeResult> hits = new ArrayList<>();
        ExecutorService probePool = Executors.newFixedThreadPool(Math.min(16, Math.max(2, candidateUrls.size())));
        List<Future<ServerConfigStore.ProbeResult>> futures = new ArrayList<>();
        for (final String url : candidateUrls) {
            futures.add(probePool.submit(() -> {
                if (cancel != null && cancel.get()) return null;
                return ServerConfigStore.probeServer(url, HTTP_PROBE_TIMEOUT_MS);
            }));
        }
        for (Future<ServerConfigStore.ProbeResult> f : futures) {
            try {
                ServerConfigStore.ProbeResult pr = f.get(HTTP_PROBE_TIMEOUT_MS + 400L, TimeUnit.MILLISECONDS);
                if (pr != null && pr.ok) {
                    hits.add(pr);
                }
            } catch (Exception ignored) {
            }
        }
        probePool.shutdownNow();

        // 4. Fetch topology for entry discovery & deduplicate by cluster_id
        List<DiscoveredCluster> clusters = mergeDiscoveredClusters(hits, beaconUrls);
        if (filterClusterId != null && !filterClusterId.trim().isEmpty()) {
            List<DiscoveredCluster> matching = new ArrayList<>();
            for (DiscoveredCluster c : clusters) {
                if (filterClusterId.equals(c.clusterId)) {
                    matching.add(c);
                }
            }
            if (!matching.isEmpty()) {
                return matching;
            }
        }
        return clusters;
    }

    private static List<String> listenUdpBeacons(int timeoutMs, AtomicBoolean cancel) {
        Set<String> urls = new LinkedHashSet<>();
        DatagramSocket socket = null;
        try {
            socket = new DatagramSocket(null);
            socket.setReuseAddress(true);
            socket.setBroadcast(true);
            socket.bind(new InetSocketAddress(BEACON_PORT));
            socket.setSoTimeout(220);

            long deadline = System.currentTimeMillis() + timeoutMs;
            byte[] buf = new byte[4096];
            while (System.currentTimeMillis() < deadline && (cancel == null || !cancel.get())) {
                DatagramPacket pkt = new DatagramPacket(buf, buf.length);
                try {
                    socket.receive(pkt);
                    String raw = new String(pkt.getData(), pkt.getOffset(), pkt.getLength(), StandardCharsets.UTF_8);
                    JSONObject obj = new JSONObject(raw);
                    String entryUrl = normalizeBaseUrl(obj.optString("entry_url", ""));
                    String httpUrl = normalizeBaseUrl(obj.optString("http_url", ""));
                    if (!entryUrl.isEmpty()) urls.add(entryUrl);
                    if (!httpUrl.isEmpty()) urls.add(httpUrl);
                } catch (Exception ignored) {
                }
            }
        } catch (Exception ignored) {
        } finally {
            if (socket != null) {
                try {
                    socket.close();
                } catch (Exception ignored) {
                }
            }
        }
        return new ArrayList<>(urls);
    }

    private static List<String> sweepLocalSubnetsFor8080(AtomicBoolean cancel) {
        List<String> hostIps = enumerateScanIPs();
        if (hostIps.isEmpty()) return Collections.emptyList();

        List<String> openUrls = Collections.synchronizedList(new ArrayList<>());
        ExecutorService pool = Executors.newFixedThreadPool(48);
        List<Callable<Void>> tasks = new ArrayList<>(hostIps.size());
        for (final String ip : hostIps) {
            tasks.add(() -> {
                if (cancel != null && cancel.get()) return null;
                try (Socket sock = new Socket()) {
                    sock.connect(new InetSocketAddress(ip, DEFAULT_PORT), TCP_CONNECT_TIMEOUT_MS);
                    openUrls.add("http://" + ip + ":" + DEFAULT_PORT);
                } catch (Exception ignored) {
                }
                return null;
            });
        }
        try {
            pool.invokeAll(tasks, 5000, TimeUnit.MILLISECONDS);
        } catch (InterruptedException ignored) {
        } finally {
            pool.shutdownNow();
        }
        return openUrls;
    }

    private static List<String> enumerateScanIPs() {
        Set<String> prefixes = new LinkedHashSet<>();
        Set<String> localIps = new LinkedHashSet<>();
        try {
            Enumeration<NetworkInterface> nics = NetworkInterface.getNetworkInterfaces();
            while (nics != null && nics.hasMoreElements()) {
                NetworkInterface nic = nics.nextElement();
                if (!nic.isUp() || nic.isLoopback() || nic.isPointToPoint() || nic.isVirtual()) {
                    continue;
                }
                String name = nic.getName() == null ? "" : nic.getName().toLowerCase(Locale.US);
                if (isVirtualOrTunnelName(name)) {
                    continue;
                }
                for (InterfaceAddress ia : nic.getInterfaceAddresses()) {
                    InetAddress addr = ia.getAddress();
                    if (!(addr instanceof Inet4Address)) continue;
                    byte[] raw = addr.getAddress();
                    int b0 = raw[0] & 0xFF;
                    int b1 = raw[1] & 0xFF;
                    int b2 = raw[2] & 0xFF;
                    if (b0 == 127 || (b0 == 169 && b1 == 254)) continue;
                    String ipStr = addr.getHostAddress();
                    if (ipStr != null) localIps.add(ipStr);
                    String prefix = b0 + "." + b1 + "." + b2 + ".";
                    prefixes.add(prefix);
                    if (prefixes.size() >= 3) break;
                }
                if (prefixes.size() >= 3) break;
            }
        } catch (Exception ignored) {
        }

        Set<String> targets = new LinkedHashSet<>(localIps);
        for (String prefix : prefixes) {
            for (int last = 1; last <= 254; last++) {
                targets.add(prefix + last);
            }
        }
        return new ArrayList<>(targets);
    }

    private static boolean isVirtualOrTunnelName(String lowerName) {
        return lowerName.startsWith("tun")
                || lowerName.startsWith("tap")
                || lowerName.startsWith("utun")
                || lowerName.startsWith("wg")
                || lowerName.startsWith("ppp")
                || lowerName.startsWith("docker")
                || lowerName.startsWith("veth")
                || lowerName.startsWith("br-")
                || lowerName.startsWith("dummy")
                || lowerName.contains("rmnet");
    }

    public static List<DiscoveredCluster> mergeDiscoveredClusters(
            List<ServerConfigStore.ProbeResult> probes,
            Set<String> beaconUrls
    ) {
        Map<String, DiscoveredCluster> byKey = new LinkedHashMap<>();
        Map<String, ServerConfigStore.SavedTopology> topoCache = new LinkedHashMap<>();

        for (ServerConfigStore.ProbeResult pr : probes) {
            if (pr == null || !pr.ok) continue;

            ServerConfigStore.SavedTopology topo = topoCache.get(pr.url);
            if (topo == null) {
                topo = ServerConfigStore.fetchTopology(pr.url, 1200);
                if (topo != null) topoCache.put(pr.url, topo);
            }

            String key = pr.clusterId != null && !pr.clusterId.trim().isEmpty()
                    ? pr.clusterId.trim()
                    : pr.url;
            DiscoveredCluster c = byKey.get(key);
            if (c == null) {
                c = new DiscoveredCluster();
                c.clusterId = pr.clusterId == null ? "" : pr.clusterId.trim();
                c.version = pr.version;
                c.onlineNodes = Math.max(1, pr.onlineNodes);
                c.totalNodes = Math.max(1, pr.totalNodes);
                byKey.put(key, c);
            }

            if (beaconUrls != null && beaconUrls.contains(pr.url)) {
                c.viaBeacon = true;
            }
            if (!c.urls.contains(pr.url)) {
                c.urls.add(pr.url);
            }
            boolean isEntry = "entry".equalsIgnoreCase(pr.role);
            boolean curIsEntry = "entry".equalsIgnoreCase(c.role);
            if (c.primaryUrl.isEmpty() || (isEntry && !curIsEntry) || (isEntry == curIsEntry && pr.latencyMs < c.latencyMs)) {
                c.primaryUrl = pr.url;
                c.nodeId = pr.nodeId;
                c.nodeName = pr.nodeName;
                c.role = pr.role;
                c.latencyMs = pr.latencyMs;
                if (pr.version != null && !pr.version.isEmpty()) c.version = pr.version;
                c.onlineNodes = Math.max(1, pr.onlineNodes);
                c.totalNodes = Math.max(1, pr.totalNodes);
            }

            if (topo != null) {
                for (String u : topo.activeEntryUrls) {
                    String norm = normalizeBaseUrl(u);
                    if (!norm.isEmpty() && !c.urls.contains(norm)) c.urls.add(norm);
                }
                for (String u : topo.homePrimaryUrls) {
                    String norm = normalizeBaseUrl(u);
                    if (!norm.isEmpty() && !c.urls.contains(norm)) c.urls.add(norm);
                }
                for (ServerConfigStore.TopologyNode tn : topo.nodes) {
                    for (String u : tn.urls) {
                        String norm = normalizeBaseUrl(u);
                        if (!norm.isEmpty() && !c.urls.contains(norm)) c.urls.add(norm);
                    }
                    if (tn.nodeId != null && tn.nodeId.equals(topo.activeEntryNodeId)
                            && tn.nodeName != null && !tn.nodeName.isEmpty()) {
                        c.clusterName = tn.nodeName;
                    }
                }
            }
            if (c.clusterName.isEmpty()) {
                c.clusterName = (c.nodeName != null && !c.nodeName.isEmpty())
                        ? c.nodeName
                        : ((pr.appName != null && !pr.appName.isEmpty()) ? pr.appName : "QuailNest");
            }
        }

        return new ArrayList<>(byKey.values());
    }

    public static String normalizeBaseUrl(String raw) {
        if (raw == null) return "";
        String s = raw.trim().replaceAll("/+$", "");
        if (s.startsWith("http://") || s.startsWith("https://")) {
            return s;
        }
        return "";
    }
}
