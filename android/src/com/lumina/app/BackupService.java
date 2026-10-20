package com.lumina.app;

import android.app.job.JobInfo;
import android.app.job.JobParameters;
import android.app.job.JobScheduler;
import android.app.job.JobService;
import android.content.ComponentName;
import android.content.Context;
import android.database.Cursor;
import android.net.Uri;
import android.provider.MediaStore;
import android.util.Log;

import java.util.ArrayList;
import java.util.HashSet;
import java.util.List;
import java.util.Set;

/**
 * 自动备份：系统级周期任务，仅 Wi-Fi 触发。
 * 扫 MediaStore 里的新照片/视频，逐个上传；已传过的记在 Prefs 里不重复传。
 */
public class BackupService extends JobService {
    static final int JOB_ID = 4711;
    private static final String TAG = "LuminaBackup";

    public static void schedule(Context c) {
        JobScheduler js = (JobScheduler) c.getSystemService(Context.JOB_SCHEDULER_SERVICE);
        JobInfo job = new JobInfo.Builder(JOB_ID, new ComponentName(c, BackupService.class))
                .setRequiredNetworkType(JobInfo.NETWORK_TYPE_UNMETERED)
                .setPeriodic(15 * 60 * 1000)
                .setPersisted(true)
                .build();
        js.schedule(job);
    }

    public static void cancel(Context c) {
        JobScheduler js = (JobScheduler) c.getSystemService(Context.JOB_SCHEDULER_SERVICE);
        js.cancel(JOB_ID);
    }

    public static volatile boolean isBackingUp = false;

    public static void triggerNow(final Context c) {
        if (isBackingUp) {
            Log.i(TAG, "triggerNow: already backing up, skipping");
            return;
        }
        new Thread(new Runnable() {
            public void run() {
                try {
                    isBackingUp = true;
                    new BackupService().runBackup(c);
                } catch (Throwable t) {
                    Log.e(TAG, "triggerNow error", t);
                } finally {
                    isBackingUp = false;
                }
            }
        }).start();
    }

    @Override public boolean onStartJob(final JobParameters p) {
        if (!Prefs.backupOn(this)) return false;
        final Context ctx = this;
        new Thread(new Runnable() {
            public void run() {
                try {
                    isBackingUp = true;
                    runBackup(ctx);
                } catch (Exception e) {
                    Log.e(TAG, "job run error", e);
                } finally {
                    isBackingUp = false;
                }
                jobFinished(p, false);
            }
        }).start();
        return true;
    }

    @Override public boolean onStopJob(JobParameters p) { return true; }

    void runBackup(Context c) {
        String owner = Prefs.owner(c);
        if (owner == null || owner.isEmpty()) owner = Prefs.user(c);
        Set<String> seen = new HashSet<>();
        String s = Prefs.seen(c);
        if (!s.isEmpty()) {
            for (String x : s.split(",")) {
                if (!x.isEmpty()) seen.add(x);
            }
        }
        Log.i(TAG, "runBackup start: owner=" + owner + ", seenCount=" + seen.size());

        List<Uri> found = new ArrayList<>();
        List<String> keys = new ArrayList<>();
        // 扫 MediaStore 里的未备份照片/视频，每轮扫描收集最多 1000 个未备份项
        scan(c, MediaStore.Images.Media.EXTERNAL_CONTENT_URI, "image/", found, keys, seen, 1000);
        scan(c, MediaStore.Video.Media.EXTERNAL_CONTENT_URI, "video/", found, keys, seen, 200);

        Log.i(TAG, "scan finished: found " + found.size() + " items to backup");

        int ok = 0;
        for (int i = 0; i < found.size(); i++) {
            Uri u = found.get(i);
            String key = keys.get(i);
            if (Uploader.upload(c, u, owner, "", "", 0)) {
                ok++;
                seen.add(key);
            }
            if ((i + 1) % 10 == 0 || i + 1 == found.size()) {
                Log.i(TAG, "Backup progress: " + (i + 1) + "/" + found.size() + " (ok=" + ok + ")");
            }
        }

        // 保存完整已备份列表，不再做破坏性的截断
        StringBuilder sb = new StringBuilder();
        for (String k : seen) {
            if (sb.length() > 0) sb.append(',');
            sb.append(k);
        }
        Prefs.setSeen(c, sb.toString());
        Log.i(TAG, "runBackup complete: uploaded " + ok + " items, total seen is now " + seen.size());
    }

    void scan(Context c, Uri uri, String mime, List<Uri> out, List<String> keys, Set<String> seen, int maxNew) {
        Cursor cur = null;
        try {
            cur = c.getContentResolver().query(uri, new String[]{MediaStore.MediaColumns._ID},
                    MediaStore.MediaColumns.MIME_TYPE + " LIKE ? OR " + MediaStore.MediaColumns.MIME_TYPE + " IS NULL",
                    new String[]{mime + "%"},
                    MediaStore.MediaColumns.DATE_MODIFIED + " DESC");
            if (cur != null) {
                int idCol = cur.getColumnIndex(MediaStore.MediaColumns._ID);
                // 持续向前遍历，跳过已备份项，直到集齐 maxNew 个未备份文件
                while (cur.moveToNext() && out.size() < maxNew) {
                    long id = cur.getLong(idCol);
                    String key = uri.getLastPathSegment() + ":" + id;
                    if (seen.contains(key)) continue;
                    out.add(Uri.withAppendedPath(uri, String.valueOf(id)));
                    keys.add(key);
                }
            }
        } catch (Exception e) {
            Log.e(TAG, "scan error for " + uri, e);
        } finally { if (cur != null) cur.close(); }
    }
}
