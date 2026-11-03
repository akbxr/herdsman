package dev.akbxr.herdsman

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

    // herdr's session.snapshot, as GET /api/state carries it under "herdr".
    private val snapshot = """
        {
          "agents": [
            {"agent": "claude", "agent_status": "working", "pane_id": "wF:p2", "tab_id": "wF:t2", "workspace_id": "wF",
             "terminal_id": "x1", "terminal_title": "✳ Fix the login form", "cwd": "/Users/me/api", "foreground_cwd": "/Users/me/api", "focused": true},
            {"agent": "codex", "agent_status": "unknown", "pane_id": "wA:p1", "tab_id": "wA:t1", "workspace_id": "wA",
             "terminal_id": "x2", "terminal_title": "codex", "cwd": "/Users/me", "foreground_cwd": "/Users/me", "focused": false}
          ],
          "panes": [
            {"pane_id": "wF:p2", "tab_id": "wF:t2", "workspace_id": "wF", "cwd": "/Users/me/api", "foreground_cwd": "/Users/me/api",
             "agent_status": "working", "terminal_title": "✳ Fix the login form", "focused": true},
            {"pane_id": "wF:p3", "tab_id": "wF:t2", "workspace_id": "wF", "cwd": "/Users/me/api", "foreground_cwd": "/Users/me/api",
             "agent_status": "unknown", "terminal_title": "", "focused": false},
            {"pane_id": "wA:p1", "tab_id": "wA:t1", "workspace_id": "wA", "cwd": "/Users/me", "foreground_cwd": "/Users/me",
             "agent_status": "blocked", "terminal_title": "codex", "focused": false}
          ],
          "tabs": [
            {"tab_id": "wF:t2", "workspace_id": "wF", "label": "", "number": 2, "pane_count": 2, "agent_status": "working", "focused": true},
            {"tab_id": "wA:t1", "workspace_id": "wA", "label": "build", "number": 1, "pane_count": 1, "agent_status": "blocked", "focused": true}
          ],
          "workspaces": [
            {"workspace_id": "wA", "label": "home", "number": 1, "active_tab_id": "wA:t1", "tab_count": 1, "pane_count": 1, "agent_status": "blocked", "focused": false},
            {"workspace_id": "wF", "label": "api", "number": 2, "active_tab_id": "wF:t2", "tab_count": 1, "pane_count": 2, "agent_status": "working", "focused": true}
          ],
          "focused_workspace_id": "wF", "focused_tab_id": "wF:t2", "focused_pane_id": "wF:p2"
        }
    """.trimIndent()

    @Test fun readsHerdrCliPanesWithoutTheApp() {
        val herd = Herd.from(JSONObject("""{"herdr": $snapshot}"""))
        assertFalse(herd.app)
        assertTrue(herd.cli)
        assertEquals("", herd.layout)
        assertEquals(emptyList<Tab>(), herd.tabs)  // the Screen tab has none of herdr's
        // By workspace number, then tab, then pane.
        assertEquals(listOf("herdr:wA:p1", "herdr:wF:p2", "herdr:wF:p3"), herd.agents.map { it.terminal })
        assertTrue(herd.agents.all { it.cli && it.source == "herdr" && it.machine == "" })

        val codex = herd.agents[0]
        assertEquals("codex", codex.app)
        assertEquals(null, codex.label)  // its title says only its name
        assertEquals(State.NeedsYou, codex.state)  // the pane's status
        assertEquals("build", codex.tabName)
        assertEquals("home", codex.spaceName)
        assertFalse(codex.tabActive)  // its workspace isn't focused
        assertEquals("p1", codex.tagNumber)

        val claude = herd.agents[1]
        assertEquals("claude", claude.app)
        assertEquals("Fix the login form", claude.title)
        assertEquals(State.Working, claude.state)
        assertEquals("2", claude.tabName)  // no label: its number
        assertEquals("wF:t2", claude.tabId)
        assertEquals("wF", claude.spaceId)
        assertEquals("api", claude.spaceName)
        assertEquals("wF:p2", claude.pane)
        assertTrue(claude.tabActive)

        val shell = herd.agents[2]
        assertEquals("shell", shell.app)
        assertEquals(null, shell.label)
        assertEquals(State.Idle, shell.state)  // unknown

        assertEquals(listOf(Workspace("wF", "api", true), Workspace("wA", "home", false)), herd.workspaces)
        assertEquals("", machineOf("herdr:wF:p2"))
    }

    @Test fun readsBothTheAppAndTheCli() {
        val root = JSONObject(state).put("herdr", JSONObject(snapshot))
        val herd = Herd.from(root)
        assertTrue(herd.app)
        assertTrue(herd.cli)
        assertEquals("main-full", herd.layout)
        assertEquals(listOf("t12", "vps/t3", "t4"), herd.appAgents.map { it.terminal })
        assertEquals(3, herd.cliAgents.size)
        assertEquals(listOf("", "vps"), herd.tabs.map { it.machine })  // the app's tabs only
        // Machines group the app's terminals only.
        assertEquals(listOf(1, 2, 0), machineGroups(herd, herd.appAgents).map { it.agents.size })
    }

    @Test fun readsSessionTitles() {
        val root = JSONObject(state).put("herdr", JSONObject(snapshot))
            .put("titles", JSONObject("""{"t12": "Fix the bug in the nav", "herdr:wF:p2": "Fix the login form", "herdr:wA:p1": "  "}"""))
        val herd = Herd.from(root)
        val tego = herd.agents.first { it.terminal == "t12" }
        assertEquals("Fix the bug in the nav", tego.session)
        assertEquals(tego.title, tego.name)  // its own name stays
        val claude = herd.agents.first { it.terminal == "herdr:wF:p2" }
        assertEquals("Fix the login form", claude.session)
        assertEquals("claude", claude.name)  // its terminal title only said the session again
        assertEquals(null, herd.agents.first { it.terminal == "herdr:wA:p1" }.session)  // blank: none
        assertEquals(null, herd.agents.first { it.terminal == "t4" }.session)
    }

    @Test fun pinsGoOnTopInTheirOrder() {
        val herd = Herd.from(JSONObject(state).put("herdr", JSONObject(snapshot)))
        val all = herd.agents
        fun of(t: String) = all.first { it.terminal == t }
        // A herdr CLI tab, then an agent of the app on its own: in the order pinned.
        val wF = of("herdr:wF:p2")
        val (pinned, rest) = splitPinned(all, listOf(tabPinKey(wF), pinKey(of("t12"))))
        val inTab = all.filter { it.cli && it.tabId == wF.tabId }.map { it.terminal }
        assertEquals(inTab + "t12", pinned.map { it.terminal })
        assertEquals(all.map { it.terminal } - pinned.map { it.terminal }.toSet(), rest.map { it.terminal })
        // An agent pinned on its own and in a pinned tab: once, where its first pin is.
        val twice = splitPinned(all, listOf(pinKey(of("t12")), pinKey(wF), tabPinKey(wF))).first.map { it.terminal }
        assertEquals("t12", twice[0])
        assertEquals(twice.distinct(), twice)
        // A tab key of the app's tabs is not a herdr CLI tab's of the same id.
        assertTrue(tabPinKey(of("t12")) != tabPinKey(wF.copy(tabId = of("t12").tabId)))
        // Pins of terminals gone: nothing.
        assertEquals(emptyList<Agent>(), splitPinned(all, listOf("a:t999", "t:/tb99")).first)
    }

    @Test fun searchMatchesEveryWord() {
        val herd = Herd.from(JSONObject(state).put("herdr", JSONObject(snapshot))
            .put("titles", JSONObject("""{"herdr:wF:p2": "Research what PKI is"}""")))
        fun find(q: String) = herd.agents.filter { it.matches(q) }.map { it.terminal }
        assertEquals(listOf("herdr:wF:p2"), find("claude pki"))  // agent + session, any case
        assertEquals(listOf("herdr:wF:p2"), find("  research   PKI "))
        assertTrue(find("api").containsAll(listOf("herdr:wF:p2", "herdr:wF:p3")))  // the workspace (and the app's space)
        assertEquals(listOf("herdr:wF:p3"), find("p3"))  // the address
        assertEquals(listOf("t12", "herdr:wA:p1"), find("needs"))  // the state
        assertEquals(listOf("vps/t3", "t4"), find("vps"))  // the machine (t4 is in vps's space)
        assertEquals(herd.agents.size, find("").size)
        assertEquals(emptyList<String>(), find("claude nav"))
    }

    @Test fun aCommandLineTitleIsNoName() {
        assertEquals(null, Herd.herdrTitle("tego -r 20261029-161421-e603e8 -m anthropic/claude-opus-5-5 -t high", "tego"))
        assertEquals(null, Herd.herdrTitle("codex", "codex"))
        assertEquals("Fix the login form", Herd.herdrTitle("✳ Fix the login form", "claude"))
        assertEquals("tegory notes", Herd.herdrTitle("tegory notes", "tego"))  // not its command
    }

    @Test fun appOnlyHasNoCli() {
        val herd = Herd.from(JSONObject(state))
        assertTrue(herd.app)
        assertFalse(herd.cli)
        assertTrue(herd.agents.none { it.cli })
        assertEquals(emptyList<Workspace>(), herd.workspaces)
        val empty = Herd.from(JSONObject("{}"))
        assertFalse(empty.app)
        assertEquals(emptyList<Agent>(), empty.agents)
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
