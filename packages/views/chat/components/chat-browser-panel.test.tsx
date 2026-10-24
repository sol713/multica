import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { act, cleanup, fireEvent, render, screen, waitFor } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { I18nProvider } from "@multica/core/i18n/react";
import type { BrowserSnapshot } from "@multica/core/types/browser";
import { api } from "@multica/core/api";
import enChat from "../../locales/en/chat.json";
import { ChatBrowserPanel } from "./chat-browser-panel";

vi.mock("@multica/core/api", () => ({ api: { getChatBrowser: vi.fn(), sendChatBrowserCommand: vi.fn() } }));
let snapshot: BrowserSnapshot;

function mount() {
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  return render(<QueryClientProvider client={client}><I18nProvider locale="en" resources={{ en: { chat: enChat } }}>
    <ChatBrowserPanel wsId="ws" sessionId="chat" onClose={vi.fn()} />
  </I18nProvider></QueryClientProvider>);
}

beforeEach(() => {
  vi.clearAllMocks();
  snapshot = { enabled: true, active: true, control_owner: "user", epoch: 7, width: 1280, height: 800, frame: "jpeg", tabs: [{tab_id:"tab",title:"Local app",url:"http://localhost:3000",active:true}] };
  vi.mocked(api.getChatBrowser).mockImplementation(async () => snapshot);
  vi.mocked(api.sendChatBrowserCommand).mockResolvedValue(undefined);
});
afterEach(() => { cleanup(); vi.useRealTimers(); });

describe("chat shared browser", () => {
  it("observes agent ownership and sends a handoff rather than page input", async () => {
    snapshot.control_owner = "agent";
    mount();
    const viewport = await screen.findByRole("img");
    expect(screen.getByLabelText("Browser address")).toBeDisabled();
    fireEvent.click(viewport);
    expect(api.sendChatBrowserCommand).not.toHaveBeenCalled();
    fireEvent.click(screen.getByRole("button", { name: "Take control" }));
    await waitFor(() => expect(api.sendChatBrowserCommand).toHaveBeenCalledWith("chat", {action:"handoff",params:{target:"user"},epoch:7}));
  });

  it("coalesces a touchpad burst into its actual pixel delta", async () => {
    mount();
    const viewport = await screen.findByRole("img");
    vi.useFakeTimers();
    for (let i=0; i<50; i++) fireEvent.wheel(viewport,{deltaY:2,deltaMode:0});
    expect(api.sendChatBrowserCommand).not.toHaveBeenCalled();
    await act(() => vi.advanceTimersByTimeAsync(110));
    expect(api.sendChatBrowserCommand).toHaveBeenCalledTimes(1);
    expect(api.sendChatBrowserCommand).toHaveBeenCalledWith("chat",{action:"input",params:{kind:"scroll",direction:"down",amount:100},epoch:7});
  });

  it("inserts text by clicking Insert and clears it after success", async () => {
    mount();
    const input = await screen.findByRole("textbox", { name: enChat.browser.text_input });
    fireEvent.change(input, { target: { value: "human-paste" } });
    fireEvent.click(screen.getByRole("button", { name: "Insert" }));
    await waitFor(() => expect(api.sendChatBrowserCommand).toHaveBeenCalledWith("chat", {
      action: "input", params: { kind: "text", text: "human-paste" }, epoch: 7,
    }));
    await waitFor(() => expect(input).toHaveValue(""));
  });

  it("discards pending wheel input when the panel closes", async () => {
    const view = mount();
    const viewport = await screen.findByRole("img");
    vi.useFakeTimers();
    fireEvent.wheel(viewport,{deltaY:100});
    view.unmount();
    await act(() => vi.advanceTimersByTimeAsync(200));
    expect(api.sendChatBrowserCommand).not.toHaveBeenCalled();
  });

  it("shows daemon errors without enabling takeover", async () => {
    vi.mocked(api.getChatBrowser).mockRejectedValue(new Error("Daemon offline"));
    mount();
    expect(await screen.findByRole("alert")).toHaveTextContent("Daemon offline");
    expect(screen.queryByRole("button",{name:"Take control"})).not.toBeInTheDocument();
  });
});
