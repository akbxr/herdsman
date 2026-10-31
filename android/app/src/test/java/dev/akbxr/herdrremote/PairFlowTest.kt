package dev.akbxr.herdrremote

import app.cash.paparazzi.DeviceConfig
import app.cash.paparazzi.Paparazzi
import org.junit.Assert.assertEquals
import org.junit.Assert.assertNull
import org.junit.Rule
import org.junit.Test

class PairFlowTest {
    // Paparazzi's rule brings Android's classes (android.net.Uri) to the JVM.
    @get:Rule
    val paparazzi = Paparazzi(deviceConfig = DeviceConfig.PIXEL_6)

    private val token = "a4711137e83aafaa1d085e375019b7fe132c9b701ad3c38b"

    @Test fun readsTheServersLink() {
        // Exactly what herdr-remote pair put in the QR code (decoded from it).
        val link = "herdr-remote://pair?host=AKBARs-MacBook-Air.local&token=$token&url=http%3A%2F%2F127.0.0.1%3A7710"
        assertEquals(PairInfo("http://127.0.0.1:7710", token, "AKBARs-MacBook-Air.local"), PairFlow.parseLink(link))
    }

    @Test fun refusesOtherCodes() {
        assertNull(PairFlow.parseLink("https://example.com"))
        assertNull(PairFlow.parseLink("herdr-remote://pair?url=http%3A%2F%2Fx&token=short"))
        assertNull(PairFlow.parseLink("herdr-remote://pair?url=file%3A%2F%2Fx&token=$token"))
    }

    @Test fun typedAddresses() {
        assertEquals("http://100.64.1.2:7710", PairFlow.normalizeAddress("100.64.1.2"))
        assertEquals("http://100.64.1.2:8000", PairFlow.normalizeAddress(" 100.64.1.2:8000/ "))
        assertEquals("http://mac.tail1234.ts.net:7710", PairFlow.normalizeAddress("http://mac.tail1234.ts.net"))
    }
}
