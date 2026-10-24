"""Private NDJSON bridge to Octop Browser; launched over pipes by the daemon."""

import asyncio
import contextlib
import json
import logging
from pathlib import Path
import socket
import sys
import time
import uuid
from dataclasses import dataclass, field
from urllib.parse import urlsplit

from octop_browser import BrowserSession
from octop_browser.settings import OctopSettings
from octop_browser.profile import ProfileManager

logging.basicConfig(stream=sys.stderr, level=logging.WARNING)
WIDTH, HEIGHT = 1280, 800
MAX_SESSIONS = 8
IDLE_SECONDS = 15 * 60
MAX_FRAME_BYTES = 2 * 1024 * 1024
ACTION_PARAMS = {
    "navigate": {"url"}, "dom_tree": set(),
    "click": {"ref", "selector", "x", "y"},
    "type": {"text", "ref", "selector"},
    "fill": {"text", "ref", "selector"}, "press": {"key"},
    "scroll": {"direction", "amount"}, "go_back": set(),
    "go_forward": set(), "reload": set(), "new_tab": {"url"},
    "switch_tab": {"tab_id"}, "close_tab": {"tab_id"},
    "list_tabs": set(), "screenshot": set(),
}
AGENT_ACTIONS = set(ACTION_PARAMS)
TOOL_PARAMS = set().union(*ACTION_PARAMS.values())


@dataclass
class Session:
    browser: object = None
    owner: str = "agent"
    epoch: int = field(default_factory=lambda: uuid.uuid4().int % (2**30))
    touched: float = field(default_factory=time.monotonic)
    lock: asyncio.Lock = field(default_factory=asyncio.Lock)


class BrowserBridge:
    def __init__(self, factory=None):
        self.sessions = {}
        self.factory = factory or self.create_browser

    @staticmethod
    async def create_browser(profile):
        settings = OctopSettings()
        settings.cdp_host = "127.0.0.1"
        settings.cdp_ws_url = None
        settings.profiles_dir = Path.home() / ".multica" / "browser-profiles"
        settings.viewport_width = WIDTH
        settings.viewport_height = HEIGHT
        manager = ProfileManager(settings=settings)
        managed_profile = manager.get_or_create(profile)
        # Never attach to an unrelated Chrome already listening on port 9222.
        with socket.socket() as reservation:
            reservation.bind(("127.0.0.1", 0))
            managed_profile.cdp_port = reservation.getsockname()[1]
        return await BrowserSession.create(profile=profile, mode="headless", settings=settings,
                                           profile_manager=manager)

    async def call(self, request):
        # Neither tool arguments nor UI input may select another profile or CDP endpoint.
        workspace = str(uuid.UUID(request["workspace_id"]))
        chat = str(uuid.UUID(request["session_id"]))
        key = workspace + "-" + chat
        actor = request["actor"]
        action = request["action"]
        params = request.get("params", {})
        if actor not in {"agent", "user"} or not isinstance(params, dict):
            raise ValueError("Invalid browser request")
        if actor == "agent" and action not in AGENT_ACTIONS:
            raise ValueError("Unsupported browser tool")
        if action in ACTION_PARAMS:
            if params.keys() - TOOL_PARAMS:
                raise ValueError("Unsupported browser parameters")
            params = {key: value for key, value in params.items()
                      if key in ACTION_PARAMS[action] and value is not None}
        state = self.sessions.get(key)
        if state is None:
            if action == "snapshot":
                return {"enabled": True, "active": False, "control_owner": "agent"}
            if action not in AGENT_ACTIONS | {"start"}:
                raise ValueError("Start the browser first")
            if len(self.sessions) >= MAX_SESSIONS:
                raise ValueError("Browser session limit reached; close another browser first")
            state = self.sessions[key] = Session()
        async with state.lock:
            if request.get("expires_at", 0) and request["expires_at"] <= time.time() * 1000:
                raise ValueError("Browser request expired; refresh before interacting")
            state.touched = time.monotonic()
            if action == "close":
                if state.browser:
                    await state.browser.close(kill=True)
                state.browser = None
                state.owner = "agent"
                state.epoch += 1
                self.sessions.pop(key, None)
                return {"enabled": True, "active": False, "control_owner": "agent"}
            if action == "handoff":
                target = params.get("target")
                if target not in {"agent", "user"}:
                    raise ValueError("Invalid control owner")
                state.owner = target
                state.epoch += 1
                return self.status(state)
            if action == "snapshot" and state.browser is None:
                return {"enabled": True, "active": False, "control_owner": state.owner}
            if action not in {"snapshot", "start"}:
                if actor != state.owner:
                    raise ValueError("Browser is controlled by the user; wait for them to return control before retrying" if actor == "agent" else "Take control before interacting")
                if actor == "user" and request.get("epoch") != state.epoch:
                    raise ValueError("Browser control changed; refresh before interacting")
            if state.browser is None:
                state.browser = await self.factory(key)
            browser = state.browser
            if action == "snapshot":
                result = self.status(state)
                result.update(await self.snapshot(browser))
                return result
            if action == "start":
                return self.status(state)
            if action == "input":
                await self.input(browser, params)
                return self.status(state)
            if action not in AGENT_ACTIONS:
                raise ValueError("Unsupported browser action")
            if action in {"navigate", "new_tab"}:
                url = params.get("url", "about:blank")
                if url != "about:blank" and urlsplit(url).scheme not in {"http", "https"}:
                    raise ValueError("Only HTTP(S) pages can be opened")
            if action == "screenshot":
                result = await self.snapshot(browser)
                return {"content": "Current browser viewport", "image": result["frame"]}
            result = await getattr(browser, action)(**params)
            if not result.success:
                raise ValueError(result.error or "Browser action failed")
            return {"content": result.content}

    @staticmethod
    def status(state):
        return {"enabled": True, "active": state.browser is not None,
                "control_owner": state.owner, "epoch": state.epoch,
                "width": WIDTH, "height": HEIGHT}

    @staticmethod
    async def snapshot(browser):
        # Like Octop's stream router, capture through CDP without writing a screenshot file.
        frame = await browser._internal.client.send(
            "Page.captureScreenshot", {"format": "jpeg", "quality": 60}
        )
        data = frame.get("data", "")
        if len(data) > MAX_FRAME_BYTES:
            raise ValueError("Browser frame is too large")
        tabs = await browser.list_tabs()
        if not tabs.success:
            raise ValueError(tabs.error or "Cannot read browser tabs")
        return {"frame": data, "tabs": (tabs.metadata or {}).get("tabs", [])}

    @staticmethod
    async def input(browser, params):
        kind = params.get("kind")
        client = browser._internal.client
        if kind == "click":
            x, y = params["x"], params["y"]
            if not (isinstance(x, (int, float)) and isinstance(y, (int, float))
                    and 0 <= x <= WIDTH and 0 <= y <= HEIGHT):
                raise ValueError("Click is outside the browser viewport")
            for event in ("mousePressed", "mouseReleased"):
                await client.send("Input.dispatchMouseEvent", {
                    "type": event, "x": x, "y": y, "button": "left", "clickCount": 1,
                })
        elif kind == "text":
            text = params["text"]
            if not isinstance(text, str) or len(text) > 10000:
                raise ValueError("Invalid text input")
            await client.send("Input.insertText", {"text": text})
        elif kind == "key":
            result = await browser.press(params["key"])
            if not result.success:
                raise ValueError(result.error or "Key input failed")
        elif kind == "scroll":
            amount = params.get("amount", 300)
            if type(amount) is not int or not 0 < amount <= 2000 or params.get("direction") not in {"up", "down", "left", "right"}:
                raise ValueError("Invalid scroll input")
            result = await browser.scroll(direction=params["direction"], amount=amount)
            if not result.success:
                raise ValueError(result.error or "Scroll failed")
        else:
            raise ValueError("Unsupported input event")

    async def reap(self):
        for key, state in list(self.sessions.items()):
            if state.lock.locked() or time.monotonic() - state.touched < IDLE_SECONDS:
                continue
            async with state.lock:
                if state.browser:
                    with contextlib.suppress(Exception):
                        await state.browser.close(kill=True)
                self.sessions.pop(key, None)

    async def close(self):
        for state in self.sessions.values():
            if state.browser:
                with contextlib.suppress(Exception):
                    await state.browser.close(kill=True)


async def main():
    bridge = BrowserBridge()
    pending = set()

    async def respond(request):
        try:
            async with asyncio.timeout(25):
                result = await bridge.call(request)
            response = {"id": request["id"], "result": result}
        except Exception as error:
            response = {"id": request.get("id", ""), "error": str(error)}
        print(json.dumps(response, ensure_ascii=True), flush=True)

    async def reap():
        while True:
            await asyncio.sleep(60)
            await bridge.reap()

    reaper = asyncio.create_task(reap())
    try:
        while line := await asyncio.to_thread(sys.stdin.readline):
            if len(line) > 65536 or len(pending) >= 32:
                continue
            try:
                request = json.loads(line)
                if not isinstance(request, dict):
                    continue
            except ValueError:
                continue
            task = asyncio.create_task(respond(request))
            pending.add(task)
            task.add_done_callback(pending.discard)
    finally:
        reaper.cancel()
        for task in pending:
            task.cancel()
        await asyncio.gather(*pending, return_exceptions=True)
        await bridge.close()


if __name__ == "__main__":
    asyncio.run(main())
