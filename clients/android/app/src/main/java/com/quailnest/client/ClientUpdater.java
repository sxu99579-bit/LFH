package com.quailnest.client;

import android.app.Activity;
import android.app.AlertDialog;
import android.content.Context;
import android.content.Intent;
import android.net.Uri;
import android.os.Build;
import android.os.Handler;
import android.os.Looper;
import android.provider.Settings;
import android.widget.Toast;

import org.json.JSONArray;
import org.json.JSONObject;

import java.io.BufferedReader;
import java.io.File;
import java.io.FileInputStream;
import java.io.FileOutputStream;
import java.io.InputStream;
import java.io.InputStreamReader;
import java.net.HttpURLConnection;
import java.net.URL;
import java.net.URLEncoder;
import java.nio.charset.StandardCharsets;
import java.security.MessageDigest;
import java.util.ArrayList;
import java.util.List;
import java.util.Locale;
import java.util.concurrent.ExecutorService;
import java.util.concurrent.Executors;

/**
 * Checks for QuailNest Android APK updates via server LAN cache (/api/client/update/check)
 * with GitHub Releases fallback, verifies SHA-256, and invokes the system APK installer.
 */
public class ClientUpdater {
    public static final String CLIENT_VERSION = "V1.11.25.2";
    private static final String GITHUB_LATEST_API = "https://api.github.com/repos/sxu99579-bit/QuailNest/releases/latest";

    public static class UpdateInfo {
        public boolean hasUpdate;
        public String currentVersion = CLIENT_VERSION;
        public String latestVersion = "";
        public String assetName = "";
        public long assetSize = 0L;
        public String downloadUrl = "";
        public String sha256 = "";
        public String releaseNotes = "";
        public boolean viaServer = false;
    }

    public interface ProgressListener {
        void onProgress(long downloaded, long total);
    }

    public static UpdateInfo checkUpdate(String activeServerUrl) throws Exception {
        Exception serverErr = null;
        if (activeServerUrl != null && !activeServerUrl.trim().isEmpty()) {
            String base = activeServerUrl.trim().replaceAll("/+$", "");
            try {
                String checkUrl = base + "/api/client/update/check?platform=android&current="
                        + URLEncoder.encode(CLIENT_VERSION, "UTF-8");
                JSONObject json = fetchJson(checkUrl, 5000);
                UpdateInfo info = new UpdateInfo();
                info.currentVersion = json.optString("current_version", CLIENT_VERSION);
                info.latestVersion = json.optString("latest_version", "");
                info.hasUpdate = json.optBoolean("has_update", false);
                if (!info.hasUpdate && !info.latestVersion.isEmpty()) {
                    info.hasUpdate = compareVersions(info.latestVersion, CLIENT_VERSION) > 0;
                }
                info.assetName = json.optString("asset_name", "");
                info.assetSize = json.optLong("asset_size", 0L);
                info.sha256 = json.optString("sha256", "").trim().toLowerCase(Locale.US);
                info.releaseNotes = json.optString("release_notes", "");
                String dl = json.optString("download_url", "").trim();
                if (dl.startsWith("/")) {
                    dl = base + dl;
                } else if (dl.isEmpty()) {
                    dl = json.optString("asset_url", "").trim();
                }
                info.downloadUrl = dl;
                info.viaServer = true;
                return info;
            } catch (Exception e) {
                serverErr = e;
            }
        }

        try {
            JSONObject rel = fetchJson(GITHUB_LATEST_API, 7000);
            UpdateInfo info = new UpdateInfo();
            info.latestVersion = rel.optString("tag_name", "").trim();
            info.hasUpdate = !info.latestVersion.isEmpty() && compareVersions(info.latestVersion, CLIENT_VERSION) > 0;
            info.releaseNotes = rel.optString("body", "");
            info.viaServer = false;

            JSONArray assets = rel.optJSONArray("assets");
            if (assets != null) {
                for (int i = 0; i < assets.length(); i++) {
                    JSONObject a = assets.optJSONObject(i);
                    if (a == null) continue;
                    String name = a.optString("name", "");
                    if (name.toLowerCase(Locale.US).endsWith(".apk")) {
                        info.assetName = name;
                        info.assetSize = a.optLong("size", 0L);
                        info.downloadUrl = a.optString("browser_download_url", "");
                        break;
                    }
                }
            }
            return info;
        } catch (Exception ghErr) {
            if (serverErr != null) {
                throw new Exception("服务端检测失败（" + serverErr.getMessage() + "），GitHub 直连也失败（" + ghErr.getMessage() + "）");
            }
            throw ghErr;
        }
    }

    public static File downloadAndVerifyApk(
            Context context,
            UpdateInfo info,
            ProgressListener listener
    ) throws Exception {
        if (info == null || info.downloadUrl == null || info.downloadUrl.trim().isEmpty()) {
            throw new IllegalArgumentException("未找到可用的 APK 安装包下载地址");
        }
        File dir = UpdateFileProvider.getUpdatesDir(context);
        File part = new File(dir, "quailnest-update.apk.part");
        File finalApk = new File(dir, "quailnest-update.apk");

        HttpURLConnection conn = null;
        try {
            URL url = new URL(info.downloadUrl);
            conn = (HttpURLConnection) url.openConnection();
            conn.setRequestMethod("GET");
            conn.setConnectTimeout(10000);
            conn.setReadTimeout(30000);
            conn.setInstanceFollowRedirects(true);
            conn.setRequestProperty("User-Agent", "QuailNest-Android/" + CLIENT_VERSION);
            int code = conn.getResponseCode();
            if (code < 200 || code >= 300) {
                throw new Exception("下载安装包失败：HTTP " + code);
            }

            long total = conn.getContentLengthLong();
            if (total <= 0) total = info.assetSize;
            String headerSha = conn.getHeaderField("X-Content-Sha256");

            MessageDigest md = MessageDigest.getInstance("SHA-256");
            try (InputStream is = conn.getInputStream();
                 FileOutputStream fos = new FileOutputStream(part)) {
                byte[] buf = new byte[16384];
                long readSum = 0L;
                int n;
                while ((n = is.read(buf)) > 0) {
                    fos.write(buf, 0, n);
                    md.update(buf, 0, n);
                    readSum += n;
                    if (listener != null) {
                        listener.onProgress(readSum, total);
                    }
                }
                fos.getFD().sync();
            }

            String actualSha = bytesToHex(md.digest());
            String expectedSha = (info.sha256 != null && !info.sha256.isEmpty())
                    ? info.sha256
                    : (headerSha != null ? headerSha.trim().toLowerCase(Locale.US) : "");
            if (!expectedSha.isEmpty() && !expectedSha.equalsIgnoreCase(actualSha)) {
                //noinspection ResultOfMethodCallIgnored
                part.delete();
                throw new Exception("APK 安装包 SHA-256 校验失败，已拒绝安装");
            }

            if (finalApk.exists()) {
                //noinspection ResultOfMethodCallIgnored
                finalApk.delete();
            }
            if (!part.renameTo(finalApk)) {
                copyFile(part, finalApk);
                //noinspection ResultOfMethodCallIgnored
                part.delete();
            }
            return finalApk;
        } catch (Exception e) {
            if (part.exists()) {
                //noinspection ResultOfMethodCallIgnored
                part.delete();
            }
            throw e;
        } finally {
            if (conn != null) conn.disconnect();
        }
    }

    public static boolean needsInstallPermission(Context context) {
        return Build.VERSION.SDK_INT >= Build.VERSION_CODES.O
                && !context.getPackageManager().canRequestPackageInstalls();
    }

    public static void openInstallPermissionSettings(Activity activity) {
        if (Build.VERSION.SDK_INT >= Build.VERSION_CODES.O) {
            Intent intent = new Intent(
                    Settings.ACTION_MANAGE_UNKNOWN_APP_SOURCES,
                    Uri.parse("package:" + activity.getPackageName())
            );
            activity.startActivity(intent);
        }
    }

    public interface StatusCallback {
        void onStatus(String text, boolean isError);
    }

    public static void startInteractiveUpdateCheck(
            final Activity activity,
            final String activeServerUrl,
            final StatusCallback statusCb
    ) {
        if (activity == null || activity.isFinishing() || activity.isDestroyed()) return;
        final Handler main = new Handler(Looper.getMainLooper());
        if (statusCb != null) {
            statusCb.onStatus("正在检查客户端更新（优先经由主服务器局域网缓存）...", false);
        }
        final ExecutorService exec = Executors.newSingleThreadExecutor();
        exec.execute(() -> {
            try {
                final UpdateInfo info = checkUpdate(activeServerUrl);
                main.post(() -> {
                    if (activity.isFinishing() || activity.isDestroyed()) return;
                    if (!info.hasUpdate) {
                        String msg = "当前已是最新版本：" + CLIENT_VERSION
                                + (info.latestVersion != null && !info.latestVersion.isEmpty()
                                ? "（服务端最新 " + info.latestVersion + "）"
                                : "");
                        if (statusCb != null) statusCb.onStatus(msg, false);
                        new AlertDialog.Builder(activity)
                                .setTitle("检查更新")
                                .setMessage(msg)
                                .setPositiveButton("确定", null)
                                .show();
                        return;
                    }
                    String srcLabel = info.viaServer ? "服务端局域网高速缓存" : "GitHub Releases";
                    String sizeMb = info.assetSize > 0
                            ? String.format(Locale.US, " · %.2f MB", info.assetSize / (1024.0 * 1024.0))
                            : "";
                    String notes = (info.releaseNotes == null || info.releaseNotes.trim().isEmpty())
                            ? "包含局域网自动发现、主备故障切换与客户端体验优化。"
                            : info.releaseNotes.trim();
                    if (notes.length() > 400) {
                        notes = notes.substring(0, 400) + "...";
                    }
                    String body = "当前版本：" + CLIENT_VERSION
                            + "\n最新版本：" + info.latestVersion
                            + "\n下载来源：" + srcLabel + sizeMb
                            + (info.sha256 != null && !info.sha256.isEmpty() ? "\n校验：下载后将自动校验 SHA-256" : "")
                            + "\n\n更新说明：\n" + notes;
                    if (statusCb != null) {
                        statusCb.onStatus("发现新版本 " + info.latestVersion + "（来源：" + srcLabel + "）", false);
                    }
                    new AlertDialog.Builder(activity)
                            .setTitle("发现新版本 " + info.latestVersion)
                            .setMessage(body)
                            .setPositiveButton("立即下载安装", (d, w) -> startDownloadAndPromptInstall(activity, info, statusCb))
                            .setNegativeButton("暂不更新", null)
                            .show();
                });
            } catch (final Exception e) {
                main.post(() -> {
                    if (activity.isFinishing() || activity.isDestroyed()) return;
                    String msg = "检查更新失败：" + e.getMessage();
                    if (statusCb != null) statusCb.onStatus(msg, true);
                    Toast.makeText(activity, msg, Toast.LENGTH_LONG).show();
                });
            } finally {
                exec.shutdown();
            }
        });
    }

    private static void startDownloadAndPromptInstall(
            final Activity activity,
            final UpdateInfo info,
            final StatusCallback statusCb
    ) {
        final Handler main = new Handler(Looper.getMainLooper());
        final android.widget.TextView progressTv = new android.widget.TextView(activity);
        progressTv.setPadding(56, 36, 56, 20);
        progressTv.setText("正在连接下载源并校验 SHA-256...");
        progressTv.setTextSize(14f);

        final AlertDialog progressDialog = new AlertDialog.Builder(activity)
                .setTitle("正在下载 " + (info.latestVersion.isEmpty() ? "APK 更新" : info.latestVersion))
                .setView(progressTv)
                .setCancelable(false)
                .create();
        progressDialog.show();

        final ExecutorService dlExec = Executors.newSingleThreadExecutor();
        dlExec.execute(() -> {
            try {
                final File apk = downloadAndVerifyApk(activity, info, (downloaded, total) -> {
                    final String text;
                    if (total > 0) {
                        int pct = (int) Math.min(100L, (downloaded * 100L) / total);
                        text = String.format(
                                Locale.US,
                                "正在下载安装包：%d%%（%.2f / %.2f MB）",
                                pct,
                                downloaded / (1024.0 * 1024.0),
                                total / (1024.0 * 1024.0)
                        );
                    } else {
                        text = String.format(Locale.US, "正在下载安装包：%.2f MB", downloaded / (1024.0 * 1024.0));
                    }
                    main.post(() -> {
                        if (!activity.isFinishing() && !activity.isDestroyed()) {
                            progressTv.setText(text);
                            if (statusCb != null) statusCb.onStatus(text, false);
                        }
                    });
                });
                main.post(() -> {
                    if (progressDialog.isShowing()) progressDialog.dismiss();
                    if (activity.isFinishing() || activity.isDestroyed()) return;
                    if (statusCb != null) statusCb.onStatus("安装包下载并校验完成，正在调起系统安装器...", false);
                    promptInstallWithPermissionCheck(activity, apk);
                });
            } catch (final Exception e) {
                main.post(() -> {
                    if (progressDialog.isShowing()) progressDialog.dismiss();
                    if (activity.isFinishing() || activity.isDestroyed()) return;
                    String msg = "更新失败：" + e.getMessage();
                    if (statusCb != null) statusCb.onStatus(msg, true);
                    new AlertDialog.Builder(activity)
                            .setTitle("下载更新失败")
                            .setMessage(msg)
                            .setPositiveButton("确定", null)
                            .show();
                });
            } finally {
                dlExec.shutdown();
            }
        });
    }

    private static void promptInstallWithPermissionCheck(final Activity activity, final File apk) {
        if (needsInstallPermission(activity)) {
            new AlertDialog.Builder(activity)
                    .setTitle("需要授权安装未知应用")
                    .setMessage("安装包已下载并校验完成，请先允许 QuailNest 安装应用包，然后点击「立即安装」。")
                    .setPositiveButton("前往授权", (d, w) -> {
                        openInstallPermissionSettings(activity);
                        new AlertDialog.Builder(activity)
                                .setTitle("授权完成后继续")
                                .setMessage("返回本页面后，点击下方按钮即可直接安装更新。")
                                .setPositiveButton("立即安装", (d2, w2) -> triggerApkInstall(activity, apk))
                                .setNegativeButton("稍后", null)
                                .show();
                    })
                    .setNegativeButton("取消", null)
                    .show();
        } else {
            triggerApkInstall(activity, apk);
        }
    }

    public static void triggerApkInstall(Context context, File apkFile) {
        Uri uri = UpdateFileProvider.getUriForApk(context, apkFile.getName());
        Intent intent = new Intent(Intent.ACTION_VIEW);
        intent.setDataAndType(uri, "application/vnd.android.package-archive");
        intent.addFlags(Intent.FLAG_GRANT_READ_URI_PERMISSION | Intent.FLAG_ACTIVITY_NEW_TASK);
        context.startActivity(intent);
    }

    public static int compareVersions(String a, String b) {
        List<Long> va = parseNums(a);
        List<Long> vb = parseNums(b);
        int len = Math.max(va.size(), vb.size());
        for (int i = 0; i < len; i++) {
            long x = i < va.size() ? va.get(i) : 0L;
            long y = i < vb.size() ? vb.get(i) : 0L;
            if (x != y) return Long.compare(x, y);
        }
        return 0;
    }

    private static List<Long> parseNums(String raw) {
        List<Long> nums = new ArrayList<>();
        if (raw == null) return nums;
        String s = raw.trim();
        if (s.startsWith("v") || s.startsWith("V")) {
            s = s.substring(1);
        }
        for (String p : s.split("\\.")) {
            String digits = p.replaceAll("[^0-9].*$", "");
            if (!digits.isEmpty()) {
                try {
                    nums.add(Long.parseLong(digits));
                } catch (NumberFormatException ignored) {
                }
            }
        }
        return nums;
    }

    private static JSONObject fetchJson(String rawUrl, int timeoutMs) throws Exception {
        HttpURLConnection conn = null;
        try {
            conn = (HttpURLConnection) new URL(rawUrl).openConnection();
            conn.setRequestMethod("GET");
            conn.setConnectTimeout(timeoutMs);
            conn.setReadTimeout(timeoutMs);
            conn.setInstanceFollowRedirects(true);
            conn.setRequestProperty("Accept", "application/json");
            conn.setRequestProperty("User-Agent", "QuailNest-Android/" + CLIENT_VERSION);
            int code = conn.getResponseCode();
            if (code < 200 || code >= 300) {
                throw new Exception("HTTP " + code);
            }
            StringBuilder sb = new StringBuilder();
            try (BufferedReader br = new BufferedReader(new InputStreamReader(conn.getInputStream(), StandardCharsets.UTF_8))) {
                String line;
                while ((line = br.readLine()) != null) sb.append(line);
            }
            return new JSONObject(sb.toString());
        } finally {
            if (conn != null) conn.disconnect();
        }
    }

    private static void copyFile(File src, File dst) throws Exception {
        try (FileInputStream fis = new FileInputStream(src);
             FileOutputStream fos = new FileOutputStream(dst)) {
            byte[] buf = new byte[16384];
            int n;
            while ((n = fis.read(buf)) > 0) fos.write(buf, 0, n);
        }
    }

    private static String bytesToHex(byte[] bytes) {
        StringBuilder sb = new StringBuilder(bytes.length * 2);
        for (byte b : bytes) {
            sb.append(Character.forDigit((b >> 4) & 0xF, 16));
            sb.append(Character.forDigit(b & 0xF, 16));
        }
        return sb.toString();
    }
}
