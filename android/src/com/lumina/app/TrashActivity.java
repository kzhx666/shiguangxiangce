package com.lumina.app;

import android.app.Activity;
import android.graphics.Color;
import android.os.Bundle;
import android.os.Handler;
import android.os.Looper;
import android.view.Gravity;
import android.view.View;
import android.view.ViewGroup;
import android.widget.BaseAdapter;
import android.widget.Button;
import android.widget.LinearLayout;
import android.widget.ListView;
import android.widget.TextView;
import android.widget.Toast;

import org.json.JSONArray;
import org.json.JSONObject;

import java.util.ArrayList;
import java.util.List;

public class TrashActivity extends Activity {
    final List<String> ids = new ArrayList<>();
    final List<String> names = new ArrayList<>();
    ListView list;
    BaseAdapter adapter;
    final Handler ui = new Handler(Looper.getMainLooper());

    @Override protected void onCreate(Bundle b) {
        super.onCreate(b);
        LinearLayout root = new LinearLayout(this);
        root.setOrientation(LinearLayout.VERTICAL);
        root.setBackgroundColor(Color.parseColor("#07080B"));
        TextView t = new TextView(this);
        t.setText("回收站");
        t.setTextColor(Color.WHITE);
        t.setTextSize(18);
        t.setPadding(32, 40, 32, 16);
        root.addView(t);

        list = new ListView(this);
        adapter = new BaseAdapter() {
            public int getCount() { return ids.size(); }
            public Object getItem(int i) { return ids.get(i); }
            public long getItemId(int i) { return i; }
            public View getView(final int i, View cv, ViewGroup p) {
                LinearLayout row = new LinearLayout(TrashActivity.this);
                row.setOrientation(LinearLayout.HORIZONTAL);
                row.setGravity(Gravity.CENTER_VERTICAL);
                row.setPadding(32, 20, 16, 20);
                TextView nm = new TextView(TrashActivity.this);
                nm.setText(names.get(i));
                nm.setTextColor(Color.WHITE);
                nm.setTextSize(14);
                row.addView(nm, new LinearLayout.LayoutParams(0, ViewGroup.LayoutParams.WRAP_CONTENT, 1f));

                Button r = new Button(TrashActivity.this);
                r.setText("恢复");
                r.setTextSize(11);
                r.setOnClickListener(new View.OnClickListener() {
                    public void onClick(View v) { act("restore", ids.get(i)); }
                });
                row.addView(r);

                Button d = new Button(TrashActivity.this);
                d.setText("删除");
                d.setTextSize(11);
                d.setOnClickListener(new View.OnClickListener() {
                    public void onClick(View v) { act("purge", ids.get(i)); }
                });
                row.addView(d);
                return row;
            }
        };
        list.setAdapter(adapter);
        root.addView(list, new LinearLayout.LayoutParams(ViewGroup.LayoutParams.MATCH_PARENT, 0, 1f));
        setContentView(root);
        load();
    }

    void act(final String what, final String id) {
        new Thread(new Runnable() {
            public void run() {
                try {
                    if ("restore".equals(what)) Api.post("/api/restore", "{\"ids\":[" + id + "]}");
                    else Api.post("/api/purge", "{\"ids\":[" + id + "]}");
                    load();
                    ui.post(new Runnable() {
                        public void run() { Toast.makeText(TrashActivity.this, "restore".equals(what) ? "已恢复" : "已删除", Toast.LENGTH_SHORT).show(); }
                    });
                } catch (Exception e) { }
            }
        }).start();
    }

    void load() {
        new Thread(new Runnable() {
            public void run() {
                try {
                    JSONObject o = new JSONObject(Api.get("/api/trash"));
                    JSONArray arr = o.optJSONArray("items");
                    ids.clear(); names.clear();
                    if (arr != null) for (int i = 0; i < arr.length(); i++) {
                        JSONObject a = arr.getJSONObject(i);
                        ids.add(a.optString("id"));
                        names.add(a.optString("name"));
                    }
                    ui.post(new Runnable() { public void run() { adapter.notifyDataSetChanged(); } });
                } catch (Exception e) { }
            }
        }).start();
    }
}