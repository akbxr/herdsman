package dev.akbxr.herdsman

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
        // Exactly what herdsman pair put in the QR code (decoded from it).
        val link = "herdsman://pair?host=AKBARs-MacBook-Air.local&token=$token&url=http%3A%2F%2F127.0.0.1%3A7710"
        assertEquals(PairInfo("http://127.0.0.1:7710", token, "AKBARs-MacBook-Air.local"), PairFlow.parseLink(link))
    }

    @Test fun refusesOtherCodes() {
        assertNull(PairFlow.parseLink("https://example.com"))
        assertNull(PairFlow.parseLink("herdsman://pair?url=http%3A%2F%2Fx&token=short"))
        assertNull(PairFlow.parseLink("herdsman://pair?url=file%3A%2F%2Fx&token=$token"))
    }

    @Test fun typedAddresses() {
        assertEquals("http://100.64.1.2:7710", PairFlow.normalizeAddress("100.64.1.2"))
        assertEquals("http://100.64.1.2:8000", PairFlow.normalizeAddress(" 100.64.1.2:8000/ "))
        assertEquals("http://mac.tail1234.ts.net:7710", PairFlow.normalizeAddress("http://mac.tail1234.ts.net"))
    }

    @Test fun readsEveryRoute() {
        // herdsman pair with a tunnel: url= for older apps, routes= for this one.
        val link = "herdsman://pair?host=Mac&routes=http%3A%2F%2F192.168.1.5%3A7710%2Chttp%3A%2F%2F100.64.1.2%3A7710%2Chttps%3A%2F%2Fherdr.example.com" +
            "&token=$token&url=http%3A%2F%2F100.64.1.2%3A7710"
        val info = PairFlow.parseLink(link)!!
        assertEquals("http://100.64.1.2:7710", info.address)
        assertEquals(listOf("http://192.168.1.5:7710", "http://100.64.1.2:7710", "https://herdr.example.com"), info.routes)
        // A link from before routes: its one address.
        assertEquals(listOf("http://127.0.0.1:7710"), PairFlow.parseLink("herdsman://pair?host=M&token=$token&url=http%3A%2F%2F127.0.0.1%3A7710")!!.routes)
    }

    @Test fun typedTunnelNames() {
        assertEquals("https://herdr.example.com", PairFlow.normalizeAddress("herdr.example.com"))
        assertEquals("https://herdr.example.com", PairFlow.normalizeAddress(" https://herdr.example.com/ "))
        assertEquals("http://mac.tail1234.ts.net:7710", PairFlow.normalizeAddress("mac.tail1234.ts.net"))
        assertEquals("http://macbook.local:7710", PairFlow.normalizeAddress("macbook.local"))
        assertEquals("http://192.168.1.5:7710", PairFlow.normalizeAddress("192.168.1.5"))
    }

    @Test fun routeKinds() {
        assertEquals(true, Routes.isTailscale("http://100.122.252.28:7710"))
        assertEquals(false, Routes.isTailscale("http://100.200.1.1:7710"))  // outside 100.64.0.0/10
        assertEquals(true, Routes.isLocal("http://192.168.1.5:7710"))
        assertEquals(true, Routes.isLocal("http://172.20.0.3:7710"))
        assertEquals(false, Routes.isLocal("https://herdr.example.com"))
        assertEquals(1500, Routes.timeoutMs("http://192.168.1.5:7710"))
        assertEquals(6000, Routes.timeoutMs("https://herdr.example.com"))
    }

    @Test fun picksTheBestRouteThatAnswers() = kotlinx.coroutines.runBlocking {
        val lan = "http://192.168.1.5:7710"; val ts = "http://100.64.1.2:7710"; val cf = "https://herdr.example.com"
        // Away from home: the LAN is slow to fail, Tailscale is off; the tunnel answers.
        val away = Routes.probeAll(listOf(lan, ts, cf), token) { r, _ ->
            when (r) { lan -> { kotlinx.coroutines.delay(200); false }; ts -> false; else -> true }
        }
        assertEquals(cf, away)
        // At home: the LAN wins though the tunnel answered first.
        val home = Routes.probeAll(listOf(lan, ts, cf), token) { r, _ ->
            if (r == lan) { kotlinx.coroutines.delay(100); true } else true
        }
        assertEquals(lan, home)
        assertNull(Routes.probeAll(listOf(lan, cf), token) { _, _ -> false })
    }

    @Test fun learnsRoutesAndVpnAdvice() {
        val r = Routes.of(listOf("http://100.64.1.2:7710"), "learn-$token")
        assertEquals(true, r.needsVpn)
        assertEquals(Link.NoVpn, Link.NoVpn.forRoutes(r))
        var told: List<String>? = null
        r.onLearned = { told = it }
        r.learn(listOf("http://100.64.1.2:7710", "https://herdr.example.com/"))
        assertEquals(listOf("http://100.64.1.2:7710", "https://herdr.example.com"), told)
        assertEquals(false, r.needsVpn)
        // A tunnel reaches the Mac: Tailscale being off isn't the problem.
        assertEquals(Link.Up, Link.NoVpn.forRoutes(r))
        assertEquals(Link.Offline, Link.Offline.forRoutes(r))
        // Shared in the process: the same object for the same Mac.
        org.junit.Assert.assertSame(r, Routes.of(r.all, "learn-$token"))
    }
}
