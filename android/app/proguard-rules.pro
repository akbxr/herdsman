# The terminal page calls into the app through window.Android (TerminalView.kt);
# proguard-android-optimize.txt keeps @JavascriptInterface methods, and this
# keeps the bridge classes' names out of R8's way too.
-keep class dev.akbxr.herdrremote.*$Bridge { *; }

# Stack traces in crash reports stay readable.
-keepattributes SourceFile,LineNumberTable
