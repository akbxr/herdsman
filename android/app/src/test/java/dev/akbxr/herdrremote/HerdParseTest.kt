package dev.akbxr.herdrremote

import app.cash.paparazzi.DeviceConfig
import app.cash.paparazzi.Paparazzi
import org.json.JSONObject
import org.junit.Assert.assertEquals
import org.junit.Assert.assertFalse
import org.junit.Assert.assertTrue
import org.junit.Rule
import org.junit.Test

class HerdParseTest {
    // Paparazzi's rule brings Android's classes (org.json) to the JVM.
    @get:Rule
    val paparazzi = Paparazzi(deviceConfig = DeviceConfig.PIXEL_6)

    // Shaped like GET /api/state with a machine added.
    private val state = """
        {
          "status": {"layout": "main-full"},
          "ls": {"spaces": [
            {"id": "s1", "name": "~", "folder": "/Users/me", "machine": null, "active": true, "tabs": [
              {"id": "tb1", "name": "1", "active": true, "terminals": [
                {"terminal": "t12", "pane": "p1", "app": "tego", "name": null, "status": "blocked"}
              ]}
            ]},
            {"id": "s2", "name": "api", "folder": "/home/me/api", "machine": "vps", "active": false, "tabs": [
              {"id": "tb2", "name": "1", "active": true, "terminals": [
                {"terminal": "vps/t3", "pane": "p2", "app": "claude-code", "name": "deploy", "status": "working"},
                {"terminal": "t4", "pane": "p3", "app": null, "name": "", "status": null}
              ]}
            ]}
          ]},
          "machines": [
            {"name": "vps", "host": "me@vps.example", "id": "m1", "session": "x", "state": "connected", "since_ms": 1000,
             "why": "", "target": "", "temporary": false, "version": "0.9", "via": ""},
            {"name": "gpu", "host": "gpu-box", "state": "unreachable", "why": "ssh: connect to host gpu-box port 22: Operation timed out"}
          ]
        }
    """.trimIndent()

    @Test fun readsMachinesAndWhereTerminalsRun() {
        val herd = Herd.from(JSONObject(state))
        assertEquals("main-full", herd.layout)
        assertEquals(listOf("t12", "vps/t3", "t4"), herd.agents.map { it.terminal })

        val local = herd.agents[0]
        assertEquals("", local.machine)
        assertEquals(State.NeedsYou, local.state)
        assertTrue(local.tabActive)

        val remote = herd.agents[1]
        assertEquals("vps", remote.machine)
        assertEquals("deploy", remote.title)
        assertEquals("t3", remote.tagNumber)
        assertFalse(remote.tabActive)  // its space doesn't show

        // No prefix in the address: its space's machine.
        assertEquals("vps", herd.agents[2].machine)
        assertEquals("shell", herd.agents[2].app)

        assertEquals(listOf("", "vps"), herd.tabs.map { it.machine })
        assertEquals(
            listOf(
                Machine("vps", "vps", "me@vps.example", "connected", ""),
                Machine("gpu", "gpu", "gpu-box", "unreachable", "ssh: connect to host gpu-box port 22: Operation timed out"),
            ),
            herd.machines,
        )
    }

    @Test fun olderServerHasNoMachines() {
        val herd = Herd.from(JSONObject("""{"status":{"layout":"x"},"ls":{"spaces":[{"id":"s","name":"~","active":true,"tabs":[{"id":"t","name":"1","active":true,"terminals":[{"terminal":"t1","app":"tego","status":"idle"}]}]}]}}"""))
        assertEquals(emptyList<Machine>(), herd.machines)
        assertEquals("", herd.agents.single().machine)
        assertEquals(emptyList<MachineGroup>(), machineGroups(herd, herd.agents))
    }

    @Test fun groupsThisMacFirstAndKeepsEmptyMachines() {
        val herd = Herd.from(JSONObject(state))
        val groups = machineGroups(herd, herd.agents)
        assertEquals(listOf("", "vps", "gpu"), groups.map { it.name })
        assertEquals(listOf("This Mac", "vps", "gpu"), groups.map { it.label })
        assertEquals(listOf(1, 2, 0), groups.map { it.agents.size })
        assertEquals("unreachable", groups[2].state)
    }

    @Test fun machinesListAndModelsSupport() {
        val list = JSONObject("""{"machines":[{"name":"","label":"This Mac","host":"mbp","state":"connected","local":true},{"name":"vps","label":"vps","host":"h","state":"needs_login","why":"needs a password","temporary":false}]}""")
            .getJSONArray("machines")
        val mac = Machine.from(list.getJSONObject(0))
        assertTrue(mac.local)
        assertEquals("This Mac", mac.label)
        val vps = Machine.from(list.getJSONObject(1))
        assertTrue(vps.reconnectable)
        assertEquals("needs a password", vps.why)

        val unsupported = ModelChoices.from(JSONObject("""{"supported":false,"why":"not on another machine"}"""))
        assertFalse(unsupported.supported)
        assertEquals("not on another machine", unsupported.why)
        assertTrue(ModelChoices.from(JSONObject("""{"agent":"tego"}""")).supported)
    }
}
