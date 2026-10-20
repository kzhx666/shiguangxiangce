package com.lumina.app;

import android.app.Activity;
import android.app.AlertDialog;
import android.content.Intent;
import android.graphics.Bitmap;
import android.graphics.BitmapFactory;
import android.graphics.Color;
import android.os.Bundle;
import android.os.Handler;
import android.os.Looper;
import android.text.InputType;
import android.view.Gravity;
import android.view.MotionEvent;
import android.view.ScaleGestureDetector;
import android.view.View;
import android.view.ViewGroup;
import android.widget.BaseAdapter;
import android.widget.Button;
import android.widget.EditText;
import android.widget.FrameLayout;
import android.widget.ImageView;
import android.widget.LinearLayout;
import android.widget.ListView;
import android.widget.TextView;
import android.widget.Toast;

import org.json.JSONArray;
import org.json.JSONObject;

import java.util.ArrayList;
import java.util.LinkedHashSet;
import java.util.List;
import java.util.Set;

/**
 * 主界面：顶部 标题 / 账号 / 上传，下面一排 Tab（文件夹 相册 回收站 秘密 备份）。
 * 照片按月份分组三列网格，双指缩放列数，长按多选后可批量删除或移入/移出秘密相册。
 */
public class MainActivity extends Activity {

    final Handler ui = new Handler(Looper.getMainLooper());
    static android.util.LruCache<String, Bitmap> memCache =
            new android.util.LruCache<String, Bitmap>(64 * 1024 * 1024) {
                protected int sizeOf(String k, Bitmap b) { return b.getByteCount(); }
            };
    static java.io.File thumbDir;

    static class Row {
        String header;
        final List<JSONObject> cells = new ArrayList<JSONObject>();
    }

    final List<Row> rows = new ArrayList<Row>();
    final List<JSONObject> items = new ArrayList<JSONObject>();
    final Set<String> sel = new LinkedHashSet<String>();
    ListView list;
    TextView title, status, selCount;
    LinearLayout selBar, selBar2;
    GridAdapter adapter;
    String album = "", cursor = "", ownerFilter = "";
    int personId = 0;
    String personName = "";
    int tabW = 0;   // 七个 tab 平分一屏的宽度
    boolean secret = false, loading = false, done = false;
    int gen = 0;
    static volatile boolean dataDirty = false;   // 别的页面改了数据，回来要刷新
    long lastResetAt = 0;
    int cols = 3;
    float pinchAcc = 1f;

    @Override protected void onCreate(Bundle b) {
        super.onCreate(b);
        Api.base = Prefs.server(this);
        Api.cookie = Prefs.cookie(this);
        Api.user = Prefs.user(this);
        album = getIntent().getStringExtra("album");
        if (album == null) album = "";
        secret = getIntent().getBooleanExtra("secret", false);
        personId = getIntent().getIntExtra("person", 0);
        personName = getIntent().getStringExtra("person_name");
        if (personName == null) personName = "人物";
        ownerFilter = getIntent().getStringExtra("owner");
        if (ownerFilter == null) ownerFilter = "";

        if (Api.cookie.isEmpty() || Api.user.isEmpty()) { showLogin(); return; }
        cols = Prefs.gridCols(this);   // 记住上次的列数
        thumbDir = new java.io.File(getCacheDir(), "thumbs");
        if (!thumbDir.exists()) thumbDir.mkdirs();
        buildUi();
        reset();
    }

    /* ---------------- 登录 ---------------- */

    void showLogin() {
        LinearLayout root = new LinearLayout(this);
        root.setOrientation(LinearLayout.VERTICAL);
        root.setGravity(Gravity.CENTER);
        root.setBackgroundColor(0xFF07080B);
        root.setPadding(60, 0, 60, 0);
        TextView t = new TextView(this);
        t.setText("拾光");
        t.setTextColor(Color.WHITE);
        t.setTextSize(28);
        t.setGravity(Gravity.CENTER);
        t.setPadding(0, 0, 0, 40);
        root.addView(t);
        final EditText server = field(root, "服务器", Prefs.server(this), false);
        final EditText user = field(root, "账号", Prefs.user(this), false);
        final EditText pass = field(root, "密码", "", true);
        Button go = new Button(this);
        go.setText("登录");
        go.setOnClickListener(new View.OnClickListener() {
            public void onClick(View v) {
                // 网络请求必须放后台：主线程发请求会抛 NetworkOnMainThreadException，
                // 被空的 catch 吞掉后就变成"总是登录失败"
                Api.base = server.getText().toString().trim();
                final String u = user.getText().toString().trim();
                final String pw = pass.getText().toString();
                new Thread(new Runnable() {
                    public void run() {
                        final boolean ok = Api.login(u, pw);
                        ui.post(new Runnable() {
                            public void run() {
                                if (ok) {
                                    Prefs.setServer(MainActivity.this, Api.base);
                                    Prefs.setCookie(MainActivity.this, Api.cookie);
                                    Prefs.setUser(MainActivity.this, Api.user);
                                    Prefs.setOwner(MainActivity.this, Api.user);
                                    recreate();
                                } else {
                                    Toast.makeText(MainActivity.this,
                                            "登录失败：账号/密码不对，或服务器地址不通",
                                            Toast.LENGTH_LONG).show();
                                }
                            }
                        });
                    }
                }).start();
            }
        });
        root.addView(go);
        setContentView(root);
    }

    EditText field(LinearLayout root, String hint, String val, boolean pwd) {
        EditText e = new EditText(this);
        e.setHint(hint);
        e.setText(val);
        e.setTextColor(Color.WHITE);
        e.setHintTextColor(0xFF6B7280);
        if (pwd) e.setInputType(InputType.TYPE_CLASS_TEXT | InputType.TYPE_TEXT_VARIATION_PASSWORD);
        root.addView(e);
        return e;
    }

    /* ---------------- 界面 ---------------- */

    void buildUi() {
        LinearLayout root = new LinearLayout(this);
        root.setOrientation(LinearLayout.VERTICAL);
        root.setBackgroundColor(0xFF0A0C11);

        LinearLayout top = new LinearLayout(this);
        top.setGravity(Gravity.CENTER_VERTICAL);
        top.setPadding(24, 36, 18, 6);
        title = new TextView(this);
        title.setText(personId > 0 ? personName
                : secret ? "秘密相册"
                : ("family".equals(ownerFilter) ? "家庭共享"
                : (album.isEmpty() ? "照片" : album)));
        title.setTextColor(Color.WHITE);
        title.setTextSize(20);
        top.addView(title, new LinearLayout.LayoutParams(0,
                LinearLayout.LayoutParams.WRAP_CONTENT, 1f));
        TextView account = Ui.chip(this, "👤 " + (Api.user.isEmpty() ? "账号" : Api.user) + " ▾");
        account.setOnClickListener(new View.OnClickListener() {
            public void onClick(View v) { showAccountMenu(); }
        });
        android.widget.TextView peopleBtn = Ui.chip(this, "✨ 智能");
        peopleBtn.setOnClickListener(new View.OnClickListener() {
            public void onClick(View v) {
                startActivity(new Intent(MainActivity.this, SmartActivity.class));
            }
        });
        LinearLayout.LayoutParams plp2 = new LinearLayout.LayoutParams(
                LinearLayout.LayoutParams.WRAP_CONTENT, LinearLayout.LayoutParams.WRAP_CONTENT);
        plp2.setMargins(0, 0, dp(8), 0);
        top.addView(peopleBtn, plp2);
        top.addView(account);
        root.addView(top);

        // 让 7 个 tab 平分一屏：屏宽 - 两侧留白 - 每个 tab 的间距
        int screenW = getResources().getDisplayMetrics().widthPixels;
        tabW = (screenW - dp(20) - dp(8) * 8) / 7;
        android.widget.HorizontalScrollView tabScroll = new android.widget.HorizontalScrollView(this);
        tabScroll.setHorizontalScrollBarEnabled(false);
        LinearLayout tabBar = new LinearLayout(this);
        tabBar.setGravity(Gravity.CENTER);
        tabBar.setPadding(10, 6, 10, 8);
        tabScroll.addView(tabBar);
        tabBar.addView(tab("照片", new View.OnClickListener() {
            public void onClick(View v) {
                // 回到全部照片：只把当前页的过滤条件清掉，不跳 Activity
                if (secret || !album.isEmpty()) {
                    startActivity(new Intent(MainActivity.this, MainActivity.class));
                    finish();
                } else {
                    reset();
                }
            }
        }, album.isEmpty() && !secret));
        tabBar.addView(tab("文件夹", new View.OnClickListener() {
            public void onClick(View v) {
                Intent fi = new Intent(MainActivity.this, FolderActivity.class);
                fi.putExtra("secret", secret);   // 秘密相册里点文件夹 → 只看秘密分区
                fi.putExtra("owner", ownerFilter);   // 共享页里点文件夹 → 只看共享分区
                startActivity(fi);
            }
        }));
        tabBar.addView(tab("相册", new View.OnClickListener() {
            public void onClick(View v) {
                startActivity(new Intent(MainActivity.this, AlbumsActivity.class));
            }
        }));
        tabBar.addView(tab("共享", new View.OnClickListener() {
            public void onClick(View v) {
                Intent fi = new Intent(MainActivity.this, MainActivity.class);
                fi.putExtra("owner", "family");
                startActivity(fi);
            }
        }));
        tabBar.addView(tab("回收站", new View.OnClickListener() {
            public void onClick(View v) {
                startActivity(new Intent(MainActivity.this, TrashActivity.class));
            }
        }));
        tabBar.addView(tab("秘密", new View.OnClickListener() {
            public void onClick(View v) { openSecret(); }
        }));
        tabBar.addView(tab("备份", new View.OnClickListener() {
            public void onClick(View v) {
                startActivity(new Intent(MainActivity.this, BackupScopeActivity.class));
            }
        }));
        root.addView(tabScroll);

        status = Ui.label(this, "", 11, 0xFF8A93A6);
        status.setPadding(24, 0, 24, 6);
        root.addView(status);

        selBar = new LinearLayout(this);
        selBar.setGravity(Gravity.CENTER_VERTICAL);
        selBar.setPadding(24, 4, 24, 4);
        selBar.setVisibility(View.GONE);
        selCount = Ui.label(this, "", 12, 0xFF2DD4BF);
        selBar.addView(selCount, new LinearLayout.LayoutParams(0,
                LinearLayout.LayoutParams.WRAP_CONTENT, 1f));

        TextView delBtn = Ui.chip(this, "删除");
        delBtn.setOnClickListener(new View.OnClickListener() {
            public void onClick(View v) { batchTrash(); }
        });
        TextView secBtn = Ui.chip(this, secret ? "移出秘密" : "移入秘密");
        secBtn.setOnClickListener(new View.OnClickListener() {
            public void onClick(View v) { batchSecret(!secret); }
        });
        TextView cancel = Ui.chip(this, "取消");
        cancel.setOnClickListener(new View.OnClickListener() {
            public void onClick(View v) { clearSel(); }
        });
        selBar.addView(delBtn);
        LinearLayout.LayoutParams g = new LinearLayout.LayoutParams(
                LinearLayout.LayoutParams.WRAP_CONTENT, LinearLayout.LayoutParams.WRAP_CONTENT);
        g.setMargins(8, 0, 0, 0);
        selBar.addView(secBtn, g);
        selBar.addView(cancel, g);
        root.addView(selBar);

        // 选择模式第二排：全选 / 按时间选
        selBar2 = new LinearLayout(this);
        selBar2.setGravity(Gravity.CENTER_VERTICAL);
        selBar2.setPadding(24, 0, 24, 6);
        selBar2.setVisibility(View.GONE);
        TextView selAll = Ui.chip(this, "全选");
        selAll.setOnClickListener(new View.OnClickListener() {
            public void onClick(View v) { selectAll(); }
        });
        selBar2.addView(selAll);
        TextView selFav = Ui.chip(this, "收藏");
        selFav.setOnClickListener(new View.OnClickListener() {
            public void onClick(View v) { batchFav(); }
        });
        TextView selPush = Ui.chip(this, "推送");
        selPush.setOnClickListener(new View.OnClickListener() {
            public void onClick(View v) { batchPush(); }
        });
        TextView selZip = Ui.chip(this, "打包");
        selZip.setOnClickListener(new View.OnClickListener() {
            public void onClick(View v) { batchZip(); }
        });
        LinearLayout.LayoutParams sg2 = new LinearLayout.LayoutParams(
                LinearLayout.LayoutParams.WRAP_CONTENT, LinearLayout.LayoutParams.WRAP_CONTENT);
        sg2.setMargins(8, 0, 0, 0);
        selBar2.addView(selFav, sg2);
        selBar2.addView(selPush, sg2);
        selBar2.addView(selZip, sg2);
        root.addView(selBar2);

        list = new ListView(this);
        list.setDivider(null);
        list.setBackgroundColor(0xFF0A0C11);
        adapter = new GridAdapter();
        list.setAdapter(adapter);
        list.setOnScrollListener(new android.widget.AbsListView.OnScrollListener() {
            public void onScrollStateChanged(android.widget.AbsListView v, int s) { }
            public void onScroll(android.widget.AbsListView v, int first, int visible, int total) {
                if (!done && !loading && total > 0 && first + visible >= total - 6) load();
            }
        });
        root.addView(list, new LinearLayout.LayoutParams(
                LinearLayout.LayoutParams.MATCH_PARENT, 0, 1f));
        setContentView(root);

        final ScaleGestureDetector sd = new ScaleGestureDetector(this,
                new ScaleGestureDetector.SimpleOnScaleGestureListener() {
                    public boolean onScale(ScaleGestureDetector d) {
                        pinchAcc *= d.getScaleFactor();
                        return true;
                    }
                    public void onScaleEnd(ScaleGestureDetector d) {
                        if (pinchAcc >= 1.2f) cols = Math.max(2, cols - 1);
                        else if (pinchAcc <= 0.84f) cols = Math.min(7, cols + 1);
                        pinchAcc = 1f;
                        Prefs.setGridCols(MainActivity.this, cols);   // 记住，下次进来还是这个列数
                        rebuildRows();
                        status.setText(cols + " 列 · " + items.size() + " 项");
                    }
                });
        list.setOnTouchListener(new View.OnTouchListener() {
            public boolean onTouch(View v, MotionEvent e) {
                sd.onTouchEvent(e);
                return e.getPointerCount() >= 2;
            }
        });
    }

    TextView tab(String text, View.OnClickListener l) { return tab(text, l, false); }

    TextView tab(String text, View.OnClickListener l, boolean active) {
        TextView t = new TextView(this);
        t.setText(text);
        t.setTextColor(0xFFD6DBE5);
        t.setTextSize(11.5f);
        t.setGravity(Gravity.CENTER);
        t.setPadding(0, dp(9), 0, dp(9));
        // 圆角胶囊底：比原来的纯文字好看，也更容易点
        // 当前页：青色实底 + 深色字；其他：8% 白底 + 浅灰字
        android.graphics.drawable.GradientDrawable bg =
                new android.graphics.drawable.GradientDrawable();
        bg.setColor(active ? 0xFF2DD4BF : 0x14FFFFFF);
        bg.setCornerRadius(dp(15));
        t.setBackground(bg);
        t.setTextColor(active ? 0xFF06231F : 0xFFD6DBE5);
        if (active) t.setTypeface(null, android.graphics.Typeface.BOLD);
        t.setOnClickListener(l);
        LinearLayout.LayoutParams lp = new LinearLayout.LayoutParams(
                tabW > 0 ? tabW : dp(56), LinearLayout.LayoutParams.WRAP_CONTENT);
        lp.setMargins(dp(4), 0, dp(4), 0);
        t.setLayoutParams(lp);
        return t;
    }

    /* ---------------- 秘密相册 ---------------- */

    void openSecret() {
        if (secret) { clearSel(); return; }
        if (tryBiometric()) return;
        askLoginPassword();
    }

    /** 用登录密码验证后进入秘密相册。 */
    void askLoginPassword() {
        final EditText e = new EditText(this);
        e.setInputType(InputType.TYPE_CLASS_TEXT | InputType.TYPE_TEXT_VARIATION_PASSWORD);
        e.setHint("账号密码");
        new AlertDialog.Builder(this)
                .setTitle("进入秘密相册")
                .setMessage("请输入 " + Api.user + " 的登录密码")
                .setView(e)
                .setPositiveButton("进入", new android.content.DialogInterface.OnClickListener() {
                    public void onClick(android.content.DialogInterface d, int w) {
                        final String pass = e.getText().toString();
                        new Thread(new Runnable() {
                            public void run() {
                                final boolean ok = Api.login(Api.user, pass);
                                ui.post(new Runnable() {
                                    public void run() {
                                        if (ok) {
                                            Prefs.setCookie(MainActivity.this, Api.cookie);
                                            Intent i = new Intent(MainActivity.this, MainActivity.class);
                                            i.putExtra("secret", true);
                                            startActivity(i);
                                        } else {
                                            Toast.makeText(MainActivity.this, "密码不对",
                                                    Toast.LENGTH_SHORT).show();
                                        }
                                    }
                                });
                            }
                        }).start();
                    }
                })
                .setNegativeButton("取消", null).show();
    }

    /** 指纹/人脸；不可用时返回 false 交给密码。 */
    boolean tryBiometric() {
        try {
            if (android.os.Build.VERSION.SDK_INT < 29) return false;
            android.hardware.biometrics.BiometricManager bm =
                    (android.hardware.biometrics.BiometricManager)
                            getSystemService(android.content.Context.BIOMETRIC_SERVICE);
            if (bm == null) return false;
            int can = bm.canAuthenticate(
                    android.hardware.biometrics.BiometricManager.Authenticators.BIOMETRIC_WEAK);
            android.util.Log.i("LuminaSecret", "canAuthenticate=" + can);
            if (can != android.hardware.biometrics.BiometricManager.BIOMETRIC_SUCCESS) return false;

            final java.util.concurrent.Executor ex = getMainExecutor();
            android.hardware.biometrics.BiometricPrompt prompt =
                    new android.hardware.biometrics.BiometricPrompt.Builder(this)
                            .setTitle("进入秘密相册")
                            .setSubtitle("用指纹或人脸解锁")
                            .setNegativeButton("用密码", ex,
                                    new android.content.DialogInterface.OnClickListener() {
                                        public void onClick(android.content.DialogInterface d, int w) {
                                            askLoginPassword();
                                        }
                                    })
                            .build();
            // 2 参数版本不要求 CryptoObject；4 参数版本传 null 会抛
            // IllegalArgumentException: Must supply a crypto object
            if (android.os.Build.VERSION.SDK_INT < 30) return false;
            prompt.authenticate(new android.os.CancellationSignal(), ex,
                    new android.hardware.biometrics.BiometricPrompt.AuthenticationCallback() {
                        public void onAuthenticationSucceeded(
                                android.hardware.biometrics.BiometricPrompt.AuthenticationResult r) {
                            android.util.Log.i("LuminaSecret", "bio ok");
                            Intent i = new Intent(MainActivity.this, MainActivity.class);
                            i.putExtra("secret", true);
                            startActivity(i);
                        }
                        public void onAuthenticationError(int code, CharSequence msg) {
                            android.util.Log.i("LuminaSecret", "bio error " + code + " " + msg);
                        }
                        public void onAuthenticationFailed() {
                            android.util.Log.i("LuminaSecret", "bio failed, retry");
                        }
                    });
            return true;
        } catch (Throwable t) {
            android.util.Log.e("LuminaSecret", "biometric exception: " + t);
            return false;
        }
    }

    /* ---------------- 账号 ---------------- */

    void showAccountMenu() {
        new AlertDialog.Builder(this)
                .setTitle("账号：" + Api.user)
                .setItems(new String[]{"上传照片/视频", "退出登录", "更换账号", "注册新账号", "修改密码"},
                        new android.content.DialogInterface.OnClickListener() {
                            public void onClick(android.content.DialogInterface d, int which) {
                                if (which == 0) showUploadMenu();
                                else if (which == 1) doLogout();
                                else if (which == 2) doSwitch();
                                else if (which == 3) showRegister();
                                else showChangePass();
                            }
                        }).show();
    }

    void doLogout() {
        new Thread(new Runnable() {
            public void run() {
                try { Api.post("/api/logout", "{}"); } catch (Throwable ignored) { }
                Prefs.setCookie(MainActivity.this, "");
                Prefs.setUser(MainActivity.this, "");
                Prefs.setOwner(MainActivity.this, "");
                Api.cookie = "";
                Api.user = "";
                ui.post(new Runnable() { public void run() { recreate(); } });
            }
        }).start();
    }

    void doSwitch() {
        Prefs.setCookie(this, "");
        Prefs.setUser(this, "");
        Api.cookie = "";
        Api.user = "";
        recreate();
    }

    void showRegister() {
        LinearLayout box = new LinearLayout(this);
        box.setOrientation(LinearLayout.VERTICAL);
        box.setPadding(40, 20, 40, 0);
        final EditText u = new EditText(this);
        u.setHint("账号");
        final EditText p = new EditText(this);
        p.setHint("密码（至少 6 位）");
        p.setInputType(InputType.TYPE_CLASS_TEXT | InputType.TYPE_TEXT_VARIATION_PASSWORD);
        box.addView(u);
        box.addView(p);
        new AlertDialog.Builder(this)
                .setTitle("注册新账号")
                .setView(box)
                .setPositiveButton("注册", new android.content.DialogInterface.OnClickListener() {
                    public void onClick(android.content.DialogInterface d, int w) {
                        final String name = u.getText().toString().trim();
                        final String pass = p.getText().toString();
                        new Thread(new Runnable() {
                            public void run() {
                                String msg;
                                boolean ok = false;
                                try {
                                    JSONObject req = new JSONObject();
                                    req.put("name", name);
                                    req.put("password", pass);
                                    String r = Api.post("/api/register", req.toString());
                                    ok = r.contains("\"ok\":true");
                                    msg = ok ? "注册成功，正在登录…" : r;
                                    if (ok) {
                                        ok = Api.login(name, pass);
                                        if (ok) {
                                            Prefs.setCookie(MainActivity.this, Api.cookie);
                                            Prefs.setUser(MainActivity.this, name);
                                            Prefs.setOwner(MainActivity.this, name);
                                        }
                                    }
                                } catch (Exception e) {
                                    msg = "失败：" + e.getMessage();
                                }
                                final String m = msg;
                                final boolean okf = ok;
                                ui.post(new Runnable() {
                                    public void run() {
                                        Toast.makeText(MainActivity.this, m, Toast.LENGTH_LONG).show();
                                        if (okf) recreate();
                                    }
                                });
                            }
                        }).start();
                    }
                })
                .setNegativeButton("取消", null).show();
    }

    void showChangePass() {
        final EditText p = new EditText(this);
        p.setHint("新密码");
        p.setInputType(InputType.TYPE_CLASS_TEXT | InputType.TYPE_TEXT_VARIATION_PASSWORD);
        new AlertDialog.Builder(this)
                .setTitle("修改密码")
                .setView(p)
                .setPositiveButton("保存", new android.content.DialogInterface.OnClickListener() {
                    public void onClick(android.content.DialogInterface d, int w) {
                        final String np = p.getText().toString();
                        new Thread(new Runnable() {
                            public void run() {
                                try {
                                    JSONObject req = new JSONObject();
                                    req.put("password", np);
                                    Api.post("/api/password", req.toString());
                                    ui.post(new Runnable() { public void run() {
                                        Toast.makeText(MainActivity.this, "密码已修改",
                                                Toast.LENGTH_SHORT).show();
                                    }});
                                } catch (final Exception e) {
                                    ui.post(new Runnable() { public void run() {
                                        Toast.makeText(MainActivity.this, "失败：" + e.getMessage(),
                                                Toast.LENGTH_LONG).show();
                                    }});
                                }
                            }
                        }).start();
                    }
                })
                .setNegativeButton("取消", null).show();
    }

    void showUploadMenu() {
        new AlertDialog.Builder(this)
                .setTitle("上传")
                .setItems(new String[]{"选择照片/视频", "选择文件夹"},
                        new android.content.DialogInterface.OnClickListener() {
                            public void onClick(android.content.DialogInterface d, int which) {
                                if (which == 0) Uploader.pickFiles(MainActivity.this);
                                else Uploader.pickFolder(MainActivity.this);
                            }
                        }).show();
    }

    /* ---------------- 数据 ---------------- */

    void reset() {
        // 去抖：短时间连续 reset 会打断长按手势，也会重复请求
        long now = System.currentTimeMillis();
        if (now - lastResetAt < 800 && !items.isEmpty()) return;
        lastResetAt = now;
        // 记住当前滚动位置，重载后回到原处（否则批量操作后列表会跳回顶部）
        final int keepPos = list == null ? 0 : list.getFirstVisiblePosition();
        final android.view.View keepChild = list == null ? null : list.getChildAt(0);
        final int keepTop = keepChild == null ? 0 : keepChild.getTop();
        gen++;
        items.clear();
        rows.clear();
        cursor = "";
        done = false;
        adapter.notifyDataSetChanged();
        load();
        ui.post(new Runnable() {
            public void run() {
                if (list != null) list.setSelectionFromTop(keepPos, keepTop);
            }
        });
    }

    void load() {
        if (loading || done) return;
        loading = true;
        final int myGen = gen;
        new Thread(new Runnable() {
            public void run() {
                try {
                    StringBuilder q = new StringBuilder("/api/timeline?limit=120");
                    if (!album.isEmpty())
                        // 服务器上 folder 才是"文件夹名"过滤；album 是相册 id（整数）
                        q.append("&folder=").append(java.net.URLEncoder.encode(album, "UTF-8"));
                    // 秘密相册只查 secret=1（这行之前丢了，导致秘密里显示全部照片）
                    if (secret) q.append("&secret=1");
                    if (personId > 0) q.append("&person=").append(personId);
                    if (!ownerFilter.isEmpty())
                        q.append("&owner=").append(java.net.URLEncoder.encode(ownerFilter, "UTF-8"));
                    if (!cursor.isEmpty())
                        q.append("&before=").append(java.net.URLEncoder.encode(cursor, "UTF-8"));
                    android.util.Log.i("LuminaQ", "GET " + q);
                    final String dbg = q.toString();
                    JSONObject o = new JSONObject(Api.get(q.toString()));
                    JSONArray arr = o.optJSONArray("items");
                    final List<JSONObject> fresh = new ArrayList<JSONObject>();
                    if (arr != null) for (int i = 0; i < arr.length(); i++) fresh.add(arr.getJSONObject(i));
                    final String next = o.optString("next", "");
                    ui.post(new Runnable() {
                        public void run() {
                            loading = false;
                            if (fresh.isEmpty() || next.isEmpty()) done = true;
                            cursor = next;
                            // 按 id 去重：多次 load 叠加时不会出现重复条目
                            java.util.Set<Long> have = new java.util.HashSet<Long>();
                            for (JSONObject o : items) have.add(o.optLong("id"));
                            for (JSONObject o : fresh) {
                                if (have.add(o.optLong("id"))) items.add(o);
                            }
                            rebuildRows();
                            updateStatus();
                        }
                    });
                } catch (final Exception e) {
                    ui.post(new Runnable() {
                        public void run() {
                            loading = false;
                            status.setText("加载失败：" + e.getMessage());
                        }
                    });
                }
            }
        }).start();
    }

    void rebuildRows() {
        rows.clear();
        String lastYm = "";
        Row cur = null;
        for (JSONObject o : items) {
            String ym = monthOf(o.optLong("taken_at", 0));
            if (!ym.equals(lastYm)) {
                Row h = new Row();
                h.header = ym;
                rows.add(h);
                lastYm = ym;
                cur = null;
            }
            if (cur == null || cur.cells.size() >= cols) {
                cur = new Row();
                rows.add(cur);
            }
            cur.cells.add(o);
        }
        adapter.notifyDataSetChanged();
    }

    static String monthOf(long sec) {
        if (sec <= 0) return "未知时间";
        java.util.Calendar c = java.util.Calendar.getInstance();
        c.setTimeInMillis(sec * 1000L);
        return c.get(java.util.Calendar.YEAR) + " 年 " + (c.get(java.util.Calendar.MONTH) + 1) + " 月";
    }

    void updateStatus() {
        int vids = 0;
        for (JSONObject o : items) if ("video".equals(o.optString("kind"))) vids++;
        status.setText(items.size() + " 项" + (vids > 0 ? " · " + vids + " 视频" : "")
                + (done ? "" : " · 加载中…"));
    }

    void clearSel() {
        sel.clear();
        selBar.setVisibility(View.GONE);
        adapter.notifyDataSetChanged();
    }

    void toggleSel(String id) {
        if (sel.contains(id)) sel.remove(id); else sel.add(id);
        if (sel.isEmpty()) {
            selBar.setVisibility(View.GONE);
            selBar2.setVisibility(View.GONE);
        } else {
            selBar.setVisibility(View.VISIBLE);
            selBar2.setVisibility(View.VISIBLE);
            selCount.setText("已选 " + sel.size() + " 项");
        }
        adapter.notifyDataSetChanged();
    }

    void batchTrash() {
        if (sel.isEmpty()) return;
        final StringBuilder body = new StringBuilder("{\"ids\":[");
        boolean first = true;
        for (String x : sel) { if (!first) body.append(','); body.append(x); first = false; }
        body.append("]}");
        new Thread(new Runnable() {
            public void run() {
                try {
                    Api.post("/api/trash", body.toString());
                    ui.post(new Runnable() { public void run() {
                        Toast.makeText(MainActivity.this, "已移入回收站", Toast.LENGTH_SHORT).show();
                        clearSel();
                        dataDirty = true;   // 通知普通相册刷新
                        reset();
                    }});
                } catch (final Exception e) {
                    ui.post(new Runnable() { public void run() {
                        Toast.makeText(MainActivity.this, "失败：" + e.getMessage(),
                                Toast.LENGTH_LONG).show();
                    }});
                }
            }
        }).start();
    }

    void batchSecret(final boolean toSecret) {
        if (sel.isEmpty()) return;
        final StringBuilder body = new StringBuilder("{\"secret\":" + toSecret + ",\"ids\":[");
        boolean first = true;
        for (String x : sel) { if (!first) body.append(','); body.append(x); first = false; }
        body.append("]}");
        new Thread(new Runnable() {
            public void run() {
                try {
                    Api.post("/api/secret/move", body.toString());
                    ui.post(new Runnable() { public void run() {
                        Toast.makeText(MainActivity.this,
                                toSecret ? "已移入秘密相册" : "已移回普通相册",
                                Toast.LENGTH_SHORT).show();
                        clearSel();
                        dataDirty = true;   // 通知普通相册刷新
                        reset();
                    }});
                } catch (final Exception e) {
                    ui.post(new Runnable() { public void run() {
                        Toast.makeText(MainActivity.this, "失败：" + e.getMessage(),
                                Toast.LENGTH_LONG).show();
                    }});
                }
            }
        }).start();
    }

    String idsJson() {
        StringBuilder b = new StringBuilder("[");
        boolean first = true;
        for (String x : sel) { if (!first) b.append(','); b.append(x); first = false; }
        return b.append(']').toString();
    }

    /** 批量收藏。 */
    void batchFav() {
        if (sel.isEmpty()) return;
        final String body = "{\"ids\":" + idsJson() + ",\"value\":1}";
        new Thread(new Runnable() {
            public void run() {
                try {
                    Api.post("/api/batch/fav", body);
                    ui.post(new Runnable() { public void run() {
                        Toast.makeText(MainActivity.this, "已收藏 " + sel.size() + " 项",
                                Toast.LENGTH_SHORT).show();
                        clearSel();
                    }});
                } catch (final Exception e) {
                    ui.post(new Runnable() { public void run() {
                        Toast.makeText(MainActivity.this, "失败：" + e.getMessage(),
                                Toast.LENGTH_LONG).show();
                    }});
                }
            }
        }).start();
    }

    /** 推送到家庭共享空间（服务器做硬链接，不占额外空间）。 */
    void batchPush() {
        if (sel.isEmpty()) return;
        final String body = "{\"ids\":" + idsJson() + "}";
        new Thread(new Runnable() {
            public void run() {
                try {
                    String r = Api.post("/api/batch/push", body);
                    final String msg = r.contains("pushed") ? "已推送到家庭共享" : r;
                    ui.post(new Runnable() { public void run() {
                        Toast.makeText(MainActivity.this, msg, Toast.LENGTH_SHORT).show();
                        clearSel();
                    }});
                } catch (final Exception e) {
                    ui.post(new Runnable() { public void run() {
                        Toast.makeText(MainActivity.this, "失败：" + e.getMessage(),
                                Toast.LENGTH_LONG).show();
                    }});
                }
            }
        }).start();
    }

    /** 打包成 ZIP 存到手机（App 专属目录，不用存储权限）。 */
    void batchZip() {
        if (sel.isEmpty()) return;
        final String body = "{\"ids\":" + idsJson() + "}";
        Toast.makeText(this, "正在打包 " + sel.size() + " 项…", Toast.LENGTH_SHORT).show();
        new Thread(new Runnable() {
            public void run() {
                try {
                    byte[] data = Api.postRaw("/api/zip", body.getBytes("UTF-8"),
                            "application/json");
                    java.io.File dir = getExternalFilesDir(null);
                    if (dir == null) dir = getFilesDir();
                    if (!dir.exists()) dir.mkdirs();
                    String name = "lumina-" + new java.text.SimpleDateFormat("yyyyMMdd-HHmm")
                            .format(new java.util.Date()) + ".zip";
                    java.io.File out = new java.io.File(dir, name);
                    java.io.FileOutputStream fo = new java.io.FileOutputStream(out);
                    fo.write(data);
                    fo.close();
                    final String msg = "已保存：" + out.getAbsolutePath()
                            + "（" + (data.length / 1048576) + " MB）";
                    ui.post(new Runnable() { public void run() {
                        Toast.makeText(MainActivity.this, msg, Toast.LENGTH_LONG).show();
                        clearSel();
                    }});
                } catch (final Exception e) {
                    ui.post(new Runnable() { public void run() {
                        Toast.makeText(MainActivity.this, "打包失败：" + e.getMessage(),
                                Toast.LENGTH_LONG).show();
                    }});
                }
            }
        }).start();
    }

    /* ---------------- 适配器 ---------------- */

    class GridAdapter extends BaseAdapter {
        public int getCount() { return rows.size(); }
        public Object getItem(int i) { return rows.get(i); }
        public long getItemId(int i) { return i; }

        public View getView(int pos, View convert, ViewGroup parent) {
            Row r = rows.get(pos);
            if (r.header != null) {
                final String mon = r.header;
                int cnt = 0;
                boolean all = true;
                for (JSONObject o : items) {
                    if (monthOf(o.optLong("taken_at", 0)).equals(mon)) {
                        cnt++;
                        if (!sel.contains(String.valueOf(o.optLong("id")))) all = false;
                    }
                }
                LinearLayout hrow = new LinearLayout(MainActivity.this);
                hrow.setOrientation(LinearLayout.HORIZONTAL);
                hrow.setGravity(Gravity.CENTER_VERTICAL);
                hrow.setPadding(28, 22, 28, 6);
                TextView t = new TextView(MainActivity.this);
                t.setText(mon + "   " + cnt + " 项");
                t.setTextColor(0xFFB9C0CF);
                t.setTextSize(13);
                hrow.addView(t, new LinearLayout.LayoutParams(0,
                        LinearLayout.LayoutParams.WRAP_CONTENT, 1f));
                TextView box = new TextView(MainActivity.this);
                box.setText(all && cnt > 0 ? "\u2611 全选本月" : "\u2610 全选本月");
                box.setTextColor(all && cnt > 0 ? 0xFF2DD4BF : 0xFF8A93A6);
                box.setTextSize(12);
                box.setPadding(12, 6, 12, 6);
                hrow.addView(box);
                hrow.setOnClickListener(new View.OnClickListener() {
                    public void onClick(View v) { toggleMonth(mon); }
                });
                return hrow;
            }
            LinearLayout rowView = new LinearLayout(MainActivity.this);
            rowView.setOrientation(LinearLayout.HORIZONTAL);
            rowView.setPadding(10, 2, 10, 2);
            int w = (getResources().getDisplayMetrics().widthPixels - 20) / cols;
            for (int i = 0; i < cols; i++) {
                FrameLayout cell = new FrameLayout(MainActivity.this);
                LinearLayout.LayoutParams lp = new LinearLayout.LayoutParams(w, w);
                lp.setMargins(3, 3, 3, 3);
                cell.setLayoutParams(lp);
                if (i < r.cells.size()) bindCell(cell, r.cells.get(i));
                rowView.addView(cell);
            }
            return rowView;
        }
    }

    void bindCell(FrameLayout cell, JSONObject o) {
        final String id = String.valueOf(o.optLong("id"));
        cell.removeAllViews();
        ImageView iv = new ImageView(this);
        iv.setScaleType(ImageView.ScaleType.CENTER_CROP);
        iv.setBackgroundColor(Color.parseColor("#12151C"));
        cell.addView(iv, new FrameLayout.LayoutParams(
                ViewGroup.LayoutParams.MATCH_PARENT, ViewGroup.LayoutParams.MATCH_PARENT));
        Bitmap bm = memCache.get(id);
        if (bm != null) iv.setImageBitmap(bm);
        else loadThumb(id, iv);

        if ("video".equals(o.optString("kind"))) {
            TextView badge = new TextView(this);
            badge.setText("▶ " + fmtDur(Math.round(o.optDouble("duration", 0))));
            badge.setTextSize(9);
            badge.setTextColor(0xFFFFFFFF);
            badge.setBackgroundColor(0xCC000000);
            badge.setPadding(8, 2, 8, 2);
            FrameLayout.LayoutParams blp = new FrameLayout.LayoutParams(
                    ViewGroup.LayoutParams.WRAP_CONTENT, ViewGroup.LayoutParams.WRAP_CONTENT);
            blp.gravity = Gravity.BOTTOM | Gravity.END;
            blp.setMargins(0, 0, 6, 6);
            cell.addView(badge, blp);
        }

        if (sel.contains(id)) {
            // 选中：深青遮罩 + 左上角白色对勾 + 缩到 0.88，一眼能分辨
            View overlay = new View(this);
            overlay.setBackgroundColor(0x9934D399);
            cell.addView(overlay, new FrameLayout.LayoutParams(
                    ViewGroup.LayoutParams.MATCH_PARENT, ViewGroup.LayoutParams.MATCH_PARENT));
            TextView tick = new TextView(this);
            tick.setText("\u2713");
            tick.setTextSize(15);
            tick.setGravity(Gravity.CENTER);
            tick.setTextColor(0xFF0A0C11);
            tick.setBackgroundColor(0xFFFFFFFF);
            FrameLayout.LayoutParams tlp = new FrameLayout.LayoutParams(dp(26), dp(26));
            tlp.setMargins(dp(6), dp(6), 0, 0);
            cell.addView(tick, tlp);
            iv.setScaleX(0.88f);
            iv.setScaleY(0.88f);
        }

        cell.setOnClickListener(new View.OnClickListener() {
            public void onClick(View v) {
                if (!sel.isEmpty()) { toggleSel(id); return; }
                openViewer(id);
            }
        });
        cell.setLongClickable(true);
        cell.setHapticFeedbackEnabled(true);
        cell.setOnLongClickListener(new View.OnLongClickListener() {
            public boolean onLongClick(View v) { toggleSel(id); return true; }
        });
    }

    void loadThumb(final String id, final ImageView iv) {
        iv.setTag(id);
        new Thread(new Runnable() {
            public void run() {
                try {
                    byte[] d;
                    java.io.File f = new java.io.File(thumbDir, id + ".jpg");
                    if (f.isFile() && f.length() > 0) {
                        d = readAll(f);
                    } else {
                        d = Api.raw("/t/" + id + "/t.jpg");
                        try {
                            java.io.FileOutputStream fo = new java.io.FileOutputStream(f);
                            fo.write(d);
                            fo.close();
                        } catch (Throwable ignored) { }
                    }
                    final Bitmap bm = BitmapFactory.decodeByteArray(d, 0, d.length);
                    if (bm == null) return;
                    memCache.put(id, bm);
                    ui.post(new Runnable() {
                        public void run() {
                            if (id.equals(iv.getTag())) iv.setImageBitmap(bm);
                        }
                    });
                } catch (Throwable ignored) { }
            }
        }).start();
    }

    static byte[] readAll(java.io.File f) throws Exception {
        java.io.FileInputStream in = new java.io.FileInputStream(f);
        java.io.ByteArrayOutputStream bo = new java.io.ByteArrayOutputStream();
        byte[] buf = new byte[32768];
        int n;
        while ((n = in.read(buf)) > 0) bo.write(buf, 0, n);
        in.close();
        return bo.toByteArray();
    }

    static String fmtDur(long sec) {
        if (sec <= 0) return "00:00";
        if (sec >= 3600) return String.format("%d:%02d:%02d", sec / 3600, (sec % 3600) / 60, sec % 60);
        return String.format("%02d:%02d", sec / 60, sec % 60);
    }

    static void dropThumb(long id) {
        try { memCache.remove(String.valueOf(id)); } catch (Throwable ignored) { }
        try { new java.io.File(thumbDir, id + ".jpg").delete(); } catch (Throwable ignored) { }
    }

    void openViewer(String id) {
        StringBuilder ids = new StringBuilder();
        int idx = 0;
        for (int i = 0; i < items.size(); i++) {
            JSONObject o = items.get(i);
            if (ids.length() > 0) ids.append(',');
            ids.append(o.optLong("id"));
            if (String.valueOf(o.optLong("id")).equals(id)) idx = i;
        }
        Intent it = new Intent(this, ViewerActivity.class);
        it.putExtra("ids", ids.toString());
        it.putExtra("index", idx);
        startActivity(it);
    }

    @Override protected void onResume() {
        super.onResume();
        // 从查看器返回时不要重新加载：整表重建会把滚动位置弹回顶部
        if (adapter != null && (items.isEmpty() || dataDirty)) {
            dataDirty = false;
            reset();
        }
    }

    @Override protected void onActivityResult(int req, int res, android.content.Intent data) {
        super.onActivityResult(req, res, data);
        Uploader.handleResult(this, req, res, data);
    }

    /** 申请读取照片/视频的权限（Android 13+ 是 READ_MEDIA_*）。 */
    void askMediaPerm() {
        try {
            if (android.os.Build.VERSION.SDK_INT < 23) return;
            java.util.List<String> need = new java.util.ArrayList<String>();
            String[] want = android.os.Build.VERSION.SDK_INT >= 33
                    ? new String[]{"android.permission.READ_MEDIA_IMAGES",
                                   "android.permission.READ_MEDIA_VIDEO"}
                    : new String[]{"android.permission.READ_EXTERNAL_STORAGE"};
            for (String w : want) {
                if (checkSelfPermission(w) != android.content.pm.PackageManager.PERMISSION_GRANTED) {
                    need.add(w);
                }
            }
            if (!need.isEmpty()) {
                requestPermissions(need.toArray(new String[0]), 1001);
            }
        } catch (Throwable ignored) { }
    }

    /** 全选当前已加载的全部照片。 */
    void selectAll() {
        for (JSONObject o : items) sel.add(String.valueOf(o.optLong("id")));
        if (!sel.isEmpty()) {
            selBar.setVisibility(View.VISIBLE);
            selBar2.setVisibility(View.VISIBLE);
            selCount.setText("已选 " + sel.size() + " 项");
        }
        adapter.notifyDataSetChanged();
    }

    /** 点月份标题：整月全选 / 取消。 */
    void toggleMonth(String mon) {
        java.util.List<String> ids = new ArrayList<String>();
        for (JSONObject o : items) {
            if (monthOf(o.optLong("taken_at", 0)).equals(mon)) {
                ids.add(String.valueOf(o.optLong("id")));
            }
        }
        if (ids.isEmpty()) return;
        boolean all = sel.containsAll(ids);
        if (all) sel.removeAll(ids); else sel.addAll(ids);
        if (sel.isEmpty()) {
            selBar.setVisibility(View.GONE);
            selBar2.setVisibility(View.GONE);
        } else {
            selBar.setVisibility(View.VISIBLE);
            selBar2.setVisibility(View.VISIBLE);
            selCount.setText("已选 " + sel.size() + " 项");
        }
        adapter.notifyDataSetChanged();
    }
    int dp(int v) { return (int) (v * getResources().getDisplayMetrics().density); }
}
