package com.lumina.app;

import android.app.Activity;
import android.content.ContentResolver;
import android.content.Context;
import android.content.Intent;
import android.database.Cursor;
import android.net.Uri;
import android.provider.OpenableColumns;
import android.provider.MediaStore;
import android.widget.Toast;

import org.json.JSONObject;

import android.graphics.Bitmap;
import android.graphics.BitmapFactory;
import android.media.MediaMetadataRetriever;
import java.io.ByteArrayOutputStream;
import java.io.InputStream;

public class Uploader {

    /** true 时上传的文件直接进秘密相册（服务器 init 接口支持 secret 字段）。 */
    public static volatile boolean secretUpload = false;
    public static final int REQ_FILES = 101;
    public static final int REQ_FOLDER = 102;

    public static void pickFiles(Activity a) {
        Intent i = new Intent(Intent.ACTION_OPEN_DOCUMENT);
        i.addCategory(Intent.CATEGORY_OPENABLE);
        i.setType("*/*");
        i.putExtra(Intent.EXTRA_ALLOW_MULTIPLE, true);
        a.startActivityForResult(i, REQ_FILES);
    }

    public static void pickFolder(Activity a) {
        Intent i = new Intent(Intent.ACTION_OPEN_DOCUMENT_TREE);
        a.startActivityForResult(i, REQ_FOLDER);
    }

    static String nameOf(Context c, Uri u) {
        Cursor cur = null;
        try {
            cur = c.getContentResolver().query(u, null, null, null, null);
            if (cur != null && cur.moveToFirst()) {
                int i = cur.getColumnIndex(OpenableColumns.DISPLAY_NAME);
                if (i >= 0) return cur.getString(i);
            }
        } catch (Exception e) {
        } finally { if (cur != null) cur.close(); }
        String p = u.getLastPathSegment();
        return p == null ? "upload.bin" : p.substring(p.lastIndexOf('/') + 1);
    }

        static String bucketOf(Context c, Uri u) {
        Cursor cur = null;
        try {
            cur = c.getContentResolver().query(u, new String[]{MediaStore.MediaColumns.BUCKET_DISPLAY_NAME}, null, null, null);
            if (cur != null && cur.moveToFirst()) {
                int i = cur.getColumnIndex(MediaStore.MediaColumns.BUCKET_DISPLAY_NAME);
                if (i >= 0) {
                    String b = cur.getString(i);
                    if (b != null && !b.trim().isEmpty()) return b.trim();
                }
            }
        } catch (Exception e) {
        } finally { if (cur != null) cur.close(); }
        return "";
    }

    static long mtimeOf(Context c, Uri u) {
        Cursor cur = null;
        try {
            cur = c.getContentResolver().query(u, new String[]{MediaStore.MediaColumns.DATE_MODIFIED}, null, null, null);
            if (cur != null && cur.moveToFirst()) {
                int i = cur.getColumnIndex(MediaStore.MediaColumns.DATE_MODIFIED);
                if (i >= 0) return cur.getLong(i) * 1000;
            }
        } catch (Exception e) {
        } finally { if (cur != null) cur.close(); }
        return 0;
    }

    static long sizeOf(Context c, Uri u) {
        Cursor cur = null;
        try {
            cur = c.getContentResolver().query(u, null, null, null, null);
            if (cur != null && cur.moveToFirst()) {
                int i = cur.getColumnIndex(OpenableColumns.SIZE);
                if (i >= 0) return cur.getLong(i);
            }
        } catch (Exception e) {
        } finally { if (cur != null) cur.close(); }
        return -1;
    }

    /** 上传一个 Uri，owner/folder/sub 由调用方给定。返回 true 表示成功或已存在。 */
    public static boolean upload(Context c, Uri u, String owner, String folder, String sub, long mtime) {
        if(folder==null||folder.isEmpty())folder=bucketOf(c,u);
        if(sub==null)sub="";
        if(mtime<=0)mtime=mtimeOf(c,u);
        return Transfers.upload(c,u,owner,folder,sub,mtime,secretUpload);
    }

    static void sendPosterIfPossible(Context c, Uri u, String name, long assetId) {
        if (assetId <= 0) return;
        try {
            String lower = name.toLowerCase();
            byte[] jpeg = null;
            if (lower.endsWith(".heic") || lower.endsWith(".heif")) {
                BitmapFactory.Options opts = new BitmapFactory.Options();
                opts.inJustDecodeBounds = true;
                InputStream in = c.getContentResolver().openInputStream(u);
                BitmapFactory.decodeStream(in, null, opts);
                if (in != null) in.close();

                int maxDim = Math.max(opts.outWidth, opts.outHeight);
                int sample = 1;
                while (maxDim / sample > 1280) sample *= 2;
                opts.inJustDecodeBounds = false;
                opts.inSampleSize = sample;

                in = c.getContentResolver().openInputStream(u);
                Bitmap bm = BitmapFactory.decodeStream(in, null, opts);
                if (in != null) in.close();
                if (bm != null) {
                    ByteArrayOutputStream bos = new ByteArrayOutputStream();
                    bm.compress(Bitmap.CompressFormat.JPEG, 85, bos);
                    bm.recycle();
                    jpeg = bos.toByteArray();
                }
            } else if (lower.endsWith(".mp4") || lower.endsWith(".mov") || lower.endsWith(".3gp") || lower.endsWith(".mkv")) {
                MediaMetadataRetriever mmr = new MediaMetadataRetriever();
                mmr.setDataSource(c, u);
                Bitmap bm = mmr.getFrameAtTime(1000000); // 1s
                if (bm == null) bm = mmr.getFrameAtTime();
                mmr.release();
                if (bm != null) {
                    int maxDim = Math.max(bm.getWidth(), bm.getHeight());
                    if (maxDim > 1280) {
                        float scale = 1280f / maxDim;
                        Bitmap scaled = Bitmap.createScaledBitmap(bm, Math.round(bm.getWidth() * scale), Math.round(bm.getHeight() * scale), true);
                        bm.recycle();
                        bm = scaled;
                    }
                    ByteArrayOutputStream bos = new ByteArrayOutputStream();
                    bm.compress(Bitmap.CompressFormat.JPEG, 80, bos);
                    bm.recycle();
                    jpeg = bos.toByteArray();
                }
            }
            if (jpeg != null && jpeg.length > 512) {
                Api.postBytes("/api/poster/" + assetId, jpeg, "image/jpeg");
            }
        } catch (Throwable t) {
            // Ignore failure, server has fallback
        }
    }

    /** 递归上传一个用 SAF 选中的文件夹。 */
    public static int uploadTree(Context c, Uri tree, String owner) {
        try {
            android.provider.DocumentsContract.getTreeDocumentId(tree);
        } catch (Exception e) {
            return 0;
        }
        java.util.List<Uri> files = new java.util.ArrayList<Uri>();
        java.util.List<String> subs = new java.util.ArrayList<String>();
        walk(c, tree, "", files, subs, 0);
        for(int i=0;i<files.size();i++)try{Transfers.prepare(c,files.get(i),owner,"",subs.get(i),0,secretUpload);}catch(Exception ignored){}
        int ok = 0;
        for (int i = 0; i < files.size(); i++) {
            if (upload(c, files.get(i), owner, "", subs.get(i), 0)) ok++;
        }
        return ok;
    }

    static void walk(Context c, Uri tree, String sub, java.util.List<Uri> files, java.util.List<String> subs, int depth) {
        if (depth > 6) return;
        String docId = android.provider.DocumentsContract.getTreeDocumentId(tree);
        Uri children = android.provider.DocumentsContract.buildChildDocumentsUriUsingTree(tree, docId);
        android.database.Cursor cur = null;
        try {
            cur = c.getContentResolver().query(children, new String[]{
                    android.provider.DocumentsContract.Document.COLUMN_DOCUMENT_ID,
                    android.provider.DocumentsContract.Document.COLUMN_DISPLAY_NAME,
                    android.provider.DocumentsContract.Document.COLUMN_MIME_TYPE}, null, null, null);
            if (cur == null) return;
            while (cur.moveToNext()) {
                String id = cur.getString(0);
                String name = cur.getString(1);
                String mime = cur.getString(2);
                Uri child = android.provider.DocumentsContract.buildDocumentUriUsingTree(tree, id);
                if (android.provider.DocumentsContract.Document.MIME_TYPE_DIR.equals(mime)) {
                    walk(c, child, sub.isEmpty() ? name : sub + "/" + name, files, subs, depth + 1);
                } else if (mime != null && (mime.startsWith("image/") || mime.startsWith("video/"))) {
                    files.add(child);
                    subs.add(sub);
                }
            }
        } catch (Exception e) {
        } finally {
            if (cur != null) cur.close();
        }
    }
    /** 为超大图生成 1280 预览：优先本地原图，没有再下载。 */
    public static byte[] makePreview(Context c, String name, long assetId) {
        Bitmap bm = null;
        android.database.Cursor cur = null;
        try {
            cur = c.getContentResolver().query(
                    android.provider.MediaStore.Images.Media.EXTERNAL_CONTENT_URI,
                    new String[]{android.provider.MediaStore.MediaColumns._ID},
                    android.provider.MediaStore.MediaColumns.DISPLAY_NAME + "=?",
                    new String[]{name}, null);
            if (cur != null && cur.moveToFirst()) {
                Uri u = Uri.withAppendedPath(
                        android.provider.MediaStore.Images.Media.EXTERNAL_CONTENT_URI,
                        String.valueOf(cur.getLong(0)));
                bm = decodeScaled(c.getContentResolver().openInputStream(u), 1600);
            }
        } catch (Throwable ignored) {
        } finally { if (cur != null) cur.close(); }

        if (bm == null) {
            try {
                byte[] raw = Api.raw("/m/" + assetId);
                if (raw != null && raw.length > 0)
                    bm = decodeScaled(new java.io.ByteArrayInputStream(raw), 1600);
            } catch (Throwable ignored) { }
        }
        if (bm == null) return null;

        int w = bm.getWidth(), h = bm.getHeight();
        float sc = Math.min(1f, 1280f / Math.max(w, h));
        Bitmap small = sc < 1f ? Bitmap.createScaledBitmap(bm, Math.max(1, Math.round(w * sc)),
                Math.max(1, Math.round(h * sc)), true) : bm;
        ByteArrayOutputStream bo = new ByteArrayOutputStream();
        small.compress(Bitmap.CompressFormat.JPEG, 85, bo);
        if (small != bm) small.recycle();
        bm.recycle();
        return bo.toByteArray();
    }

    /** 按目标最大边采样解码，避免把上亿像素的原图整张读进内存。 */
    static Bitmap decodeScaled(InputStream in, int maxSide) {
        if (in == null) return null;
        try {
            ByteArrayOutputStream bo = new ByteArrayOutputStream();
            byte[] buf = new byte[65536];
            int r;
            while ((r = in.read(buf)) > 0) bo.write(buf, 0, r);
            in.close();
            byte[] data = bo.toByteArray();
            BitmapFactory.Options o = new BitmapFactory.Options();
            o.inJustDecodeBounds = true;
            BitmapFactory.decodeByteArray(data, 0, data.length, o);
            int sample = 1;
            while (Math.max(o.outWidth, o.outHeight) / sample > maxSide * 2) sample *= 2;
            BitmapFactory.Options o2 = new BitmapFactory.Options();
            o2.inSampleSize = sample;
            return BitmapFactory.decodeByteArray(data, 0, data.length, o2);
        } catch (Throwable t) {
            return null;
        }
    }

    /** 统一处理文件/文件夹选择结果。返回 true 表示这个 requestCode 归我们管。 */
    public static boolean handleResult(final android.app.Activity a, int req, int res,
                                       final Intent data) {
        if (req != REQ_FOLDER && req != REQ_FILES) return false;
        if (res != android.app.Activity.RESULT_OK || data == null) return true;
        final android.content.Context c = a.getApplicationContext();
        final String owner = Prefs.owner(c);
        if (req == REQ_FOLDER) {
            final android.net.Uri tree = data.getData();
            if (tree == null) return true;
            try {
                c.getContentResolver().takePersistableUriPermission(tree,
                        Intent.FLAG_GRANT_READ_URI_PERMISSION);
            } catch (Throwable ignored) { }
            android.widget.Toast.makeText(c, "开始上传这个文件夹…", android.widget.Toast.LENGTH_SHORT).show();
            new Thread(new Runnable() {
                public void run() {
                    int n = uploadTree(c, tree, owner);
                    a.runOnUiThread(()->android.widget.Toast.makeText(c,"文件夹上传完成："+n+" 个文件",android.widget.Toast.LENGTH_LONG).show());
                }
            }).start();
            return true;
        }
        // Persist SAF grants so retries survive process death.
        try {
            if(data.getClipData()!=null)for(int i=0;i<data.getClipData().getItemCount();i++)c.getContentResolver().takePersistableUriPermission(data.getClipData().getItemAt(i).getUri(),Intent.FLAG_GRANT_READ_URI_PERMISSION);
            else if(data.getData()!=null)c.getContentResolver().takePersistableUriPermission(data.getData(),Intent.FLAG_GRANT_READ_URI_PERMISSION);
        }catch(SecurityException ignored){}
        // REQ_FILES：多选
        new Thread(new Runnable() {
            public void run() {
                int ok = 0, all = 0;
                try{if(data.getClipData()!=null)for(int i=0;i<data.getClipData().getItemCount();i++)Transfers.prepare(c,data.getClipData().getItemAt(i).getUri(),owner,"","",0,secretUpload);
                else if(data.getData()!=null)Transfers.prepare(c,data.getData(),owner,"","",0,secretUpload);}catch(Exception e){a.runOnUiThread(()->Toast.makeText(c,"任务记录保存失败，请检查存储空间",1).show());return;}
                if (data.getClipData() != null) {
                    int n = data.getClipData().getItemCount();
                    all = n;
                    for (int i = 0; i < n; i++) {
                        android.net.Uri u = data.getClipData().getItemAt(i).getUri();
                        try {
                            if (upload(c, u, owner, "", "", mtimeOf(c, u))) ok++;
                        } catch (Throwable ignored) { }
                    }
                } else if (data.getData() != null) {
                    all = 1;
                    try {
                        if (upload(c, data.getData(), owner, "", "",
                                mtimeOf(c, data.getData()))) ok++;
                    } catch (Throwable ignored) { }
                }
                final int uploaded=ok,total=all;a.runOnUiThread(()->android.widget.Toast.makeText(c,"上传完成："+uploaded+" / "+total,android.widget.Toast.LENGTH_LONG).show());
            }
        }).start();
        return true;
    }
}
