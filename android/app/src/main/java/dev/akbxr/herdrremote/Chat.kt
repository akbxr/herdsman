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
import androidx.compose.foundation.layout.size
import androidx.compose.foundation.layout.width
import androidx.compose.foundation.layout.widthIn
import androidx.compose.foundation.lazy.LazyColumn
import androidx.compose.foundation.lazy.itemsIndexed
import androidx.compose.foundation.lazy.rememberLazyListState
import androidx.compose.foundation.rememberScrollState
import androidx.compose.foundation.shape.CircleShape
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

/** One thing in an agent's conversation, as the Mac reads it from the transcript. */
data class ChatItem(val kind: String, val text: String = "", val tools: List<ChatTool> = emptyList())

data class ChatTool(val name: String, val detail: String, val result: String, val error: Boolean)

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
) {
    companion object {
        fun from(o: JSONObject): ChatLog {
            val items = if (o.optBoolean("same")) null else o.optJSONArray("items")?.let { a ->
                (0 until a.length()).map { i ->
                    val it = a.getJSONObject(i)
                    val tools = it.optJSONArray("tools")?.let { t ->
                        (0 until t.length()).map { j ->
                            t.getJSONObject(j).let { x -> ChatTool(x.optString("name"), x.optString("detail"), x.optString("result"), x.optBoolean("error")) }
                        }
                    } ?: emptyList()
                    ChatItem(it.optString("kind"), it.optString("text"), tools)
                }
            } ?: emptyList()
            return ChatLog(
                agent = o.optString("agent"),
                source = o.optString("source"),
                version = o.optString("version"),
                items = items,
                status = o.optJSONObject("status")?.let(AgentEvent::from),
            )
        }
    }
}

/**
 * The conversation, newest at the bottom, following new messages while you
 * are at the bottom. Below it, what the agent is doing now: working, or
 * waiting for you, with the answer buttons when it asks for approval.
 */
@Composable
fun ChatView(
    items: List<ChatItem>,
    status: AgentEvent?,
    agentName: String,
    modifier: Modifier = Modifier,
    onAnswer: (String) -> Unit,
    onOpenTerminal: () -> Unit,
) {
    val list = rememberLazyListState()
    val atBottom by remember {
        derivedStateOf {
            val last = list.layoutInfo.visibleItemsInfo.lastOrNull()
            last == null || last.index >= list.layoutInfo.totalItemsCount - 2
        }
    }
    val tail = items.size + (if (status != null) 1 else 0)
    var first by remember { mutableStateOf(true) }
    LaunchedEffect(tail, items.lastOrNull()) {
        if (tail == 0) return@LaunchedEffect
        if (first) { list.scrollToItem(tail - 1); first = false }
        else if (atBottom) list.animateScrollToItem(tail - 1)
    }
    if (items.isEmpty() && status == null) {
        Box(modifier.fillMaxSize().padding(24.dp), contentAlignment = Alignment.Center) {
            Text("Nothing said yet. Write to $agentName below.", style = Type.small, color = Ink.dust)
        }
        return
    }
    SelectionContainer(modifier) {
        LazyColumn(
            state = list,
            contentPadding = PaddingValues(start = 14.dp, end = 14.dp, top = 12.dp, bottom = 16.dp),
            verticalArrangement = Arrangement.spacedBy(12.dp),
            modifier = Modifier.fillMaxSize(),
        ) {
            itemsIndexed(items, key = { i, _ -> i }) { _, it ->
                when (it.kind) {
                    "user" -> UserBubble(it.text)
                    "assistant" -> Markdown(it.text)
                    "tools" -> ToolRun(it.tools)
                    else -> NoteLine(it.text)
                }
            }
            if (status != null && status.state in setOf("working", "blocked", "error")) {
                item(key = "status") { StatusFoot(status, agentName, onAnswer, onOpenTerminal) }
            }
        }
    }
}

@Composable
private fun UserBubble(text: String) {
    Box(Modifier.fillMaxWidth(), contentAlignment = Alignment.CenterEnd) {
        Box(
            Modifier
                .widthIn(max = 310.dp)
                .clip(RoundedCornerShape(16.dp, 16.dp, 4.dp, 16.dp))
                .background(Ink.pen)
                .border(1.dp, Ink.rule, RoundedCornerShape(16.dp, 16.dp, 4.dp, 16.dp))
                .padding(horizontal = 14.dp, vertical = 10.dp),
        ) { Text(text, style = Type.body, color = Ink.text) }
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
private fun ToolRun(tools: List<ChatTool>) {
    var open by rememberSaveable(tools.size, tools.firstOrNull()?.detail) { mutableStateOf(false) }
    val failed = tools.count { it.error }
    val names = tools.map { it.name }.distinct()
    Column(
        Modifier
            .fillMaxWidth()
            .clip(RoundedCornerShape(10.dp))
            .border(1.dp, Ink.rule, RoundedCornerShape(10.dp))
            .clickable(role = Role.Button, onClickLabel = if (open) "Fold the tool calls" else "Show the tool calls") { open = !open }
            .animateContentSize()
            .padding(horizontal = 12.dp, vertical = 9.dp),
    ) {
        Row(verticalAlignment = Alignment.CenterVertically) {
            Text(if (open) "▾" else "▸", style = Type.label, color = Ink.dust)
            Spacer(Modifier.width(8.dp))
            val head = if (tools.size == 1) "${tools[0].name}  ${tools[0].detail}" else "${tools.size} tool calls · ${names.take(4).joinToString(", ")}${if (names.size > 4) "…" else ""}"
            Text(head, style = Type.small.copy(fontFamily = JetBrains, fontSize = 12.sp), color = Ink.dust,
                maxLines = 1, overflow = TextOverflow.Ellipsis, modifier = Modifier.weight(1f))
            if (failed > 0) {
                Spacer(Modifier.width(8.dp))
                Text(if (failed == 1) "1 failed" else "$failed failed", style = Type.label, color = Ink.red)
            }
        }
        if (open) {
            tools.forEach { t ->
                Column(Modifier.padding(top = 10.dp)) {
                    Text(
                        buildAnnotatedString {
                            withStyle(SpanStyle(color = if (t.error) Ink.red else Ink.text, fontWeight = FontWeight.Bold)) { append(t.name) }
                            if (t.detail.isNotEmpty()) { append("  "); append(t.detail) }
                        },
                        style = Type.small.copy(fontFamily = JetBrains, fontSize = 12.sp), color = Ink.dust,
                    )
                    if (t.result.isNotEmpty()) {
                        Text(t.result, style = Type.small.copy(fontFamily = JetBrains, fontSize = 11.sp, lineHeight = 15.sp),
                            color = if (t.error) Ink.red.copy(alpha = 0.85f) else Ink.dust.copy(alpha = 0.8f),
                            modifier = Modifier.padding(start = 12.dp, top = 2.dp))
                    }
                }
            }
        }
    }
}

@Composable
private fun StatusFoot(status: AgentEvent, agentName: String, onAnswer: (String) -> Unit, onOpenTerminal: () -> Unit) {
    when (status.state) {
        "working" -> Row(verticalAlignment = Alignment.CenterVertically, modifier = Modifier.padding(top = 2.dp)) {
            Box(Modifier.size(8.dp).clip(CircleShape).background(Ink.blue))
            Spacer(Modifier.width(10.dp))
            Text(status.msg.ifBlank { "Working…" }, style = Type.small, color = Ink.blue, maxLines = 2, overflow = TextOverflow.Ellipsis)
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
            if (status.msg.isNotBlank()) Text(status.msg, style = Type.body, color = Ink.text, modifier = Modifier.padding(top = 4.dp))
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
 * The Markdown agents write, enough of it for a phone: paragraphs, headings,
 * lists, quotes, **bold**, *italic*, `code` and fenced code blocks (which
 * scroll sideways rather than wrap).
 */
@Composable
fun Markdown(text: String, modifier: Modifier = Modifier) {
    Column(modifier.fillMaxWidth(), verticalArrangement = Arrangement.spacedBy(8.dp)) {
        mdBlocks(text).forEach { b ->
            when (b) {
                is MdBlock.Code -> Box(
                    Modifier
                        .fillMaxWidth()
                        .clip(RoundedCornerShape(8.dp))
                        .background(TermBg)
                        .border(1.dp, Ink.rule, RoundedCornerShape(8.dp))
                        .horizontalScroll(rememberScrollState())
                        .padding(horizontal = 12.dp, vertical = 10.dp),
                ) { Text(b.text, style = Type.small.copy(fontFamily = JetBrains, fontSize = 12.sp, lineHeight = 17.sp), color = Ink.text, softWrap = false) }
                is MdBlock.Heading -> Text(inline(b.text), style = Type.title.copy(fontSize = if (b.level <= 2) 17.sp else 15.sp), color = Ink.text)
                is MdBlock.Quote -> Row {
                    Box(Modifier.width(3.dp).height(20.dp).background(Ink.rule))
                    Spacer(Modifier.width(10.dp))
                    Text(inline(b.text), style = Type.body, color = Ink.dust)
                }
                is MdBlock.Item -> Row(Modifier.padding(start = (b.depth * 14).dp)) {
                    Text(b.marker, style = Type.body, color = Ink.dust, modifier = Modifier.width(if (b.marker.length > 2) 26.dp else 16.dp))
                    Text(inline(b.text), style = Type.body, color = Ink.text)
                }
                is MdBlock.Para -> Text(inline(b.text), style = Type.body, color = Ink.text)
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
