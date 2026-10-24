// @vitest-environment node
import { afterEach, describe, expect, it, vi } from "vitest";
import { ApiClient } from "./client";

afterEach(() => vi.unstubAllGlobals());

describe("chat browser API compatibility", () => {
  it.each([null, { active: "yes" }, { tabs: [{}] }])("fails closed for malformed snapshots %j", async (value) => {
    vi.stubGlobal("fetch", vi.fn().mockResolvedValue(new Response(JSON.stringify(value))));
    const result = await new ApiClient("https://api.test").getChatBrowser("chat-1");
    expect(result.enabled).toBe(false);
    expect(result.active).toBe(false);
    expect(result.control_owner).toBe("agent");
  });

  it("defaults new fields and preserves unknown ownership without granting control", async () => {
    vi.stubGlobal("fetch", vi.fn().mockResolvedValue(new Response(JSON.stringify({ enabled: true, control_owner: "future-owner" }))));
    const result = await new ApiClient("https://api.test").getChatBrowser("chat-1");
    expect(result.control_owner).toBe("future-owner");
    expect(result.tabs).toEqual([]);
    expect(result.frame).toBe("");
  });

  it("sends only the command and its control epoch", async () => {
    const fetchMock = vi.fn().mockResolvedValue(new Response("{}"));
    vi.stubGlobal("fetch", fetchMock);
    const command = { action: "handoff" as const, params: { target: "user" }, epoch: 3 };
    await new ApiClient("https://api.test").sendChatBrowserCommand("chat-1", command);
    expect(fetchMock).toHaveBeenCalledWith(
      expect.stringContaining("/api/chat/sessions/chat-1/browser"),
      expect.objectContaining({ body: JSON.stringify(command) }),
    );
  });
});
