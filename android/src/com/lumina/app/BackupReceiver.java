package com.lumina.app;

import android.content.BroadcastReceiver;
import android.content.Context;
import android.content.Intent;
import android.util.Log;

public class BackupReceiver extends BroadcastReceiver {
    private static final String TAG = "LuminaBackup";

    @Override
    public void onReceive(Context context, Intent intent) {
        Log.i(TAG, "BackupReceiver: received action " + intent.getAction());
        if ("com.lumina.app.BACKUP_NOW".equals(intent.getAction())) {
            BackupService.triggerNow(context);
        }
    }
}
