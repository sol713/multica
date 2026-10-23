import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { api } from "../api";
import type { BrowserCommand } from "../types/browser";

const browserKey = (wsId: string, sessionId: string) => ["chat-browser", wsId, sessionId] as const;

export function useChatBrowser(wsId: string, sessionId: string) {
  const queryClient = useQueryClient();
  const queryKey = browserKey(wsId, sessionId);
  const snapshot = useQuery({
    queryKey,
    queryFn: ({ signal }) => api.getChatBrowser(sessionId, signal),
    refetchInterval: (query) => query.state.error || query.state.data?.enabled === false ? false : 500,
    retry: false,
    gcTime: 0,
    staleTime: 0,
  });
  const command = useMutation({
    mutationFn: (command: BrowserCommand) => api.sendChatBrowserCommand(sessionId, command),
    onSettled: () => queryClient.invalidateQueries({ queryKey }),
  });
  return { snapshot, command };
}
