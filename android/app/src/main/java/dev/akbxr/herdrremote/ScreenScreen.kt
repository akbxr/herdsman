package dev.akbxr.herdrremote

import android.graphics.Bitmap
import androidx.compose.foundation.Image
import androidx.compose.foundation.background
import androidx.compose.foundation.border
import androidx.compose.foundation.clickable
import androidx.compose.foundation.gestures.detectTapGestures
import androidx.compose.foundation.gestures.detectTransformGestures
import androidx.compose.foundation.horizontalScroll
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.Spacer
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.height
import androidx.compose.foundation.layout.heightIn
import androidx.compose.foundation.layout.width
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.rememberScrollState
import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableFloatStateOf
import androidx.compose.runtime.mutableLongStateOf
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.rememberCoroutineScope
import androidx.compose.runtime.setValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.draw.clip
import androidx.compose.ui.geometry.Offset
import androidx.compose.ui.graphics.asImageBitmap
import androidx.compose.ui.graphics.graphicsLayer
import androidx.compose.ui.input.pointer.pointerInput
import androidx.compose.ui.layout.ContentScale
import androidx.compose.ui.semantics.Role
import androidx.compose.ui.unit.dp
import kotlinx.coroutines.delay
import kotlinx.coroutines.launch
import org.json.JSONObject

@Composable
fun ScreenScreen(api: Api, herd: Herd?) {
    // Only the herdr CLI runs: no window to picture.
    if (herd != null && !herd.app) NoWindow() else WindowPicture(api, herd)
}

@Composable
private fun WindowPicture(api: Api, herd: Herd?) {
    var picture by remember { mutableStateOf<Bitmap?>(null) }
    var takenAt by remember { mutableLongStateOf(0L) }
    var now by remember { mutableLongStateOf(System.currentTimeMillis()) }
    var problem by remember { mutableStateOf<String?>(null) }
    var scale by remember { mutableFloatStateOf(1f) }
    var offset by remember { mutableStateOf(Offset.Zero) }
    val scope = rememberCoroutineScope()

    val onScreen = rememberOnScreen()
    LaunchedEffect(onScreen) {
        if (!onScreen) return@LaunchedEffect
        while (true) {
            runCatching { api.shot() }
                .onSuccess { picture = it; takenAt = System.currentTimeMillis(); problem = null }
                .onFailure { problem = it.advice() }
            delay(3000)
        }
    }
    LaunchedEffect(Unit) { while (true) { now = System.currentTimeMillis(); delay(1000) } }

    fun act(block: suspend () -> Unit) {
        scope.launch { runCatching { block() }.onFailure { problem = it.advice() } }
    }

    Column(Modifier.fillMaxSize()) {
        Row(
            Modifier.fillMaxWidth().padding(start = 24.dp, end = 12.dp, top = 20.dp),
            verticalAlignment = Alignment.CenterVertically,
        ) {
            Column(Modifier.weight(1f)) {
                Text("The Mac's window", style = Type.title, color = Ink.text)
                val age = if (takenAt == 0L) "Taking a picture…" else "Pictured ${((now - takenAt) / 1000).coerceAtLeast(0)}s ago · pinch to zoom"
                Text(age, style = Type.small, color = Ink.dust)
            }
            QuietButton("Next layout", color = Ink.text) { act { api.command("layout.switch") } }
        }

        // Tabs on the Mac, every space's (the one showing first): tap one to
        // show it there, its space too.
        val tabs = herd?.tabs.orEmpty().sortedByDescending { it.spaceActive }
        val spaces = tabs.map { it.spaceId }.distinct()
        if (tabs.isNotEmpty()) {
            Row(
                Modifier.fillMaxWidth().horizontalScroll(rememberScrollState()).padding(horizontal = 24.dp, vertical = 12.dp),
                horizontalArrangement = Arrangement.spacedBy(8.dp),
                verticalAlignment = Alignment.CenterVertically,
            ) {
                tabs.forEachIndexed { i, tab ->
                    // Each space's name before its tabs, when there are several.
                    if (spaces.size > 1 && (i == 0 || tabs[i - 1].spaceId != tab.spaceId)) {
                        if (i > 0) Spacer(Modifier.width(6.dp))
                        // Another machine's space says which: "api · vps".
                        val spaceLabel = if (tab.machine.isEmpty()) tab.spaceName else "${tab.spaceName} · ${tab.machine}"
                        Text(spaceLabel, style = Type.small, color = if (tab.spaceActive) Ink.text else Ink.dust, maxLines = 1)
                    }
                    val shown = tab.active && tab.spaceActive
                    val agents = herd?.agents.orEmpty().filter { !it.cli && it.tabId == tab.id }
                    val label = agents.firstOrNull()?.title?.let { "${tab.name}  $it" } ?: tab.name
                    Box(
                        Modifier
                            .heightIn(min = 40.dp)
                            .clip(RoundedCornerShape(10.dp))
                            .background(if (shown) Ink.text else Ink.pen)
                            .border(1.dp, if (shown) Ink.text else Ink.rule, RoundedCornerShape(10.dp))
                            .clickable(role = Role.Tab) { act { api.showTab(tab.id, if (tab.spaceActive) "" else tab.spaceId) } }
                            .padding(horizontal = 14.dp, vertical = 9.dp),
                        contentAlignment = Alignment.Center,
                    ) {
                        Text(label, style = Type.label, color = if (shown) Ink.night else Ink.text, maxLines = 1)
                    }
                }
            }
        } else {
            Spacer(Modifier.height(12.dp))
        }

        problem?.let { Notice(it, Modifier.padding(horizontal = 24.dp).padding(bottom = 12.dp)) }

        Box(
            Modifier
                .weight(1f)
                .fillMaxWidth()
                .padding(horizontal = 12.dp)
                .padding(bottom = 12.dp)
                .clip(RoundedCornerShape(14.dp))
                .background(Ink.pen)
                .pointerInput(Unit) {
                    detectTransformGestures { _, pan, zoom, _ ->
                        scale = (scale * zoom).coerceIn(1f, 6f)
                        offset = if (scale == 1f) Offset.Zero else offset + pan
                    }
                }
                .pointerInput(Unit) {
                    detectTapGestures(onDoubleTap = { scale = 1f; offset = Offset.Zero })
                },
            contentAlignment = Alignment.Center,
        ) {
            picture?.let {
                Image(
                    bitmap = it.asImageBitmap(),
                    contentDescription = "Picture of the Herdr window on your Mac",
                    contentScale = ContentScale.Fit,
                    modifier = Modifier.fillMaxSize().graphicsLayer {
                        scaleX = scale; scaleY = scale
                        translationX = offset.x; translationY = offset.y
                    },
                )
            }
        }
    }
}

/** The Screen tab without the Herdr app: a calm note, not an error. */
@Composable
fun NoWindow() {
    Column(Modifier.fillMaxSize().padding(start = 24.dp, end = 24.dp, top = 20.dp)) {
        Text("The Mac's window", style = Type.title, color = Ink.text)
        Text("The Herdr app isn't running", style = Type.small, color = Ink.dust)
        Spacer(Modifier.height(16.dp))
        Hint("The Screen tab shows the Herdr app's window; herdr CLI has none. Its panes are on the Herd tab.")
    }
}
