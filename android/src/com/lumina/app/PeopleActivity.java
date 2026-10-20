package com.lumina.app;

import android.app.Activity;
import android.content.Intent;
import android.graphics.Bitmap;
import android.graphics.BitmapFactory;
import android.graphics.Color;
import android.graphics.drawable.GradientDrawable;
import android.os.Bundle;
import android.os.Handler;
import android.os.Looper;
import android.text.InputType;
import android.view.Gravity;
import android.view.View;
import android.view.ViewGroup;
import android.widget.EditText;
import android.widget.ImageView;
import android.widget.LinearLayout;
import android.widget.ScrollView;
import android.widget.TextView;
import android.widget.Toast;

import org.json.JSONArray;
import org.json.JSONObject;

import java.util.ArrayList;
import java.util.List;

/**
 * 人物页：列出服务器聚类出来的人，点头像看这个人的所有照片。
 * 长按可以改名。
 */
public class PeopleActivity extends Activity {

    final Handler ui = new Handler(Looper.getMainLooper());

    int dp(int v) {
        return (int) (v * getResources().getDisplayMetrics().density + 0.5f);
    }
    LinearLayout box;
    TextView status;

    @Override
    protected void onCreate(Bundle b) {
        super.onCreate(b);
        LinearLayout root = new LinearLayout(this);
        root.setOrientation(LinearLayout.VERTICAL);
        root.setBackgroundColor(0xFF0A0C11);
        root.setPadding(dp(14), dp(18), dp(14), 0);

        // 顶栏
        LinearLayout top = new LinearLayout(this);
        top.setGravity(Gravity.CENTER_VERTICAL);
        TextView back = Ui.chip(this, "← 返回");
        back.setOnClickListener(new View.OnClickListener() {
            public void onClick(View v) { finish(); }
        });
        top.addView(back);
        TextView title = Ui.label(this, "人物", 20, 0xFFFFFFFF);
        title.setPadding(dp(12), 0, 0, 0);
        top.addView(title);
        LinearLayout.LayoutParams tlp = new LinearLayout.LayoutParams(0,
                ViewGroup.LayoutParams.WRAP_CONTENT, 1f);
        title.setLayoutParams(tlp);
        root.addView(top);

        status = Ui.label(this, "加载中…", 12, 0xFF8A93A6);
        status.setPadding(dp(4), dp(10), 0, dp(6));
        root.addView(status);

        ScrollView sv = new ScrollView(this);
        box = new LinearLayout(this);
        box.setOrientation(LinearLayout.VERTICAL);
        sv.addView(box);
        root.addView(sv, new LinearLayout.LayoutParams(
                ViewGroup.LayoutParams.MATCH_PARENT, 0, 1f));

        setContentView(root);
        load();
    }

    void load() {
        new Thread(new Runnable() {
            public void run() {
                String err = null;
                final List<JSONObject> list = new ArrayList<JSONObject>();
                try {
                    JSONObject o = new JSONObject(Api.get("/api/people"));
                    JSONArray arr = o.optJSONArray("people");
                    if (arr != null) for (int i = 0; i < arr.length(); i++) list.add(arr.getJSONObject(i));
                } catch (Throwable e) {
                    err = e.toString();
                }
                final String e2 = err;
                ui.post(new Runnable() {
                    public void run() {
                        if (e2 != null) { status.setText("加载失败：" + e2); return; }
                        render(list);
                    }
                });
            }
        }).start();
    }

    void render(List<JSONObject> list) {
        box.removeAllViews();
        if (list.isEmpty()) {
            status.setText("还没有人物");
            TextView hint = Ui.label(this,
                    "在电脑上运行 tools/face_agent.py 扫一遍照片，\n这里就会出现识别到的人。",
                    12, 0xFF6B7280);
            hint.setPadding(0, dp(20), 0, 0);
            box.addView(hint);
            return;
        }
        status.setText(list.size() + " 个人物");

        // 每行两人
        LinearLayout row = null;
        for (int i = 0; i < list.size(); i++) {
            if (i % 2 == 0) {
                row = new LinearLayout(this);
                row.setOrientation(LinearLayout.HORIZONTAL);
                row.setPadding(0, dp(6), 0, dp(6));
                box.addView(row);
            }
            final JSONObject p = list.get(i);
            row.addView(cell(p), new LinearLayout.LayoutParams(0,
                    ViewGroup.LayoutParams.WRAP_CONTENT, 1f));
        }
    }

    View cell(final JSONObject p) {
        final int pid = p.optInt("id", 0);
        String nm = p.optString("name", "");
        if (nm.isEmpty()) nm = "未命名";
        final int count = p.optInt("count", 0);
        final long cover = p.optLong("cover", 0);

        LinearLayout cellBox = new LinearLayout(this);
        cellBox.setOrientation(LinearLayout.VERTICAL);
        cellBox.setGravity(Gravity.CENTER_HORIZONTAL);
        cellBox.setPadding(dp(6), dp(6), dp(6), dp(6));

        ImageView iv = new ImageView(this);
        int s = dp(84);
        LinearLayout.LayoutParams ilp = new LinearLayout.LayoutParams(s, s);
        iv.setLayoutParams(ilp);
        GradientDrawable bg = new GradientDrawable();
        bg.setShape(GradientDrawable.OVAL);
        bg.setColor(0xFF1B2130);
        iv.setBackground(bg);
        iv.setClipToOutline(true);
        iv.setScaleType(ImageView.ScaleType.CENTER_CROP);
        cellBox.addView(iv);
        if (cover > 0) loadFace(cover, iv);

        final TextView nameTv = Ui.label(this, nm, 13, 0xFFE6EAF2);
        nameTv.setGravity(Gravity.CENTER);
        nameTv.setPadding(0, dp(6), 0, 0);
        cellBox.addView(nameTv);
        TextView sub = Ui.label(this, count + " 张", 11, 0xFF8A93A6);
        sub.setGravity(Gravity.CENTER);
        cellBox.addView(sub);

        cellBox.setOnClickListener(new View.OnClickListener() {
            public void onClick(View v) {
                Intent i = new Intent(PeopleActivity.this, MainActivity.class);
                i.putExtra("person", pid);
                i.putExtra("person_name", nameTv.getText().toString());
                startActivity(i);
            }
        });
        cellBox.setOnLongClickListener(new View.OnLongClickListener() {
            public boolean onLongClick(View v) {
                askName(pid, nameTv);
                return true;
            }
        });
        return cellBox;
    }

    void askName(final int pid, final TextView tv) {
        final EditText et = new EditText(this);
        et.setHint("输入名字");
        et.setSingleLine(true);
        et.setInputType(InputType.TYPE_CLASS_TEXT);
        et.setTextColor(Color.WHITE);
        new android.app.AlertDialog.Builder(this)
                .setTitle("给这个人命名")
                .setView(et)
                .setPositiveButton("保存", new android.content.DialogInterface.OnClickListener() {
                    public void onClick(android.content.DialogInterface d, int w) {
                        final String nm = et.getText().toString().trim();
                        if (nm.isEmpty()) return;
                        new Thread(new Runnable() {
                            public void run() {
                                try {
                                    JSONObject req = new JSONObject();
                                    req.put("name", nm);
                                    Api.post("/api/people/" + pid + "/name", req.toString());
                                    ui.post(new Runnable() { public void run() {
                                        tv.setText(nm);
                                        Toast.makeText(PeopleActivity.this, "已命名：" + nm,
                                                Toast.LENGTH_SHORT).show();
                                    }});
                                } catch (Throwable ignored) { }
                            }
                        }).start();
                    }
                })
                .setNegativeButton("取消", null)
                .show();
    }

    void loadFace(final long id, final ImageView iv) {
        new Thread(new Runnable() {
            public void run() {
                try {
                    final byte[] d = Api.raw("/f/" + id + ".jpg");
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
