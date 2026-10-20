package com.lumina.app;

import android.content.Context;

import org.json.JSONArray;
import org.json.JSONObject;

import java.util.ArrayList;
import java.util.HashSet;
import java.util.LinkedHashMap;
import java.util.List;
import java.util.Map;
import java.util.Set;

/**
 * 备份引擎：扫描手机相册 → 问服务器哪些没有 → 逐个上传。
 * 界面上由 BackupScopeActivity 调用，前台服务 RunBackupService 也调它。
 */
public class BackupJob {

    public interface Progress { void onProgress(int done, int total); }

    public static volatile boolean running = false;
    public static volatile boolean paused = false;
    public static volatile int done = 0, total = 0, uploaded = 0;
    public static volatile String lastMsg = "";

    public static void setPaused(boolean p) { paused = p; }

    /** 扫描手机媒体，返回 [Uri, 相册名, 文件名, 大小, 修改时间, 是否视频]。 */
    static List<Object[]> scan(Context c, Set<String> onlyBuckets) {
        List<Object[]> out = new ArrayList<Object[]>();
        collect(c, android.provider.MediaStore.Images.Media.EXTERNAL_CONTENT_URI, out, onlyBuckets, false);
        collect(c, android.provider.MediaStore.Video.Media.EXTERNAL_CONTENT_URI, out, onlyBuckets, true);
        return out;
    }

    static void collect(Context c, android.net.Uri uri, List<Object[]> out,
                        Set<String> only, boolean video) {
        android.database.Cursor cur = null;
        try {
            cur = c.getContentResolver().query(uri, new String[]{
                    android.provider.MediaStore.MediaColumns._ID,
                    android.provider.MediaStore.MediaColumns.BUCKET_ID,
                    android.provider.MediaStore.MediaColumns.BUCKET_DISPLAY_NAME,
                    android.provider.MediaStore.MediaColumns.DISPLAY_NAME,
                    android.provider.MediaStore.MediaColumns.SIZE,
                    android.provider.MediaStore.MediaColumns.DATE_MODIFIED}, null, null,
                    android.provider.MediaStore.MediaColumns.DATE_MODIFIED + " DESC");
            if (cur == null) return;
            int idc = cur.getColumnIndex("_id"), bkc = cur.getColumnIndex("bucket_id");
            int bnc = cur.getColumnIndex("bucket_display_name"), nmc = cur.getColumnIndex("_display_name");
            int szc = cur.getColumnIndex("_size"), dtc = cur.getColumnIndex("date_modified");
            int n = 0;
            while (cur.moveToNext() && n < 60000) {
                String bk = bkc >= 0 ? String.valueOf(cur.getLong(bkc)) : "";
                if (only != null && !only.isEmpty() && !only.contains(bk)) continue;
                String bucket = bnc >= 0 && cur.getString(bnc) != null ? cur.getString(bnc) : "";
                String name = nmc >= 0 && cur.getString(nmc) != null ? cur.getString(nmc) : "m" + cur.getLong(idc);
                long size = szc >= 0 ? cur.getLong(szc) : 0L;
                long mt = dtc >= 0 ? cur.getLong(dtc) * 1000L : 0L;
                out.add(new Object[]{
                        android.net.Uri.withAppendedPath(uri, String.valueOf(cur.getLong(idc))),
                        bucket, name, size, mt, video});
                n++;
            }
        } catch (Throwable ignored) {
        } finally {
            if (cur != null) cur.close();
        }
    }

    /** 手机上的相册清单：bucket_id → 相册名。 */
    public static Map<String, String> buckets(Context c) {
        Map<String, String> m = new LinkedHashMap<String, String>();
        for (android.net.Uri uri : new android.net.Uri[]{
                android.provider.MediaStore.Images.Media.EXTERNAL_CONTENT_URI,
                android.provider.MediaStore.Video.Media.EXTERNAL_CONTENT_URI}) {
            android.database.Cursor cur = null;
            try {
                cur = c.getContentResolver().query(uri,
                        new String[]{"bucket_id", "bucket_display_name"}, null, null,
                        "bucket_display_name ASC");
                if (cur != null) {
                    while (cur.moveToNext()) {
                        String id = String.valueOf(cur.getLong(0));
                        String nm = cur.getString(1);
                        if (nm != null && !m.containsKey(id)) m.put(id, nm);
                    }
                }
            } catch (Throwable ignored) {
            } finally {
                if (cur != null) cur.close();
            }
        }
        return m;
    }

    /** 扫描结果：bucket_id -> {名字, 图片+视频总数, 封面id}，按数量从多到少。 */
    public static java.util.List<Object[]> scanFolderCounts(Context c) {
        final java.util.LinkedHashMap<String, long[]> cnt =
                new java.util.LinkedHashMap<String, long[]>();
        final java.util.LinkedHashMap<String, String> names =
                new java.util.LinkedHashMap<String, String>();
        final java.util.LinkedHashMap<String, Long> cover =
                new java.util.LinkedHashMap<String, Long>();
        android.net.Uri[] uris = new android.net.Uri[]{
                android.provider.MediaStore.Images.Media.EXTERNAL_CONTENT_URI,
                android.provider.MediaStore.Video.Media.EXTERNAL_CONTENT_URI};
        for (android.net.Uri uri : uris) {
            android.database.Cursor cur = null;
            try {
                cur = c.getContentResolver().query(uri,
                        new String[]{"bucket_id", "bucket_display_name", "_id"}, null, null,
                        "_id DESC");
                if (cur != null) {
                    while (cur.moveToNext()) {
                        String id = String.valueOf(cur.getLong(0));
                        String nm = cur.getString(1);
                        long mid = cur.getLong(2);
                        long[] cell = cnt.get(id);
                        if (cell == null) {
                            cell = new long[]{0};
                            cnt.put(id, cell);
                            names.put(id, nm == null ? "(未知)" : nm);
                            cover.put(id, mid);
                        }
                        cell[0]++;
                    }
                }
            } catch (Throwable ignored) {
            } finally {
                if (cur != null) cur.close();
            }
        }
        java.util.List<Object[]> out = new java.util.ArrayList<Object[]>();
        for (String id : cnt.keySet()) {
            out.add(new Object[]{id, names.get(id), cnt.get(id)[0], cover.get(id)});
        }
        out.sort(new java.util.Comparator<Object[]>() {
            public int compare(Object[] a, Object[] b) {
                return Long.compare((Long) b[2], (Long) a[2]);
            }
        });
        return out;
    }
    /** 跑一轮备份，返回 {上传成功数, 需要上传数}。 */
    public static int[] runNow(Context c, Progress cb) {
        if (running) return new int[]{0, 0};
        running = true;
        done = 0; total = 0; uploaded = 0;
        try {
            String owner = Prefs.owner(c);
            if (owner == null || owner.isEmpty()) owner = Prefs.user(c);

            Uploader.secretUpload = Prefs.backupSecret(c);   // 备份目标：普通 / 秘密
            List<Object[]> files = scan(c, Prefs.backupBuckets(c));
            lastMsg = "扫描到 " + files.size() + " 个文件";

            // 分批问服务器：这些「名字|大小」里哪些已经备份过
            Set<String> have = new HashSet<String>();
            JSONArray batch = new JSONArray();
            for (Object[] f : files) {
                JSONObject o = new JSONObject();
                o.put("n", f[2]);
                o.put("s", f[3]);
                batch.put(o);
                if (batch.length() >= 400) {
                    have.addAll(askServer(owner, batch));
                    batch = new JSONArray();
                }
            }
            if (batch.length() > 0) have.addAll(askServer(owner, batch));

            List<Object[]> need = new ArrayList<Object[]>();
            for (Object[] f : files) {
                if (!have.contains(f[2] + "|" + f[3])) need.add(f);
            }
            total = need.size();
            lastMsg = "需要上传 " + total + " 个";
            if (cb != null) cb.onProgress(0, total);

            int ok = 0;
            for (Object[] f : need) {
                while (paused) {
                    try { Thread.sleep(500); } catch (InterruptedException ie) { break; }
                }
                android.net.Uri u = (android.net.Uri) f[0];
                String bucket = (String) f[1];
                long mt = (Long) f[4];
                try {
                    if (Uploader.upload(c, u, owner, bucket, "", mt)) { ok++; uploaded = ok; }
                } catch (Throwable ignored) { }
                done++;
                if (cb != null) cb.onProgress(done, total);
            }
            lastMsg = "完成：上传 " + ok + " / " + total;
            return new int[]{ok, total};
        } catch (Exception e) {
            lastMsg = "出错：" + e.getMessage();
            return new int[]{0, 0};
        } finally {
            running = false;
        }
    }

    /** 问服务器这一批里哪些已经有了，返回 "名字|大小" 集合。 */
    static Set<String> askServer(String owner, JSONArray items) {
        Set<String> out = new HashSet<String>();
        try {
            JSONObject req = new JSONObject();
            req.put("owner", owner);
            req.put("items", items);
            JSONObject r = new JSONObject(Api.post("/api/backup/check", req.toString()));
            JSONArray have = r.optJSONArray("have");
            if (have != null) for (int i = 0; i < have.length(); i++) out.add(have.getString(i));
        } catch (Throwable ignored) { }
        return out;
    }
}
