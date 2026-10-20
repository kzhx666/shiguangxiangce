package com.lumina.app;

import android.app.Activity;
import android.content.Intent;
import android.graphics.Color;
import android.os.Bundle;
import android.os.Handler;
import android.os.Looper;
import android.view.View;
import android.view.ViewGroup;
import android.widget.AdapterView;
import android.widget.BaseAdapter;
import android.widget.Button;
import android.widget.EditText;
import android.widget.LinearLayout;
import android.widget.ListView;
import android.widget.TextView;
import android.widget.Toast;

import org.json.JSONArray;
import org.json.JSONObject;

import java.util.ArrayList;
import java.util.List;

public class AlbumsActivity extends Activity {
    final List<String> names = new ArrayList<>();
    final List<String> albumIds = new ArrayList<>();
    final List<Integer> counts = new ArrayList<>();
    ListView list;
    final Handler ui = new Handler(Looper.getMainLooper());
    BaseAdapter adapter;

    @Override protected void onCreate(Bundle b) {
        super.onCreate(b);
        LinearLayout root = new LinearLayout(this);
        root.setOrientation(LinearLayout.VERTICAL);
        root.setBackgroundColor(Color.parseColor("#07080B"));

        LinearLayout bar = new LinearLayout(this);
        bar.setPadding(16, 24, 16, 8);
        TextView t = new TextView(this);
        t.setText("相册");
        t.setTextColor(Color.WHITE);
        t.setTextSize(18);
        LinearLayout.LayoutParams lp = new LinearLayout.LayoutParams(0, ViewGroup.LayoutParams.WRAP_CONTENT, 1f);
        bar.addView(t, lp);
        root.addView(bar);

        LinearLayout addRow = new LinearLayout(this);
        addRow.setPadding(16, 0, 16, 8);
        final EditText nm = new EditText(this);
        nm.setHint("新相册名");
        nm.setTextColor(Color.WHITE);
        LinearLayout.LayoutParams elp = new LinearLayout.LayoutParams(0, ViewGroup.LayoutParams.WRAP_CONTENT, 1f);
        addRow.addView(nm, elp);
        Button add = new Button(this);
        add.setText("新建");
        add.setOnClickListener(new View.OnClickListener() {
            public void onClick(View v) {
                final String n = nm.getText().toString().trim();
                if (n.isEmpty()) return;
                new Thread(new Runnable() {
                    public void run() {
                        try {
                            Api.post("/api/albums", "{\"name\":\"" + n + "\"}");
                            nm.setText("");
                            load();
                        } catch (Exception e) { }
                    }
                }).start();
            }
        });
        addRow.addView(add);
        root.addView(addRow);

        list = new ListView(this);
        adapter = new BaseAdapter() {
            public int getCount() { return names.size(); }
            public Object getItem(int i) { return names.get(i); }
            public long getItemId(int i) { return i; }
            public View getView(int i, View cv, ViewGroup p) {
                LinearLayout row = new LinearLayout(AlbumsActivity.this);
                row.setOrientation(LinearLayout.VERTICAL);
                row.setPadding(32, 28, 32, 28);
                TextView a = new TextView(AlbumsActivity.this);
                a.setText(names.get(i));
                a.setTextColor(Color.WHITE);
                a.setTextSize(16);
                TextView c = new TextView(AlbumsActivity.this);
                c.setText(counts.get(i) + " 项");
                c.setTextColor(Color.parseColor("#9AA0AC"));
                c.setTextSize(12);
                row.addView(a); row.addView(c);
                return row;
            }
        };
        list.setAdapter(adapter);
        list.setOnItemClickListener(new AdapterView.OnItemClickListener() {
            public void onItemClick(AdapterView<?> p, View v, int i, long id) {
                Intent t = new Intent(AlbumsActivity.this, MainActivity.class);
                t.putExtra("album", albumIds.get(i));
                t.addFlags(Intent.FLAG_ACTIVITY_CLEAR_TOP);
                startActivity(t);
                finish();
            }
        });
        root.addView(list, new LinearLayout.LayoutParams(ViewGroup.LayoutParams.MATCH_PARENT, 0, 1f));
        setContentView(root);
        load();
    }

    void load() {
        new Thread(new Runnable() {
            public void run() {
                try {
                    JSONObject o = new JSONObject(Api.get("/api/albums"));
                    JSONArray arr = o.optJSONArray("albums");
                    names.clear(); albumIds.clear(); counts.clear();
                    if (arr != null) for (int i = 0; i < arr.length(); i++) {
                        JSONObject a = arr.getJSONObject(i);
                        names.add(a.optString("name"));
                        albumIds.add(a.optString("id"));
                        counts.add(a.optInt("count"));
                    }
                    ui.post(new Runnable() { public void run() { adapter.notifyDataSetChanged(); } });
                } catch (Exception e) { }
            }
        }).start();
    }
}