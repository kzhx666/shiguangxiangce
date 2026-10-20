package com.lumina.app;

import android.app.Activity;
import android.content.Intent;
import android.graphics.Color;
import android.os.Bundle;
import android.view.Gravity;
import android.view.View;
import android.view.ViewGroup;
import android.widget.ImageView;
import android.widget.LinearLayout;
import android.widget.TextView;

import org.json.JSONArray;
import org.json.JSONObject;

/** 文件夹分区：按目录列出照片数量，点进去看这个目录的照片。 */
public class FolderActivity extends Activity {

    final android.os.Handler ui = new android.os.Handler(android.os.Looper.getMainLooper());
    LinearLayout box;
    boolean secretMode = false;
    String ownerMode = "";
    final java.util.Set<String> selFolder = new java.util.LinkedHashSet<String>();
    LinearLayout selBar;
    TextView selCount;

    @Override protected void onCreate(Bundle b) {
        super.onCreate(b);
        Api.base = Prefs.server(this);
        Api.cookie = Prefs.cookie(this);

        android.widget.ScrollView sv = new android.widget.ScrollView(this);
        LinearLayout root = new LinearLayout(this);
        root.setOrientation(LinearLayout.VERTICAL);
        root.setBackgroundColor(0xFF0A0C11);
        root.setPadding(0, 40, 0, 40);
        sv.addView(root);
        setContentView(sv);

        LinearLayout top = new LinearLayout(this);
        top.setGravity(Gravity.CENTER_VERTICAL);
        top.setPadding(24, 8, 24, 16);
        TextView t = new TextView(this);
        t.setText("文件夹");
        t.setTextColor(Color.WHITE);
        t.setTextSize(20);
        top.addView(t);
        root.addView(top);

        selBar = new LinearLayout(this);
        selBar.setGravity(Gravity.CENTER_VERTICAL);
        selBar.setPadding(24, 4, 24, 4);
        selBar.setVisibility(View.GONE);
        selCount = Ui.label(this, "", 12, 0xFF2DD4BF);
        LinearLayout.LayoutParams slp = new LinearLayout.LayoutParams(0,
                LinearLayout.LayoutParams.WRAP_CONTENT, 1f);
        selBar.addView(selCount, slp);
        TextView delSel = Ui.chip(this, "删除文件夹");
        delSel.setOnClickListener(new View.OnClickListener() {
            public void onClick(View v) { deleteSelected(); }
        });
        TextView cancelSel = Ui.chip(this, "取消");
        cancelSel.setOnClickListener(new View.OnClickListener() {
            public void onClick(View v) {
                selFolder.clear(); selBar.setVisibility(View.GONE); load();
            }
        });
        TextView secSel = Ui.chip(this, "移入秘密");
        secSel.setOnClickListener(new View.OnClickListener() {
            public void onClick(View v) { secretSelected(); }
        });
        android.widget.TextView pushSel = Ui.chip(this, "推送到共享");
        pushSel.setOnClickListener(new View.OnClickListener() {
            public void onClick(View v) { pushSelected(); }
        });
        selBar.addView(delSel);
        LinearLayout.LayoutParams g4 = new LinearLayout.LayoutParams(
                LinearLayout.LayoutParams.WRAP_CONTENT, LinearLayout.LayoutParams.WRAP_CONTENT);
        g4.setMargins(8, 0, 0, 0);
        selBar.addView(pushSel, g4);
        LinearLayout.LayoutParams g3 = new LinearLayout.LayoutParams(
                LinearLayout.LayoutParams.WRAP_CONTENT, LinearLayout.LayoutParams.WRAP_CONTENT);
        g3.setMargins(8, 0, 0, 0);
        selBar.addView(secSel, g3);
        LinearLayout.LayoutParams g2 = new LinearLayout.LayoutParams(
                LinearLayout.LayoutParams.WRAP_CONTENT, LinearLayout.LayoutParams.WRAP_CONTENT);
        g2.setMargins(8, 0, 0, 0);
        selBar.addView(cancelSel, g2);
        root.addView(selBar);

        box = new LinearLayout(this);
        box.setOrientation(LinearLayout.VERTICAL);
        box.setPadding(20, 0, 20, 0);
        root.addView(box);

        secretMode = getIntent().getBooleanExtra("secret", false);
        ownerMode = getIntent().getStringExtra("owner");
        if (ownerMode == null) ownerMode = "";
        load();
    }

    void load() {
        new Thread(new Runnable() {
            public void run() {
                try {
                    // 秘密相册里进来时只看秘密分区
        String u = "/api/folders";
        if (secretMode) u += "?secret=1";
        else if (!ownerMode.isEmpty()) u += "?owner=" + java.net.URLEncoder.encode(ownerMode, "UTF-8");
        JSONObject o = new JSONObject(Api.get(u));
                    final JSONArray arr = o.optJSONArray("folders");
                    ui.post(new Runnable() {
                        public void run() {
                            box.removeAllViews();
                            if (arr == null || arr.length() == 0) {
                                TextView e = Ui.label(FolderActivity.this, "还没有文件夹", 13, 0xFF8A93A6);
                                e.setPadding(8, 20, 8, 20);
                                box.addView(e);
                                return;
                            }
                            for (int i = 0; i < arr.length(); i++) {
                                JSONObject f = arr.optJSONObject(i);
                                if (f == null) continue;
                                addFolder(f.optString("name"), f.optInt("count"), f.optLong("cover"));
                            }
                        }
                    });
                } catch (final Exception e) {
                    ui.post(new Runnable() {
                        public void run() {
                            TextView t = Ui.label(FolderActivity.this, "加载失败：" + e.getMessage(),
                                    13, 0xFFFF8A8A);
                            t.setPadding(8, 20, 8, 20);
                            box.addView(t);
                        }
                    });
                }
            }
        }).start();
    }

    void addFolder(final String name, int count, long cover) {
        LinearLayout row = new LinearLayout(this);
        row.setGravity(Gravity.CENTER_VERTICAL);
        row.setPadding(6, 12, 6, 12);

        ImageView iv = new ImageView(this);
        iv.setBackgroundColor(0xFF12151C);
        iv.setScaleType(ImageView.ScaleType.CENTER_CROP);
        LinearLayout.LayoutParams ilp = new LinearLayout.LayoutParams(dp(56), dp(56));
        row.addView(iv, ilp);
        if (cover > 0) loadCover(cover, iv);

        LinearLayout texts = new LinearLayout(this);
        texts.setOrientation(LinearLayout.VERTICAL);
        LinearLayout.LayoutParams tlp = new LinearLayout.LayoutParams(0,
                LinearLayout.LayoutParams.WRAP_CONTENT, 1f);
        tlp.setMargins(16, 0, 0, 0);
        TextView n = new TextView(this);
        n.setText(name);
        n.setTextColor(0xFFE8ECF4);
        n.setTextSize(14);
        TextView c = new TextView(this);
        c.setText(count + " 项");
        c.setTextColor(0xFF8A93A6);
        c.setTextSize(11);
        texts.addView(n);
        texts.addView(c);
        row.addView(texts, tlp);

        row.setOnClickListener(new View.OnClickListener() {
            public void onClick(View v) {
                if (!selFolder.isEmpty()) { toggleFolder(name); return; }
                Intent i = new Intent(FolderActivity.this, MainActivity.class);
                i.putExtra("album", name);
                // 秘密模式下点进文件夹，必须继续带 secret 标记，
                // 否则主列表会按普通分区查，把该文件夹里所有照片都列出来
                i.putExtra("secret", secretMode);
                startActivity(i);
            }
        });
        row.setOnLongClickListener(new View.OnLongClickListener() {
            public boolean onLongClick(View v) { toggleFolder(name); return true; }
        });
        if (selFolder.contains(name)) row.setBackgroundColor(0x3334D399);
        box.addView(row);
    }

    void loadCover(final long id, final ImageView iv) {
        new Thread(new Runnable() {
            public void run() {
                try {
                    final byte[] d = Api.raw("/t/" + id + "/t.jpg");
                    ui.post(new Runnable() {
                        public void run() {
                            android.graphics.Bitmap bm =
                                    android.graphics.BitmapFactory.decodeByteArray(d, 0, d.length);
                            if (bm != null) iv.setImageBitmap(bm);
                        }
                    });
                } catch (Throwable ignored) { }
            }
        }).start();
    }

    int dp(int v) { return (int) (v * getResources().getDisplayMetrics().density); }
    void toggleFolder(String name) {
        if (selFolder.contains(name)) selFolder.remove(name); else selFolder.add(name);
        if (selFolder.isEmpty()) selBar.setVisibility(View.GONE);
        else {
            selBar.setVisibility(View.VISIBLE);
            selCount.setText("已选 " + selFolder.size() + " 个文件夹");
        }
        load();
    }

    void deleteSelected() {
        if (selFolder.isEmpty()) return;
        new android.app.AlertDialog.Builder(this)
                .setTitle("删除 " + selFolder.size() + " 个文件夹？")
                .setMessage("文件夹里的照片会一起移入回收站，30 天内可恢复。")
                .setPositiveButton("删除", new android.content.DialogInterface.OnClickListener() {
                    public void onClick(android.content.DialogInterface d, int w) {
                        new Thread(new Runnable() {
                            public void run() {
                                try {
                                    org.json.JSONObject req = new org.json.JSONObject();
                                    org.json.JSONArray arr = new org.json.JSONArray();
                                    for (String n : selFolder) arr.put(n);
                                    req.put("folders", arr);
                                    String url = "/api/folders/delete"
                                                + (ownerMode.equals("family") ? "?owner=family" : "");
                                    String r = Api.post(url, req.toString());
                                    final String msg = r.contains("\"ok\":true") ? "已删除" : r;
                                    ui.post(new Runnable() { public void run() {
                                        android.widget.Toast.makeText(FolderActivity.this, msg,
                                                android.widget.Toast.LENGTH_LONG).show();
                                        selFolder.clear();
                                        selBar.setVisibility(View.GONE);
                                        load();
                                    }});
                                } catch (final Exception e) {
                                    ui.post(new Runnable() { public void run() {
                                        android.widget.Toast.makeText(FolderActivity.this,
                                                "失败：" + e.getMessage(),
                                                android.widget.Toast.LENGTH_LONG).show();
                                    }});
                                }
                            }
                        }).start();
                    }
                })
                .setNegativeButton("取消", null).show();
    }
    /** 把选中的文件夹整块移入秘密相册。 */
    void secretSelected() {
        if (selFolder.isEmpty()) return;
        new android.app.AlertDialog.Builder(this)
                .setTitle("移入秘密相册？")
                .setMessage("选中的 " + selFolder.size() + " 个文件夹里的照片会藏进秘密相册")
                .setPositiveButton("移入", new android.content.DialogInterface.OnClickListener() {
                    public void onClick(android.content.DialogInterface d, int w) {
                        new Thread(new Runnable() {
                            public void run() {
                                int ok = 0;
                                for (String n : selFolder) {
                                    try {
                                        org.json.JSONObject req = new org.json.JSONObject();
                                        req.put("name", n);
                                        req.put("secret", true);
                                        String r = Api.post("/api/secret/folder", req.toString());
                                        if (r.contains("\"ok\":true")) ok++;
                                    } catch (Throwable ignored) { }
                                }
                                final int okf = ok;
                                ui.post(new Runnable() { public void run() {
                                    android.widget.Toast.makeText(FolderActivity.this,
                                            "已移入 " + okf + " 个文件夹",
                                            android.widget.Toast.LENGTH_LONG).show();
                                    selFolder.clear();
                                    selBar.setVisibility(View.GONE);
                                    load();
                                }});
                            }
                        }).start();
                    }
                })
                .setNegativeButton("取消", null).show();
    }


    /** 把选中的文件夹整个推送到家庭共享空间。 */
    void pushSelected() {
        if (selFolder.isEmpty()) return;
        final java.util.List<String> names = new java.util.ArrayList<String>(selFolder);
        new Thread(new Runnable() {
            public void run() {
                int total = 0, ok = 0;
                for (String n : names) {
                    try {
                        org.json.JSONObject req = new org.json.JSONObject();
                        req.put("name", n);
                        String r = Api.post("/api/family/folder", req.toString());
                        org.json.JSONObject o = new org.json.JSONObject(r);
                        total += o.optInt("total", 0);
                        ok += o.optInt("pushed", 0);
                    } catch (Throwable ignored) { }
                }
                final int okf = ok, tf = total;
                ui.post(new Runnable() { public void run() {
                    android.widget.Toast.makeText(FolderActivity.this,
                            "已推送 " + okf + " / " + tf + " 张到家庭共享",
                            android.widget.Toast.LENGTH_LONG).show();
                    selFolder.clear();
                    selBar.setVisibility(View.GONE);
                    load();
                }});
            }
        }).start();
    }

}
