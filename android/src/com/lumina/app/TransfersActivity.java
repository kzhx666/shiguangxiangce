package com.lumina.app;
import android.app.Activity;
import android.os.*;
import android.widget.*;
import org.json.JSONObject;
import java.util.Map;

public class TransfersActivity extends Activity {
 final Handler handler=new Handler();LinearLayout body;
 final Runnable refresh=new Runnable(){public void run(){render();handler.postDelayed(this,1500);}};
 public void onCreate(Bundle b){super.onCreate(b);ScrollView scroll=new ScrollView(this);body=new LinearLayout(this);body.setOrientation(1);body.setPadding(24,24,24,24);body.setBackgroundColor(0xff101923);scroll.addView(body);setContentView(scroll);}
 protected void onResume(){super.onResume();handler.post(refresh);}protected void onPause(){super.onPause();handler.removeCallbacks(refresh);}
 void render(){body.removeAllViews();TextView title=Ui.label(this,"上传任务",24,0xffeff6ff);body.addView(title);
  Map<String,JSONObject> tasks=Transfers.list(this);if(tasks.isEmpty())body.addView(Ui.label(this,"暂无任务，选择照片后会在这里显示",16,0xffaabbcc));
  for(Map.Entry<String,JSONObject> entry:tasks.entrySet()){final String key=entry.getKey();JSONObject t=entry.getValue();String state=t.optString("state");if(state.equals("uploading")&&!Transfers.active.contains(key))state="interrupted";
   String label=state.equals("done")?"已完成":state.equals("failed")?"失败":state.equals("paused")?"已暂停":state.equals("cancelled")?"已取消":state.equals("interrupted")?"等待恢复":state.equals("queued")?"等待上传":"上传中";
   long size=t.optLong("size"),done=t.optLong("done");TextView text=Ui.label(this,t.optString("name")+"\n"+label+" · "+(size>0?done*100/size:0)+"% · "+(t.optLong("speed")/1024)+" KB/s\n"+t.optString("error"),15,0xffeff6ff);text.setPadding(0,28,0,10);body.addView(text);
   LinearLayout row=Ui.row(this);body.addView(row);
   if(state.equals("uploading")){TextView pause=Ui.chip(this,"暂停");row.addView(pause);pause.setOnClickListener(v->{try{Transfers.state(this,key,"paused");render();}catch(Exception e){Toast.makeText(this,e.getMessage(),1).show();}});}
   if(state.equals("failed")||state.equals("paused")||state.equals("interrupted")||state.equals("queued")){TextView retry=Ui.chip(this,"继续 / 重试");row.addView(retry);retry.setOnClickListener(v->{Transfers.resume(this,key);render();});}
   if(!state.equals("done")&&!state.equals("cancelled")){TextView cancel=Ui.chip(this,"取消");row.addView(cancel);cancel.setOnClickListener(v->{try{Transfers.cancel(this,key);render();}catch(Exception e){Toast.makeText(this,e.getMessage(),1).show();}});}
  }
 }
}
