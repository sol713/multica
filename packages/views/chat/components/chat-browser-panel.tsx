"use client";

import { useEffect, useRef, useState, type KeyboardEvent } from "react";
import { ArrowLeft, ArrowRight, Globe, RefreshCw, X, Plus, Hand, Play } from "lucide-react";
import { useChatBrowser } from "@multica/core/chat/browser";
import type { BrowserCommand } from "@multica/core/types/browser";
import { Button } from "@multica/ui/components/ui/button";
import { Input } from "@multica/ui/components/ui/input";
import { useT } from "../../i18n";

export function ChatBrowserPanel({ wsId, sessionId, onClose }: { wsId: string; sessionId: string; onClose: () => void }) {
  const { t } = useT("chat");
  const { snapshot, command } = useChatBrowser(wsId, sessionId);
  const state = snapshot.data;
  const [address, setAddress] = useState("");
  const [input, setInput] = useState("");
  const [localError, setLocalError] = useState<string | null>(null);
  const queue = useRef<Promise<unknown>>(Promise.resolve());
  const queueSize = useRef(0);
  const activeTab = state?.tabs.find((tab) => tab.active);
  const canControl = !!state?.active && state.control_owner === "user" && !snapshot.isError;
  useEffect(() => { setAddress(activeTab?.url ?? ""); }, [activeTab?.url]);

  async function send(action: BrowserCommand["action"], params?: Record<string, unknown>) {
    if (queueSize.current >= 32) { setLocalError(t(($) => $.browser.busy)); return false; }
    queueSize.current += 1;
    const epoch = state?.epoch;
    const next = queue.current.then(async () => {
      setLocalError(null);
      try {
        await command.mutateAsync({ action, params, epoch });
        return true;
      } catch (error) {
        setLocalError(error instanceof Error ? error.message : t(($) => $.browser.failed));
        return false;
      } finally { queueSize.current -= 1; }
    });
    queue.current = next;
    return next;
  }

  function press(event: KeyboardEvent) {
    if (!canControl || event.key === "Tab" || event.nativeEvent.isComposing) return;
    if (["Shift", "Control", "Alt", "Meta"].includes(event.key)) return;
    event.preventDefault();
    if (event.key.length === 1 && !event.ctrlKey && !event.metaKey && !event.altKey) {
      void send("input", { kind: "text", text: event.key });
    } else {
      const modifiers = [event.ctrlKey ? "Control" : "", event.metaKey ? "Meta" : "", event.altKey ? "Alt" : "", event.shiftKey ? "Shift" : ""].filter(Boolean);
      void send("input", { kind: "key", key: [...modifiers, event.key].join("+") });
    }
  }

  return (
    <section aria-label={t(($) => $.browser.title)} className="flex h-full min-h-0 flex-col bg-background">
      <div className="flex h-12 shrink-0 items-center gap-2 border-b px-3">
        <Globe className="h-4 w-4 text-muted-foreground" />
        <span className="text-title-sm">{t(($) => $.browser.title)}</span>
        {state?.active && <span className="min-w-0 flex-1 truncate text-caption text-muted-foreground">{state.control_owner === "user" ? t(($) => $.browser.user_control) : t(($) => $.browser.agent_control)}</span>}
        {state?.active && !snapshot.isError && (
          <Button variant="outline" size="xs" disabled={command.isPending} onClick={() => void send("handoff", { target: state.control_owner === "user" ? "agent" : "user" })}>
            {state.control_owner === "user" ? <Play /> : <Hand />}
            {state.control_owner === "user" ? t(($) => $.browser.return_control) : t(($) => $.browser.take_control)}
          </Button>
        )}
        <Button variant="ghost" size="icon-sm" className="ml-auto" aria-label={t(($) => $.browser.hide)} onClick={onClose}><X /></Button>
      </div>
      {state?.active && (
        <>
          <div className="flex shrink-0 items-center gap-1 overflow-x-auto border-b px-2 py-1">
            {state.tabs.map((tab) => (
              <div key={tab.tab_id} className={`flex min-w-0 max-w-48 shrink-0 items-center rounded-md ${tab.active ? "bg-muted" : ""}`}>
                <Button variant="ghost" size="xs" disabled={!canControl} aria-pressed={tab.active} className="min-w-0 max-w-40" onClick={() => void send("switch_tab", { tab_id: tab.tab_id })}>
                  <span className="truncate">{tab.title || tab.url || t(($) => $.browser.new_tab)}</span>
                </Button>
                <Button variant="ghost" size="icon-xs" disabled={!canControl} aria-label={t(($) => $.browser.close_tab)} onClick={() => void send("close_tab", { tab_id: tab.tab_id })}><X /></Button>
              </div>
            ))}
            <Button variant="ghost" size="icon-xs" disabled={!canControl} aria-label={t(($) => $.browser.new_tab)} onClick={() => void send("new_tab", { url: "about:blank" })}><Plus /></Button>
          </div>
          <form className="flex shrink-0 items-center gap-1 border-b p-2" onSubmit={(event) => {
            event.preventDefault();
            const url = address.trim();
            if (canControl && url) void send("navigate", { url: /^(https?:\/\/|about:blank$)/i.test(url) ? url : `https://${url}` });
          }}>
            <Button type="button" variant="ghost" size="icon-xs" disabled={!canControl} aria-label={t(($) => $.browser.back)} onClick={() => void send("go_back")}><ArrowLeft /></Button>
            <Button type="button" variant="ghost" size="icon-xs" disabled={!canControl} aria-label={t(($) => $.browser.forward)} onClick={() => void send("go_forward")}><ArrowRight /></Button>
            <Button type="button" variant="ghost" size="icon-xs" disabled={!canControl} aria-label={t(($) => $.browser.reload)} onClick={() => void send("reload")}><RefreshCw /></Button>
            <Input aria-label={t(($) => $.browser.address)} value={address} onChange={(event) => setAddress(event.target.value)} disabled={!canControl} className="font-mono" />
          </form>
        </>
      )}
      {(localError || snapshot.error) && <div role="alert" className="border-b p-3 text-caption text-destructive">
        {localError || snapshot.error?.message}
        <Button variant="ghost" size="xs" onClick={() => { setLocalError(null); void snapshot.refetch(); }}>{t(($) => $.browser.retry)}</Button>
      </div>}
      <div className="flex min-h-0 flex-1 items-center justify-center overflow-auto bg-muted/30 p-2">
        {snapshot.isPending ? <p role="status" className="text-caption text-muted-foreground">{t(($) => $.browser.connecting)}</p>
          : state?.active ? (
            <div className="w-full self-start">
              {state.frame ? (
                // This is a live remote viewport, not a local navigation link.
                // eslint-disable-next-line @next/next/no-img-element
                <img src={`data:image/jpeg;base64,${state.frame}`} alt={t(($) => $.browser.viewport)} draggable={false} tabIndex={canControl ? 0 : -1}
                  className={`block h-auto w-full outline-none focus-visible:ring-2 focus-visible:ring-ring ${canControl ? "cursor-crosshair" : ""}`}
                  onClick={(event) => {
                    if (!canControl) return;
                    event.currentTarget.focus();
                    const rect = event.currentTarget.getBoundingClientRect();
                    void send("input", { kind: "click", x: (event.clientX - rect.left) * state.width / rect.width, y: (event.clientY - rect.top) * state.height / rect.height });
                  }}
                  onKeyDown={press}
                  onWheel={(event) => { if (canControl) void send("input", { kind: "scroll", direction: event.deltaY < 0 ? "up" : "down" }); }} />
              ) : <p role="status" className="p-4 text-caption text-muted-foreground">{t(($) => $.browser.connecting)}</p>}
            </div>
          ) : !snapshot.isError && (
            <div className="flex max-w-72 flex-col items-center gap-3 text-center">
              <Globe className="h-8 w-8 text-muted-foreground" />
              <p className="text-body text-muted-foreground">{state?.enabled ? t(($) => $.browser.empty) : t(($) => $.browser.setup)}</p>
              {state?.enabled && <Button variant="outline" size="sm" disabled={command.isPending} onClick={() => void send("start")}>{t(($) => $.browser.start)}</Button>}
            </div>
          )}
      </div>
      {state?.active && (
        <div className="shrink-0 border-t p-2">
          {state.control_owner === "user" ? (
            <form className="flex items-center gap-2" onSubmit={(event) => { event.preventDefault(); if (canControl && input) { void send("input", { kind: "text", text: input }).then((ok) => { if (ok) setInput(""); }); } }}>
              <Input aria-label={t(($) => $.browser.text_input)} placeholder={t(($) => $.browser.text_input)} value={input} onChange={(event) => setInput(event.target.value)} disabled={!canControl} maxLength={10000} />
              <Button variant="outline" size="sm" disabled={!canControl || !input}>{t(($) => $.browser.type_text)}</Button>
              <Button type="button" variant="ghost" size="xs" disabled={!canControl} onClick={() => void send("input", { kind:"key", key:"Tab" })}>Tab</Button>
            </form>
          ) : <p className="text-caption text-muted-foreground">{t(($) => $.browser.observe)}</p>}
          <div className="mt-1 flex justify-end"><Button variant="ghost" size="xs" disabled={command.isPending} onClick={() => void send("close")}>{t(($) => $.browser.stop)}</Button></div>
        </div>
      )}
    </section>
  );
}
