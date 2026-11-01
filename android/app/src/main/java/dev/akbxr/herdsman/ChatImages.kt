package dev.akbxr.herdsman

import android.graphics.Bitmap
import android.graphics.BitmapFactory
import android.util.LruCache
import androidx.compose.foundation.Image
import androidx.compose.foundation.background
import androidx.compose.foundation.clickable
import androidx.compose.foundation.gestures.detectTapGestures
import androidx.compose.foundation.gestures.detectTransformGestures
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.ExperimentalLayoutApi
import androidx.compose.foundation.layout.FlowRow
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.size
import androidx.compose.foundation.layout.statusBarsPadding
import androidx.compose.foundation.layout.widthIn
import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.filled.Close
import androidx.compose.material3.Icon
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableFloatStateOf
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.setValue
import androidx.compose.runtime.staticCompositionLocalOf
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.draw.clip
import androidx.compose.ui.geometry.Offset
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.graphics.asImageBitmap
import androidx.compose.ui.graphics.graphicsLayer
import androidx.compose.ui.input.pointer.pointerInput
import androidx.compose.ui.layout.ContentScale
import androidx.compose.ui.layout.onSizeChanged
import androidx.compose.ui.semantics.Role
import androidx.compose.ui.unit.Dp
import androidx.compose.ui.unit.IntSize
import androidx.compose.ui.unit.dp
import androidx.compose.ui.unit.sp
import androidx.compose.ui.window.Dialog
import androidx.compose.ui.window.DialogProperties
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.withContext

/** Pixels on the longer side: a picture in a bubble, and in the viewer. */
const val THUMB_PX = 640
const val FULL_PX = 2048

/**
 * The pictures of your messages, from the Mac (GET /api/image): by id, at
 * most [px] on the longer side. Null: it can't be had (gone from the Mac).
 */
interface ChatImages {
    fun cached(id: String, px: Int): Bitmap?
    suspend fun load(id: String, px: Int): Bitmap?
}

val LocalChatImages = staticCompositionLocalOf<ChatImages?> { null }

/** Fetches with the token, decodes off the main thread, keeps what it decoded. */
class ChatImageLoader(private val api: Api) : ChatImages {
    // An eighth of the app's memory, in KB.
    private val cache = object : LruCache<String, Bitmap>((Runtime.getRuntime().maxMemory() / 1024 / 8).toInt()) {
        override fun sizeOf(key: String, value: Bitmap) = value.byteCount / 1024
    }

    override fun cached(id: String, px: Int): Bitmap? = cache.get("$id@$px")

    override suspend fun load(id: String, px: Int): Bitmap? {
        cached(id, px)?.let { return it }
        val bytes = runCatching { api.image(id, px) }.getOrNull() ?: return null
        return withContext(Dispatchers.Default) { decodeSampled(bytes, px) }?.also { cache.put("$id@$px", it) }
    }
}

/** Decodes at the smallest power-of-two step that keeps [maxPx] on the longer side. */
fun decodeSampled(bytes: ByteArray, maxPx: Int): Bitmap? {
    val bounds = BitmapFactory.Options().apply { inJustDecodeBounds = true }
    BitmapFactory.decodeByteArray(bytes, 0, bytes.size, bounds)
    if (bounds.outWidth <= 0 || bounds.outHeight <= 0) return null
    var sample = 1
    while (maxOf(bounds.outWidth, bounds.outHeight) / (sample * 2) >= maxPx) sample *= 2
    return BitmapFactory.decodeByteArray(bytes, 0, bytes.size, BitmapFactory.Options().apply { inSampleSize = sample })
}

private enum class Loading { Waiting, Done, Failed }

/** A picture by id, at [px]: what it is so far. */
@Composable
private fun rememberPicture(id: String, px: Int): Pair<Bitmap?, Loading> {
    val source = LocalChatImages.current
    var bitmap by remember(id, px) { mutableStateOf(source?.cached(id, px)) }
    var state by remember(id, px) { mutableStateOf(if (bitmap != null) Loading.Done else if (source == null) Loading.Failed else Loading.Waiting) }
    LaunchedEffect(id, px) {
        if (bitmap != null || source == null) return@LaunchedEffect
        val b = source.load(id, px)
        bitmap = b
        state = if (b != null) Loading.Done else Loading.Failed
    }
    return bitmap to state
}

/**
 * The pictures in a message: one as it is shaped (within 220×280), more as
 * squares two to a row. Tap one to see it whole.
 */
@OptIn(ExperimentalLayoutApi::class)
@Composable
fun ChatPictures(ids: List<String>, onOpen: (String) -> Unit, modifier: Modifier = Modifier) {
    if (ids.size == 1) {
        Picture(ids[0], single = true, onOpen = onOpen, modifier = modifier)
        return
    }
    FlowRow(
        modifier.widthIn(max = 230.dp),
        horizontalArrangement = Arrangement.spacedBy(6.dp),
        verticalArrangement = Arrangement.spacedBy(6.dp),
        maxItemsInEachRow = 2,
    ) { ids.forEach { Picture(it, single = false, onOpen = onOpen) } }
}

@Composable
private fun Picture(id: String, single: Boolean, onOpen: (String) -> Unit, modifier: Modifier = Modifier) {
    val (bitmap, state) = rememberPicture(id, THUMB_PX)
    val shape = RoundedCornerShape(10.dp)
    val (w, h) = when {
        !single -> 108.dp to 108.dp
        bitmap != null -> fit(bitmap.width, bitmap.height, 220.dp, 280.dp)
        else -> 220.dp to 140.dp
    }
    when {
        bitmap != null -> Image(
            bitmap.asImageBitmap(),
            contentDescription = "Picture, tap to open",
            contentScale = if (single) ContentScale.Fit else ContentScale.Crop,
            modifier = modifier
                .size(w, h)
                .clip(shape)
                .background(TermBg)
                .clickable(role = Role.Image, onClickLabel = "Open the picture") { onOpen(id) },
        )
        state == Loading.Failed -> Box(
            modifier.size(if (single) 160.dp else w, if (single) 56.dp else h).clip(shape).background(Ink.night),
            contentAlignment = Alignment.Center,
        ) { Text("Picture not available", style = Type.small.copy(fontSize = 11.sp), color = Ink.dust) }
        // On its way: a shimmer the shape it will take (darker than the bubble it sits in).
        else -> Box(modifier.size(w, h).clip(shape).shimmerBackground(Ink.night, Ink.rule))
    }
}

/** A w×h picture's size within maxW×maxH, keeping its shape; a thin strip stays tall enough to tap. */
private fun fit(w: Int, h: Int, maxW: Dp, maxH: Dp): Pair<Dp, Dp> {
    val scale = minOf(maxW.value / w, maxH.value / h)
    return (w * scale).coerceAtLeast(56f).dp to (h * scale).coerceAtLeast(40f).dp
}

/**
 * A picture over everything: pinch to zoom, drag when zoomed, double-tap to
 * zoom in or back, tap (unzoomed) or ✕ to close.
 */
@Composable
fun PictureViewer(id: String, onDismiss: () -> Unit) {
    Dialog(onDismissRequest = onDismiss, properties = DialogProperties(usePlatformDefaultWidth = false)) {
        val thumb = LocalChatImages.current?.cached(id, THUMB_PX)
        val (full, state) = rememberPicture(id, FULL_PX)
        val bitmap = full ?: thumb
        var scale by remember { mutableFloatStateOf(1f) }
        var offset by remember { mutableStateOf(Offset.Zero) }
        var size by remember { mutableStateOf(IntSize.Zero) }
        fun bounded(o: Offset): Offset {
            val mx = (scale - 1f) * size.width / 2f
            val my = (scale - 1f) * size.height / 2f
            return Offset(o.x.coerceIn(-mx, mx), o.y.coerceIn(-my, my))
        }
        Box(Modifier.fillMaxSize().background(Color.Black.copy(alpha = 0.95f)).onSizeChanged { size = it }) {
            when {
                bitmap != null -> Image(
                    bitmap.asImageBitmap(),
                    contentDescription = "Picture",
                    contentScale = ContentScale.Fit,
                    modifier = Modifier
                        .fillMaxSize()
                        .pointerInput(Unit) {
                            detectTransformGestures { _, pan, zoom, _ ->
                                scale = (scale * zoom).coerceIn(1f, 6f)
                                offset = if (scale == 1f) Offset.Zero else bounded(offset + pan)
                            }
                        }
                        .pointerInput(Unit) {
                            detectTapGestures(
                                onDoubleTap = {
                                    if (scale > 1f) { scale = 1f; offset = Offset.Zero } else scale = 2.5f
                                },
                                onTap = { if (scale == 1f) onDismiss() },
                            )
                        }
                        .graphicsLayer { scaleX = scale; scaleY = scale; translationX = offset.x; translationY = offset.y },
                )
                state == Loading.Failed -> Text("This picture isn't on the Mac any more.", style = Type.body, color = Ink.dust, modifier = Modifier.align(Alignment.Center))
                else -> ShimmerText("Loading the picture…", Type.small, base = Ink.dust, light = Ink.text, modifier = Modifier.align(Alignment.Center))
            }
            Box(
                Modifier
                    .align(Alignment.TopEnd)
                    .statusBarsPadding()
                    .padding(8.dp)
                    .size(48.dp)
                    .clip(RoundedCornerShape(24.dp))
                    .background(Color.Black.copy(alpha = 0.5f))
                    .clickable(role = Role.Button, onClickLabel = "Close", onClick = onDismiss),
                contentAlignment = Alignment.Center,
            ) { Icon(Icons.Filled.Close, contentDescription = "Close the picture", tint = Color.White) }
        }
    }
}
