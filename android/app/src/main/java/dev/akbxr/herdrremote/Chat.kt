package dev.akbxr.herdrremote

import androidx.compose.animation.animateContentSize
import androidx.compose.foundation.background
import androidx.compose.foundation.border
import androidx.compose.foundation.clickable
import androidx.compose.foundation.horizontalScroll
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.PaddingValues
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.Spacer
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.height
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.width
import androidx.compose.foundation.layout.widthIn
import androidx.compose.foundation.lazy.LazyColumn
import androidx.compose.foundation.lazy.LazyListState
import androidx.compose.foundation.lazy.itemsIndexed
import androidx.compose.foundation.lazy.rememberLazyListState
import androidx.compose.foundation.rememberScrollState
import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.foundation.text.selection.SelectionContainer
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.derivedStateOf
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.saveable.rememberSaveable
import androidx.compose.runtime.setValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.draw.clip
import androidx.compose.ui.graphics.Brush
import androidx.compose.ui.semantics.Role
import androidx.compose.ui.text.AnnotatedString
import androidx.compose.ui.text.SpanStyle
import androidx.compose.ui.text.buildAnnotatedString
import androidx.compose.ui.text.font.FontWeight
import androidx.compose.ui.text.style.TextOverflow
import androidx.compose.ui.text.withStyle
import androidx.compose.ui.unit.dp
import androidx.compose.ui.unit.sp
import org.json.JSONObject

/**
 * One thing in an agent's conversation, as the Mac reads it from the
 * transcript. [id]: stable while more is loaded above it; [images]: the
 * pictures of a message you wrote (ChatImages.kt).
 */
data class ChatItem(
    val kind: String,
    val text: String = "",
    val tools: List<ChatTool> = emptyList(),
    val images: List<String> = emptyList(),
    val id: String = "",
)

/** Items asked for at first; more as you scroll back, the whole conversation for a search. */
const val CHAT_PAGE = 120
const val CHAT_ALL = 20000
private val CHAT_STEPS = listOf(CHAT_PAGE, 300, 800, 2000, 5000)

fun nextChatLimit(limit: Int): Int = CHAT_STEPS.firstOrNull { it > limit } ?: CHAT_ALL

/** The list's keys: the items' ids, unique (an item without one goes by its place). */
fun chatKeys(items: List<ChatItem>): List<String> {
    val seen = HashSet<String>()
    return items.mapIndexed { i, it ->
        var k = it.id.ifEmpty { "#$i" }
        while (!seen.add(k)) k += "~"
        k
    }
}

data class ChatTool(val name: String, val detail: String, val result: String, val error: Boolean)

/** The menu a waiting agent shows: its question and choices, one selected. */
data class Prompt(val question: String, val options: List<String>, val selected: Int) {
    companion object {
        fun from(o: JSONObject) = Prompt(
            question = o.optString("question"),
            options = o.optJSONArray("options")?.let { a -> (0 until a.length()).map { a.getString(it) } } ?: emptyList(),
            selected = o.optInt("selected"),
        )
    }
}

/** A "/" suggestion. */
data class Command(val name: String, val help: String)

/**
 * The conversation: [items] null when it is unchanged since [version] was
 * asked for. [source] empty: no transcript known for this program (a shell),
 * so there is nothing to chat about.
 */
data class ChatLog(
    val agent: String,
    val source: String,
    val version: String,
    val items: List<ChatItem>?,
    val status: AgentEvent?,
    val live: List<String> = emptyList(),  // while it works: what its screen shows being written
    val start: Boolean = true,             // the items begin the conversation: nothing older to load
    val limit: Int = CHAT_PAGE,            // the most items asked for
    val current: CurrentModel = CurrentModel(),  // its model and thinking level now (Models.kt)
) {
    companion object {
        fun from(o: JSONObject, asked: Int = CHAT_PAGE): ChatLog {
            val items = if (o.optBoolean("same")) null else o.optJSONArray("items")?.let { a ->
                (0 until a.length()).map { i ->
                    val it = a.getJSONObject(i)
                    val tools = it.optJSONArray("tools")?.let { t ->
                        (0 until t.length()).map { j ->
                            t.getJSONObject(j).let { x -> ChatTool(x.optString("name"), x.optString("detail"), x.optString("result"), x.optBoolean("error")) }
                        }
                    } ?: emptyList()
                    val images = it.optJSONArray("images")?.let { a -> (0 until a.length()).map { j -> a.getString(j) } } ?: emptyList()
                    ChatItem(it.optString("kind"), it.optString("text"), tools, images, it.optString("id"))
                }
            } ?: emptyList()
            return ChatLog(
                agent = o.optString("agent"),
                source = o.optString("source"),
                version = o.optString("version"),
                items = items,
                status = o.optJSONObject("status")?.let(AgentEvent::from),
                live = o.optJSONArray("live")?.let { a -> (0 until a.length()).map { a.getString(it) } } ?: emptyList(),
                // A Mac from before paging: all it has is all there is.
                start = o.optBoolean("start", true),
                limit = o.optInt("limit", asked),
                current = CurrentModel.from(o),
            )
        }
    }
}

/**
 * The conversation, newest at the bottom, following new messages while you
 * are at the bottom. Below it, what the agent is doing now: working, or
 * waiting for you, with the answer buttons when it asks for approval.
 *
 * Scrolled near the top with more to come ([hasOlder]), it asks for older
 * messages ([onLoadOlder]); they come in above without moving what you are
 * reading (the items are keyed by their ids). With a [search], its matches
 * are marked and the one shown is scrolled to.
 */
@Composable
fun ChatView(
    items: List<ChatItem>,
    status: AgentEvent?,
    agentName: String,
    modifier: Modifier = Modifier,
    prompt: Prompt? = null,               // the menu on its screen, when it waits for you
    pending: List<String> = emptyList(),  // sent from here, not in the transcript yet
    loading: Boolean = false,
    live: List<String> = emptyList(),     // the screen's last lines while it works
    list: LazyListState = rememberLazyListState(),
    hasOlder: Boolean = false,            // older messages to load
    loadingOlder: Boolean = false,        // asked for, on their way
    reachedStart: Boolean = false,        // scrolled back to the very start: say so at the top
    onLoadOlder: () -> Unit = {},
    search: ChatSearch? = null,
    onAnswer: (String) -> Unit,
    onOption: (String) -> Unit = {},
    onOpenTerminal: () -> Unit,
) {
    val atBottom by remember(list) {
        derivedStateOf {
            val last = list.layoutInfo.visibleItemsInfo.lastOrNull()
            last == null || last.index >= list.layoutInfo.totalItemsCount - 2
        }
    }
    val nearTop by remember(list) { derivedStateOf { list.firstVisibleItemIndex < 4 } }
    val working = status?.state == "working"
    // A tool call without its result while the agent works: running now.
    val running = working && items.lastOrNull()?.let { it.kind == "tools" && it.tools.lastOrNull()?.result.isNullOrEmpty() } == true
    val showLive = working && live.any { it.isNotBlank() }
    // "Thinking…" says nothing the live screen or a running tool doesn't.
    val foot = status != null && status.state in setOf("working", "blocked", "error") && !running && !showLive
    val showStart = reachedStart && items.isNotEmpty()
    val head = if (showStart) 1 else 0
    val tail = head + items.size + pending.size + (if (showLive) 1 else 0) + (if (foot) 1 else 0)
    val searching = search != null && search.query.isNotBlank()
    var first by remember { mutableStateOf(true) }
    LaunchedEffect(tail, items.lastOrNull(), live.lastOrNull()) {
        if (tail == 0) return@LaunchedEffect
        if (first) { list.scrollToItem(tail - 1); first = false }
        else if (atBottom && !searching) list.animateScrollToItem(tail - 1)
    }
    // Near the top with more above: ask for it.
    LaunchedEffect(nearTop, hasOlder, loadingOlder, first, items.size) {
        if (nearTop && hasOlder && !loadingOlder && !first && items.isNotEmpty()) onLoadOlder()
    }
    // The match shown: its item brought up (again when older items come in above it).
    val hit = search?.hits?.getOrNull(search.current)
    LaunchedEffect(hit, search?.query) {
        if (hit != null && !first) list.animateScrollToItem(head + hit.item)
    }
    if (loading && items.isEmpty()) {
        SkeletonList(modifier)
        return
    }
    if (items.isEmpty() && pending.isEmpty() && !foot) {
        Box(modifier.fillMaxSize().padding(24.dp), contentAlignment = Alignment.Center) {
            Text("Nothing said yet. Write to $agentName below.", style = Type.small, color = Ink.dust)
        }
        return
    }
    val keys = remember(items) { chatKeys(items) }
    Box(modifier) {
        SelectionContainer(Modifier.fillMaxSize()) {
            LazyColumn(
                state = list,
                // Loading older messages: room above the first item for the row that says so
                // (padding, not an item: the list keeps its place by the first item's key).
                contentPadding = PaddingValues(start = 14.dp, end = 14.dp, top = if (loadingOlder && hasOlder) 110.dp else 12.dp, bottom = 16.dp),
                verticalArrangement = Arrangement.spacedBy(12.dp),
                modifier = Modifier.fillMaxSize(),
            ) {
                if (showStart) item(key = "start") { NoteLine("The start of the conversation") }
                itemsIndexed(items, key = { i, _ -> keys[i] }) { i, it ->
                    val marks = search?.marks(i)
                    when (it.kind) {
                        "user" -> UserBubble(it.text, images = it.images, marks = marks)
                        "assistant" -> Markdown(it.text, marks = marks)
                        "tools" -> ToolRun(it.tools, running = running && i == items.lastIndex, marks = marks)
                        else -> NoteLine(it.text)
                    }
                }
                itemsIndexed(pending, key = { i, _ -> "pending$i" }) { _, text -> UserBubble(text, sending = true) }
                if (showLive) item(key = "live") { LiveScreen(live) }
                if (foot) item(key = "status") { StatusFoot(status!!, agentName, prompt, onAnswer, onOption, onOpenTerminal) }
            }
        }
        // Over the list, not in it: the list keeps its place when the older items come in.
        if (loadingOlder && hasOlder) OlderLoading(Modifier.align(Alignment.TopCenter))
    }
}

/**
 * Older messages on their way: a row at the top of the chat, the messages
 * fading out under it, and a skeleton of what is coming.
 */
@Composable
fun OlderLoading(modifier: Modifier = Modifier) {
    Column(
        modifier
            .fillMaxWidth()
            .background(Brush.verticalGradient(0.8f to Ink.night, 1f to Ink.night.copy(alpha = 0f)))
            .padding(start = 14.dp, end = 14.dp, top = 12.dp, bottom = 14.dp),
        horizontalAlignment = Alignment.CenterHorizontally,
    ) {
        Box(Modifier.fillMaxWidth(), contentAlignment = Alignment.CenterEnd) { SkeletonBar(Modifier.fillMaxWidth(0.5f), height = 30) }
        Spacer(Modifier.height(8.dp))
        SkeletonBar(Modifier.fillMaxWidth(0.85f).align(Alignment.Start), height = 12)
        Spacer(Modifier.height(10.dp))
        ShimmerText("Loading earlier messages", Type.small.copy(fontSize = 12.sp), base = Ink.dust, light = Ink.text)
    }
}

@Composable
private fun UserBubble(text: String, images: List<String> = emptyList(), sending: Boolean = false, marks: Marks? = null) {
    var open by remember { mutableStateOf<String?>(null) }
    open?.let { PictureViewer(it) { open = null } }
    Column(Modifier.fillMaxWidth(), horizontalAlignment = Alignment.End) {
        Column(
            Modifier
                .widthIn(max = 310.dp)
                .clip(RoundedCornerShape(16.dp, 16.dp, 4.dp, 16.dp))
                .background(Ink.pen)
                .border(1.dp, Ink.rule, RoundedCornerShape(16.dp, 16.dp, 4.dp, 16.dp))
                .padding(horizontal = if (images.isNotEmpty() && text.isEmpty()) 6.dp else 14.dp, vertical = if (images.isNotEmpty() && text.isEmpty()) 6.dp else 10.dp),
        ) {
            if (images.isNotEmpty()) ChatPictures(images, onOpen = { open = it }, modifier = Modifier.padding(bottom = if (text.isEmpty()) 0.dp else 8.dp))
            if (text.isNotEmpty()) {
                val (shown, _) = marked(AnnotatedString(text), marks, 0)
                Text(shown, style = Type.body, color = if (sending) Ink.text.copy(alpha = 0.7f) else Ink.text,
                    modifier = revealIf(marks != null && marks.current >= 0, marks?.current ?: 0))
            }
        }
        if (sending) Text("Sent", style = Type.small.copy(fontSize = 11.sp), color = Ink.dust, modifier = Modifier.padding(top = 3.dp, end = 4.dp))
    }
}

@Composable
private fun NoteLine(text: String) {
    Row(Modifier.fillMaxWidth().padding(vertical = 4.dp), verticalAlignment = Alignment.CenterVertically) {
        Box(Modifier.weight(1f).height(1.dp).background(Ink.rule))
        Text(text, style = Type.small, color = Ink.dust, modifier = Modifier.padding(horizontal = 10.dp).widthIn(max = 260.dp))
        Box(Modifier.weight(1f).height(1.dp).background(Ink.rule))
    }
}

/** A run of tool calls, folded to one line: tap to see each. */
@Composable
private fun ToolRun(tools: List<ChatTool>, running: Boolean = false, marks: Marks? = null) {
    var unfolded by rememberSaveable(tools.size, tools.firstOrNull()?.detail) { mutableStateOf(false) }
    val open = unfolded || marks != null  // a match of the search in it: shown
    var occ = 0  // the matches before this piece, among the item's
    val failed = tools.count { it.error }
    val names = tools.map { it.name }.distinct()
    Column(
        Modifier
            .fillMaxWidth()
            .clip(RoundedCornerShape(10.dp))
            .border(1.dp, Ink.rule, RoundedCornerShape(10.dp))
            .clickable(role = Role.Button, onClickLabel = if (open) "Fold the tool calls" else "Show the tool calls") { unfolded = !unfolded }
            .animateContentSize()
            .padding(horizontal = 12.dp, vertical = 9.dp),
    ) {
        Row(verticalAlignment = Alignment.CenterVertically) {
            Text(if (open) "▾" else "▸", style = Type.label, color = Ink.dust)
            Spacer(Modifier.width(8.dp))
            val mono = Type.small.copy(fontFamily = JetBrains, fontSize = 12.sp)
            if (running) {
                // What runs now, in the light that says it's still going.
                val now = tools.last()
                ShimmerText("${now.name}  ${now.detail}".trim(), mono, base = Ink.blue, light = Ink.text, modifier = Modifier.weight(1f))
            } else {
                val head = if (tools.size == 1) "${tools[0].name}  ${tools[0].detail}" else "${tools.size} tool calls · ${names.take(4).joinToString(", ")}${if (names.size > 4) "…" else ""}"
                Text(head, style = mono, color = Ink.dust, maxLines = 1, overflow = TextOverflow.Ellipsis, modifier = Modifier.weight(1f))
            }
            if (running && tools.size > 1) {
                Spacer(Modifier.width(8.dp))
                Text("${tools.size}", style = Type.label, color = Ink.dust)
            }
            if (failed > 0) {
                Spacer(Modifier.width(8.dp))
                Text(if (failed == 1) "1 failed" else "$failed failed", style = Type.label, color = Ink.red)
            }
        }
        if (open) {
            tools.forEach { t ->
                val (detail, nd) = marked(AnnotatedString(t.detail), marks, occ)
                val (result, nr) = marked(AnnotatedString(t.result), marks, occ + nd)
                val here = marks != null && marks.current in occ until occ + nd + nr
                occ += nd + nr
                Column(Modifier.padding(top = 10.dp).then(revealIf(here, marks?.current ?: 0))) {
                    Text(
                        buildAnnotatedString {
                            withStyle(SpanStyle(color = if (t.error) Ink.red else Ink.text, fontWeight = FontWeight.Bold)) { append(t.name) }
                            if (t.detail.isNotEmpty()) { append("  "); append(detail) }
                        },
                        style = Type.small.copy(fontFamily = JetBrains, fontSize = 12.sp), color = Ink.dust,
                    )
                    if (t.result.isNotEmpty()) {
                        Text(result, style = Type.small.copy(fontFamily = JetBrains, fontSize = 11.sp, lineHeight = 15.sp),
                            color = if (t.error) Ink.red.copy(alpha = 0.85f) else Ink.dust.copy(alpha = 0.8f),
                            modifier = Modifier.padding(start = 12.dp, top = 2.dp))
                    }
                }
            }
        }
    }
}

@Composable
private fun StatusFoot(
    status: AgentEvent,
    agentName: String,
    prompt: Prompt?,
    onAnswer: (String) -> Unit,
    onOption: (String) -> Unit,
    onOpenTerminal: () -> Unit,
) {
    when (status.state) {
        "working" -> Row(verticalAlignment = Alignment.CenterVertically, modifier = Modifier.padding(top = 2.dp)) {
            PulseDot(Ink.blue)
            Spacer(Modifier.width(10.dp))
            ShimmerText("Thinking…", Type.small, base = Ink.blue, light = Ink.text)
        }
        "error" -> Text("Stopped with an error" + (status.msg.takeIf { it.isNotBlank() }?.let { ": $it" } ?: "."), style = Type.small, color = Ink.red)
        "blocked" -> Column(
            Modifier
                .fillMaxWidth()
                .clip(RoundedCornerShape(12.dp))
                .background(Ink.yellow.copy(alpha = 0.10f))
                .border(1.dp, Ink.yellow.copy(alpha = 0.55f), RoundedCornerShape(12.dp))
                .padding(14.dp),
        ) {
            Text("$agentName needs you", style = Type.label, color = Ink.yellow)
            val question = status.msg.ifBlank { prompt?.question.orEmpty() }
            if (question.isNotBlank()) Text(question, style = Type.body, color = Ink.text, modifier = Modifier.padding(top = 4.dp))
            if (prompt != null) {
                // The choices on its screen, as they are there.
                Column(Modifier.padding(top = 10.dp), verticalArrangement = Arrangement.spacedBy(6.dp)) {
                    prompt.options.forEachIndexed { i, option ->
                        OptionButton(option, highlighted = i == prompt.selected) { onOption(option) }
                    }
                }
                QuietButton("Terminal", color = Ink.dust, modifier = Modifier.padding(top = 4.dp), onClick = onOpenTerminal)
                return@Column
            }
            Row(Modifier.padding(top = 10.dp), horizontalArrangement = Arrangement.spacedBy(8.dp), verticalAlignment = Alignment.CenterVertically) {
                if (status.kind == "permission") {
                    PrimaryButton("Allow") { onAnswer("allow") }
                    QuietButton("Deny", color = Ink.text) { onAnswer("deny") }
                }
                QuietButton(if (status.kind == "permission") "Terminal" else "Answer in the terminal", color = Ink.text, onClick = onOpenTerminal)
            }
        }
    }
}

/**
 * What the agent is writing now, as its terminal shows it: the transcript
 * only has finished messages, so a long answer or a running command shows
 * here first.
 */
@Composable
private fun LiveScreen(lines: List<String>) {
    Column(
        Modifier
            .fillMaxWidth()
            .clip(RoundedCornerShape(10.dp))
            .background(TermBg)
            .border(1.dp, Ink.rule, RoundedCornerShape(10.dp))
            .padding(horizontal = 12.dp, vertical = 9.dp),
    ) {
        Row(verticalAlignment = Alignment.CenterVertically) {
            PulseDot(Ink.blue, size = 6.dp)
            Spacer(Modifier.width(8.dp))
            ShimmerText("Live on the terminal", Type.small.copy(fontSize = 11.sp), base = Ink.dust, light = Ink.text)
        }
        Box(Modifier.padding(top = 6.dp).horizontalScroll(rememberScrollState())) {
            Text(
                lines.joinToString("\n"),
                style = Type.small.copy(fontFamily = JetBrains, fontSize = 11.5.sp, lineHeight = 16.sp),
                color = Ink.text.copy(alpha = 0.85f),
                softWrap = false,
            )
        }
    }
}

@Composable
private fun OptionButton(text: String, highlighted: Boolean, onClick: () -> Unit) {
    Box(
        Modifier
            .fillMaxWidth()
            .clip(RoundedCornerShape(10.dp))
            .background(if (highlighted) Ink.yellow.copy(alpha = 0.18f) else Ink.pen)
            .border(1.dp, if (highlighted) Ink.yellow.copy(alpha = 0.6f) else Ink.rule, RoundedCornerShape(10.dp))
            .clickable(role = Role.Button, onClick = onClick)
            .padding(horizontal = 14.dp, vertical = 11.dp),
    ) { Text(text, style = Type.label, color = Ink.text) }
}

/**
 * The Markdown agents write, enough of it for a phone: paragraphs, headings,
 * lists, quotes, **bold**, *italic*, `code` and fenced code blocks (which
 * scroll sideways rather than wrap).
 */
@Composable
fun Markdown(text: String, modifier: Modifier = Modifier, marks: Marks? = null) {
    Column(modifier.fillMaxWidth(), verticalArrangement = Arrangement.spacedBy(8.dp)) {
        var occ = 0  // the matches of the search before this block, among the answer's
        mdBlocks(text).forEach { b ->
            val (shown, n) = marked(shownText(b), marks, occ)
            val reveal = revealIf(marks != null && marks.current in occ until occ + n, marks?.current ?: 0)
            occ += n
            when (b) {
                is MdBlock.Code -> Box(
                    Modifier
                        .fillMaxWidth()
                        .clip(RoundedCornerShape(8.dp))
                        .background(TermBg)
                        .border(1.dp, Ink.rule, RoundedCornerShape(8.dp))
                        .horizontalScroll(rememberScrollState())
                        .padding(horizontal = 12.dp, vertical = 10.dp)
                        .then(reveal),
                ) { Text(shown, style = Type.small.copy(fontFamily = JetBrains, fontSize = 12.sp, lineHeight = 17.sp), color = Ink.text, softWrap = false) }
                is MdBlock.Heading -> Text(shown, style = Type.title.copy(fontSize = if (b.level <= 2) 17.sp else 15.sp), color = Ink.text, modifier = reveal)
                is MdBlock.Quote -> Row(reveal) {
                    Box(Modifier.width(3.dp).height(20.dp).background(Ink.rule))
                    Spacer(Modifier.width(10.dp))
                    Text(shown, style = Type.body, color = Ink.dust)
                }
                is MdBlock.Item -> Row(Modifier.padding(start = (b.depth * 14).dp).then(reveal)) {
                    Text(b.marker, style = Type.body, color = Ink.dust, modifier = Modifier.width(if (b.marker.length > 2) 26.dp else 16.dp))
                    Text(shown, style = Type.body, color = Ink.text)
                }
                is MdBlock.Para -> Text(shown, style = Type.body, color = Ink.text, modifier = reveal)
            }
        }
    }
}

sealed interface MdBlock {
    data class Para(val text: String) : MdBlock
    data class Heading(val level: Int, val text: String) : MdBlock
    data class Code(val text: String) : MdBlock
    data class Quote(val text: String) : MdBlock
    data class Item(val marker: String, val text: String, val depth: Int) : MdBlock
}

private val bullet = Regex("""^(\s*)[-*+]\s+(.*)$""")
private val numbered = Regex("""^(\s*)(\d+)[.)]\s+(.*)$""")
private val heading = Regex("""^(#{1,6})\s+(.*)$""")

fun mdBlocks(text: String): List<MdBlock> {
    val out = mutableListOf<MdBlock>()
    val para = StringBuilder()
    fun flush() { if (para.isNotBlank()) out += MdBlock.Para(para.toString().trim()); para.clear() }
    val lines = text.replace("\r\n", "\n").split("\n")
    var i = 0
    while (i < lines.size) {
        val l = lines[i]
        val t = l.trim()
        when {
            t.startsWith("```") -> {
                flush()
                val code = StringBuilder()
                i++
                while (i < lines.size && !lines[i].trim().startsWith("```")) { code.appendLine(lines[i]); i++ }
                out += MdBlock.Code(code.toString().trimEnd('\n'))
            }
            t.isEmpty() -> flush()
            heading.matches(t) -> { flush(); heading.find(t)!!.let { out += MdBlock.Heading(it.groupValues[1].length, it.groupValues[2]) } }
            t.startsWith(">") -> { flush(); out += MdBlock.Quote(t.removePrefix(">").trim()) }
            bullet.matches(l) -> { flush(); bullet.find(l)!!.let { out += MdBlock.Item("•", it.groupValues[2], it.groupValues[1].length / 2) } }
            numbered.matches(l) -> { flush(); numbered.find(l)!!.let { out += MdBlock.Item(it.groupValues[2] + ".", it.groupValues[3], it.groupValues[1].length / 2) } }
            t.matches(Regex("""^(-{3,}|\*{3,}|_{3,})$""")) -> flush()
            else -> { if (para.isNotEmpty()) para.append(' '); para.append(t) }
        }
        i++
    }
    flush()
    return out
}

private val inlineRe = Regex("""`([^`]+)`|\*\*([^*]+)\*\*|__([^_]+)__|(?<![\w*])\*([^*\s][^*]*)\*(?!\w)|\[([^\]]+)]\(([^)\s]+)\)""")

/** **bold**, *italic*, `code` and [links](…) (shown as their text). */
fun inline(text: String): AnnotatedString = buildAnnotatedString {
    var at = 0
    for (m in inlineRe.findAll(text)) {
        append(text.substring(at, m.range.first))
        val g = m.groupValues
        when {
            g[1].isNotEmpty() -> withStyle(SpanStyle(fontFamily = JetBrains, fontSize = 13.sp, background = Ink.pen, color = Ink.text)) { append(g[1]) }
            g[2].isNotEmpty() -> withStyle(SpanStyle(fontWeight = FontWeight.Bold)) { append(g[2]) }
            g[3].isNotEmpty() -> withStyle(SpanStyle(fontWeight = FontWeight.Bold)) { append(g[3]) }
            g[4].isNotEmpty() -> withStyle(SpanStyle(fontStyle = androidx.compose.ui.text.font.FontStyle.Italic)) { append(g[4]) }
            g[5].isNotEmpty() -> withStyle(SpanStyle(color = Ink.blue)) { append(g[5]) }
        }
        at = m.range.last + 1
    }
    append(text.substring(at))
}
