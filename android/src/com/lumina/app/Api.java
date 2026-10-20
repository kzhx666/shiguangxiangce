package com.lumina.app;

import org.json.JSONArray;
import org.json.JSONObject;

import java.io.ByteArrayOutputStream;
import java.io.InputStream;
import java.io.OutputStream;
import java.net.HttpURLConnection;
import java.net.URL;
import java.net.URLEncoder;
import java.util.ArrayList;
import java.util.List;

/** 极简 HTTP 客户端：不依赖任何第三方库，会话靠手工维护 Cookie。 */
public class Api {
    public static String base = "http://192.168.2.1:8088";
    public static String cookie = "";
    public static String user = "";

    static HttpURLConnection conn(String path, String method) throws Exception {
        HttpURLConnection c = (HttpURLConnection) new URL(base + path).openConnection();
        c.setRequestMethod(method);
        c.setConnectTimeout(8000);
        c.setReadTimeout(30000);
        c.setRequestProperty("Accept", "application/json");
        if (!cookie.isEmpty()) c.setRequestProperty("Cookie", cookie);
        return c;
    }

    static String body(InputStream in) throws Exception {
        ByteArrayOutputStream bo = new ByteArrayOutputStream();
        byte[] b = new byte[8192];
        int n;
        while ((n = in.read(b)) > 0) bo.write(b, 0, n);
        return new String(bo.toByteArray(), "UTF-8");
    }

    static void grabCookie(HttpURLConnection c) {
        List<String> list = c.getHeaderFields().get("Set-Cookie");
        if (list == null) return;
        for (String s : list) {
            int i = s.indexOf(';');
            String kv = i > 0 ? s.substring(0, i) : s;
            if (kv.startsWith("lumina_sess=")) cookie = kv;
        }
    }

    public static String get(String path) throws Exception {
        HttpURLConnection c = conn(path, "GET");
        int code = c.getResponseCode();
        InputStream in = code >= 400 ? c.getErrorStream() : c.getInputStream();
        String s = in == null ? "" : body(in);
        c.disconnect();
        if (code >= 400) throw new Exception("HTTP " + code + " " + s);
        return s;
    }

    public static String post(String path, String json) throws Exception {
        HttpURLConnection c = conn(path, "POST");
        c.setDoOutput(true);
        c.setRequestProperty("Content-Type", "application/json; charset=utf-8");
        OutputStream os = c.getOutputStream();
        os.write(json.getBytes("UTF-8"));
        os.close();
        int code = c.getResponseCode();
        grabCookie(c);
        InputStream in = code >= 400 ? c.getErrorStream() : c.getInputStream();
        String s = in == null ? "" : body(in);
        c.disconnect();
        if (code >= 400) throw new Exception("HTTP " + code + " " + s);
        return s;
    }

    /** POST 并返回原始字节（打包 ZIP 这类二进制响应用）。 */
    public static byte[] postRaw(String path, byte[] data, String contentType) throws Exception {
        HttpURLConnection c = conn(path, "POST");
        c.setDoOutput(true);
        c.setFixedLengthStreamingMode(data.length);
        c.setRequestProperty("Content-Type", contentType);
        OutputStream os = c.getOutputStream();
        os.write(data);
        os.close();
        int code = c.getResponseCode();
        grabCookie(c);
        InputStream in = code >= 400 ? c.getErrorStream() : c.getInputStream();
        ByteArrayOutputStream bo = new ByteArrayOutputStream();
        byte[] buf = new byte[65536];
        int n;
        while (in != null && (n = in.read(buf)) > 0) bo.write(buf, 0, n);
        c.disconnect();
        if (code >= 400) throw new Exception("HTTP " + code + " " + bo.toString("UTF-8"));
        return bo.toByteArray();
    }
    public static String postBytes(String path, byte[] data, String contentType) throws Exception {
        HttpURLConnection c = conn(path, "POST");
        c.setDoOutput(true);
        c.setFixedLengthStreamingMode(data.length);
        c.setRequestProperty("Content-Type", contentType);
        OutputStream os = c.getOutputStream();
        os.write(data);
        os.close();
        int code = c.getResponseCode();
        grabCookie(c);
        InputStream in = code >= 400 ? c.getErrorStream() : c.getInputStream();
        String s = in == null ? "" : body(in);
        c.disconnect();
        if (code >= 400) throw new Exception("HTTP " + code + " " + s);
        return s;
    }

    public static boolean login(String name, String pass) {
        try {
            String r = post("/api/login", "{\"name\":\"" + name + "\",\"password\":\"" + pass + "\"}");
            if (r.contains("\"ok\":true")) { user = name; return true; }
        } catch (Exception e) {
        }
        return false;
    }

    public static boolean hasSession() {
        if (cookie.isEmpty()) return false;
        try {
            get("/api/me");
            return true;
        } catch (Exception e) {
            return false;
        }
    }

    public static List<JSONObject> timeline(String owner, String kind, String album, String before) throws Exception {
        StringBuilder q = new StringBuilder("/api/timeline?limit=120");
        if (owner != null && !owner.isEmpty()) q.append("&owner=").append(enc(owner));
        if (kind != null && !kind.isEmpty()) q.append("&kind=").append(enc(kind));
        if (album != null && !album.isEmpty()) q.append("&album=").append(enc(album));
        if (before != null && !before.isEmpty()) q.append("&before=").append(enc(before));
        JSONObject o = new JSONObject(get(q.toString()));
        JSONArray arr = o.optJSONArray("items");
        List<JSONObject> out = new ArrayList<>();
        if (arr != null) for (int i = 0; i < arr.length(); i++) out.add(arr.getJSONObject(i));
        return out;
    }

    static String enc(String s) throws Exception { return URLEncoder.encode(s, "UTF-8"); }

    /** 上传一个分片。 */
    public static void putChunk(String sid, int idx, byte[] data, int len) throws Exception {
        HttpURLConnection c = conn("/api/upload/" + sid + "/" + idx, "PUT");
        c.setDoOutput(true);
        c.setFixedLengthStreamingMode(len);
        c.setRequestProperty("Content-Type", "application/octet-stream");
        OutputStream os = c.getOutputStream();
        os.write(data, 0, len);
        os.close();
        int code = c.getResponseCode();
        InputStream in = code >= 400 ? c.getErrorStream() : c.getInputStream();
        String s = in == null ? "" : body(in);
        c.disconnect();
        if (code >= 400) throw new Exception("chunk " + idx + " HTTP " + code + " " + s);
    }

    public static byte[] raw(String path) throws Exception {
        HttpURLConnection c = conn(path, "GET");
        int code = c.getResponseCode();
        if (code >= 400) { c.disconnect(); throw new Exception("HTTP " + code); }
        InputStream in = c.getInputStream();
        ByteArrayOutputStream bo = new ByteArrayOutputStream();
        byte[] b = new byte[16384];
        int n;
        while ((n = in.read(b)) > 0) bo.write(b, 0, n);
        in.close();
        c.disconnect();
        return bo.toByteArray();
    }
    /** 上传一张封面/预览图（JPEG 字节）。 */
    public static void postPoster(long assetId, byte[] jpeg) throws Exception {
        postBytes("/api/poster/" + assetId, jpeg, "image/jpeg");
    }

    /** 记一条别名：这份内容在手机上原本叫什么、多大。 */
    public static void postAlias(long assetId, String name, long size) {
        try {
            JSONObject o = new JSONObject();
            o.put("n", name);
            o.put("s", size);
            post("/api/alias/" + assetId, o.toString());
        } catch (Throwable ignored) { }
    }
}