import { useState, useEffect, useCallback } from "react";
import { useTranslation } from "react-i18next";
import { Mic, MicOff, ChevronDown } from "lucide-react";
import { useVoiceChat, type VoiceState } from "./hooks/use-voice-chat";
import { useSpeechRecognition } from "./hooks/use-speech-recognition";
import { useWs } from "@/hooks/use-ws";

interface Agent {
  id: string;
  display_name: string;
  agent_key: string;
}

const ORB_COLORS: Record<VoiceState, string> = {
  idle: "rgba(168, 85, 247, 0.3)",
  listening: "rgba(168, 85, 247, 0.9)",
  thinking: "rgba(59, 130, 246, 0.8)",
  speaking: "rgba(34, 197, 94, 0.8)",
};

const ORB_GLOW: Record<VoiceState, string> = {
  idle: "0 0 30px rgba(168, 85, 247, 0.2)",
  listening: "0 0 60px rgba(168, 85, 247, 0.6), 0 0 120px rgba(168, 85, 247, 0.3)",
  thinking: "0 0 60px rgba(59, 130, 246, 0.5), 0 0 120px rgba(59, 130, 246, 0.2)",
  speaking: "0 0 60px rgba(34, 197, 94, 0.5), 0 0 120px rgba(34, 197, 94, 0.2)",
};

const STATUS_TEXT: Record<string, Record<VoiceState, string>> = {
  "pt-BR": { idle: "Toque para falar", listening: "Ouvindo...", thinking: "Pensando...", speaking: "Falando..." },
  en: { idle: "Tap to speak", listening: "Listening...", thinking: "Thinking...", speaking: "Speaking..." },
  vi: { idle: "Chạm để nói", listening: "Đang nghe...", thinking: "Đang suy nghĩ...", speaking: "Đang nói..." },
  zh: { idle: "点击说话", listening: "聆听中...", thinking: "思考中...", speaking: "说话中..." },
};

export function VoicePage() {
  const { i18n } = useTranslation();
  const ws = useWs();
  const [agents, setAgents] = useState<Agent[]>([]);
  const [selectedAgent, setSelectedAgent] = useState<string | null>(null);
  const [showAgentPicker, setShowAgentPicker] = useState(false);
  const { state, setState: setListening, sendMessage, stopSpeaking } = useVoiceChat(selectedAgent);
  const recognition = useSpeechRecognition("pt-BR");

  // Load agents
  useEffect(() => {
    ws.call<{ agents: Agent[] }>("agents.list", {}).then((res: { agents?: Agent[] } | undefined) => {
      const list = res?.agents ?? [];
      setAgents(list);
      if (list.length > 0 && !selectedAgent) {
        const def = list.find((a: Agent) => a.agent_key === "juh") ?? list[0];
        if (def) setSelectedAgent(def.id);
      }
    }).catch(() => {});
  }, [ws, selectedAgent]);

  // Handle voice result — auto-send to agent
  const handleVoiceResult = useCallback(
    (text: string) => {
      if (text.trim()) {
        sendMessage(text);
      }
    },
    [sendMessage]
  );

  // Toggle mic
  const toggleMic = useCallback(() => {
    if (state === "speaking") {
      stopSpeaking();
      return;
    }
    if (state === "listening") {
      recognition.stop();
      return;
    }
    if (state !== "idle") return;

    setListening();
    recognition.start(handleVoiceResult);
  }, [state, recognition, handleVoiceResult, stopSpeaking, setListening]);

  // Space bar shortcut
  useEffect(() => {
    const handler = (e: KeyboardEvent) => {
      if (e.code === "Space" && !e.target?.toString().includes("Input") && !e.target?.toString().includes("Textarea")) {
        e.preventDefault();
        toggleMic();
      }
    };
    window.addEventListener("keydown", handler);
    return () => window.removeEventListener("keydown", handler);
  }, [toggleMic]);

  // Load voices on mount
  useEffect(() => {
    window.speechSynthesis?.getVoices();
    const handler = () => window.speechSynthesis?.getVoices();
    window.speechSynthesis?.addEventListener("voiceschanged", handler);
    return () => window.speechSynthesis?.removeEventListener("voiceschanged", handler);
  }, []);

  const lang = i18n.language || "en";
  const statusTexts = STATUS_TEXT[lang] ?? STATUS_TEXT["en"];
  const agentName = agents.find((a) => a.id === selectedAgent)?.display_name || "Agente";
  const isActive = state === "listening" || state === "thinking" || state === "speaking";

  return (
    <div className="relative flex h-dvh flex-col items-center justify-center bg-gradient-to-b from-zinc-950 via-zinc-900 to-zinc-950 overflow-hidden select-none">
      {/* Inline animations */}
      <style>{`
        @keyframes orbPulse {
          0%, 100% { transform: scale(1); }
          50% { transform: scale(1.08); }
        }
        @keyframes orbBreath {
          0%, 100% { transform: scale(1); opacity: 0.3; }
          50% { transform: scale(1.03); opacity: 0.5; }
        }
        @keyframes ripple {
          0% { transform: scale(1); opacity: 0.4; }
          100% { transform: scale(2.5); opacity: 0; }
        }
        .orb-active { animation: orbPulse 1.2s ease-in-out infinite; }
        .orb-idle { animation: orbBreath 3s ease-in-out infinite; }
        .ripple-ring {
          animation: ripple 1.5s ease-out infinite;
          border: 2px solid currentColor;
          border-radius: 50%;
          position: absolute;
          inset: 0;
        }
      `}</style>

      {/* Agent picker - top */}
      <div className="absolute top-6 left-1/2 -translate-x-1/2 z-10">
        <button
          onClick={() => setShowAgentPicker(!showAgentPicker)}
          className="flex items-center gap-2 rounded-full bg-white/10 px-4 py-2 text-sm text-white/80 backdrop-blur-sm hover:bg-white/15 transition-colors"
        >
          <span>{agentName}</span>
          <ChevronDown className="h-4 w-4" />
        </button>
        {showAgentPicker && (
          <div className="absolute top-full mt-2 left-1/2 -translate-x-1/2 min-w-48 rounded-xl bg-zinc-800/95 border border-white/10 backdrop-blur-md shadow-xl overflow-hidden">
            {agents.map((agent) => (
              <button
                key={agent.id}
                onClick={() => {
                  setSelectedAgent(agent.id);
                  setShowAgentPicker(false);
                }}
                className={`w-full px-4 py-3 text-left text-sm transition-colors hover:bg-white/10 ${
                  agent.id === selectedAgent ? "text-purple-400 bg-white/5" : "text-white/70"
                }`}
              >
                {agent.display_name || agent.agent_key}
              </button>
            ))}
          </div>
        )}
      </div>

      {/* Orb */}
      <div className="relative flex items-center justify-center">
        {/* Ripple rings when listening */}
        {state === "listening" && (
          <>
            <div className="ripple-ring text-purple-500" style={{ animationDelay: "0s" }} />
            <div className="ripple-ring text-purple-500" style={{ animationDelay: "0.5s" }} />
            <div className="ripple-ring text-purple-500" style={{ animationDelay: "1s" }} />
          </>
        )}

        {/* Main orb */}
        <div
          className={`relative h-48 w-48 rounded-full cursor-pointer transition-all duration-500 ${
            isActive ? "orb-active" : "orb-idle"
          }`}
          style={{
            background: `radial-gradient(circle at 35% 35%, ${ORB_COLORS[state]}, transparent 70%)`,
            boxShadow: ORB_GLOW[state],
          }}
          onClick={toggleMic}
        >
          {/* Inner glow */}
          <div
            className="absolute inset-4 rounded-full transition-all duration-500"
            style={{
              background: `radial-gradient(circle at 40% 40%, ${ORB_COLORS[state].replace("0.", "0.4")}, transparent 60%)`,
            }}
          />
        </div>
      </div>

      {/* Status text */}
      <p className="mt-8 text-lg font-light text-white/70 transition-all duration-300">
        {statusTexts![state]}
      </p>

      {/* Mic button */}
      <button
        onClick={toggleMic}
        disabled={state === "thinking"}
        className={`mt-8 flex h-16 w-16 items-center justify-center rounded-full transition-all duration-300 ${
          state === "listening"
            ? "bg-red-500 hover:bg-red-600 scale-110"
            : state === "thinking"
            ? "bg-blue-500/50 cursor-not-allowed"
            : state === "speaking"
            ? "bg-green-500 hover:bg-green-600"
            : "bg-white/10 hover:bg-white/20"
        }`}
      >
        {state === "listening" ? (
          <MicOff className="h-7 w-7 text-white" />
        ) : (
          <Mic className="h-7 w-7 text-white" />
        )}
      </button>

      {/* Hint */}
      <p className="mt-4 text-xs text-white/30">
        {lang === "pt-BR" ? "Pressione espaço ou toque na orb" : "Press space or tap the orb"}
      </p>

      {/* Browser support warning */}
      {!recognition.supported && (
        <div className="absolute bottom-6 left-1/2 -translate-x-1/2 rounded-lg bg-red-500/20 px-4 py-2 text-sm text-red-300 backdrop-blur-sm">
          {lang === "pt-BR"
            ? "Seu navegador não suporta reconhecimento de voz. Use Chrome ou Edge."
            : "Your browser doesn't support speech recognition. Use Chrome or Edge."}
        </div>
      )}
    </div>
  );
}
