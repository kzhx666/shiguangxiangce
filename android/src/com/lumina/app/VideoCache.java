package com.lumina.app;

import android.content.Context;

import org.json.JSONArray;
import org.json.JSONObject;

import java.io.File;
import java.util.ArrayList;
import java.util.Arrays;
import java.util.Comparator;
import java.util.List;

/**
 * 视频后台缓存：把还没缓存的视频慢慢下到本地，下次点开秒播。
 *
 * 特点：
 *  - 从小到大下，先让常用的小视频就位
 *  - 单个视频只用 2 条连接，避免把家里的网占满
 *  - 总缓存有上限（默认 6GB），超了自动删最旧的
 *  - 开关存在 Prefs，App 关掉就暂停，下次打开继续
 */
public class VideoCache {

    public static volatile boolean running = false;
    public static volatile int done = 0, total = 0;
    public static volatile String lastMsg = "";
    static Thread worker;

    static final long BUDGET = 6L * 1024 * 1024 * 1024;   // 6GB
    static final long MAX_ONE = 2L * 1024 * 1024 * 1024;  // 单个超过 2GB 的跳过

    public static File dir(Context c) {
        File d = new File(c.getCacheDir(), "videos");
        if (!d.exists()) d.mkdirs();
        return d;
    }

    public static File fileFor(Context c, long id) {
        return new File(dir(c), id + ".mp4");
    }

    public static boolean isCached(Context c, long id, long size) {
        File f = fileFor(c, id);
        return f.isFile() && f.length() > 0 && (size <= 0 || f.length() == size);
    }

    /** 统计已缓存大小（字节）。 */
    public static long usedBytes(Context c) {
        long sum = 0;
        File[] fs = dir(c).listFiles();
        if (fs != null) for (File f : fs) sum += f.length();
        return sum;
    }

    public static void start(final Context c) {
        if (running) return;
        running = true;
        Prefs.setVideoCache(c, true);
        worker = new Thread(new Runnable() {
            public void run() {
                try {
                    loop(c.getApplicationContext());
                } catch (Throwable t) {
                    lastMsg = "缓存中断：" + t;
                } finally {
                    running = false;
                }
            }
        }, "video-cache");
        worker.setPriority(Thread.MIN_PRIORITY);
        worker.start();
    }

    public static void stop(Context c) {
        running = false;
        Prefs.setVideoCache(c, false);
        if (worker != null) {
            try { worker.interrupt(); } catch (Throwable ignored) { }
            worker = null;
        }
        lastMsg = "已暂停";
    }

    static void loop(Context c) {
        List<JSONObject> vids = fetchVideos();
        // 从小到大
        vids.sort(new Comparator<JSONObject>() {
            public int compare(JSONObject a, JSONObject b) {
                return Long.compare(a.optLong("size"), b.optLong("size"));
            }
        });
        List<JSONObject> todo = new ArrayList<JSONObject>();
        for (JSONObject v : vids) {
            long id = v.optLong("id"), size = v.optLong("size");
            if (size <= 0 || size > MAX_ONE) continue;
            if (isCached(c, id, size)) continue;
            todo.add(v);
        }
        total = todo.size();
        done = 0;
        lastMsg = "待缓存 " + total + " 个";

        for (JSONObject v : todo) {
            if (!running) return;
            long id = v.optLong("id"), size = v.optLong("size");
            File f = fileFor(c, id);
            try {
                ensureBudget(c, size);
                Net.download(String.valueOf(id), size, f, new Net.Progress() {
                    public void onProgress(int pct) { }
                });
                done++;
                lastMsg = "已缓存 " + done + " / " + total;
            } catch (Throwable t) {
                try { f.delete(); } catch (Throwable ignored) { }
                done++;
            }
            // 慢慢来：每个之间歇 1 秒，别把路由器打满
            try { Thread.sleep(1000); } catch (InterruptedException ie) { return; }
        }
        lastMsg = "缓存完成：" + done + " 个";
    }

    static List<JSONObject> fetchVideos() {
        List<JSONObject> out = new ArrayList<JSONObject>();
        String cur = "";
        try {
            for (int page = 0; page < 20; page++) {
                String q = "/api/timeline?kind=video&limit=200"
                        + (cur.isEmpty() ? "" : "&before=" + java.net.URLEncoder.encode(cur, "UTF-8"));
                JSONObject o = new JSONObject(Api.get(q));
                JSONArray arr = o.optJSONArray("items");
                if (arr == null) break;
                for (int i = 0; i < arr.length(); i++) out.add(arr.getJSONObject(i));
                cur = o.optString("next", "");
                if (cur.isEmpty()) break;
            }
        } catch (Throwable ignored) { }
        return out;
    }

    /** 保证加上这个文件后不超过预算：超了就删最旧的缓存文件。 */
    static void ensureBudget(Context c, long add) {
        File[] fs = dir(c).listFiles();
        if (fs == null) return;
        long sum = 0;
        for (File f : fs) sum += f.length();
        if (sum + add <= BUDGET) return;
        Arrays.sort(fs, new Comparator<File>() {
            public int compare(File a, File b) {
                return Long.compare(a.lastModified(), b.lastModified());
            }
        });
        for (File f : fs) {
            if (sum + add <= BUDGET) break;
            long len = f.length();
            if (f.delete()) sum -= len;
        }
    }
}
