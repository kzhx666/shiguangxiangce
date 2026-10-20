package com.lumina.app;

import android.app.Activity;
import android.content.Intent;
import android.graphics.Bitmap;
import android.graphics.BitmapFactory;
import android.graphics.drawable.GradientDrawable;
import android.os.Bundle;
import android.os.Handler;
import android.os.Looper;
import android.view.Gravity;
import android.view.View;
import android.view.ViewGroup;
import android.widget.ImageView;
import android.widget.LinearLayout;
import android.widget.ScrollView;
import android.widget.TextView;

import org.json.JSONArray;
import org.json.JSONObject;

import java.util.ArrayList;
import java.util.List;

/**
 * 智能分类：相似照片 / 事件 / 地点 / 人物。
 * 数据和网页端完全一致：/api/dupes、/api/events、/api/places、/api/people。
 */
public class SmartActivity extends Activity {

    final Handler ui = new Handler(Looper.getMainLooper());
    LinearLayout box;
    TextView status;
    TextView[] tabs = new TextView[4];
    final String[] KEYS = {"dupes", "events", "places", "people"};
    final String[] LABELS = {"相似照片", "事件", "地点", "人物"};
    String cur = "dupes";

    int dp(int v) { return (int) (v * getResources().getDisplayMetrics().density + 0.5f); }

    @Override
    protected void onCreate(Bundle b) {
        super.onCreate(b);
        LinearLayout root = new LinearLayout(this);
        root.setOrientation(LinearLayout.VERTICAL);
        root.setBackgroundColor(0xFF0A0C11);
        root.setPadding(dp(14), dp(18), dp(14), 0);

        LinearLayout top = new LinearLayout(this);
        top.setGravity(Gravity.CENTER_VERTICAL);
        TextView back = Ui.chip(this, "← 返回");
        back.setOnClickListener(new View.OnClickListener() {
            public void onClick(View v) { finish(); }
        });
        top.addView(back);
        TextView title = Ui.label(this, "智能分类", 20, 0xFFFFFFFF);
        title.setPadding(dp(12), 0, 0, 0);
        top.addView(title);
        root.addView(top);

        // 四个标签
        LinearLayout bar = new LinearLayout(this);
        bar.setGravity(Gravity.CENTER);
        bar.setPadding(0, dp(10), 0, dp(6));
        for (int i = 0; i < KEYS.length; i++) {
            final String k = KEYS[i];
            TextView t = Ui.chip(this, LABELS[i]);
            t.setGravity(Gravity.CENTER);
            t.setOnClickListener(new View.OnClickListener() {
                public void onClick(View v) { cur = k; syncTabs(); load(); }
            });
            LinearLayout.LayoutParams lp = new LinearLayout.LayoutParams(0,
                    ViewGroup.LayoutParams.WRAP_CONTENT, 1f);
            lp.setMargins(dp(3), 0, dp(3), 0);
            bar.addView(t, lp);
            tabs[i] = t;
        }
        root.addView(bar);

        status = Ui.label(this, "加载中…", 12, 0xFF8A93A6);
        status.setPadding(dp(4), dp(4), 0, dp(6));
        root.addView(status);

        ScrollView sv = new ScrollView(this);
        box = new LinearLayout(this);
        box.setOrientation(LinearLayout.VERTICAL);
        sv.addView(box);
        root.addView(sv, new LinearLayout.LayoutParams(
                ViewGroup.LayoutParams.MATCH_PARENT, 0, 1f));

        setContentView(root);
        syncTabs();
        load();
    }

    void syncTabs() {
        for (int i = 0; i < KEYS.length; i++) {
            boolean on = KEYS[i].equals(cur);
            GradientDrawable bg = new GradientDrawable();
            bg.setColor(on ? 0xFF2DD4BF : 0x14FFFFFF);
            bg.setCornerRadius(dp(15));
            tabs[i].setBackground(bg);
            tabs[i].setTextColor(on ? 0xFF06231F : 0xFFD6DBE5);
            tabs[i].setTypeface(null, on ? android.graphics.Typeface.BOLD : android.graphics.Typeface.NORMAL);
        }
    }

    void load() {
        status.setText("加载中…");
        box.removeAllViews();
        final String which = cur;
        new Thread(new Runnable() {
            public void run() {
                String err = null;
                JSONObject o = null;
                try {
                    String path = "/api/" + which;
                    o = new JSONObject(Api.get(path));
                } catch (Throwable e) {
                    err = e.toString();
                }
                final JSONObject jo = o;
                final String e2 = err;
                ui.post(new Runnable() {
                    public void run() {
                        if (e2 != null) { status.setText("加载失败：" + e2); return; }
                        try { render(which, jo); } catch (Throwable t) { status.setText("解析失败"); }
                    }
                });
            }
        }).start();
    }

    void render(String which, JSONObject o) throws Exception {
        box.removeAllViews();
        if ("dupes".equals(which)) {
            JSONArray gs = o.optJSONArray("groups");
            int n = gs == null ? 0 : gs.length();
            status.setText(n + " 组相似照片");
            if (n == 0) { empty("没有发现重复或相似的照片"); return; }
            for (int i = 0; i < n; i++) {
                JSONObject g = gs.getJSONObject(i);
                JSONArray items = g.optJSONArray("items");
                int cnt = items == null ? 0 : items.length();
                long wasted = g.optLong("wasted", 0);
                long cover = cnt > 0 ? items.getJSONObject(0).optLong("id", 0) : 0;
                box.addView(rowCard(cover, cnt + " 张相似", fmt(wasted) + " 可释放",
                        items, 0));
            }
        } else if ("events".equals(which)) {
            JSONArray es = o.optJSONArray("events");
            int n = es == null ? 0 : es.length();
            status.setText(n + " 个事件");
            if (n == 0) { empty("还没有识别出事件"); return; }
            for (int i = 0; i < n; i++) {
                JSONObject e = es.getJSONObject(i);
                box.addView(rowCard(e.optLong("cover", 0),
                        e.optString("title", "事件"),
                        e.optInt("count", 0) + " 张", null, e.optLong("id", 0)));
            }
        } else if ("places".equals(which)) {
            JSONArray ps = o.optJSONArray("places");
            int n = ps == null ? 0 : ps.length();
            status.setText(n + " 个地点");
            if (n == 0) { empty("没有带 GPS 的照片"); return; }
            for (int i = 0; i < n; i++) {
                JSONObject p = ps.getJSONObject(i);
                box.addView(rowCard(p.optLong("cover", 0),
                        p.optString("city", "未知"),
                        p.optInt("count", 0) + " 张", null, 0));
            }
        } else {
            JSONArray ps = o.optJSONArray("people");
            int n = ps == null ? 0 : ps.length();
            status.setText(n + " 个人物");
            if (n == 0) { empty("还没有识别到人物"); return; }
            for (int i = 0; i < n; i++) {
                JSONObject p = ps.getJSONObject(i);
                String nm = p.optString("name", "");
                if (nm.isEmpty()) nm = "未命名";
                box.addView(rowCard(p.optLong("cover", 0), nm,
                        p.optInt("count", 0) + " 张", null, 0, p.optInt("id", 0)));
            }
        }
    }

    String fmt(long b) {
        if (b > 1024L * 1024 * 1024) return String.format("%.1f GB", b / 1073741824.0);
        if (b > 1024L * 1024) return String.format("%.0f MB", b / 1048576.0);
        return (b / 1024) + " KB";
    }

    void empty(String s) {
        TextView t = Ui.label(this, s, 12, 0xFF6B7280);
        t.setPadding(0, dp(24), 0, 0);
        box.addView(t);
    }

    /** 一行：左边缩略/头像，右边标题+副标题。 */
    View rowCard(long cover, String title, String sub, JSONArray items, long eventId) {
        return rowCard(cover, title, sub, items, eventId, 0);
    }

    View rowCard(long cover, String title, String sub, JSONArray items, long eventId, int personId) {
        LinearLayout row = new LinearLayout(this);
        row.setGravity(Gravity.CENTER_VERTICAL);
        row.setPadding(dp(4), dp(8), dp(4), dp(8));

        ImageView iv = new ImageView(this);
        int s = dp(56);
        iv.setLayoutParams(new LinearLayout.LayoutParams(s, s));
        GradientDrawable bg = new GradientDrawable();
        bg.setShape(personId > 0 ? GradientDrawable.OVAL : GradientDrawable.RECTANGLE);
        bg.setCornerRadius(personId > 0 ? 0 : dp(10));
        bg.setColor(0xFF1B2130);
        iv.setBackground(bg);
        iv.setClipToOutline(true);
        iv.setScaleType(ImageView.ScaleType.CENTER_CROP);
        row.addView(iv);
        if (cover > 0) loadImg(personId > 0 ? "/f/" + cover + ".jpg" : "/t/" + cover + "/t.jpg", iv);

        LinearLayout m = new LinearLayout(this);
        m.setOrientation(LinearLayout.VERTICAL);
        m.setPadding(dp(12), 0, 0, 0);
        TextView t = Ui.label(this, title, 14, 0xFFE6EAF2);
        TextView st = Ui.label(this, sub, 12, 0xFF8A93A6);
        m.addView(t); m.addView(st);
        row.addView(m, new LinearLayout.LayoutParams(0, ViewGroup.LayoutParams.WRAP_CONTENT, 1f));

        if (personId > 0) {
            row.setOnClickListener(new View.OnClickListener() {
                public void onClick(View v) {
                    Intent i = new Intent(SmartActivity.this, PeopleActivity.class);
                    startActivity(i);
                }
            });
        }
        return row;
    }

    void loadImg(final String path, final ImageView iv) {
        new Thread(new Runnable() {
            public void run() {
                try {
                    final byte[] d = Api.raw(path);
                    ui.post(new Runnable() {
                        public void run() {
                            Bitmap bm = BitmapFactory.decodeByteArray(d, 0, d.length);
                            if (bm != null) iv.setImageBitmap(bm);
                        }
                    });
                } catch (Throwable ignored) { }
            }
        }).start();
    }
}
