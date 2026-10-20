package com.lumina.app;

import java.io.File;
import java.io.RandomAccessFile;
import java.net.HttpURLConnection;
import java.net.URL;

/**
 * 多线程分段下载。
 *
 * 实测这台路由器单条 TCP 连接只有 ~1.3MB/s，但 4 条并发合计能到 ~6MB/s
 * （链路是通的，瓶颈在单连接的窗口/整形）。所以下载视频一律分段并发，
 * 播放前先落到本地，本地播放就不会卡。
 */
public class Net {

    public interface Progress { void onProgress(int pct); }

    static final int THREADS = 8;   // 8 条并发约 9.5MB/s

    /** 分段并发下载；size<=0 时先探测长度。 */
    public static void download(String id, long size, File dst, Progress cb) throws Exception {
        File dir = dst.getParentFile();
        if (dir != null && !dir.exists()) dir.mkdirs();

        if (size <= 0) size = contentLength(id);
        if (size <= 0) throw new Exception("拿不到文件大小");

        final long total = size;
        final File tmp = new File(dst.getAbsolutePath() + ".part");
        if (tmp.exists()) tmp.delete();
        RandomAccessFile raf = new RandomAccessFile(tmp, "rw");
        raf.setLength(total);
        raf.close();

        int n = (int) Math.min(THREADS, Math.max(1, total / (1024 * 1024)));
        if (n < 1) n = 1;
        long per = total / n;

        final long[] got = new long[n];
        final Exception[] err = new Exception[1];
        Thread[] ts = new Thread[n];
        for (int i = 0; i < n; i++) {
            final int idx = i;
            final long from = i * per;
            final long to = (i == n - 1) ? total - 1 : (from + per - 1);
            ts[i] = new Thread(new Runnable() {
                public void run() {
                    try {
                        fetchRange(id, from, to, tmp, idx, got, total, cb);
                    } catch (Exception e) {
                        if (err[0] == null) err[0] = e;
                    }
                }
            });
            ts[i].start();
        }
        for (Thread t : ts) t.join();
        if (err[0] != null) {
            tmp.delete();
            throw err[0];
        }
        if (dst.exists()) dst.delete();
        if (!tmp.renameTo(dst)) throw new Exception("rename failed");
    }

    static void fetchRange(String id, long from, long to, File dst, int idx,
                           long[] got, long total, Progress cb) throws Exception {
        HttpURLConnection c = (HttpURLConnection)
                new URL(Api.base + "/m/" + id).openConnection();
        c.setConnectTimeout(15000);
        c.setReadTimeout(120000);
        if (!Api.cookie.isEmpty()) c.setRequestProperty("Cookie", Api.cookie);
        c.setRequestProperty("Range", "bytes=" + from + "-" + to);
        int code = c.getResponseCode();
        if (code >= 400) throw new Exception("HTTP " + code);

        byte[] buf = new byte[131072];
        long pos = from;
        int n;
        int lastPct = -1;
        java.io.InputStream in = c.getInputStream();
        RandomAccessFile raf = new RandomAccessFile(dst, "rw");
        try {
            while ((n = in.read(buf)) > 0) {
                raf.seek(pos);
                raf.write(buf, 0, n);
                pos += n;
                synchronized (got) {
                    got[idx] += n;
                    long sum = 0;
                    for (long g : got) sum += g;
                    if (cb != null) {
                        int pct = (int) (sum * 100 / total);
                        if (pct != lastPct) {
                            lastPct = pct;
                            cb.onProgress(pct);
                        }
                    }
                }
                if (pos > to) break;
            }
        } finally {
            try { raf.close(); } catch (Exception ignored) { }
            try { in.close(); } catch (Exception ignored) { }
            c.disconnect();
        }
    }

    static long contentLength(String id) throws Exception {
        HttpURLConnection c = (HttpURLConnection)
                new URL(Api.base + "/m/" + id).openConnection();
        c.setRequestMethod("HEAD");
        c.setConnectTimeout(15000);
        c.setReadTimeout(30000);
        if (!Api.cookie.isEmpty()) c.setRequestProperty("Cookie", Api.cookie);
        long len = c.getContentLength();
        c.disconnect();
        return len;
    }
}
