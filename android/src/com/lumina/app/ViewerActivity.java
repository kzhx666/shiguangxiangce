package com.lumina.app;

import android.app.Activity;
import android.content.Intent;
import android.graphics.BitmapFactory;
import android.graphics.Color;
import android.media.MediaPlayer;
import android.os.Bundle;
import android.os.Handler;
import android.os.Looper;
import android.view.Gravity;
import android.view.GestureDetector;
import android.view.MotionEvent;
import android.view.ScaleGestureDetector;
import android.view.View;
import android.view.ViewGroup;
import android.widget.FrameLayout;
import android.widget.ImageView;
import android.widget.LinearLayout;
import android.widget.MediaController;
import android.widget.TextView;
import android.widget.Toast;
import android.widget.VideoView;

import org.json.JSONObject;

import java.io.File;
import java.io.FileOutputStream;

public class ViewerActivity extends Activity {
    String[] ids = new String[0];
    int idx = 0;
    FrameLayout stage;
    TextView caption;
    final Handler ui = new Handler(Looper.getMainLooper());
    float scale = 1f, tx = 0, ty = 0;
    ScaleGestureDetector pinch;
    View rotBtnV, infoBtnV;
    final android.os.Handler uiH = new android.os.Handler(android.os.Looper.getMainLooper());
    final Runnable hideCtrl = new Runnable() { public void run() { setCtrl(false); } };
    GestureDetector fling;
    ImageView img;

    @Override protected void onCreate(Bundle b) {
        super.onCreate(b);
        String s = getIntent().getStringExtra("ids");
        if (s != null && !s.isEmpty()) ids = s.split(",");
        idx = getIntent().getIntExtra("index", 0);

        FrameLayout root = new FrameLayout(this);
        root.setBackgroundColor(Color.BLACK);
        stage = new FrameLayout(this);
        root.addView(stage, new FrameLayout.LayoutParams(ViewGroup.LayoutParams.MATCH_PARENT, ViewGroup.LayoutParams.MATCH_PARENT));

        caption = new TextView(this);
        caption.setTextColor(Color.WHITE);
        caption.setTextSize(12);
        caption.setPadding(28, 40, 28, 40);
        FrameLayout.LayoutParams clp = new FrameLayout.LayoutParams(ViewGroup.LayoutParams.MATCH_PARENT, ViewGroup.LayoutParams.WRAP_CONTENT);
        clp.gravity = Gravity.BOTTOM;
        clp.bottomMargin = 110;
        caption.setVisibility(android.view.View.GONE);   // 默认不显示，点 ⓘ 才出来
        root.addView(caption, clp);
        // 旋转：只对照片有效，转完由服务器写回原文件
        TextView rotBtn = new TextView(this);
        rotBtn.setText("旋转");
        // 详情：小图标，点一下才展开/收起下面那行文字
        android.widget.TextView infoBtn = Ui.chip(this, "ⓘ");
        infoBtn.setOnClickListener(new View.OnClickListener() {
            public void onClick(View v) {
                caption.setVisibility(caption.getVisibility() == View.VISIBLE
                        ? View.GONE : View.VISIBLE);
            }
        });
        android.graphics.drawable.GradientDrawable rbg =
                new android.graphics.drawable.GradientDrawable();
        rbg.setColor(0x66000000);
        rbg.setCornerRadius(48);
        rotBtn.setBackground(rbg);
        rotBtn.setTextColor(0xFFE8ECF4);
        rotBtn.setTextSize(13);
        rotBtn.setPadding(30, 14, 30, 14);
        FrameLayout.LayoutParams rlp = new FrameLayout.LayoutParams(
                ViewGroup.LayoutParams.WRAP_CONTENT, ViewGroup.LayoutParams.WRAP_CONTENT);
        rlp.gravity = Gravity.BOTTOM | Gravity.END;
        rlp.rightMargin = 24;
        rlp.bottomMargin = 28;
        rotBtn.setOnClickListener(new View.OnClickListener() {
            public void onClick(View v) { doRotate(); }
        });
        root.addView(rotBtn, rlp);
        rotBtnV = rotBtn;
        rotBtn.setVisibility(View.GONE);

        FrameLayout.LayoutParams ilp = new FrameLayout.LayoutParams(
                ViewGroup.LayoutParams.WRAP_CONTENT, ViewGroup.LayoutParams.WRAP_CONTENT);
        ilp.gravity = Gravity.BOTTOM | Gravity.START;
        ilp.leftMargin = 24;
        ilp.bottomMargin = 28;
        root.addView(infoBtn, ilp);
        infoBtnV = infoBtn;
        infoBtn.setVisibility(View.GONE);
        setContentView(root);

        pinch = new ScaleGestureDetector(this, new ScaleGestureDetector.SimpleOnScaleGestureListener() {
            public boolean onScale(ScaleGestureDetector d) {
                scale = Math.max(1f, Math.min(20f, scale * d.getScaleFactor()));
                if (img != null) { img.setScaleX(scale); img.setScaleY(scale); }
                return true;
            }
        });
        fling = new GestureDetector(this, new GestureDetector.SimpleOnGestureListener() {
            public boolean onFling(MotionEvent a, MotionEvent c, float vx, float vy) {
                if (scale > 1.2f) return false;
                if (Math.abs(vx) > 400) {
                    if (vx < 0) show(idx + 1); else show(idx - 1);
                    return true;
                }
                return false;
            }
            public boolean onDown(MotionEvent e) { return true; }
            public boolean onDoubleTap(MotionEvent e) {
                // 双击：显示控件，3 秒后自动淡出
                setCtrl(true);
                uiH.removeCallbacks(hideCtrl);
                uiH.postDelayed(hideCtrl, 3000);
                return true;
            }
        });
        stage.setOnTouchListener(new View.OnTouchListener() {
            public boolean onTouch(View v, MotionEvent e) {
                pinch.onTouchEvent(e);
                fling.onTouchEvent(e);
                return true;
            }
        });

        show(idx);
    }

    /** 显示/隐藏查看器上的控件（旋转、详情）。 */
    void setCtrl(boolean on) {
        int v = on ? View.VISIBLE : View.GONE;
        if (rotBtnV != null) rotBtnV.setVisibility(v);
        if (infoBtnV != null) infoBtnV.setVisibility(v);
        if (!on && caption != null) caption.setVisibility(View.GONE);
    }

    void show(int i) {
        if (ids.length == 0) return;
        if (i < 0) i = 0;
        if (i >= ids.length) i = ids.length - 1;
        idx = i;
        final String id = ids[idx];
        stage.removeAllViews();
        scale = 1f; tx = 0; ty = 0;
        img = null;
        caption.setText((i + 1) + " / " + ids.length + "  ·  #" + id);

        new Thread(new Runnable() {
            public void run() {
                try {
                    JSONObject a = new JSONObject(Api.get("/api/asset/" + id));
                    final String kind = a.optString("kind");
                    final String name = a.optString("name");
                    final String path = a.optString("rel_path");
                    final long size = a.optLong("size", 0);
                    final boolean hasPoster = a.optBoolean("poster", false);

                    if ("video".equals(kind)) {
                        // 边下边播：本地服务 + 4 路并行取块，播放器连本地
                        final int port = PlayCache.ensureServer();
                        if (port > 0 && size > 0) {
                            final PlayCache pc = new PlayCache(id, size,
                                    new java.io.File(getCacheDir(), "stream"));
                            java.io.File sd = new java.io.File(getCacheDir(), "stream");
                            if (!sd.exists()) sd.mkdirs();
                            PlayCache.register(pc);
                            pc.start();
                            ui.post(new Runnable() {
                                public void run() { playUrl("http://127.0.0.1:" + port + "/v/" + id,
                                        name, id, hasPoster); }
                            });
                            return;
                        }
                    }
                    if (false) {
                        final java.io.File cache =
                                new java.io.File(getCacheDir(), "videos/" + id + ".mp4");
                        if (cache.isFile() && cache.length() > 0
                                && (size <= 0 || cache.length() == size)) {
                            // 已经缓存过：直接本地播放，秒开
                            ui.post(new Runnable() {
                                public void run() { playLocal(cache, name, id, hasPoster); }
                            });
                            return;
                        }
                        // 分段并发下载：单连接只有 1.3MB/s，4 路并发约 6MB/s
                        ui.post(new Runnable() {
                            public void run() { caption.setText("缓冲中… 0%"); }
                        });
                        try {
                            Net.download(id, size, cache, new Net.Progress() {
                                public void onProgress(final int pct) {
                                    ui.post(new Runnable() {
                                        public void run() {
                                            caption.setText("缓冲中… " + pct + "%");
                                        }
                                    });
                                }
                            });
                            ui.post(new Runnable() {
                                public void run() { playLocal(cache, name, id, hasPoster); }
                            });
                        } catch (final Throwable t) {
                            // 下载失败就退回在线播
                            ui.post(new Runnable() {
                                public void run() { playStream(name, id, hasPoster); }
                            });
                        }
                    } else {
                        final byte[] d = Api.raw("/m/" + id);
                        ui.post(new Runnable() {
                            public void run() {
                                img = new ImageView(ViewerActivity.this);
                                img.setImageBitmap(BitmapFactory.decodeByteArray(d, 0, d.length));
                                img.setScaleType(ImageView.ScaleType.FIT_CENTER);
                                stage.addView(img, new FrameLayout.LayoutParams(
                                        ViewGroup.LayoutParams.MATCH_PARENT,
                                        ViewGroup.LayoutParams.MATCH_PARENT));
                                caption.setText(name + "\n" + path);
                            }
                        });
                    }
                } catch (final Exception e) {
                    ui.post(new Runnable() {
                        public void run() {
                            Toast.makeText(ViewerActivity.this, "加载失败：" + e.getMessage(),
                                    Toast.LENGTH_LONG).show();
                        }
                    });
                }
            }
        }).start();
    }

    /** 顺时针旋转 90°，服务器会把像素写回原文件。 */
    void doRotate() {
        if (idx < 0 || idx >= ids.length) return;
        final String id = ids[idx];
        new Thread(new Runnable() {
            public void run() {
                try {
                    org.json.JSONObject req = new org.json.JSONObject();
                    req.put("dir", 1);
                    Api.post("/api/rotate/" + id, req.toString());
                    ui.post(new Runnable() {
                        public void run() {
                            Toast.makeText(ViewerActivity.this, "已旋转并保存",
                                    Toast.LENGTH_SHORT).show();
                            show(idx);
                        }
                    });
                } catch (final Exception e) {
                    ui.post(new Runnable() {
                        public void run() {
                            Toast.makeText(ViewerActivity.this, "旋转失败：" + e.getMessage(),
                                    Toast.LENGTH_LONG).show();
                        }
                    });
                }
            }
        }).start();
    }
    /** 播放失败：说清楚原因，并给一个"用系统播放器打开"的出口，别让页面直接退出。 */
    /**
     * 从视频流里截一帧，压缩成 1280px 的 JPEG 传给服务器当封面。
     * 服务器的 ffmpeg 在这台路由器上解不了视频，所以封面只能由手机生成。
     * @param id 资产 id
     * @param need true 表示服务器还没有封面
     */
    void capturePoster(final String id, final boolean need) {
        if (!need) return;
        new Thread(new Runnable() {
            public void run() {
                android.media.MediaMetadataRetriever r = new android.media.MediaMetadataRetriever();
                try {
                    java.util.Map<String, String> hdr = new java.util.HashMap<String, String>();
                    if (!Api.cookie.isEmpty()) hdr.put("Cookie", Api.cookie);
                    r.setDataSource(Api.base + "/m/" + id, hdr);
                    android.graphics.Bitmap bm = r.getFrameAtTime(1500000,
                            android.media.MediaMetadataRetriever.OPTION_CLOSEST_SYNC);
                    if (bm == null) bm = r.getFrameAtTime(0);
                    if (bm == null) return;
                    int w = bm.getWidth(), h = bm.getHeight();
                    float sc = Math.min(1f, 1280f / Math.max(w, h));
                    android.graphics.Bitmap small = sc < 1f
                            ? android.graphics.Bitmap.createScaledBitmap(bm,
                                Math.max(1, Math.round(w * sc)), Math.max(1, Math.round(h * sc)), true)
                            : bm;
                    java.io.ByteArrayOutputStream bo = new java.io.ByteArrayOutputStream();
                    small.compress(android.graphics.Bitmap.CompressFormat.JPEG, 85, bo);
                    if (small != bm) small.recycle();
                    bm.recycle();
                    long assetId = Long.parseLong(id);
                    Api.postPoster(assetId, bo.toByteArray());
                    try { MainActivity.dropThumb(assetId); } catch (Throwable ignored) { }
                    android.util.Log.i("LuminaSecret", "poster captured for " + id);
                } catch (Throwable t) {
                    android.util.Log.e("LuminaSecret", "poster capture failed " + id + ": " + t);
                } finally {
                    try { r.release(); } catch (Throwable ignored) { }
                }
            }
        }).start();
    }

    void showPlaybackError(String name, final String id) {
        try {
            stage.removeAllViews();
            LinearLayout box = new LinearLayout(this);
            box.setOrientation(LinearLayout.VERTICAL);
            box.setGravity(Gravity.CENTER);
            TextView t = new TextView(this);
            t.setText("这个视频手机解不了\n可能是特殊编码（MPEG-4 Part2 / 部分 MOV）\n文件本身没问题");
            t.setTextColor(0xFFB9C0CF);
            t.setTextSize(13);
            t.setGravity(Gravity.CENTER);
            t.setPadding(60, 0, 60, 30);
            box.addView(t);
            TextView open = Ui.chipOn(this, "用系统播放器打开", true);
            open.setOnClickListener(new View.OnClickListener() {
                public void onClick(View v) {
                    try {
                        Intent i = new Intent(Intent.ACTION_VIEW);
                        i.setDataAndType(android.net.Uri.parse(Api.base + "/m/" + id), "video/*");
                        startActivity(Intent.createChooser(i, "打开方式"));
                    } catch (Throwable e) {
                        Toast.makeText(ViewerActivity.this, "没有可用的播放器",
                                Toast.LENGTH_SHORT).show();
                    }
                }
            });
            box.addView(open);
            stage.addView(box, new FrameLayout.LayoutParams(
                    ViewGroup.LayoutParams.MATCH_PARENT, ViewGroup.LayoutParams.MATCH_PARENT));
            caption.setText(name);
        } catch (Throwable ignored) { }
    }
    /** 把视频流式写到本地文件（64KB 缓冲，不占内存）。 */

    /** 播放本地缓存的视频；播起来之后从本地文件截一帧当封面。 */
    void playLocal(final java.io.File f, String name, final String id, final boolean needPoster) {
        stage.removeAllViews();
        final VideoView vv = new VideoView(this);
        FrameLayout.LayoutParams lp = new FrameLayout.LayoutParams(
                ViewGroup.LayoutParams.MATCH_PARENT, ViewGroup.LayoutParams.MATCH_PARENT);
        stage.addView(vv, lp);
        MediaController mc = new MediaController(this);
        mc.setAnchorView(stage);
        vv.setMediaController(mc);
        vv.setVideoPath(f.getAbsolutePath());
        vv.setOnPreparedListener(new MediaPlayer.OnPreparedListener() {
            public void onPrepared(MediaPlayer mp) {
                vv.start();
                caption.setText(name);
                if (needPoster) capturePoster(f.getAbsolutePath(), id);
            }
        });
        vv.setOnErrorListener(new MediaPlayer.OnErrorListener() {
            public boolean onError(MediaPlayer mp, int what, int extra) {
                showPlaybackError(name, id);
                return true;
            }
        });
    }

    /** 超大视频直接在线播（不占磁盘），封面尽力截一次。 */
    void playStream(String name, final String id, final boolean needPoster) {
        stage.removeAllViews();
        final VideoView vv = new VideoView(this);
        FrameLayout.LayoutParams lp = new FrameLayout.LayoutParams(
                ViewGroup.LayoutParams.MATCH_PARENT, ViewGroup.LayoutParams.MATCH_PARENT);
        stage.addView(vv, lp);
        MediaController mc = new MediaController(this);
        mc.setAnchorView(stage);
        vv.setMediaController(mc);
        java.util.Map<String, String> hdr = new java.util.HashMap<String, String>();
        if (!Api.cookie.isEmpty()) hdr.put("Cookie", Api.cookie);
        vv.setVideoURI(android.net.Uri.parse(Api.base + "/m/" + id), hdr);
        vv.setOnPreparedListener(new MediaPlayer.OnPreparedListener() {
            public void onPrepared(MediaPlayer mp) {
                vv.start();
                if (needPoster) capturePoster(id, true);
            }
        });
        vv.setOnErrorListener(new MediaPlayer.OnErrorListener() {
            public boolean onError(MediaPlayer mp, int what, int extra) {
                showPlaybackError(name, id);
                return true;
            }
        });
        caption.setText(name);
    }

    /** 从本地文件截一帧当封面。 */
    void capturePoster(final String pathOrId, final String id) {
        new Thread(new Runnable() {
            public void run() {
                android.media.MediaMetadataRetriever r = new android.media.MediaMetadataRetriever();
                try {
                    r.setDataSource(pathOrId);
                    android.graphics.Bitmap bm = r.getFrameAtTime(1500000,
                            android.media.MediaMetadataRetriever.OPTION_CLOSEST_SYNC);
                    if (bm == null) bm = r.getFrameAtTime(0);
                    if (bm == null) return;
                    int w = bm.getWidth(), h = bm.getHeight();
                    float sc = Math.min(1f, 1280f / Math.max(w, h));
                    android.graphics.Bitmap small = sc < 1f
                            ? android.graphics.Bitmap.createScaledBitmap(bm,
                                Math.max(1, Math.round(w * sc)), Math.max(1, Math.round(h * sc)), true)
                            : bm;
                    java.io.ByteArrayOutputStream bo = new java.io.ByteArrayOutputStream();
                    small.compress(android.graphics.Bitmap.CompressFormat.JPEG, 85, bo);
                    if (small != bm) small.recycle();
                    bm.recycle();
                    long assetId = Long.parseLong(id);
                    Api.postPoster(assetId, bo.toByteArray());
                    try { MainActivity.dropThumb(assetId); } catch (Throwable ignored) { }
                    android.util.Log.i("LuminaPoster", "poster ok " + id);
                } catch (Throwable t) {
                    android.util.Log.e("LuminaPoster", "poster fail " + id + ": " + t);
                } finally {
                    try { r.release(); } catch (Throwable ignored) { }
                }
            }
        }).start();
    }
    /** 超大视频：在线播一定卡，问用户是先下载还是交给系统播放器。 */
    void askBigVideo(final String name, final String id, final boolean needPoster, long size) {
        final int mb = (int) (size / 1048576L);
        new android.app.AlertDialog.Builder(this)
                .setTitle("这个视频很大（" + mb + " MB）")
                .setMessage("在线播放会卡。建议先下载到手机再播，下载期间会显示进度。")
                .setPositiveButton("下载后播放", new android.content.DialogInterface.OnClickListener() {
                    public void onClick(android.content.DialogInterface d, int w) {
                        final java.io.File cache =
                                new java.io.File(getCacheDir(), "videos/" + id + ".mp4");
                        caption.setText("缓冲中… 0%");
                        new Thread(new Runnable() {
                            public void run() {
                                try {
                                    Net.download(id, size, cache, new Net.Progress() {
                                        public void onProgress(final int pct) {
                                            ui.post(new Runnable() {
                                                public void run() { caption.setText("缓冲中… " + pct + "%"); }
                                            });
                                        }
                                    });
                                    ui.post(new Runnable() {
                                        public void run() { playLocal(cache, name, id, needPoster); }
                                    });
                                } catch (final Throwable t) {
                                    ui.post(new Runnable() {
                                        public void run() { showPlaybackError(name, id); }
                                    });
                                }
                            }
                        }).start();
                    }
                })
                .setNegativeButton("用系统播放器", new android.content.DialogInterface.OnClickListener() {
                    public void onClick(android.content.DialogInterface d, int w) {
                        try {
                            Intent i = new Intent(Intent.ACTION_VIEW);
                            i.setDataAndType(android.net.Uri.parse(Api.base + "/m/" + id), "video/*");
                            startActivity(Intent.createChooser(i, "打开方式"));
                        } catch (Throwable e) {
                            Toast.makeText(ViewerActivity.this, "没有可用的播放器",
                                    Toast.LENGTH_SHORT).show();
                        }
                    }
                })
                .show();
    }


    /** 播放本地代理地址（边下边播）。 */
    void playUrl(String url, String name, final String id, final boolean needPoster) {
        stage.removeAllViews();
        final VideoView vv = new VideoView(this);
        FrameLayout.LayoutParams lp = new FrameLayout.LayoutParams(
                ViewGroup.LayoutParams.MATCH_PARENT, ViewGroup.LayoutParams.MATCH_PARENT);
        stage.addView(vv, lp);
        MediaController mc = new MediaController(this);
        mc.setAnchorView(stage);
        vv.setMediaController(mc);
        vv.setVideoURI(android.net.Uri.parse(url));
        vv.setOnPreparedListener(new MediaPlayer.OnPreparedListener() {
            public void onPrepared(MediaPlayer mp) {
                vv.start();
                // 播起来之后再截帧（不影响播放）
                if (needPoster) {
                    new Thread(new Runnable() {
                        public void run() {
                            try { Thread.sleep(8000); } catch (InterruptedException ignored) { }
                            java.io.File f = new java.io.File(getCacheDir(), "stream/stream_" + id + ".bin");
                            if (f.isFile() && f.length() > 0) capturePoster(f.getAbsolutePath(), id);
                        }
                    }).start();
                }
            }
        });
        vv.setOnErrorListener(new MediaPlayer.OnErrorListener() {
            public boolean onError(MediaPlayer mp, int what, int extra) {
                showPlaybackError(name, id);
                return true;
            }
        });
        caption.setText(name);
    }

    /** 退出时清掉边下边播用的临时文件（videos/ 里的正式缓存保留）。 */
    void cleanupStream() {
        try {
            java.io.File d = new java.io.File(getCacheDir(), "stream");
            java.io.File[] fs = d.listFiles();
            if (fs != null) for (java.io.File f : fs) {
                PlayCache.unregister(f.getName().replace("stream_", "").replace(".bin", ""));
                f.delete();
            }
        } catch (Throwable ignored) { }
    }

    @Override protected void onStop() {
        super.onStop();
        if (isFinishing()) cleanupStream();
    }
}
