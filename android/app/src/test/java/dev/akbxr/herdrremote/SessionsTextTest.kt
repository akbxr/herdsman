package dev.akbxr.herdrremote

import org.junit.Assert.assertEquals
import org.junit.Test
import java.time.ZoneId

class SessionsTextTest {
    private val utc = ZoneId.of("UTC")
    private val now = 1_793_500_000_000L // 2026-11-01T06:26:40Z
    private fun ago(min: Long) = now - min * 60_000

    @Test fun ago() {
        assertEquals("just now", agoText(ago(0), now, utc))
        assertEquals("just now", agoText(now + 5_000, now, utc)) // a clock a little ahead
        assertEquals("1 min ago", agoText(ago(1), now, utc))
        assertEquals("59 min ago", agoText(ago(59), now, utc))
        assertEquals("1 h ago", agoText(ago(60), now, utc))
        assertEquals("23 h ago", agoText(ago(23 * 60 + 59), now, utc))
        assertEquals("yesterday", agoText(ago(24 * 60), now, utc))
        assertEquals("2 days ago", agoText(ago(48 * 60), now, utc))
        assertEquals("6 days ago", agoText(ago(7 * 24 * 60 - 1), now, utc))
        assertEquals("25 Oct", agoText(ago(7 * 24 * 60), now, utc))
        assertEquals("1 Nov 2025", agoText(ago(365 * 24 * 60), now, utc))
    }

    @Test fun messages() {
        assertEquals("1 message", messagesText(1, false))
        assertEquals("0 messages", messagesText(0, false))
        assertEquals("14 messages", messagesText(14, false))
        assertEquals("1+ messages", messagesText(1, true))
        assertEquals("140+ messages", messagesText(140, true))
    }
}
