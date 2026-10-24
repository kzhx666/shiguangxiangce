package com.lumina.app;

import android.content.Context;
import android.content.SharedPreferences;
import android.net.Uri;
import org.json.*;
import java.io.InputStream;
import java.util.*;

/** Durable, account-scoped upload journal. The server bitmap is authoritative after restart. */
public final class Transfers {
 static final Set<String> active = Collections.synchronizedSet(new HashSet<String>());
 static SharedPreferences prefs(Context c){return c.getSharedPreferences("transfers_v2",Context.MODE_PRIVATE);}
 static String scope(Context c){return Prefs.server(c)+"\n"+Prefs.user(c)+"\n";}
 static String key(Context c,Uri uri,String owner,String folder,String sub,boolean secret){return scope(c)+uri+"\n"+owner+"/"+folder+"/"+sub+"/"+secret+"/"+Uploader.sizeOf(c,uri)+"/"+Uploader.mtimeOf(c,uri);}
 static synchronized void save(Context c,String key,JSONObject o)throws Exception{if(!prefs(c).edit().putString(key,o.toString()).commit())throw new Exception("无法保存任务记录，请检查手机空间");}
 static synchronized JSONObject read(Context c,String key)throws Exception{return new JSONObject(prefs(c).getString(key,"{}"));}
 public static synchronized Map<String,JSONObject> list(Context c){Map<String,JSONObject> out=new LinkedHashMap<>();for(Map.Entry<String,?>e:prefs(c).getAll().entrySet()){if(e.getKey().startsWith(scope(c)))try{out.put(e.getKey(),new JSONObject(String.valueOf(e.getValue())));}catch(Exception ignored){}}return out;}
 static void check(Context c,String key)throws Exception{if(!key.startsWith(scope(c))||!Api.base.equals(Prefs.server(c))||!Api.user.equals(Prefs.user(c)))throw new Exception("账号已切换，请回原账号继续上传");String state=read(c,key).optString("state");if(state.equals("paused")||state.equals("cancelled"))throw new Exception(state.equals("paused")?"已暂停":"已取消");}
 public static synchronized void state(Context c,String key,String state)throws Exception{JSONObject o=read(c,key);o.put("state",state);save(c,key,o);}
 static synchronized void progress(Context c,String key,JSONObject o)throws Exception{String state=read(c,key).optString("state");if(state.equals("paused")||state.equals("cancelled"))o.put("state",state);save(c,key,o);}
 static boolean retryable(Exception e){String m=String.valueOf(e.getMessage());return !(m.contains("HTTP 400")||m.contains("HTTP 401")||m.contains("HTTP 403")||m.contains("HTTP 409"));}
 public static void prepare(Context c,Uri uri,String owner,String folder,String sub,long mtime,boolean secret)throws Exception{
  if(folder==null||folder.isEmpty())folder=Uploader.bucketOf(c,uri);if(sub==null)sub="";if(mtime<=0)mtime=Uploader.mtimeOf(c,uri);
  String key=key(c,uri,owner,folder,sub,secret);JSONObject task=read(c,key);if(task.has("state"))return;
  task.put("uri",uri.toString()).put("owner",owner).put("folder",folder).put("sub",sub).put("secret",secret).put("mtime",mtime).put("name",Uploader.nameOf(c,uri)).put("size",Uploader.sizeOf(c,uri)).put("state","queued");save(c,key,task);
 }
 public static boolean upload(Context c,Uri uri,String owner,String folder,String sub,long mtime,boolean secret){
  String key=key(c,uri,owner,folder,sub,secret);if(!active.add(key))return false;
  JSONObject task=new JSONObject();
  try{
   task=read(c,key);String previous=task.optString("state");if(previous.equals("paused")||previous.equals("cancelled"))return false;
   long size=Uploader.sizeOf(c,uri);if(size<=0)throw new Exception("无法读取文件；请重新选择文件或授予照片权限");
   String name=Uploader.nameOf(c,uri);task.put("uri",uri.toString()).put("owner",owner).put("folder",folder).put("sub",sub).put("secret",secret).put("mtime",mtime).put("name",name).put("size",size).put("state","uploading").put("error","");save(c,key,task);
   JSONObject session=null;String sid=task.optString("sid");
   if(!sid.isEmpty())try{session=new JSONObject(Api.get("/api/upload/"+sid));}catch(Exception e){if(!String.valueOf(e.getMessage()).contains("404"))throw e;sid="";}
   if(session==null){JSONObject req=new JSONObject();req.put("owner",owner).put("name",name).put("size",size).put("mtime",mtime/1000).put("folder",folder).put("sub",sub).put("secret",secret);session=new JSONObject(Api.post("/api/upload/init",req.toString()));sid=session.getString("sid");task.put("sid",sid);progress(c,key,task);}
   int chunk=session.getInt("chunk"),total=session.getInt("total");Set<Integer> have=new HashSet<>();JSONArray arr=session.optJSONArray("received");long done=0;
   if(arr!=null)for(int i=0;i<arr.length();i++){int n=arr.getInt(i);have.add(n);done+=Math.min(chunk,size-(long)n*chunk);}
   long started=System.currentTimeMillis(),newBytes=0;
   try(InputStream in=c.getContentResolver().openInputStream(uri)){
    if(in==null)throw new Exception("文件不可读取");byte[] buf=new byte[chunk];
    for(int i=0;i<total;i++){
     check(c,key);int expected=(int)Math.min(chunk,size-(long)i*chunk),len=0;
     if(have.contains(i)){long remaining=expected;while(remaining>0){long n=in.skip(remaining);if(n<=0){if(in.read()<0)throw new Exception("文件已变化，请重新选择");n=1;}remaining-=n;}continue;}
     while(len<expected){int n=in.read(buf,len,expected-len);if(n<0)throw new Exception("文件长度变化，请重新选择");len+=n;}
     for(int attempt=0;;attempt++){check(c,key);try{Api.putChunk(sid,i,buf,len);break;}catch(Exception e){if(attempt>=2||!retryable(e))throw e;Thread.sleep(1000L<<attempt);}}
     done+=len;newBytes+=len;task.put("done",done).put("speed",newBytes*1000/Math.max(1,System.currentTimeMillis()-started));progress(c,key,task);
    }
   }
   check(c,key);JSONObject complete=new JSONObject(Api.post("/api/upload/"+sid+"/complete","{}"));long asset=complete.optLong("asset");
   task.put("state","done").put("done",size).put("speed",0);save(c,key,task);
   if(asset>0){try{Api.postAlias(asset,name,size);}catch(Exception ignored){}Uploader.sendPosterIfPossible(c,uri,name,asset);}return true;
  }catch(Exception e){try{String st=read(c,key).optString("state");task.put("state",st.equals("paused")||st.equals("cancelled")?st:"failed").put("error",String.valueOf(e.getMessage())).put("speed",0);save(c,key,task);}catch(Exception ignored){}return false;}
  finally{active.remove(key);}
 }
 public static void resume(Context c,String key){new Thread(()->{try{JSONObject t=read(c,key);state(c,key,"queued");upload(c,Uri.parse(t.getString("uri")),t.getString("owner"),t.optString("folder"),t.optString("sub"),t.optLong("mtime"),t.optBoolean("secret"));}catch(Exception ignored){}}).start();}
 public static void cancel(Context c,String key)throws Exception{state(c,key,"cancelled");new Thread(()->{try{while(active.contains(key))Thread.sleep(250);String sid=read(c,key).optString("sid");if(!sid.isEmpty()){java.net.HttpURLConnection conn=Api.conn("/api/upload/"+sid,"DELETE");conn.getResponseCode();conn.disconnect();}}catch(Exception ignored){}}).start();}
}
