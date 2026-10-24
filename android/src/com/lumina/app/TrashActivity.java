package com.lumina.app;

import android.app.Activity;
import android.app.AlertDialog;
import android.os.Bundle;
import android.os.Handler;
import android.os.Looper;
import android.view.View;
import android.view.ViewGroup;
import android.widget.*;
import org.json.*;
import java.util.*;

/** 回收站：选择状态只在主线程修改，操作使用选择快照。 */
public class TrashActivity extends Activity {
    final List<String> ids = new ArrayList<>(), names = new ArrayList<>();
    final Set<String> selected = new LinkedHashSet<>();
    final Handler ui = new Handler(Looper.getMainLooper());
    BaseAdapter adapter;
    TextView status, all, restore, purge, refresh;
    boolean busy;

    @Override protected void onCreate(Bundle saved) {
        super.onCreate(saved);
        if (saved != null) {
            ArrayList<String> old = saved.getStringArrayList("selected");
            if (old != null) selected.addAll(old);
        }
        LinearLayout root = new LinearLayout(this);
        root.setOrientation(LinearLayout.VERTICAL);
        root.setPadding(16, 32, 16, 16);
        root.setBackgroundColor(0xFF07080B);
        LinearLayout heading = Ui.row(this);
        TextView back = Ui.chip(this, "返回"); back.setOnClickListener(v -> finish());
        heading.addView(back);
        heading.addView(Ui.label(this, "  回收站", 20, 0xFFFFFFFF));
        root.addView(heading);
        status = Ui.label(this, "正在加载…", 14, 0xFFAAAAAA);
        status.setPadding(8, 16, 8, 16); root.addView(status);
        all = Ui.chip(this, "全选"); refresh = Ui.chip(this, "刷新");
        restore = Ui.chip(this, "恢复所选"); purge = Ui.chip(this, "彻底删除所选");
        root.addView(buttonRow(all, refresh)); root.addView(buttonRow(restore, purge));
        all.setOnClickListener(v -> {
            if (selected.size() == ids.size()) selected.clear(); else selected.addAll(ids);
            update();
        });
        refresh.setOnClickListener(v -> load());
        restore.setOnClickListener(v -> act("restore", new ArrayList<>(selected)));
        purge.setOnClickListener(v -> confirmPurge(new ArrayList<>(selected)));
        ListView list = new ListView(this);
        adapter = new BaseAdapter() {
            public int getCount() { return ids.size(); }
            public Object getItem(int i) { return ids.get(i); }
            public long getItemId(int i) { return Long.parseLong(ids.get(i)); }
            public View getView(int i, View cv, ViewGroup parent) {
                final String id = ids.get(i);
                LinearLayout row = new LinearLayout(TrashActivity.this);
                row.setOrientation(LinearLayout.VERTICAL);
                CheckBox check = new CheckBox(TrashActivity.this);
                check.setText(names.get(i)); check.setTextColor(0xFFFFFFFF);
                check.setChecked(selected.contains(id)); check.setEnabled(!busy);
                check.setOnCheckedChangeListener((b, checked) -> {
                    if (checked) selected.add(id); else selected.remove(id);
                    update();
                });
                row.addView(check);
                TextView r = Ui.chip(TrashActivity.this, "恢复"), d = Ui.chip(TrashActivity.this, "彻底删除");
                r.setEnabled(!busy); d.setEnabled(!busy);
                r.setOnClickListener(v -> act("restore", Collections.singletonList(id)));
                d.setOnClickListener(v -> confirmPurge(Collections.singletonList(id)));
                row.addView(buttonRow(r, d));
                return row;
            }
        };
        list.setAdapter(adapter);
        root.addView(list, new LinearLayout.LayoutParams(-1, 0, 1));
        setContentView(root); load();
    }
    LinearLayout buttonRow(TextView a, TextView b) {
        LinearLayout row = Ui.row(this);
        for (TextView view : new TextView[]{a,b}) {
            LinearLayout.LayoutParams lp = new LinearLayout.LayoutParams(0, -2, 1);
            lp.setMargins(4, 4, 4, 4); row.addView(view, lp);
        }
        return row;
    }
    @Override protected void onSaveInstanceState(Bundle out) {
        out.putStringArrayList("selected", new ArrayList<>(selected)); super.onSaveInstanceState(out);
    }
    void update() {
        status.setText(busy ? "正在处理…" : ids.isEmpty() ? "回收站为空" : "共 " + ids.size() + " 项 · 已选 " + selected.size() + " 项");
        all.setText(!ids.isEmpty() && selected.size() == ids.size() ? "取消全选" : "全选");
        all.setEnabled(!busy && !ids.isEmpty()); refresh.setEnabled(!busy);
        restore.setEnabled(!busy && !selected.isEmpty()); purge.setEnabled(!busy && !selected.isEmpty());
        adapter.notifyDataSetChanged();
    }
    void confirmPurge(final List<String> targets) {
        if (busy || targets.isEmpty()) return;
        new AlertDialog.Builder(this).setTitle("彻底删除 " + targets.size() + " 项？")
            .setMessage("删除后无法从回收站恢复。")
            .setNegativeButton("取消", null).setPositiveButton("彻底删除", (d,w) -> act("purge", targets)).show();
    }
    void act(final String what, final List<String> targets) {
        if (busy || targets.isEmpty()) return;
        busy = true; update();
        new Thread(() -> {
            try {
                JSONArray values = new JSONArray();
                for (String id : targets) values.put(Long.parseLong(id));
                JSONObject result = new JSONObject(Api.post("/api/" + what, new JSONObject().put("ids", values).toString()));
                final int count = result.getInt("restore".equals(what) ? "restored" : "purged");
                ui.post(() -> {
                    if (isFinishing() || isDestroyed()) return;
                    MainActivity.dataDirty = true;
                    Toast.makeText(this, ("restore".equals(what) ? "已恢复 " : "已删除 ") + count + "/" + targets.size() + " 项", Toast.LENGTH_LONG).show();
                    busy = false; load();
                });
            } catch (Exception e) { failed("操作失败，请刷新确认结果：", e); }
        }).start();
    }
    void failed(final String prefix, final Exception e) {
        ui.post(() -> {
            if (isFinishing() || isDestroyed()) return;
            busy = false; update(); status.setText(prefix + e.getMessage());
        });
    }
    void load() {
        if (busy) return;
        busy = true; update();
        new Thread(() -> {
            try {
                JSONArray rows = new JSONObject(Api.get("/api/trash")).getJSONArray("items");
                final List<String> newIds = new ArrayList<>(), newNames = new ArrayList<>();
                for (int i=0; i<rows.length(); i++) {
                    JSONObject row = rows.getJSONObject(i);
                    newIds.add(row.getString("id")); newNames.add(row.optString("name"));
                }
                ui.post(() -> {
                    if (isFinishing() || isDestroyed()) return;
                    ids.clear(); ids.addAll(newIds); names.clear(); names.addAll(newNames);
                    selected.retainAll(ids); busy = false; update();
                });
            } catch (Exception e) { failed("加载失败，请点击刷新：", e); }
        }).start();
    }
}
