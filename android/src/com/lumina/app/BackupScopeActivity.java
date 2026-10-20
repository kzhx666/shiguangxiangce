package com.lumina.app;

import android.content.Context;

import org.json.JSONArray;
import org.json.JSONObject;

import java.util.ArrayList;
import java.util.LinkedHashSet;
import java.util.List;
import java.util.Map;
import java.util.Set;

import android.app.Activity;

/**
 * 备份页：选相册范围、立即备份、备份对账、一键补预览。
 * 界面全部用代码搭，不依赖 XML。
 */
public class BackupScopeActivity extends Activity {

    android.widget.TextView stateText;
    android.widget.TextView countText;
    android.widget.LinearLayout bucketBox;
    final Set<String> selected = new LinkedHashSet<String>();
    final android.os.Handler ui = new android.os.Handler(android.os.Looper.getMainLooper());
    volatile boolean busy = false;
    android.widget.ProgressBar bar;
    android.widget.TextView cacheBtn;
    android.widget.TextView scanFolderTitle;
    android.widget.TextView backupBtn;
    java.util.List<Object[]> lastScan;

    @Override protected void onCreate(android.os.Bundle b) {
        super.onCreate(b);
        Api.base = Prefs.server(this);
        Api.cookie = Prefs.cookie(this);
        Api.user = Prefs.user(this);

        android.widget.ScrollView sv = new android.widget.ScrollView(this);
        android.widget.LinearLayout root = new android.widget.LinearLayout(this);
        root.setOrientation(android.widget.LinearLayout.VERTICAL);
        root.setBackgroundColor(0xFF0A0C11);
        root.setPadding(0, 40, 0, 60);
        sv.addView(root);
        setContentView(sv);

        android.widget.TextView title = Ui.label(this, "备份", 22, 0xFFFFFFFF);
        title.setPadding(28, 10, 28, 4);
        root.addView(title);

        stateText = Ui.label(this, "准备就绪", 12, 0xFF8A93A6);
        stateText.setPadding(28, 0, 28, 14);
        root.addView(stateText);

        countText = Ui.label(this, "", 12, 0xFF2DD4BF);
        countText.setPadding(28, 0, 28, 14);
        root.addView(countText);

        bar = new android.widget.ProgressBar(this, null, android.R.attr.progressBarStyleHorizontal);
        bar.setMax(100);
        bar.setProgress(0);
        android.widget.LinearLayout.LayoutParams blp = new android.widget.LinearLayout.LayoutParams(
                android.widget.LinearLayout.LayoutParams.MATCH_PARENT, dp(6));
        blp.setMargins(28, 0, 28, 8);
        root.addView(bar, blp);

        // 立即备份 / 暂停
        android.widget.LinearLayout row1 = new android.widget.LinearLayout(this);
        row1.setOrientation(android.widget.LinearLayout.HORIZONTAL);
        row1.setPadding(28, 0, 28, 12);

        final android.widget.TextView go = Ui.chipOn(this, "立即备份未上传的照片", true);
        go.setOnClickListener(new android.view.View.OnClickListener() {
            public void onClick(android.view.View v) { startBackup(go); }
        });
        final android.widget.TextView pause = Ui.chip(this, "暂停");
        pause.setOnClickListener(new android.view.View.OnClickListener() {
            public void onClick(android.view.View v) {
                boolean p = !BackupJob.paused;
                BackupJob.setPaused(p);
                pause.setText(p ? "继续" : "暂停");
            }
        });
        row1.addView(go);
        android.widget.LinearLayout.LayoutParams gap = new android.widget.LinearLayout.LayoutParams(
                android.widget.LinearLayout.LayoutParams.WRAP_CONTENT,
                android.widget.LinearLayout.LayoutParams.WRAP_CONTENT);
        gap.setMargins(12, 0, 0, 0);
        row1.addView(pause, gap);
        root.addView(row1);

        // 备份对账
        android.widget.TextView check = Ui.chip(this, "备份对账");
        android.widget.LinearLayout.LayoutParams clp = new android.widget.LinearLayout.LayoutParams(
                android.widget.LinearLayout.LayoutParams.MATCH_PARENT,
                android.widget.LinearLayout.LayoutParams.WRAP_CONTENT);
        clp.setMargins(28, 0, 28, 12);
        check.setOnClickListener(new android.view.View.OnClickListener() {
            public void onClick(android.view.View v) {
                bar.setIndeterminate(true);
                stateText.setText("正在对账…");
                new Thread(new Runnable() { public void run() { reconcile(); } }).start();
            }
        });
        root.addView(check, clp);

        // 一键补预览
        android.widget.TextView pv = Ui.chip(this, "一键补预览（大图）");
        android.widget.LinearLayout.LayoutParams plp = new android.widget.LinearLayout.LayoutParams(
                android.widget.LinearLayout.LayoutParams.MATCH_PARENT,
                android.widget.LinearLayout.LayoutParams.WRAP_CONTENT);
        plp.setMargins(28, 0, 28, 12);
        pv.setOnClickListener(new android.view.View.OnClickListener() {
            public void onClick(android.view.View v) {
                new Thread(new Runnable() { public void run() { batchPreview((android.widget.TextView) v); } }).start();
            }
        });
        root.addView(pv, plp);

        // 补视频封面
        android.widget.TextView vp = Ui.chip(this, "补视频封面");
        android.widget.LinearLayout.LayoutParams vlp = new android.widget.LinearLayout.LayoutParams(
                android.widget.LinearLayout.LayoutParams.MATCH_PARENT,
                android.widget.LinearLayout.LayoutParams.WRAP_CONTENT);
        vlp.setMargins(28, 0, 28, 12);
        vp.setOnClickListener(new android.view.View.OnClickListener() {
            public void onClick(android.view.View v) {
                new Thread(new Runnable() { public void run() {
                    batchVideoPoster((android.widget.TextView) v);
                } }).start();
            }
        });
        root.addView(vp, vlp);

        // 选择手机上的任意文件夹来备份（SAF，能选到相册以外的目录）
        android.widget.TextView pickDir = Ui.chip(this, "选择手机文件夹备份…");
        android.widget.LinearLayout.LayoutParams pdlp = new android.widget.LinearLayout.LayoutParams(
                android.widget.LinearLayout.LayoutParams.MATCH_PARENT,
                android.widget.LinearLayout.LayoutParams.WRAP_CONTENT);
        pdlp.setMargins(28, 0, 28, 12);
        pickDir.setOnClickListener(new android.view.View.OnClickListener() {
            public void onClick(android.view.View v) {
                Uploader.secretUpload = Prefs.backupSecret(BackupScopeActivity.this);
                Uploader.pickFolder(BackupScopeActivity.this);
            }
        });
        root.addView(pickDir, pdlp);

        // 备份到秘密相册
        final android.widget.TextView toSecret = Ui.chip(this,
                Prefs.backupSecret(this) ? "备份到秘密相册：已开启" : "备份到秘密相册：关闭");
        android.widget.LinearLayout.LayoutParams tslp = new android.widget.LinearLayout.LayoutParams(
                android.widget.LinearLayout.LayoutParams.MATCH_PARENT,
                android.widget.LinearLayout.LayoutParams.WRAP_CONTENT);
        tslp.setMargins(28, 0, 28, 12);
        toSecret.setOnClickListener(new android.view.View.OnClickListener() {
            public void onClick(android.view.View v) {
                boolean on = !Prefs.backupSecret(BackupScopeActivity.this);
                Prefs.setBackupSecret(BackupScopeActivity.this, on);
                Uploader.secretUpload = on;
                toSecret.setText(on ? "备份到秘密相册：已开启" : "备份到秘密相册：关闭");
            }
        });
        root.addView(toSecret, tslp);

        // 视频后台缓存
        final android.widget.TextView vc = Ui.chip(this, "视频后台缓存：关闭");
        android.widget.LinearLayout.LayoutParams vclp = new android.widget.LinearLayout.LayoutParams(
                android.widget.LinearLayout.LayoutParams.MATCH_PARENT,
                android.widget.LinearLayout.LayoutParams.WRAP_CONTENT);
        vclp.setMargins(28, 0, 28, 12);
        vc.setOnClickListener(new android.view.View.OnClickListener() {
            public void onClick(android.view.View v) {
                if (VideoCache.running) {
                    VideoCache.stop(BackupScopeActivity.this);
                    vc.setText("视频后台缓存：关闭");
                } else {
                    VideoCache.start(BackupScopeActivity.this);
                    vc.setText("视频后台缓存：已开启");
                }
                refreshCacheStat(vc);
            }
        });
        root.addView(vc, vclp);

        final android.widget.TextView delCache = Ui.chip(this, "删除视频缓存");
        android.widget.LinearLayout.LayoutParams dclp = new android.widget.LinearLayout.LayoutParams(
                android.widget.LinearLayout.LayoutParams.MATCH_PARENT,
                android.widget.LinearLayout.LayoutParams.WRAP_CONTENT);
        dclp.setMargins(28, 0, 28, 12);
        delCache.setOnClickListener(new android.view.View.OnClickListener() {
            public void onClick(android.view.View v) {
                final long before = VideoCache.usedBytes(BackupScopeActivity.this);
                if (before <= 0) {
                    android.widget.Toast.makeText(BackupScopeActivity.this, "还没有缓存",
                            android.widget.Toast.LENGTH_SHORT).show();
                    return;
                }
                new android.app.AlertDialog.Builder(BackupScopeActivity.this)
                        .setTitle("删除视频缓存？")
                        .setMessage("会释放 " + (before / 1048576L) + " MB 手机空间。"
                                + "视频本身在服务器上，不会丢；下次看会重新缓冲。")
                        .setPositiveButton("删除", new android.content.DialogInterface.OnClickListener() {
                            public void onClick(android.content.DialogInterface d, int w) {
                                clearVideoCache();
                                final long after = VideoCache.usedBytes(BackupScopeActivity.this);
                                ui.post(new Runnable() { public void run() {
                                    android.widget.Toast.makeText(BackupScopeActivity.this,
                                            "已释放 " + ((before - after) / 1048576L) + " MB",
                                            android.widget.Toast.LENGTH_LONG).show();
                                }});
                                refreshCacheStat(delCache);
                            }
                        })
                        .setNegativeButton("取消", null).show();
            }
        });
        cacheBtn = delCache;
        root.addView(delCache, dclp);

        refreshCacheStat(vc);

        // 一键扫描：列出手机里所有含图片/视频的文件夹
        android.widget.TextView scanBtn = Ui.chipOn(this, "一键扫描全部图片视频", true);
        android.widget.LinearLayout.LayoutParams sblp = new android.widget.LinearLayout.LayoutParams(
                android.widget.LinearLayout.LayoutParams.MATCH_PARENT,
                android.widget.LinearLayout.LayoutParams.WRAP_CONTENT);
        sblp.setMargins(28, 0, 28, 12);
        scanBtn.setOnClickListener(new android.view.View.OnClickListener() {
            public void onClick(android.view.View v) { doScan(); }
        });
        root.addView(scanBtn, sblp);

        // 相册范围
        scanFolderTitle = Ui.label(this, "备份范围（不选=全部）", 13, 0xFFE8ECF4);
        scanFolderTitle.setPadding(28, 16, 28, 8);
        root.addView(scanFolderTitle);

        bucketBox = new android.widget.LinearLayout(this);
        bucketBox.setOrientation(android.widget.LinearLayout.VERTICAL);
        bucketBox.setPadding(28, 0, 28, 10);
        root.addView(bucketBox);

        android.widget.LinearLayout row2 = new android.widget.LinearLayout(this);
        row2.setOrientation(android.widget.LinearLayout.HORIZONTAL);
        row2.setPadding(28, 8, 28, 8);
        android.widget.TextView all = Ui.chip(this, "全选");
        all.setOnClickListener(new android.view.View.OnClickListener() {
            public void onClick(android.view.View v) { selectAll(true); }
        });
        android.widget.TextView none = Ui.chip(this, "清空");
        none.setOnClickListener(new android.view.View.OnClickListener() {
            public void onClick(android.view.View v) { selectAll(false); }
        });
        row2.addView(all);
        android.widget.LinearLayout.LayoutParams g2 = new android.widget.LinearLayout.LayoutParams(
                android.widget.LinearLayout.LayoutParams.WRAP_CONTENT,
                android.widget.LinearLayout.LayoutParams.WRAP_CONTENT);
        g2.setMargins(12, 0, 0, 0);
        row2.addView(none, g2);
        root.addView(row2);

        loadBuckets();
        askMediaPerm2();
    }

    void loadBuckets() {
        selected.clear();
        selected.addAll(Prefs.backupBuckets(this));
        bucketBox.removeAllViews();
        final Map<String, String> bs = BackupJob.buckets(this);
        countText.setText(bs.size() + " 个相册");
        for (final Map.Entry<String, String> e : bs.entrySet()) {
            final android.widget.TextView chip = Ui.chipOn(this,
                    e.getValue(), selected.contains(e.getKey()));
            android.widget.LinearLayout.LayoutParams lp = new android.widget.LinearLayout.LayoutParams(
                    android.widget.LinearLayout.LayoutParams.WRAP_CONTENT,
                    android.widget.LinearLayout.LayoutParams.WRAP_CONTENT);
            lp.setMargins(0, 0, 10, 10);
            chip.setOnClickListener(new android.view.View.OnClickListener() {
                public void onClick(android.view.View v) {
                    if (selected.contains(e.getKey())) selected.remove(e.getKey());
                    else selected.add(e.getKey());
                    Prefs.setBackupBuckets(BackupScopeActivity.this, selected);
                    loadBuckets();
        askMediaPerm2();
                }
            });
            bucketBox.addView(chip, lp);
        }
    }

    void selectAll(boolean on) {
        selected.clear();
        if (on) {
            if (lastScan != null && !lastScan.isEmpty()) {
                for (Object[] o : lastScan) selected.add((String) o[0]);
            } else {
                selected.addAll(BackupJob.buckets(this).keySet());
            }
        }
        Prefs.setBackupBuckets(this, selected);
        // 有扫描结果就用扫描列表重绘，否则回退到相册列表
        if (lastScan != null && !lastScan.isEmpty()) showFolders(lastScan);
        else loadBuckets();
    }

    void startBackup(final android.widget.TextView go) {
        if (busy) return;
        busy = true;
        go.setText("备份中…");
        new Thread(new Runnable() {
            public void run() {
                int[] r = BackupJob.runNow(BackupScopeActivity.this, new BackupJob.Progress() {
                    public void onProgress(final int d, final int t) {
                        ui.post(new Runnable() { public void run() {
                            stateText.setText("进度 " + d + " / " + t);
                            bar.setProgress(t > 0 ? (int) (d * 100L / t) : 0);
                            go.setText("备份中 " + d + "/" + t);
                        }});
                    }
                });
                final int[] rr = r;
                ui.post(new Runnable() { public void run() {
                    busy = false;
                    stateText.setText(BackupJob.lastMsg);
                    bar.setProgress(100);
                    go.setText("立即备份未上传的照片");
                    android.widget.Toast.makeText(BackupScopeActivity.this,
                            "上传 " + rr[0] + " 个，待传 " + rr[1] + " 个",
                            android.widget.Toast.LENGTH_LONG).show();
                }});
            }
        }).start();
    }

    /** 对账：手机有多少、服务器有多少、差在哪些相册。 */
    void reconcile() {
        try {
            String owner = Prefs.owner(this);
            if (owner == null || owner.isEmpty()) owner = Prefs.user(this);
            List<Object[]> files = BackupJob.scan(this, null);
            Map<String, Integer> missByBucket = new java.util.LinkedHashMap<String, Integer>();
            int miss = 0;
            org.json.JSONArray batch = new org.json.JSONArray();
            List<Object[]> pend = new ArrayList<Object[]>();
            for (Object[] f : files) {
                JSONObject o = new JSONObject();
                o.put("n", f[2]); o.put("s", f[3]);
                batch.put(o); pend.add(f);
                if (batch.length() >= 400) {
                    miss += countMissing(owner, batch, pend, missByBucket);
                    batch = new org.json.JSONArray(); pend.clear();
                }
            }
            if (batch.length() > 0) miss += countMissing(owner, batch, pend, missByBucket);

            final StringBuilder sb = new StringBuilder();
            sb.append("手机媒体：").append(files.size()).append(" 个\n");
            sb.append("未备份：").append(miss).append(" 个\n");
            for (Map.Entry<String, Integer> e : missByBucket.entrySet()) {
                sb.append("  ").append(e.getKey()).append("：").append(e.getValue()).append(" 个\n");
            }
            final String msg = sb.toString();
            ui.post(new Runnable() { public void run() {
                bar.setIndeterminate(false);
                bar.setProgress(100);
                stateText.setText("对账完成");
            }});
            ui.post(new Runnable() { public void run() {
                new android.app.AlertDialog.Builder(BackupScopeActivity.this)
                        .setTitle("备份对账").setMessage(msg)
                        .setPositiveButton("知道了", null).show();
            }});
        } catch (Exception e) {
            final String m = e.getMessage();
            ui.post(new Runnable() { public void run() {
                android.widget.Toast.makeText(BackupScopeActivity.this, "对账失败：" + m,
                        android.widget.Toast.LENGTH_LONG).show();
            }});
        }
    }

    int countMissing(String owner, org.json.JSONArray batch, List<Object[]> files,
                     Map<String, Integer> byBucket) {
        int n = 0;
        try {
            JSONObject req = new JSONObject();
            req.put("owner", owner);
            req.put("items", batch);
            JSONObject r = new JSONObject(Api.post("/api/backup/check", req.toString()));
            JSONArray missing = r.optJSONArray("missing");
            if (missing != null) {
                for (int i = 0; i < missing.length(); i++) {
                    int idx = missing.getInt(i);
                    if (idx < 0 || idx >= files.size()) continue;
                    String bucket = (String) files.get(idx)[1];
                    Integer c = byBucket.get(bucket);
                    byBucket.put(bucket, c == null ? 1 : c + 1);
                    n++;
                }
            }
        } catch (Throwable ignored) { }
        return n;
    }

    /** 一键补预览：服务器上那些超大图，用手机生成 1280 小预览传回去。 */
    void batchPreview(final android.widget.TextView btn) {
        List<long[]> ids = new ArrayList<long[]>();
        List<String> names = new ArrayList<String>();
        try {
            JSONObject r = new JSONObject(Api.get("/api/preview/needed"));
            JSONArray arr = r.optJSONArray("items");
            if (arr == null || arr.length() == 0) {
                ui.post(new Runnable() { public void run() {
                    btn.setText("一键补预览（暂无需要补的大图）"); } });
                return;
            }
            for (int i = 0; i < arr.length(); i++) {
                JSONObject o = arr.optJSONObject(i);
                if (o == null) continue;
                ids.add(new long[]{o.optLong("id")});
                names.add(o.optString("name"));
            }
        } catch (Exception e) {
            final String m = e.getMessage();
            ui.post(new Runnable() { public void run() {
                android.widget.Toast.makeText(BackupScopeActivity.this, "取不到列表：" + m,
                        android.widget.Toast.LENGTH_LONG).show(); } });
            return;
        }

        final int total = ids.size();
        int ok = 0;
        for (int i = 0; i < total; i++) {
            final int k = i + 1;
            byte[] body = Uploader.makePreview(this, names.get(i), ids.get(i)[0]);
            if (body != null && body.length > 0) {
                try { Api.postPoster(ids.get(i)[0], body); ok++; } catch (Throwable ignored) { }
            }
            final int okk = ok;
            ui.post(new Runnable() { public void run() {
                btn.setText("补预览 " + k + " / " + total + "（成功 " + okk + "）"); } });
        }
        final int okf = ok;
        ui.post(new Runnable() { public void run() {
            btn.setText("一键补预览（大图）");
            android.widget.Toast.makeText(BackupScopeActivity.this,
                    "补预览完成：成功 " + okf + " / " + total, android.widget.Toast.LENGTH_LONG).show();
        }});
    }

    int dp(int v) { return (int) (v * getResources().getDisplayMetrics().density); }
    /**
     * 补视频封面：逐个把视频下载到临时文件 → 手机截一帧 → 传给服务器当封面 → 删掉临时文件。
     * 路由器没有视频解码器，封面只能这样生成。
     */
    void batchVideoPoster(final android.widget.TextView btn) {
        java.util.List<long[]> ids = new java.util.ArrayList<long[]>();
        try {
            org.json.JSONObject r = new org.json.JSONObject(Api.get("/api/videos/noposter"));
            org.json.JSONArray arr = r.optJSONArray("items");
            if (arr == null || arr.length() == 0) {
                ui.post(new Runnable() { public void run() {
                    btn.setText("补视频封面（都齐了）"); } });
                return;
            }
            for (int i = 0; i < arr.length(); i++) {
                org.json.JSONObject o = arr.optJSONObject(i);
                if (o == null) continue;
                ids.add(new long[]{o.optLong("id"), o.optLong("size")});
            }
        } catch (Exception e) {
            final String m = e.getMessage();
            ui.post(new Runnable() { public void run() {
                android.widget.Toast.makeText(BackupScopeActivity.this, "取不到列表：" + m,
                        android.widget.Toast.LENGTH_LONG).show(); } });
            return;
        }

        final int total = ids.size();
        int ok = 0;
        for (int i = 0; i < total; i++) {
            final int k = i + 1;
            long id = ids.get(i)[0];
            final java.io.File tmp = new java.io.File(getCacheDir(), "poster_" + id + ".tmp");
            try {
                Net.download(String.valueOf(id), ids.get(i)[1], tmp, new Net.Progress() {
                    public void onProgress(final int pct) {
                        ui.post(new Runnable() { public void run() {
                            btn.setText("下载 " + k + " / " + total + " · " + pct + "%"); } });
                    }
                });
                byte[] body = frameFromFile(tmp);
                if (body != null && body.length > 0) {
                    Api.postPoster(id, body);
                    try { MainActivity.dropThumb(id); } catch (Throwable ignored) { }
                    ok++;
                }
            } catch (Throwable ignored) {
            } finally {
                try { tmp.delete(); } catch (Throwable ignored) { }
            }
            final int okk = ok;
            ui.post(new Runnable() { public void run() {
                btn.setText("补视频封面 " + k + " / " + total + "（成功 " + okk + "）"); } });
        }
        final int okf = ok;
        ui.post(new Runnable() { public void run() {
            btn.setText("补视频封面");
            android.widget.Toast.makeText(BackupScopeActivity.this,
                    "补封面完成：成功 " + okf + " / " + total,
                    android.widget.Toast.LENGTH_LONG).show();
        }});
    }

    /** 流式下载视频到临时文件，进度显示在按钮上。 */
    void downloadVideo(long id, java.io.File dst, final android.widget.TextView btn,
                       final int idx, final int total) throws Exception {
        java.io.File dir = dst.getParentFile();
        if (dir != null && !dir.exists()) dir.mkdirs();
        java.net.HttpURLConnection c = (java.net.HttpURLConnection)
                new java.net.URL(Api.base + "/m/" + id).openConnection();
        c.setConnectTimeout(15000);
        c.setReadTimeout(120000);
        if (!Api.cookie.isEmpty()) c.setRequestProperty("Cookie", Api.cookie);
        if (c.getResponseCode() >= 400) throw new Exception("HTTP " + c.getResponseCode());
        long size = c.getContentLength();
        java.io.InputStream in = c.getInputStream();
        java.io.FileOutputStream fo = new java.io.FileOutputStream(dst);
        byte[] buf = new byte[131072];
        long got = 0;
        int n, lastPct = -1;
        while ((n = in.read(buf)) > 0) {
            fo.write(buf, 0, n);
            got += n;
            if (size > 0) {
                final int pct = (int) (got * 100 / size);
                if (pct != lastPct && pct % 5 == 0) {
                    lastPct = pct;
                    final int p = pct;
                    ui.post(new Runnable() { public void run() {
                        btn.setText("下载 " + idx + "/" + total + " · " + p + "%"); } });
                }
            }
        }
        in.close();
        fo.close();
        c.disconnect();
    }

    /** 从本地视频文件截一帧，缩到 1280 压成 JPEG。 */
    byte[] frameFromFile(java.io.File f) {
        android.media.MediaMetadataRetriever r = new android.media.MediaMetadataRetriever();
        try {
            r.setDataSource(f.getAbsolutePath());
            android.graphics.Bitmap bm = r.getFrameAtTime(1500000,
                    android.media.MediaMetadataRetriever.OPTION_CLOSEST_SYNC);
            if (bm == null) bm = r.getFrameAtTime(0);
            if (bm == null) return null;
            int w = bm.getWidth(), h = bm.getHeight();
            float sc = Math.min(1f, 1280f / Math.max(w, h));
            android.graphics.Bitmap small = sc < 1f
                    ? android.graphics.Bitmap.createScaledBitmap(bm,
                        Math.max(1, Math.round(w * sc)), Math.max(1, Math.round(h * sc)), true)
                    : bm;
            java.io.ByteArrayOutputStream bo = new java.io.ByteArrayOutputStream();
            small.compress(android.graphics.Bitmap.CompressFormat.JPEG, 85, bo);
            if (small != bm) small.recycle();
            bm.recycle();
            return bo.toByteArray();
        } catch (Throwable t) {
            return null;
        } finally {
            try { r.release(); } catch (Throwable ignored) { }
        }
    }

    /** 定时刷新缓存状态文字。 */
    void refreshCacheStat(final android.widget.TextView vc) {
        ui.postDelayed(new Runnable() {
            public void run() {
                if (isFinishing()) return;
                long used = VideoCache.usedBytes(BackupScopeActivity.this) / 1048576L;
                String msg;
                if (VideoCache.running) {
                    msg = "视频后台缓存：已开启 · " + VideoCache.lastMsg + " · 已用 " + used + " MB";
                } else {
                    msg = "视频后台缓存：关闭 · 已用 " + used + " MB";
                }
                if (cacheBtn != null) cacheBtn.setText("删除视频缓存（" + used + " MB）");
                vc.setText(msg);
                refreshCacheStat(vc);
            }
        }, 2000);
    }

    /** 清空手机里的视频缓存。 */
    void clearVideoCache() {
        try {
            java.io.File d = new java.io.File(getCacheDir(), "videos");
            java.io.File[] fs = d.listFiles();
            if (fs != null) for (java.io.File f : fs) f.delete();
        } catch (Throwable ignored) { }
    }

    @Override protected void onActivityResult(int req, int res, android.content.Intent data) {
        super.onActivityResult(req, res, data);
        Uploader.handleResult(this, req, res, data);
    }

    /** 一键扫描：列出所有含图片/视频的文件夹（名称 + 数量），可全选后直接备份。 */
    void doScan() {
        scanFolderTitle.setText("正在扫描手机…");
        new Thread(new Runnable() {
            public void run() {
                final java.util.List<Object[]> list = BackupJob.scanFolderCounts(BackupScopeActivity.this);
                lastScan = list;
                long totalItems = 0;
                for (Object[] o : list) totalItems += (Long) o[2];
                final long t = totalItems;
                ui.post(new Runnable() {
                    public void run() {
                        scanFolderTitle.setText("扫描到 " + list.size() + " 个文件夹 · "
                                + t + " 个图片/视频");
                        showFolders(list);
                    }
                });
            }
        }).start();
    }

    /** 把扫描结果渲染成可点的文件夹列表。 */
    void showFolders(java.util.List<Object[]> list) {
        bucketBox.removeAllViews();
        if (list.isEmpty()) {
            bucketBox.addView(Ui.label(this, "没扫描到任何图片或视频", 12, 0xFF8A93A6));
            return;
        }
        // 顶部再放一组全选/清空，免得要滑到最底部
        android.widget.LinearLayout quick = new android.widget.LinearLayout(this);
        quick.setOrientation(android.widget.LinearLayout.HORIZONTAL);
        quick.setPadding(0, 6, 0, 10);
        android.widget.TextView qa = Ui.chip(this, "全选");
        qa.setOnClickListener(new android.view.View.OnClickListener() {
            public void onClick(android.view.View v) { selectAll(true); }
        });
        android.widget.TextView qn = Ui.chip(this, "清空");
        qn.setOnClickListener(new android.view.View.OnClickListener() {
            public void onClick(android.view.View v) { selectAll(false); }
        });
        quick.addView(qa);
        android.widget.LinearLayout.LayoutParams qlp = new android.widget.LinearLayout.LayoutParams(
                android.widget.LinearLayout.LayoutParams.WRAP_CONTENT,
                android.widget.LinearLayout.LayoutParams.WRAP_CONTENT);
        qlp.setMargins(12, 0, 0, 0);
        quick.addView(qn, qlp);
        bucketBox.addView(quick);
        for (final Object[] o : list) {
            final String bid = (String) o[0];
            String nm = (String) o[1];
            long n = (Long) o[2];
            android.widget.LinearLayout row = new android.widget.LinearLayout(this);
            row.setOrientation(android.widget.LinearLayout.HORIZONTAL);
            row.setGravity(android.view.Gravity.CENTER_VERTICAL);
            row.setPadding(0, 14, 0, 14);
            boolean on = selected.contains(bid);
            android.widget.TextView name = Ui.label(this,
                    (on ? "☑ " : "☐ ") + nm + "   " + n + " 项", 13,
                    on ? 0xFF2DD4BF : 0xFFE8ECF4);
            row.addView(name, new android.widget.LinearLayout.LayoutParams(0,
                    android.widget.LinearLayout.LayoutParams.WRAP_CONTENT, 1f));
            row.setOnClickListener(new android.view.View.OnClickListener() {
                public void onClick(android.view.View v) {
                    if (selected.contains(bid)) selected.remove(bid); else selected.add(bid);
                    Prefs.setBackupBuckets(BackupScopeActivity.this, selected);
                    showFolders(list);
                }
            });
            bucketBox.addView(row);
        }
        final android.widget.TextView backupBtn2 = Ui.chipOn(this, "备份选中的文件夹", true);
        android.widget.LinearLayout.LayoutParams glp = new android.widget.LinearLayout.LayoutParams(
                android.widget.LinearLayout.LayoutParams.MATCH_PARENT,

                android.widget.LinearLayout.LayoutParams.WRAP_CONTENT);
        glp.setMargins(0, 12, 0, 12);
        backupBtn2.setOnClickListener(new android.view.View.OnClickListener() {
            public void onClick(android.view.View v) { startBackup(backupBtn2); }
        });
        bucketBox.addView(backupBtn2, glp);
    }

    /** 备份页也确保有媒体权限，否则列表是 0 个相册。 */
    void askMediaPerm2() {
        try {
            if (android.os.Build.VERSION.SDK_INT < 23) return;
            java.util.List<String> need = new java.util.ArrayList<String>();
            String[] want = android.os.Build.VERSION.SDK_INT >= 33
                    ? new String[]{"android.permission.READ_MEDIA_IMAGES",
                                   "android.permission.READ_MEDIA_VIDEO"}
                    : new String[]{"android.permission.READ_EXTERNAL_STORAGE"};
            for (String w : want) {
                if (checkSelfPermission(w) != android.content.pm.PackageManager.PERMISSION_GRANTED) {
                    need.add(w);
                }
            }
            if (!need.isEmpty()) requestPermissions(need.toArray(new String[0]), 1002);
        } catch (Throwable ignored) { }
    }

    @Override public void onRequestPermissionsResult(int req, String[] p, int[] g) {
        super.onRequestPermissionsResult(req, p, g);
        try { loadBuckets(); } catch (Throwable ignored) { }
    }
}
