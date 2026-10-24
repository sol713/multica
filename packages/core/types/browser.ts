export interface BrowserSnapshot {
  enabled: boolean;
  active: boolean;
  control_owner: string;
  epoch: number;
  width: number;
  height: number;
  frame: string;
  tabs: { tab_id: string; title: string; url: string; active: boolean }[];
}

export interface BrowserCommand {
  action: "start" | "close" | "handoff" | "navigate" | "go_back" | "go_forward" | "reload" | "new_tab" | "switch_tab" | "close_tab" | "input";
  params?: Record<string, unknown>;
  epoch?: number;
}
