package com.lumina.app;

import java.io.File;
import java.io.InputStream;
import java.io.OutputStream;
import java.io.RandomAccessFile;
import java.net.HttpURLConnection;
import java.net.ServerSocket;
import java.net.Socket;
import java.net.URL;

/**
 * 边下边播：本机起一个小的 HTTP 服务，播放器连本地，数据按 1MB 分块从服务器并行抓。
 *
 * 为什么这样做：路由器单条 TCP 连接只有 ~1.3MB/s，4 条并发能到 ~6MB/s。
 * 播放器只会开一条连接，所以直接连服务器会卡；让它连本地，本地再开 4 条连接并发取块，
 * 播放位置前面的块一到位就立刻吐给播放器 —— 就是网页那套「边缓冲边播」。
 */
public class PlayCache {

    static final int CHUNK = 2 << 20;      // 2MB 一块
    static final int WORKERS = 8;   // 实测 8 条并发能到 ~9.5MB/s（单条只有 1.3）

    public final String id;
    public final long size;
    final File file;
    final int chunks;
    final boolean[] ready;
    volatile boolean failed = false;
    final String err = "";

    public PlayCache(String id, long size, File dir) {
        this.id = id;
        this.size = size;
        this.file = new File(dir, "stream_" + id + ".bin");
        this.chunks = (int) ((size + CHUNK - 1) / CHUNK);
        this.ready = new boolean[Math.max(1, chunks)];
    }

    /** 起 4 个线程，从第 0 块往后按顺序抓。 */
    public void start() {
        try {
            RandomAccessFile raf = new RandomAccessFile(file, "rw");
            raf.setLength(size);
            raf.close();
        } catch (Throwable t) {
            failed = true;
            return;
        }
        for (int w = 0; w < WORKERS; w++) {
            Thread t = new Thread(new Runnable() {
                public void run() { work(); }
            }, "playcache-" + w);
            t.setPriority(Thread.NORM_PRIORITY);
            t.start();
        }
    }

    int next = 0;

    synchronized int take() {
        if (next >= chunks) return -1;
        return next++;
    }

    void work() {
        while (true) {
            int i = take();
            if (i < 0 || failed) return;
            long from = (long) i * CHUNK;
            long to = Math.min(size, from + CHUNK) - 1;
            boolean ok = false;
            for (int attempt = 0; attempt < 3 && !ok && !failed; attempt++) {
                ok = fetch(from, to, i);
            }
            if (!ok) {
                failed = true;
                synchronized (ready) { ready.notifyAll(); }
                return;
            }
            synchronized (ready) {
                ready[i] = true;
                ready.notifyAll();
            }
        }
    }

    boolean fetch(long from, long to, int idx) {
        HttpURLConnection c = null;
        RandomAccessFile raf = null;
        try {
            c = (HttpURLConnection) new URL(Api.base + "/m/" + id).openConnection();
            c.setConnectTimeout(15000);
            c.setReadTimeout(60000);
            if (!Api.cookie.isEmpty()) c.setRequestProperty("Cookie", Api.cookie);
            c.setRequestProperty("Range", "bytes=" + from + "-" + to);
            if (c.getResponseCode() >= 400) return false;
            InputStream in = c.getInputStream();
            raf = new RandomAccessFile(file, "rw");
            raf.seek(from);
            byte[] buf = new byte[65536];
            long pos = from;
            int n;
            while ((n = in.read(buf)) > 0) {
                raf.write(buf, 0, n);
                pos += n;
            }
            in.close();
            return pos > from;
        } catch (Throwable t) {
            return false;
        } finally {
            try { if (raf != null) raf.close(); } catch (Throwable ignored) { }
            try { if (c != null) c.disconnect(); } catch (Throwable ignored) { }
        }
    }

    /** 插队抓第 i 块（播放器正等它）。 */
    synchronized void fetchNow(int i) {
        if (i < 0 || i >= chunks) return;
        synchronized (ready) { if (ready[i]) return; }
        long from = (long) i * CHUNK;
        long to = Math.min(size, from + CHUNK) - 1;
        for (int attempt = 0; attempt < 2; attempt++) {
            if (fetch(from, to, i)) {
                synchronized (ready) {
                    ready[i] = true;
                    ready.notifyAll();
                }
                return;
            }
        }
    }
    /** 等第 i 块就绪；返回 false 表示失败或超时。 */
    boolean waitChunk(int i, long timeoutMs) {
        long end = System.currentTimeMillis() + timeoutMs;
        synchronized (ready) {
            while (!ready[i]) {
                if (failed) return false;
                long left = end - System.currentTimeMillis();
                if (left <= 0) return false;
                try { ready.wait(Math.min(left, 500)); } catch (InterruptedException e) { return false; }
            }
            return true;
        }
    }

    public long downloaded() {
        long sum = 0;
        synchronized (ready) {
            for (int i = 0; i < chunks; i++) if (ready[i]) sum += Math.min(CHUNK, size - (long) i * CHUNK);
        }
        return sum;
    }

    public int percent() {
        if (size <= 0) return 0;
        return (int) (downloaded() * 100 / size);
    }

    /* ---------------- 本地 HTTP 服务 ---------------- */

    static ServerSocket server;
    static int port = 0;
    static final java.util.Map<String, PlayCache> map = new java.util.HashMap<String, PlayCache>();

    /** 启动本地服务（只启一次），返回端口。 */
    public static synchronized int ensureServer() {
        if (port > 0) return port;
        try {
            server = new ServerSocket(0, 8, java.net.InetAddress.getByName("127.0.0.1"));
            port = server.getLocalPort();
            Thread t = new Thread(new Runnable() {
                public void run() { acceptLoop(); }
            }, "playcache-server");
            t.setDaemon(true);
            t.start();
        } catch (Throwable t) {
            port = 0;
        }
        return port;
    }

    static void acceptLoop() {
        while (true) {
            try {
                final Socket s = server.accept();
                Thread t = new Thread(new Runnable() {
                    public void run() { handle(s); }
                }, "playcache-conn");
                t.setDaemon(true);
                t.start();
            } catch (Throwable t) {
                return;
            }
        }
    }

    static void handle(Socket s) {
        try {
            s.setSoTimeout(30000);
            InputStream in = s.getInputStream();
            StringBuilder head = new StringBuilder();
            int c;
            while ((c = in.read()) >= 0) {
                head.append((char) c);
                if (head.length() >= 4 && head.substring(head.length() - 4).equals("\r\n\r\n")) break;
            }
            String req = head.toString();
            String path = "";
            String[] lines = req.split("\r\n");
            if (lines.length > 0) {
                String[] parts = lines[0].split(" ");
                if (parts.length > 1) path = parts[1];
            }
            boolean hasRange = false;
            long rangeStart = 0, rangeEnd = -1;
            for (String line : lines) {
                if (line.toLowerCase().startsWith("range:")) {
                    hasRange = true;
                    String v = line.substring(6).trim();
                    int eq = v.indexOf('=');
                    if (eq >= 0) v = v.substring(eq + 1);
                    int dash = v.indexOf('-');
                    if (dash >= 0) {
                        String a = v.substring(0, dash).trim();
                        String b = v.substring(dash + 1).trim();
                        try { rangeStart = a.isEmpty() ? 0 : Long.parseLong(a); } catch (Throwable ignored) { }
                        try { rangeEnd = b.isEmpty() ? -1 : Long.parseLong(b); } catch (Throwable ignored) { }
                    }
                }
            }
            // 路径形如 /v/{id}
            String id = path.startsWith("/v/") ? path.substring(3) : path;
            int q = id.indexOf('?');
            if (q >= 0) id = id.substring(0, q);

            PlayCache pc;
            synchronized (map) { pc = map.get(id); }
            if (pc == null) {
                String r = "HTTP/1.1 404 Not Found\r\nContent-Length: 0\r\n\r\n";
                s.getOutputStream().write(r.getBytes("UTF-8"));
                s.close();
                return;
            }
            long total = pc.size;
            if (rangeEnd < 0 || rangeEnd >= total) rangeEnd = total - 1;
            if (rangeStart < 0) rangeStart = 0;
            long len = rangeEnd - rangeStart + 1;

            OutputStream out = s.getOutputStream();
            // 播放器可能先发一个不带 Range 的探测请求：这种情况必须回 200，
            // 回 206 会让它一直等、画面全黑
            String hdr;
            if (hasRange) {
                hdr = "HTTP/1.1 206 Partial Content\r\n"
                        + "Content-Type: video/mp4\r\n"
                        + "Accept-Ranges: bytes\r\n"
                        + "Content-Length: " + len + "\r\n"
                        + "Content-Range: bytes " + rangeStart + "-" + rangeEnd + "/" + total + "\r\n"
                        + "Connection: close\r\n\r\n";
            } else {
                hdr = "HTTP/1.1 200 OK\r\n"
                        + "Content-Type: video/mp4\r\n"
                        + "Accept-Ranges: bytes\r\n"
                        + "Content-Length: " + total + "\r\n"
                        + "Connection: close\r\n\r\n";
                rangeStart = 0;
                rangeEnd = total - 1;
            }
            out.write(hdr.getBytes("UTF-8"));
            out.flush();

            RandomAccessFile raf = new RandomAccessFile(pc.file, "r");
            try {
                long pos = rangeStart;
                byte[] buf = new byte[65536];
                while (pos <= rangeEnd) {
                    int ci = (int) (pos / CHUNK);
                    // 先等 1.5 秒；还没好就自己插队去抓这一块。
                    // 必须这样：MP4 的索引(moov)经常在文件末尾，播放器一上来就读尾巴，
                    // 而顺序下载要很久才轮到它 → 一直黑屏。
                    if (!pc.waitChunk(ci, 1500)) {
                        pc.fetchNow(ci);
                        if (!pc.waitChunk(ci, 60000)) break;
                    }
                    long chunkStart = (long) ci * CHUNK;
                    long chunkEnd = Math.min(pc.size, chunkStart + CHUNK) - 1;
                    long readTo = Math.min(rangeEnd, chunkEnd);
                    raf.seek(pos);
                    while (pos <= readTo) {
                        int want = (int) Math.min(buf.length, readTo - pos + 1);
                        int n = raf.read(buf, 0, want);
                        if (n <= 0) break;
                        out.write(buf, 0, n);
                        pos += n;
                    }
                }
                out.flush();
            } finally {
                try { raf.close(); } catch (Throwable ignored) { }
            }
            s.close();
        } catch (Throwable t) {
            try { s.close(); } catch (Throwable ignored) { }
        }
    }

    public static void register(PlayCache pc) {
        synchronized (map) { map.put(pc.id, pc); }
    }

    public static void unregister(String id) {
        synchronized (map) { map.remove(id); }
    }
}
