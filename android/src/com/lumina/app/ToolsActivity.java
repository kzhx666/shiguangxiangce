package com.lumina.app;
import android.app.Activity;
import android.os.Bundle;
import android.webkit.*;
import android.net.Uri;
/** Authenticated first-party web tools, without native JavaScript bridges. */
public class ToolsActivity extends Activity {
 WebView web;
 public void onCreate(Bundle b){super.onCreate(b);web=new WebView(this);web.getSettings().setJavaScriptEnabled(true);web.getSettings().setDomStorageEnabled(true);web.getSettings().setAllowFileAccess(false);web.getSettings().setAllowContentAccess(false);
  final String base=Prefs.server(this);CookieManager.getInstance().setCookie(base,Prefs.cookie(this));CookieManager.getInstance().flush();
  web.setWebViewClient(new WebViewClient(){public boolean shouldOverrideUrlLoading(WebView v,WebResourceRequest req){Uri target=req.getUrl(),origin=Uri.parse(base);return !java.util.Objects.equals(target.getScheme(),origin.getScheme())||!java.util.Objects.equals(target.getAuthority(),origin.getAuthority());}});setContentView(web);web.loadUrl(base);
 }
 public void onBackPressed(){if(web.canGoBack())web.goBack();else super.onBackPressed();}
 protected void onDestroy(){web.destroy();super.onDestroy();}
}
