package com.quailnest.client;

import android.content.ContentProvider;
import android.content.ContentValues;
import android.content.Context;
import android.database.Cursor;
import android.database.MatrixCursor;
import android.net.Uri;
import android.os.ParcelFileDescriptor;
import android.provider.OpenableColumns;

import java.io.File;
import java.io.FileNotFoundException;
import java.io.IOException;

/**
 * Pure Android SDK ContentProvider serving downloaded APK updates strictly from
 * {@code <cacheDir>/updates/} without requiring AndroidX FileProvider.
 */
public class UpdateFileProvider extends ContentProvider {
    private static final String UPDATES_DIR = "updates";
    private static final String APK_MIME = "application/vnd.android.package-archive";

    public static File getUpdatesDir(Context context) {
        File dir = new File(context.getCacheDir(), UPDATES_DIR);
        if (!dir.exists()) {
            //noinspection ResultOfMethodCallIgnored
            dir.mkdirs();
        }
        return dir;
    }

    public static Uri getUriForApk(Context context, String fileName) {
        return new Uri.Builder()
                .scheme("content")
                .authority(context.getPackageName() + ".updateprovider")
                .appendPath(fileName)
                .build();
    }

    @Override
    public boolean onCreate() {
        return true;
    }

    private File resolveApkFile(Uri uri) throws FileNotFoundException {
        Context ctx = getContext();
        if (ctx == null || uri == null) {
            throw new FileNotFoundException("Invalid context or URI");
        }
        String last = uri.getLastPathSegment();
        if (last == null || last.trim().isEmpty() || last.contains("..") || last.contains("/") || last.contains("\\")) {
            throw new FileNotFoundException("Invalid APK segment");
        }
        try {
            File baseDir = getUpdatesDir(ctx).getCanonicalFile();
            File target = new File(baseDir, last).getCanonicalFile();
            String basePrefix = baseDir.getPath();
            if (!basePrefix.endsWith(File.separator)) {
                basePrefix = basePrefix + File.separator;
            }
            if (!target.getPath().startsWith(basePrefix) || !target.isFile()) {
                throw new FileNotFoundException("APK not found in updates cache");
            }
            return target;
        } catch (IOException e) {
            throw new FileNotFoundException("Failed to resolve APK: " + e.getMessage());
        }
    }

    @Override
    public ParcelFileDescriptor openFile(Uri uri, String mode) throws FileNotFoundException {
        if (mode != null && (mode.contains("w") || mode.contains("t") || mode.contains("+"))) {
            throw new SecurityException("Read-only provider");
        }
        File file = resolveApkFile(uri);
        return ParcelFileDescriptor.open(file, ParcelFileDescriptor.MODE_READ_ONLY);
    }

    @Override
    public String getType(Uri uri) {
        return APK_MIME;
    }

    @Override
    public Cursor query(Uri uri, String[] projection, String selection, String[] selectionArgs, String sortOrder) {
        File file;
        try {
            file = resolveApkFile(uri);
        } catch (FileNotFoundException e) {
            return null;
        }
        String[] cols = (projection == null || projection.length == 0)
                ? new String[]{OpenableColumns.DISPLAY_NAME, OpenableColumns.SIZE}
                : projection;
        MatrixCursor cursor = new MatrixCursor(cols, 1);
        Object[] row = new Object[cols.length];
        for (int i = 0; i < cols.length; i++) {
            if (OpenableColumns.DISPLAY_NAME.equals(cols[i])) {
                row[i] = file.getName();
            } else if (OpenableColumns.SIZE.equals(cols[i])) {
                row[i] = file.length();
            }
        }
        cursor.addRow(row);
        return cursor;
    }

    @Override
    public Uri insert(Uri uri, ContentValues values) {
        throw new UnsupportedOperationException("Read-only provider");
    }

    @Override
    public int delete(Uri uri, String selection, String[] selectionArgs) {
        throw new UnsupportedOperationException("Read-only provider");
    }

    @Override
    public int update(Uri uri, ContentValues values, String selection, String[] selectionArgs) {
        throw new UnsupportedOperationException("Read-only provider");
    }
}
