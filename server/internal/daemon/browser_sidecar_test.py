import asyncio
import time
import unittest
from types import SimpleNamespace
from unittest.mock import AsyncMock

from browser_sidecar import BrowserBridge, IDLE_SECONDS, MAX_SESSIONS

WORKSPACE = "00000000-0000-0000-0000-000000000001"
CHAT = "00000000-0000-0000-0000-000000000002"


class BrowserBridgeTest(unittest.IsolatedAsyncioTestCase):
    async def asyncSetUp(self):
        self.browser = SimpleNamespace(
            navigate=AsyncMock(return_value=SimpleNamespace(success=True, content="ok")),
            close=AsyncMock(),
            _internal=SimpleNamespace(client=SimpleNamespace(send=AsyncMock(return_value={"data": "jpeg"}))),
            list_tabs=AsyncMock(return_value=SimpleNamespace(success=True, metadata={"tabs": []})),
        )
        self.factory = AsyncMock(return_value=self.browser)
        self.bridge = BrowserBridge(self.factory)

    async def call(self, action, actor="user", params=None, **extra):
        return await self.bridge.call(dict(workspace_id=WORKSPACE, session_id=CHAT,
                                          action=action, actor=actor, params=params or {}, **extra))

    async def test_snapshot_does_not_launch_browser(self):
        result = await self.call("snapshot")
        self.assertFalse(result["active"])
        self.factory.assert_not_awaited()

    async def test_agent_and_user_share_browser_and_handoff_is_enforced(self):
        await self.call("navigate", "agent", {"url": "http://localhost:3000"})
        control = await self.call("handoff", params={"target": "user"})
        with self.assertRaisesRegex(ValueError, "controlled by the user"):
            await self.call("navigate", "agent", {"url": "https://example.com"})
        await self.call("input", params={"kind": "text", "text": "你好"}, epoch=control["epoch"])
        await self.call("handoff", params={"target": "agent"})
        await self.call("navigate", "agent", {"url": "http://localhost:3001"})
        self.assertEqual(self.factory.await_count, 1)
        self.assertEqual(self.browser.navigate.await_count, 2)

    async def test_stale_inputs_are_rejected(self):
        await self.call("start")
        old = await self.call("handoff", params={"target": "user"})
        await self.call("handoff", params={"target": "agent"})
        await self.call("handoff", params={"target": "user"})
        with self.assertRaisesRegex(ValueError, "control changed"):
            await self.call("input", params={"kind": "text", "text": "old"}, epoch=old["epoch"])

    async def test_agent_cannot_handoff_or_inject_a_profile(self):
        with self.assertRaisesRegex(ValueError, "Unsupported"):
            await self.call("handoff", "agent", {"target": "agent"})
        async def navigate(url):
            return SimpleNamespace(success=True, content=url)
        self.browser.navigate = navigate
        with self.assertRaisesRegex(ValueError, "Unsupported browser parameters"):
            await self.call("navigate", "agent", {"profile": "other", "url": "https://example.com"})

    async def test_agent_dispatches_only_parameters_for_selected_action(self):
        params = dict(url="http://localhost:3000", ref="e1", selector="#message",
                      text="hello", key="Enter", x=12, y=34, direction="down",
                      amount=73, tab_id="tab-1")
        actions = {
            "navigate": {"url"}, "dom_tree": set(),
            "click": {"ref", "selector", "x", "y"},
            "type": {"text", "ref", "selector"},
            "fill": {"text", "ref", "selector"}, "press": {"key"},
            "scroll": {"direction", "amount"}, "go_back": set(),
            "go_forward": set(), "reload": set(), "new_tab": {"url"},
            "switch_tab": {"tab_id"}, "close_tab": {"tab_id"}, "list_tabs": set(),
        }
        for action, keys in actions.items():
            with self.subTest(action=action):
                method = AsyncMock(return_value=SimpleNamespace(success=True, content="ok"))
                setattr(self.browser, action, method)
                await self.call(action, "agent", params)
                method.assert_awaited_once_with(**{key: params[key] for key in keys})

    async def test_null_optional_parameters_keep_octop_defaults(self):
        self.browser.scroll = AsyncMock(return_value=SimpleNamespace(success=True, content="ok"))
        await self.call("scroll", "agent", {"direction": None, "amount": None, "url": None})
        self.browser.scroll.assert_awaited_once_with()

    async def test_handoff_waits_for_inflight_agent_action(self):
        started, finish = asyncio.Event(), asyncio.Event()

        async def navigate(**_):
            started.set()
            await finish.wait()
            return SimpleNamespace(success=True, content="ok")

        self.browser.navigate = navigate
        action = asyncio.create_task(self.call("navigate", "agent", {"url": "https://example.com"}))
        await started.wait()
        handoff = asyncio.create_task(self.call("handoff", params={"target": "user"}))
        await asyncio.sleep(0)
        self.assertFalse(handoff.done())
        finish.set()
        await action
        self.assertEqual((await handoff)["control_owner"], "user")

    async def test_sessions_are_isolated_by_workspace_and_chat(self):
        await self.call("start")
        await self.bridge.call(dict(workspace_id=CHAT, session_id=CHAT, actor="user", action="start"))
        await self.bridge.call(dict(workspace_id=WORKSPACE, session_id=WORKSPACE, actor="user", action="start"))
        self.assertEqual(len({call.args[0] for call in self.factory.await_args_list}), 3)

    async def test_local_file_navigation_is_blocked(self):
        for url in ("file:///etc/passwd", "javascript:alert(1)", "chrome://settings"):
            with self.assertRaisesRegex(ValueError, "HTTP"):
                await self.call("navigate", "agent", {"url": url})
        self.browser.navigate.assert_not_awaited()

    async def test_snapshot_and_close(self):
        await self.call("start")
        snapshot = await self.call("snapshot")
        self.assertEqual(snapshot["frame"], "jpeg")
        self.assertTrue(snapshot["active"])
        self.assertFalse((await self.call("close"))["active"])
        self.browser.close.assert_awaited_once_with(kill=True)
        self.assertEqual(len(self.bridge.sessions), 0)

    async def test_scroll_preserves_coalesced_pixel_amount(self):
        await self.call("start")
        state = await self.call("handoff", params={"target": "user"})
        self.browser.scroll = AsyncMock(return_value=SimpleNamespace(success=True))
        await self.call("input", params={"kind": "scroll", "direction": "down", "amount": 73}, epoch=state["epoch"])
        self.browser.scroll.assert_awaited_once_with(direction="down", amount=73)
        for amount in (0, -2, 2001, "300", True):
            with self.assertRaisesRegex(ValueError, "scroll"):
                await self.call("input", params={"kind": "scroll", "direction": "down", "amount": amount}, epoch=state["epoch"])

    async def test_expired_action_cannot_change_browser(self):
        await self.call("start")
        with self.assertRaisesRegex(ValueError, "expired"):
            await self.call("navigate", "agent", {"url": "http://localhost"}, expires_at=int(time.time()*1000)-1)
        self.browser.navigate.assert_not_awaited()

    async def test_idle_cleanup_and_session_limit(self):
        for i in range(MAX_SESSIONS):
            await self.bridge.call(dict(workspace_id=WORKSPACE, session_id=f"00000000-0000-0000-0000-{i+10:012d}", actor="user", action="start"))
        with self.assertRaisesRegex(ValueError, "limit"):
            await self.call("start")
        for state in self.bridge.sessions.values():
            state.touched = time.monotonic() - IDLE_SECONDS - 1
        await self.bridge.reap()
        self.assertEqual(len(self.bridge.sessions), 0)


if __name__ == "__main__":
    unittest.main()
