package com.lumina.app;

import android.content.Context;
import android.view.Gravity;
import android.view.View;
import android.widget.LinearLayout;
import android.widget.TextView;

/** 一点点 UI 小工具：把各种按钮做成统一的小胶囊，省得每处都写一遍。 */
public class Ui {

    public static TextView chip(Context c, String text) {
        TextView t = new TextView(c);
        t.setText(text);
        t.setTextSize(13);
        t.setTextColor(0xFFE8ECF4);
        t.setGravity(Gravity.CENTER);
        t.setPadding(30, 18, 30, 18);
        android.graphics.drawable.GradientDrawable bg = new android.graphics.drawable.GradientDrawable();
        bg.setColor(0xFF171B23);
        bg.setCornerRadius(99);
        bg.setStroke(1, 0x33FFFFFF);
        t.setBackground(bg);
        return t;
    }

    public static TextView chipOn(Context c, String text, boolean on) {
        TextView t = chip(c, text);
        android.graphics.drawable.GradientDrawable bg = new android.graphics.drawable.GradientDrawable();
        bg.setColor(on ? 0xFF2DD4BF : 0xFF171B23);
        bg.setCornerRadius(99);
        bg.setStroke(1, on ? 0xFF2DD4BF : 0x33FFFFFF);
        t.setBackground(bg);
        t.setTextColor(on ? 0xFF06211E : 0xFFE8ECF4);
        return t;
    }

    public static TextView label(Context c, String text, int size, int color) {
        TextView t = new TextView(c);
        t.setText(text);
        t.setTextSize(size);
        t.setTextColor(color);
        return t;
    }

    public static LinearLayout row(Context c) {
        LinearLayout l = new LinearLayout(c);
        l.setOrientation(LinearLayout.HORIZONTAL);
        l.setGravity(Gravity.CENTER_VERTICAL);
        return l;
    }

    /** 一行里放几个 chip，自动换行用 LinearLayout 竖排包一层即可。 */
    public static LinearLayout chipRow(Context c, View... chips) {
        LinearLayout l = row(c);
        for (View v : chips) {
            LinearLayout.LayoutParams lp = new LinearLayout.LayoutParams(
                    LinearLayout.LayoutParams.WRAP_CONTENT, LinearLayout.LayoutParams.WRAP_CONTENT);
            lp.setMargins(0, 0, 12, 0);
            l.addView(v, lp);
        }
        return l;
    }
}
