import { useState, useEffect, useRef, useCallback } from "react";
import { useTranslation } from "react-i18next";
import { useNavigate } from "react-router";
import { Mic, MicOff, ArrowLeft, Bot, Volume2, Trash2 } from "lucide-react";
import { useWs } from "@/hooks/use-ws";
import { Methods } from "@/api/protocol";
import { Button } from "@/components/ui/button";
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from "@/components/ui/select";
import { ScrollArea } from "@/components/ui/scroll-area";
import { ROUTES } from "@/lib/constants";
import { useVoiceChat, type VoiceStatus, type ConversationEntry } from "./hooks/use-voice-chat";

interface Agent {
  id: string;
  name: string;
}

// --- Orb Animation Component ---

function VoiceOrb({ status }: { status: VoiceStatus }) {
  const orbColor = {
    idle: "from-purple-500/30 to-purple-700/20",
    listening: "from-purple-500 to-purple-600",
    thinking: "from-blue-500 to-blue-600",
    speaking: "from-green-500 to-green-600",
  }[status];

  const glowColor = {
    idle: "shadow-purple-500/10",
    listening: "shadow-purple-500/50",
    thinking: "shadow-blue-500/50",
    speaking: "shadow-green-500/50",
  }[status];

  const pulseClass = status !== "idle" ? "animate-voice-pulse" : "";

  return (
    <div className="relative flex items-center justify-center">
      {/* Outer glow rings */}
      {status !== "idle" && (
        <>
          <div
            className={`absolute size-56 rounded-full bg-gradient-to-br ${orbColor} opacity-20 animate-ping`}
            style={{ animationDuration: "2s" }}
          />
          <div
            className={`absolute size-48 rounded-full bg-gradient-to-br ${orbColor} opacity-30 animate-pulse`}
            style={{ animationDuration: "1.5s" }}
          />
        </>
      )}

      {/* Main orb */}
      <div
        className={`relative z-10 size-[200px] rounded-full bg-gradient-to-br ${orbColor} shadow-2xl ${glowColor} ${pulseClass} flex items-center justify-center transition-all duration-500`}
      >
        {/* Inner glow */}
        <div className="absolute inset-4 rounded-full bg-white/5 backdrop-blur-sm" />

        {/* Icon */}
        <div className="relative z-20">
          {status === "idle" && <Bot className="size-16 text-purple-300/60" />}
          {status === "listening" && <Mic className="size-16 text-white animate-pulse" />}
          {status === "thinking" && (
            <div className="flex gap-1.5">
              <div className="size-3 rounded-full bg-white animate-bounce" style={{ animationDelay: "0ms" }} />
              <div className="size-3 rounded-full bg-white animate-bounce" style={{ animationDelay: "150ms" }} />
              <div className="size-3 rounded-full bg-white animate-bounce" style={{ animationDelay: "300ms" }} />
            </div>
          )}
          {status === "speaking" && <Volume2 className="size-16 text-white animate-pulse" />}
        </div>
      </div>

      {/* Waveform bars (visible when listening) */}
      {status === "listening" && (
        <div className="absolute bottom-0 flex items-end gap-1 h-8">
          {Array.from({ length: 12 }).map((_, i) => (
            <div
              key={i}
              className="w-1 bg-purple-400 rounded-full animate-waveform"
              style={{
                animationDelay: `${i * 80}ms`,
                height: "4px",
              }}
            />
          ))}
        </div>
      )}
    </div>
  );
}

// --- Status Text ---

function StatusText({ status, t }: { status: VoiceStatus; t: (key: string) => string }) {
  const text = {
    idle: t("statusReady"),
    listening: t("statusListening"),
    thinking: t("statusThinking"),
    speaking: t("statusSpeaking"),
  }[status];

  const color = {
    idle: "text-purple-300/60",
    listening: "text-purple-300",
    thinking: "text-blue-300",
    speaking: "text-green-300",
  }[status];

  return (
    <p className={`text-lg font-medium tracking-wide ${color} transition-colors duration-300`}>
      {text}
    </p>
  );
}

// --- Conversation Bubble ---

function ConversationBubble({ entry }: { entry: ConversationEntry }) {
  const isUser = entry.role === "user";

  return (
    <div className={`flex ${isUser ? "justify-end" : "justify-start"} mb-3`}>
      <div
        className={`max-w-[80%] rounded-2xl px-4 py-2.5 text-sm leading-relaxed ${
          isUser
            ? "bg-purple-600/40 text-purple-100 rounded-br-sm"
            : "bg-white/10 text-gray-200 rounded-bl-sm"
        }`}
      >
        {entry.content}
      </div>
    </div>
  );
}

// --- Main Page ---

export function VoicePage() {
  const { t } = useTranslation("voice");
  const navigate = useNavigate();
  const ws = useWs();

  const [agents, setAgents] = useState<Agent[]>([]);
  const [selectedAgentId, setSelectedAgentId] = useState("default");
  const scrollEndRef = useRef<HTMLDivElement>(null);

  // Load agents list
  useEffect(() => {
    if (!ws.isConnected) return;

    ws.call<{ agents: Agent[] }>(Methods.AGENTS_LIST)
      .then((res) => {
        const list = res.agents ?? [];
        setAgents(list);
        if (list.length > 0 && !list.find((a) => a.id === selectedAgentId)) {
          setSelectedAgentId(list[0]!.id);
        }
      })
      .catch(() => {
        // ignore
      });
  }, [ws, ws.isConnected]);

  const voiceChat = useVoiceChat({
    agentId: selectedAgentId,
    lang: "pt-BR",
  });

  const {
    status,
    conversation,
    currentTranscript,
    streamingResponse,
    error,
    startListening,
    stopListening,
    cancel,
    clearConversation,
  } = voiceChat;

  // Auto-scroll conversation
  useEffect(() => {
    scrollEndRef.current?.scrollIntoView({ behavior: "smooth" });
  }, [conversation, streamingResponse]);

  // Handle mic button
  const handleMicPress = useCallback(async () => {
    if (status === "listening") {
      await stopListening();
    } else if (status === "idle") {
      await startListening();
    } else if (status === "speaking" || status === "thinking") {
      cancel();
    }
  }, [status, startListening, stopListening, cancel]);

  // Keyboard shortcut: space to toggle
  useEffect(() => {
    const handler = (e: KeyboardEvent) => {
      if (e.code === "Space" && e.target === document.body) {
        e.preventDefault();
        handleMicPress();
      }
    };
    window.addEventListener("keydown", handler);
    return () => window.removeEventListener("keydown", handler);
  }, [handleMicPress]);

  const micButtonLabel = {
    idle: t("tapToSpeak"),
    listening: t("tapToStop"),
    thinking: t("tapToCancel"),
    speaking: t("tapToCancel"),
  }[status];

  return (
    <div className="flex flex-col h-dvh bg-gradient-to-b from-gray-950 via-gray-900 to-gray-950 text-white overflow-hidden">
      {/* --- Custom animation styles --- */}
      <style>{`
        @keyframes voice-pulse {
          0%, 100% { transform: scale(1); }
          50% { transform: scale(1.05); }
        }
        .animate-voice-pulse {
          animation: voice-pulse 2s ease-in-out infinite;
        }
        @keyframes waveform {
          0%, 100% { height: 4px; }
          50% { height: 24px; }
        }
        .animate-waveform {
          animation: waveform 0.8s ease-in-out infinite;
        }
      `}</style>

      {/* --- Top bar --- */}
      <div className="flex items-center justify-between px-4 py-3 safe-top">
        <Button
          variant="ghost"
          size="icon"
          className="text-gray-400 hover:text-white hover:bg-white/10"
          onClick={() => navigate(ROUTES.OVERVIEW)}
        >
          <ArrowLeft className="size-5" />
        </Button>

        <h1 className="text-sm font-medium text-gray-400 tracking-wider uppercase">
          {t("title")}
        </h1>

        <Select value={selectedAgentId} onValueChange={setSelectedAgentId}>
          <SelectTrigger className="w-auto min-w-[120px] bg-white/5 border-white/10 text-gray-300 text-base md:text-sm">
            <SelectValue placeholder={t("selectAgent")} />
          </SelectTrigger>
          <SelectContent>
            {agents.map((agent) => (
              <SelectItem key={agent.id} value={agent.id}>
                <div className="flex items-center gap-2">
                  <Bot className="size-3.5 text-purple-400" />
                  {agent.name}
                </div>
              </SelectItem>
            ))}
            {agents.length === 0 && (
              <SelectItem value="default" disabled>
                {t("noAgents")}
              </SelectItem>
            )}
          </SelectContent>
        </Select>
      </div>

      {/* --- Main content --- */}
      <div className="flex-1 flex flex-col items-center justify-center gap-6 px-4 min-h-0">
        {/* Orb */}
        <VoiceOrb status={status} />

        {/* Status */}
        <StatusText status={status} t={t} />

        {/* Current transcript (while listening/thinking) */}
        {currentTranscript && (status === "listening" || status === "thinking") && (
          <div className="max-w-md text-center">
            <p className="text-sm text-gray-400 italic">
              &ldquo;{currentTranscript}&rdquo;
            </p>
          </div>
        )}

        {/* Streaming response (while thinking) */}
        {streamingResponse && status === "thinking" && (
          <div className="max-w-md text-center">
            <p className="text-sm text-blue-300/80 line-clamp-3">
              {streamingResponse}
            </p>
          </div>
        )}

        {/* Error */}
        {error && (
          <div className="max-w-md text-center">
            <p className="text-sm text-red-400">{error}</p>
          </div>
        )}
      </div>

      {/* --- Conversation history --- */}
      {conversation.length > 0 && (
        <div className="mx-4 mb-2 max-h-[30vh]">
          <div className="flex items-center justify-between mb-2 px-1">
            <span className="text-xs text-gray-500 uppercase tracking-wider">
              {t("conversation")}
            </span>
            <Button
              variant="ghost"
              size="icon-xs"
              className="text-gray-500 hover:text-red-400 hover:bg-white/5"
              onClick={clearConversation}
            >
              <Trash2 className="size-3" />
            </Button>
          </div>
          <ScrollArea className="h-full max-h-[25vh] rounded-xl bg-white/5 p-3">
            {conversation.map((entry, i) => (
              <ConversationBubble key={i} entry={entry} />
            ))}
            {/* Streaming response in conversation area */}
            {streamingResponse && (status === "thinking" || status === "speaking") && (
              <div className="flex justify-start mb-3">
                <div className="max-w-[80%] rounded-2xl rounded-bl-sm px-4 py-2.5 text-sm leading-relaxed bg-white/10 text-gray-200">
                  {streamingResponse}
                  {status === "thinking" && (
                    <span className="inline-block w-1.5 h-4 bg-blue-400 ml-0.5 animate-pulse" />
                  )}
                </div>
              </div>
            )}
            <div ref={scrollEndRef} />
          </ScrollArea>
        </div>
      )}

      {/* --- Bottom controls --- */}
      <div className="flex flex-col items-center gap-3 px-4 pb-6 pt-2 safe-bottom">
        {/* Mic button */}
        <button
          onClick={handleMicPress}
          className={`relative size-16 rounded-full flex items-center justify-center transition-all duration-300 focus:outline-none focus-visible:ring-2 focus-visible:ring-purple-400 ${
            status === "listening"
              ? "bg-red-500 hover:bg-red-600 shadow-lg shadow-red-500/30 scale-110"
              : status === "thinking" || status === "speaking"
                ? "bg-gray-600 hover:bg-gray-500 shadow-lg"
                : "bg-purple-600 hover:bg-purple-500 shadow-lg shadow-purple-500/30"
          }`}
        >
          {status === "listening" ? (
            <MicOff className="size-7 text-white" />
          ) : (
            <Mic className="size-7 text-white" />
          )}

          {/* Recording ring animation */}
          {status === "listening" && (
            <span className="absolute inset-0 rounded-full border-2 border-red-400 animate-ping opacity-50" />
          )}
        </button>

        {/* Hint text */}
        <p className="text-xs text-gray-500">
          {micButtonLabel}
          <span className="hidden sm:inline"> &middot; {t("spaceHint")}</span>
        </p>
      </div>
    </div>
  );
}
