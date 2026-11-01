package dev.akbxr.herdsman

import android.annotation.SuppressLint
import android.content.Context
import android.os.Handler
import android.os.Looper
import android.text.InputType
import android.view.KeyEvent
import android.view.View
import android.view.inputmethod.BaseInputConnection
import android.view.inputmethod.EditorInfo
import android.view.inputmethod.InputConnection
import android.view.inputmethod.InputMethodManager
import android.webkit.JavascriptInterface
import android.webkit.WebView
import android.webkit.WebViewClient
import org.json.JSONObject
import java.net.URLEncoder

/** What the terminal page reports: connection state and sizes. */
data class TermStatus(
    val state: String = "ready",       // ready, connecting, live, reconnecting, ended
    val cols: Int = 0, val rows: Int = 0,
    val phoneCols: Int = 0, val phoneRows: Int = 0,
    val driving: Boolean = false,      // the terminal has this phone's size
    val reason: String? = null,
    val attempt: Int = 0,              // reconnecting: how many tries so far
)

/**
 * Drives the terminal page (xterm.js in a WebView, from assets/term) and
 * takes the phone's keyboard. Everything typed goes to the program as is.
 */
class TerminalController(context: Context, private val onStatus: (TermStatus) -> Unit) {
    private val main = Handler(Looper.getMainLooper())
    private var last = TermStatus()
    private var pendingUrl: String? = null
    private var pageReady = false

    /** Sticky Ctrl from the key bar: the next letter becomes a control key. */
    var ctrl = false
    var onCtrlUsed: () -> Unit = {}

    @SuppressLint("SetJavaScriptEnabled")
    val web: WebView = WebView(context).apply {
        setBackgroundColor(0xFF24283B.toInt())
        settings.javaScriptEnabled = true
        settings.domStorageEnabled = true
        isFocusable = false              // the keyboard belongs to [input]
        isFocusableInTouchMode = false
        webViewClient = object : WebViewClient() {}
        addJavascriptInterface(Bridge(), "Android")
        loadUrl("file:///android_asset/term/terminal.html")
    }

    val input: KeyInput = KeyInput(context)

    private inner class Bridge {
        @JavascriptInterface
        fun onStatus(json: String) {
            val o = runCatching { JSONObject(json) }.getOrNull() ?: return
            main.post {
                if (o.optString("state") == "ready") {
                    pageReady = true
                    pendingUrl?.let { start(it) }
                    return@post
                }
                last = when (o.optString("state")) {
                    "size" -> last.copy(
                        cols = o.optInt("cols"), rows = o.optInt("rows"),
                        phoneCols = o.optInt("phoneCols"), phoneRows = o.optInt("phoneRows"),
                        driving = o.optBoolean("driving"),
                    )
                    else -> last.copy(state = o.optString("state"), reason = o.optString("reason").ifBlank { null }, attempt = o.optInt("attempt"))
                }
                onStatus(last)
            }
        }

        @JavascriptInterface
        fun onTap() = main.post { showKeyboard() }

        /** A pinch chose a font size: kept for this terminal. */
        @JavascriptInterface
        fun onZoom(px: Float) {
            if (terminal.isNotEmpty()) zooms.edit().putFloat(terminal, px).apply()
        }

        @JavascriptInterface
        fun copy(text: String) = main.post {
            val cm = web.context.getSystemService(android.content.ClipboardManager::class.java)
            cm?.setPrimaryClip(android.content.ClipData.newPlainText("Terminal", text))
            // Android 13 and later show their own confirmation.
            if (android.os.Build.VERSION.SDK_INT < 33) android.widget.Toast.makeText(web.context, "Copied", android.widget.Toast.LENGTH_SHORT).show()
        }

        @JavascriptInterface
        fun haptic() = main.post { web.performHapticFeedback(android.view.HapticFeedbackConstants.LONG_PRESS) }
    }

    private var terminal = ""
    private val zooms = context.getSharedPreferences("zoom", Context.MODE_PRIVATE)

    // Before the page has loaded there is no hr: nothing to tell it yet.
    private fun js(code: String) = main.post { web.evaluateJavascript("window.hr && $code", null) }

    fun connect(api: Api, terminal: String) {
        val ws = api.base.trimEnd('/').replaceFirst("http", "ws") +
            "/api/attach?terminal=" + URLEncoder.encode(terminal, "UTF-8") +
            "&token=" + URLEncoder.encode(api.token, "UTF-8")
        this.terminal = terminal
        if (pageReady) start(ws) else pendingUrl = ws
    }

    private fun start(ws: String) {
        pendingUrl = null
        // A herdr CLI pane keeps herdr's size: the phone doesn't resize it.
        js("hr.connect(${JSONObject.quote(ws)}, ${zooms.getFloat(terminal, 0f)}, ${isHerdrPane(terminal)})")
    }

    /** The network came back: reconnect now rather than at the next try. */
    fun retryNow() = js("hr.retry()")

    fun type(text: String) {
        if (text.isEmpty()) return
        val out = if (ctrl && text.length == 1) {
            ctrl = false
            onCtrlUsed()
            control(text[0])
        } else text
        js("hr.input(${JSONObject.quote(out)})")
    }

    /**
     * Whether the phone's keyboard covers part of the screen. While it does,
     * the terminal keeps its rows and only scrolls: shrinking it would make
     * the program reflow twice per keystroke session, and herdr-core grows a
     * terminal by adding blank rows at the bottom.
     */
    private var keyboardUp = false
    fun keyboard(up: Boolean) {
        if (up == keyboardUp) return
        keyboardUp = up
        js("hr.keyboard($up)")
    }

    fun key(name: String) = js("hr.key(${JSONObject.quote(name)})")
    fun paste(text: String) = js("hr.paste(${JSONObject.quote(text)})")

    fun showKeyboard() {
        input.requestFocus()
        val imm = input.context.getSystemService(Context.INPUT_METHOD_SERVICE) as InputMethodManager
        imm.showSoftInput(input, InputMethodManager.SHOW_IMPLICIT)
    }

    fun hideKeyboard() {
        val imm = input.context.getSystemService(Context.INPUT_METHOD_SERVICE) as InputMethodManager
        imm.hideSoftInputFromWindow(input.windowToken, 0)
    }

    fun close() {
        js("hr.close()")
        main.postDelayed({ web.destroy() }, 300)
    }

    /** ctrl+a … ctrl+z, ctrl+[ … as the bytes a terminal sends. */
    private fun control(c: Char): String = when (val u = c.uppercaseChar()) {
        in 'A'..'Z', '[', '\\', ']', '^', '_' -> (u.code - 64).toChar().toString()
        ' ', '@', '2' -> "\u0000"
        '?' -> "\u007f"
        else -> c.toString()
    }

    /**
     * An invisible view that holds the phone's keyboard, the way terminal apps
     * do: a visible-password field, so no suggestions, no autocorrect and no
     * composing; each key is sent as typed.
     */
    inner class KeyInput(context: Context) : View(context) {
        init {
            isFocusable = true
            isFocusableInTouchMode = true
        }

        override fun onCheckIsTextEditor() = true

        override fun onCreateInputConnection(outAttrs: EditorInfo): InputConnection {
            outAttrs.inputType = InputType.TYPE_CLASS_TEXT or
                InputType.TYPE_TEXT_VARIATION_VISIBLE_PASSWORD or
                InputType.TYPE_TEXT_FLAG_NO_SUGGESTIONS
            outAttrs.imeOptions = EditorInfo.IME_FLAG_NO_EXTRACT_UI or
                EditorInfo.IME_FLAG_NO_FULLSCREEN or EditorInfo.IME_ACTION_NONE
            return object : BaseInputConnection(this, false) {
                override fun commitText(text: CharSequence, newCursorPosition: Int): Boolean {
                    type(text.toString().replace('\n', '\r'))
                    return true
                }

                // Keyboards that compose anyway: send the text right away.
                override fun setComposingText(text: CharSequence, newCursorPosition: Int): Boolean {
                    if (text.isNotEmpty()) type(text.toString())
                    return true
                }

                override fun deleteSurroundingText(beforeLength: Int, afterLength: Int): Boolean {
                    repeat(beforeLength.coerceAtLeast(1)) { key("backspace") }
                    return true
                }

                override fun sendKeyEvent(event: KeyEvent): Boolean {
                    if (event.action == KeyEvent.ACTION_DOWN) handleKey(event)
                    return true
                }
            }
        }

        override fun onKeyDown(keyCode: Int, event: KeyEvent): Boolean =
            handleKey(event) || super.onKeyDown(keyCode, event)

        /** Hardware keys and the keyboard's own Enter/Delete. */
        private fun handleKey(event: KeyEvent): Boolean {
            val name = when (event.keyCode) {
                KeyEvent.KEYCODE_ENTER, KeyEvent.KEYCODE_NUMPAD_ENTER -> "enter"
                KeyEvent.KEYCODE_DEL -> "backspace"
                KeyEvent.KEYCODE_FORWARD_DEL -> "del"
                KeyEvent.KEYCODE_TAB -> if (event.isShiftPressed) "shifttab" else "tab"
                KeyEvent.KEYCODE_ESCAPE -> "esc"
                KeyEvent.KEYCODE_DPAD_UP -> "up"
                KeyEvent.KEYCODE_DPAD_DOWN -> "down"
                KeyEvent.KEYCODE_DPAD_LEFT -> "left"
                KeyEvent.KEYCODE_DPAD_RIGHT -> "right"
                KeyEvent.KEYCODE_MOVE_HOME -> "home"
                KeyEvent.KEYCODE_MOVE_END -> "end"
                KeyEvent.KEYCODE_PAGE_UP -> "pgup"
                KeyEvent.KEYCODE_PAGE_DOWN -> "pgdn"
                else -> null
            }
            if (name != null) { key(name); return true }
            val ch = event.unicodeChar
            if (ch == 0) return false
            if (event.isCtrlPressed) type(control(ch.toChar())) else type(ch.toChar().toString())
            return true
        }
    }
}
