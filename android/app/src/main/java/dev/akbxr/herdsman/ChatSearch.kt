package dev.akbxr.herdsman

import androidx.compose.foundation.ExperimentalFoundationApi
import androidx.compose.foundation.background
import androidx.compose.foundation.border
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.Spacer
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.height
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.size
import androidx.compose.foundation.layout.width
import androidx.compose.foundation.relocation.BringIntoViewRequester
import androidx.compose.foundation.relocation.bringIntoViewRequester
import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.foundation.text.BasicTextField
import androidx.compose.foundation.text.KeyboardActions
import androidx.compose.foundation.text.KeyboardOptions
import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.filled.Close
import androidx.compose.material.icons.filled.KeyboardArrowDown
import androidx.compose.material.icons.filled.KeyboardArrowUp
import androidx.compose.material.icons.filled.Search
import androidx.compose.material3.Icon
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.remember
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.draw.clip
import androidx.compose.ui.focus.FocusRequester
import androidx.compose.ui.focus.focusRequester
import androidx.compose.ui.graphics.SolidColor
import androidx.compose.ui.graphics.vector.ImageVector
import androidx.compose.ui.semantics.Role
import androidx.compose.foundation.clickable
import androidx.compose.ui.text.AnnotatedString
import androidx.compose.ui.text.buildAnnotatedString
import androidx.compose.ui.text.SpanStyle
import androidx.compose.ui.text.input.ImeAction
import androidx.compose.ui.unit.dp
import androidx.compose.ui.unit.sp
import kotlinx.coroutines.delay

/** A match of the search: in the item at [item], its [occ]-th (from 0). */
data class SearchHit(val item: Int, val occ: Int)

/**
 * A search of the chat: its query, every match in the conversation (oldest
 * first), and the one shown now.
 */
data class ChatSearch(val query: String, val hits: List<SearchHit>, val current: Int) {
    private val withHits by lazy { hits.mapTo(HashSet()) { it.item } }

    /** The match shown, as its number among the item's; -1 when it is elsewhere. */
    fun currentIn(item: Int): Int = hits.getOrNull(current)?.takeIf { it.item == item }?.occ ?: -1
    fun marks(item: Int): Marks? = if (query.isBlank() || item !in withHits) null else Marks(query, currentIn(item))
}

/** What to highlight in an item: the query, and which of its matches is the one shown (-1: none). */
data class Marks(val query: String, val current: Int)

/**
 * The texts an item shows, in the order it shows them, as they read on the
 * screen (an answer's Markdown marks gone): what search looks in.
 */
fun shownTexts(item: ChatItem): List<String> = when (item.kind) {
    "assistant" -> mdBlocks(item.text).map { shownText(it).text }
    "tools" -> item.tools.flatMap { listOf(it.detail, it.result) }
    else -> listOf(item.text)
}

/** A Markdown block as it shows: inline marks applied, code as it is. */
fun shownText(b: MdBlock): AnnotatedString = when (b) {
    is MdBlock.Code -> AnnotatedString(b.text)
    is MdBlock.Heading -> inline(b.text)
    is MdBlock.Quote -> inline(b.text)
    is MdBlock.Item -> inline(b.text)
    is MdBlock.Para -> inline(b.text)
    is MdBlock.Table -> buildAnnotatedString {
        (listOf(b.header) + b.rows).flatten().forEach { append(inline(it)); append("  ") }
    }
}

/** Where the query is in the text, ignoring case, without overlaps. */
fun matchesIn(text: String, query: String): List<IntRange> {
    if (query.isEmpty()) return emptyList()
    val out = mutableListOf<IntRange>()
    var at = text.indexOf(query, ignoreCase = true)
    while (at >= 0) {
        out += at until at + query.length
        at = text.indexOf(query, at + query.length, ignoreCase = true)
    }
    return out
}

/** Every match of the query in the conversation, oldest first. */
fun findHits(items: List<ChatItem>, query: String): List<SearchHit> {
    val q = query.trim()
    if (q.isEmpty()) return emptyList()
    val hits = mutableListOf<SearchHit>()
    items.forEachIndexed { i, item ->
        val n = shownTexts(item).sumOf { matchesIn(it, q).size }
        repeat(n) { hits += SearchHit(i, it) }
    }
    return hits
}

/**
 * The text with its matches marked, numbered from [first] among the item's:
 * the one shown ([current]) loud, the others quiet.
 */
fun highlighted(text: AnnotatedString, query: String, first: Int, current: Int): AnnotatedString {
    val ranges = matchesIn(text.text, query.trim())
    if (ranges.isEmpty()) return text
    return AnnotatedString.Builder(text).apply {
        ranges.forEachIndexed { k, r ->
            val on = first + k == current
            addStyle(
                if (on) SpanStyle(background = Ink.yellow, color = Ink.night)
                else SpanStyle(background = Ink.yellow.copy(alpha = 0.28f)),
                r.first, r.last + 1,
            )
        }
    }.toAnnotatedString()
}

/** The marked text of one piece of an item, and how many matches it holds. */
fun marked(text: AnnotatedString, marks: Marks?, first: Int): Pair<AnnotatedString, Int> {
    if (marks == null) return text to 0
    val n = matchesIn(text.text, marks.query.trim()).size
    return (if (n > 0) highlighted(text, marks.query, first, marks.current) else text) to n
}

/**
 * On the piece holding the match shown: scrolls it into view once the list
 * has brought its item up (a long answer's match may be far down in it).
 */
@OptIn(ExperimentalFoundationApi::class)
@Composable
fun revealIf(current: Boolean, key: Any): Modifier {
    if (!current) return Modifier
    val requester = remember { BringIntoViewRequester() }
    LaunchedEffect(key) {
        delay(350)
        runCatching { requester.bringIntoView() }
    }
    return Modifier.bringIntoViewRequester(requester)
}

/**
 * The search field above the chat: the query, "3 of 12", older and newer
 * match, close.
 */
@Composable
fun ChatSearchBar(
    query: String,
    onQuery: (String) -> Unit,
    count: Int,
    current: Int,
    loading: Boolean,
    onOlder: () -> Unit,
    onNewer: () -> Unit,
    onClose: () -> Unit,
    modifier: Modifier = Modifier,
    autofocus: Boolean = true,
) {
    val focus = remember { FocusRequester() }
    if (autofocus) LaunchedEffect(Unit) { runCatching { focus.requestFocus() } }
    Row(
        modifier.fillMaxWidth().padding(start = 10.dp, end = 4.dp, top = 8.dp, bottom = 4.dp),
        verticalAlignment = Alignment.CenterVertically,
    ) {
        Row(
            Modifier
                .weight(1f)
                .height(40.dp)
                .clip(RoundedCornerShape(10.dp))
                .background(Ink.pen)
                .border(1.dp, Ink.rule, RoundedCornerShape(10.dp))
                .padding(horizontal = 10.dp),
            verticalAlignment = Alignment.CenterVertically,
        ) {
            Icon(Icons.Filled.Search, contentDescription = null, tint = Ink.dust, modifier = Modifier.size(18.dp))
            Spacer(Modifier.width(8.dp))
            BasicTextField(
                value = query,
                onValueChange = onQuery,
                singleLine = true,
                textStyle = Type.body.copy(color = Ink.text),
                cursorBrush = SolidColor(Ink.yellow),
                keyboardOptions = KeyboardOptions(imeAction = ImeAction.Search),
                keyboardActions = KeyboardActions(onSearch = { onOlder() }),
                modifier = Modifier.weight(1f).focusRequester(focus),
                decorationBox = { inner ->
                    Box(contentAlignment = Alignment.CenterStart) {
                        if (query.isEmpty()) Text("Search the chat", style = Type.body, color = Ink.dust.copy(alpha = 0.7f), maxLines = 1)
                        inner()
                    }
                },
            )
            val small = Type.small.copy(fontSize = 12.sp)
            when {
                query.isBlank() -> {}
                loading && count == 0 -> ShimmerText("Loading…", small, base = Ink.dust, light = Ink.text)
                count == 0 -> Text("No matches", style = small, color = Ink.dust)
                else -> Text("${current + 1} of $count", style = small, color = Ink.text)
            }
        }
        BarKey(Icons.Filled.KeyboardArrowUp, "Older match", enabled = count > 0, onClick = onOlder)
        BarKey(Icons.Filled.KeyboardArrowDown, "Newer match", enabled = count > 0, onClick = onNewer)
        BarKey(Icons.Filled.Close, "Close the search", onClick = onClose)
    }
}

@Composable
private fun BarKey(icon: ImageVector, label: String, enabled: Boolean = true, onClick: () -> Unit) {
    Box(
        Modifier.size(40.dp).clip(RoundedCornerShape(10.dp)).clickable(enabled = enabled, role = Role.Button, onClick = onClick),
        contentAlignment = Alignment.Center,
    ) { Icon(icon, contentDescription = label, tint = if (enabled) Ink.text else Ink.dust.copy(alpha = 0.5f)) }
}
