package dev.akbxr.herdsman

import org.junit.Assert.assertEquals
import org.junit.Test

class MarkdownTest {
    @Test fun table() {
        val blocks = mdBlocks(
            """
            Before.

            | Fitur | Status | Catatan |
            |---|:---:|--:|
            | Chat | ✓ | `a|b` and \| escaped |
            | Changes | ✗ |
            After.
            """.trimIndent(),
        )
        assertEquals(3, blocks.size)
        assertEquals(MdBlock.Para("Before."), blocks[0])
        val t = blocks[1] as MdBlock.Table
        assertEquals(listOf("Fitur", "Status", "Catatan"), t.header)
        assertEquals(listOf('l', 'c', 'r'), t.align)
        assertEquals(listOf("Chat", "✓", "`a|b` and | escaped"), t.rows[0])
        assertEquals(listOf("Changes", "✗", ""), t.rows[1])  // short rows filled
        assertEquals(MdBlock.Para("After."), blocks[2])
    }

    @Test fun pipeLineWithoutRuleIsText() {
        val blocks = mdBlocks("| not a table\nnext line")
        assertEquals(listOf(MdBlock.Para("| not a table next line")), blocks)
    }

    @Test fun cells() {
        assertEquals(listOf("a", "b c", ""), tableCells("| a | b c | |"))
        assertEquals(listOf("x", "y"), tableCells("x | y"))
    }
}
