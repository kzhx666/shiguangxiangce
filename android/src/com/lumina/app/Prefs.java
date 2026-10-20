package com.lumina.app;

import android.content.Context;
import android.content.SharedPreferences;

public class Prefs {
    private static SharedPreferences sp(Context c) {
        return c.getSharedPreferences("lumina", Context.MODE_PRIVATE);
    }

    public static String server(Context c) { return sp(c).getString("server", "http://192.168.2.1:8088"); }
    public static void setServer(Context c, String v) { sp(c).edit().putString("server", v).apply(); }

    public static String cookie(Context c) { return sp(c).getString("cookie", ""); }
    public static void setCookie(Context c, String v) { sp(c).edit().putString("cookie", v).apply(); }

    public static String user(Context c) { return sp(c).getString("user", ""); }
    public static void setUser(Context c, String v) { sp(c).edit().putString("user", v).apply(); }

    public static String owner(Context c) { return sp(c).getString("owner", ""); }
    public static void setOwner(Context c, String v) { sp(c).edit().putString("owner", v).apply(); }

    public static boolean backupOn(Context c) { return sp(c).getBoolean("backup", false); }
    public static void setBackupOn(Context c, boolean v) { sp(c).edit().putBoolean("backup", v).apply(); }

    /** 已经备份过的 MediaStore _ID（逗号分隔，保留最近 5000 条）。 */
    public static String seen(Context c) { return sp(c).getString("seen", ""); }
    public static void setSeen(Context c, String v) { sp(c).edit().putString("seen", v).apply(); }
    /** 备份范围：选中的相册 bucket_id（空 = 全部）。 */
    public static java.util.Set<String> backupBuckets(android.content.Context c) {
        java.util.Set<String> out = new java.util.LinkedHashSet<String>();
        String raw = sp(c).getString("buckets", "");
        if (raw != null && !raw.isEmpty()) {
            for (String x : raw.split(",")) if (!x.isEmpty()) out.add(x);
        }
        return out;
    }

    public static void setBackupBuckets(android.content.Context c, java.util.Set<String> ids) {
        StringBuilder sb = new StringBuilder();
        for (String x : ids) { if (sb.length() > 0) sb.append(','); sb.append(x); }
        sp(c).edit().putString("buckets", sb.toString()).apply();
    }
    /** 秘密相册的本地密码（只挡界面，服务端数据仍是加密隔离的）。 */
    public static String secretPass(android.content.Context c) {
        return sp(c).getString("secretPass", "");
    }

    public static void setSecretPass(android.content.Context c, String v) {
        sp(c).edit().putString("secretPass", v).apply();
    }

    /** 网格列数（2~7），下次打开还是这个列数。 */
    public static int gridCols(android.content.Context c) {
        return sp(c).getInt("gridCols", 3);
    }

    public static void setGridCols(android.content.Context c, int v) {
        sp(c).edit().putInt("gridCols", v).apply();
    }

    /** 视频后台缓存开关。 */
    public static boolean videoCache(android.content.Context c) {
        return sp(c).getBoolean("videoCache", false);
    }

    public static void setVideoCache(android.content.Context c, boolean v) {
        sp(c).edit().putBoolean("videoCache", v).apply();
    }

    /** true = 视频缓存留在手机（下次秒开）；false = 播完即删，不占手机空间。 */
    public static boolean videoKeepCache(android.content.Context c) {
        return sp(c).getBoolean("videoKeepCache", true);
    }

    public static void setVideoKeepCache(android.content.Context c, boolean v) {
        sp(c).edit().putBoolean("videoKeepCache", v).apply();
    }

    /** 备份时直接把文件放进秘密相册。 */
    public static boolean backupSecret(android.content.Context c) {
        return sp(c).getBoolean("backupSecret", false);
    }

    public static void setBackupSecret(android.content.Context c, boolean v) {
        sp(c).edit().putBoolean("backupSecret", v).apply();
    }
}
