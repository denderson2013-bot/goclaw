import { useCallback, useRef, useState } from "react";
import { useWs } from "@/hooks/use-ws";
import { useWsEvent } from "@/hooks/use-ws-event";

export type VoiceState = "idle" | "listening" | "thinking" | "speaking";

/**
 * Clean text for natural human speech:
 * - Remove emojis (so it doesn't say "emoji roxo")
 * - Remove markdown formatting
 * - Remove URLs
 * - Remove code blocks
 * - Clean up excessive whitespace
 */
function cleanForSpeech(text: string): string {
  let s = text;
  // Remove all emoji unicode ranges
  s = s.replace(/[\u{1F600}-\u{1F64F}\u{1F300}-\u{1F5FF}\u{1F680}-\u{1F6FF}\u{1F1E0}-\u{1F1FF}\u{2600}-\u{26FF}\u{2700}-\u{27BF}\u{FE00}-\u{FE0F}\u{1F900}-\u{1F9FF}\u{200D}\u{20E3}\u{E0020}-\u{E007F}\u{2764}\u{FE0F}\u{1FA70}-\u{1FAFF}\u{2B50}\u{23F0}-\u{23FF}\u{2934}-\u{2935}\u{25AA}-\u{25AB}\u{25B6}\u{25C0}\u{25FB}-\u{25FE}\u{2614}-\u{2615}\u{2648}-\u{2653}\u{267F}\u{2693}\u{26A1}\u{26AA}-\u{26AB}\u{26BD}-\u{26BE}\u{26C4}-\u{26C5}\u{26CE}\u{26D4}\u{26EA}\u{26F2}-\u{26F3}\u{26F5}\u{26FA}\u{26FD}\u{2702}\u{2705}\u{2708}-\u{270D}\u{270F}\u{2712}\u{2714}\u{2716}\u{271D}\u{2721}\u{2728}\u{2733}-\u{2734}\u{2744}\u{2747}\u{274C}\u{274E}\u{2753}-\u{2755}\u{2757}\u{2763}-\u{2764}\u{2795}-\u{2797}\u{27A1}\u{27B0}\u{27BF}\u{2934}-\u{2935}\u{2B05}-\u{2B07}\u{2B1B}-\u{2B1C}\u{2B55}\u{3030}\u{303D}\u{3297}\u{3299}]/gu, "");
  // Remove markdown bold/italic
  s = s.replace(/\*\*(.+?)\*\*/g, "$1");
  s = s.replace(/\*(.+?)\*/g, "$1");
  s = s.replace(/__(.+?)__/g, "$1");
  s = s.replace(/_(.+?)_/g, "$1");
  // Remove code blocks and inline code
  s = s.replace(/```[\s\S]*?```/g, "");
  s = s.replace(/`(.+?)`/g, "$1");
  // Remove markdown headers
  s = s.replace(/^#{1,6}\s+/gm, "");
  // Remove markdown links, keep text
  s = s.replace(/\[(.+?)\]\(.+?\)/g, "$1");
  // Remove markdown lists markers
  s = s.replace(/^[\s]*[-*+]\s+/gm, "");
  s = s.replace(/^[\s]*\d+\.\s+/gm, "");
  // Remove URLs
  s = s.replace(/https?:\/\/[^\s]+/g, "");
  // Remove triple+ newlines
  s = s.replace(/\n{2,}/g, ". ");
  // Collapse whitespace
  s = s.replace(/\s{2,}/g, " ");
  return s.trim();
}

function speakText(text: string, onEnd: () => void) {
  if (!window.speechSynthesis || !text) {
    onEnd();
    return;
  }

  window.speechSynthesis.cancel();
  const cleaned = cleanForSpeech(text);
  if (!cleaned) {
    onEnd();
    return;
  }

  const utterance = new SpeechSynthesisUtterance(cleaned);
  utterance.lang = "pt-BR";
  utterance.rate = 1.05;
  utterance.pitch = 1.0;
  utterance.volume = 1.0;

  // Find best pt-BR voice
  const voices = window.speechSynthesis.getVoices();
  const best =
    voices.find((v) => v.lang === "pt-BR" && v.name.includes("Google")) ||
    voices.find((v) => v.lang === "pt-BR" && v.name.includes("Francisca")) ||
    voices.find((v) => v.lang === "pt-BR" && v.name.includes("Thalita")) ||
    voices.find((v) => v.lang === "pt-BR" && v.name.includes("Luciana")) ||
    voices.find((v) => v.lang === "pt-BR") ||
    voices.find((v) => v.lang.startsWith("pt"));
  if (best) utterance.voice = best;

  utterance.onend = onEnd;
  utterance.onerror = onEnd;
  window.speechSynthesis.speak(utterance);
}

export function useVoiceChat(agentId: string | null) {
  const ws = useWs();
  const [state, setState] = useState<VoiceState>("idle");
  const streamRef = useRef("");
  const sessionKey = agentId ? `agent:${agentId}:voice:direct:browser-voice` : "";

  // Listen for agent streaming events
  useWsEvent(
    "agent",
    useCallback(
      (payload: unknown) => {
        const event = payload as {
          type: string;
          agentId?: string;
          payload?: { content?: string };
        };
        if (event.agentId !== agentId) return;

        if (event.type === "chunk") {
          streamRef.current += event.payload?.content ?? "";
        }
        if (event.type === "run.completed") {
          const response = streamRef.current;
          streamRef.current = "";
          if (response) {
            setState("speaking");
            speakText(response, () => setState("idle"));
          } else {
            setState("idle");
          }
        }
        if (event.type === "run.failed") {
          streamRef.current = "";
          setState("idle");
        }
      },
      [agentId]
    )
  );

  const sendMessage = useCallback(
    async (text: string) => {
      if (!agentId || !text.trim()) return;
      setState("thinking");
      streamRef.current = "";
      try {
        await ws.call(
          "chat.send",
          { agentId, sessionKey, message: text, stream: true },
          600_000
        );
      } catch {
        setState("idle");
      }
    },
    [agentId, sessionKey, ws]
  );

  const stopSpeaking = useCallback(() => {
    window.speechSynthesis?.cancel();
    setState("idle");
  }, []);

  const setListening = useCallback(() => setState("listening"), []);

  return { state, setState: setListening, sendMessage, stopSpeaking };
}
