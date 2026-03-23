import { useState, useEffect, useCallback } from "react";
import { useTranslation } from "react-i18next";
import { Mic, Square, ChevronDown, Volume2 } from "lucide-react";
import { useVoiceChat, type VoiceState } from "./hooks/use-voice-chat";
import { useSpeechRecognition } from "./hooks/use-speech-recognition";
import { useWs } from "@/hooks/use-ws";

/* eslint-disable @typescript-eslint/no-explicit-any */

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

const STATUS: Record<string, Record<VoiceState, string>> = {
  "pt-BR": { idle: "Toque para falar", listening: "Ouvindo... toque para enviar", thinking: "Pensando...", speaking: "Falando..." },
  en: { idle: "Tap to speak", listening: "Listening... tap to send", thinking: "Thinking...", speaking: "Speaking..." },
  vi: { idle: "Chạm để nói", listening: "Đang nghe... chạm để gửi", thinking: "Đang suy nghĩ...", speaking: "Đang nói..." },
  zh: { idle: "点击说话", listening: "聆听中... 点击发送", thinking: "思考中...", speaking: "说话中..." },
};

export function VoicePage() {
  const { i18n } = useTranslation();
  const ws = useWs();
  const [agents, setAgents] = useState<Agent[]>([]);
  const [selectedAgent, setSelectedAgent] = useState<string | null>(null);
  const [showPicker, setShowPicker] = useState(false);
  const { state, setState: setListening, sendMessage, stopSpeaking } = useVoiceChat(selectedAgent);
  const recognition = useSpeechRecognition("pt-BR");

  // Load agents on mount
  useEffect(() => {
    ws.call<any>("agents.list", {})
      .then((res: any) => {
        const list: Agent[] = res?.agents ?? [];
        setAgents(list);
        if (list.length > 0 && !selectedAgent) {
          const juh = list.find((a: Agent) => a.agent_key === "juh");
          const first = list[0];
          setSelectedAgent(juh ? juh.id : first ? first.id : null);
        }
      })
      .catch(() => {});
  // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [ws]);

  // When recognition sends final text → send to agent
  const handleDone = useCallback(
    (text: string) => {
      if (text.trim()) sendMessage(text);
    },
    [sendMessage]
  );

  // Main toggle: idle→listening, listening→stop+send, speaking→stop
  const toggle = useCallback(() => {
    if (state === "speaking") {
      stopSpeaking();
      return;
    }
    if (state === "listening") {
      recognition.stop(); // triggers onend → handleDone
      return;
    }
    if (state !== "idle") return;

    setListening();
    recognition.start(handleDone);
  }, [state, recognition, handleDone, stopSpeaking, setListening]);

  // Space bar
  useEffect(() => {
    const handler = (e: KeyboardEvent) => {
      const tag = (e.target as HTMLElement)?.tagName;
      if (tag === "INPUT" || tag === "TEXTAREA" || tag === "SELECT") return;
      if (e.code === "Space") {
        e.preventDefault();
        toggle();
      }
    };
    window.addEventListener("keydown", handler);
    return () => window.removeEventListener("keydown", handler);
  }, [toggle]);

  // Load browser voices
  useEffect(() => {
    window.speechSynthesis?.getVoices();
    window.speechSynthesis?.addEventListener("voiceschanged", () => window.speechSynthesis.getVoices());
  }, []);

  const lang = i18n.language || "en";
  const statusTexts = STATUS[lang] ?? STATUS["en"];
  const selectedAgentData = agents.find((a) => a.id === selectedAgent);
  const agentName = selectedAgentData?.display_name || selectedAgentData?.agent_key || "...";

  return (
    <div className="relative flex h-dvh flex-col items-center justify-center bg-gradient-to-b from-zinc-950 via-zinc-900 to-zinc-950 overflow-hidden select-none">
      <style>{`
        @keyframes orbPulse { 0%,100%{transform:scale(1)} 50%{transform:scale(1.08)} }
        @keyframes orbBreath { 0%,100%{transform:scale(1);opacity:.3} 50%{transform:scale(1.03);opacity:.5} }
        @keyframes ripple { 0%{transform:scale(1);opacity:.4} 100%{transform:scale(2.5);opacity:0} }
        .orb-active{animation:orbPulse 1.2s ease-in-out infinite}
        .orb-idle{animation:orbBreath 3s ease-in-out infinite}
        .ripple-ring{animation:ripple 1.5s ease-out infinite;border:2px solid currentColor;border-radius:50%;position:absolute;inset:0}
      `}</style>

      {/* Agent picker */}
      <div className="absolute top-6 left-1/2 -translate-x-1/2 z-20">
        <button
          onClick={() => setShowPicker(!showPicker)}
          className="flex items-center gap-2 rounded-full bg-white/10 px-5 py-2.5 text-sm font-medium text-white/90 backdrop-blur-sm hover:bg-white/20 transition-colors"
        >
          <span>{agentName}</span>
          <ChevronDown className="h-4 w-4" />
        </button>
        {showPicker && (
          <>
            {/* Overlay to close */}
            <div className="fixed inset-0 z-10" onClick={() => setShowPicker(false)} />
            <div className="absolute top-full mt-2 left-1/2 -translate-x-1/2 min-w-56 rounded-xl bg-zinc-800/95 border border-white/10 backdrop-blur-md shadow-2xl overflow-hidden z-20">
              <div className="px-4 py-2 text-xs text-white/40 uppercase tracking-wider border-b border-white/5">
                {lang === "pt-BR" ? "Selecionar agente" : "Select agent"}
              </div>
              {agents.length === 0 && (
                <div className="px-4 py-3 text-sm text-white/40">
                  {lang === "pt-BR" ? "Nenhum agente encontrado" : "No agents found"}
                </div>
              )}
              {agents.map((agent) => (
                <button
                  key={agent.id}
                  onClick={() => {
                    setSelectedAgent(agent.id);
                    setShowPicker(false);
                  }}
                  className={`w-full px-4 py-3 text-left text-sm transition-colors hover:bg-white/10 flex items-center gap-2 ${
                    agent.id === selectedAgent ? "text-purple-400 bg-white/5" : "text-white/70"
                  }`}
                >
                  <div className={`h-2 w-2 rounded-full ${agent.id === selectedAgent ? "bg-purple-400" : "bg-white/20"}`} />
                  {agent.display_name || agent.agent_key}
                </button>
              ))}
            </div>
          </>
        )}
      </div>

      {/* Orb */}
      <div className="relative flex items-center justify-center" style={{ width: 220, height: 220 }}>
        {state === "listening" && (
          <>
            <div className="ripple-ring text-purple-500" style={{ animationDelay: "0s" }} />
            <div className="ripple-ring text-purple-500" style={{ animationDelay: "0.5s" }} />
            <div className="ripple-ring text-purple-500" style={{ animationDelay: "1s" }} />
          </>
        )}
        <div
          className={`relative h-48 w-48 rounded-full cursor-pointer transition-all duration-500 ${
            state !== "idle" ? "orb-active" : "orb-idle"
          }`}
          style={{
            background: `radial-gradient(circle at 35% 35%, ${ORB_COLORS[state]}, transparent 70%)`,
            boxShadow: ORB_GLOW[state],
          }}
          onClick={toggle}
        >
          <div
            className="absolute inset-4 rounded-full transition-all duration-500"
            style={{
              background: `radial-gradient(circle at 40% 40%, ${ORB_COLORS[state].replace(/0\.\d+\)/, "0.5)")}, transparent 60%)`,
            }}
          />
        </div>
      </div>

      {/* Live transcript (what user is saying) */}
      {state === "listening" && (recognition.transcript || recognition.interimText) && (
        <div className="mt-4 max-w-md px-6 text-center">
          <p className="text-sm text-white/50">
            {recognition.transcript}
            <span className="text-white/30">{recognition.interimText}</span>
          </p>
        </div>
      )}

      {/* Status */}
      <p className="mt-6 text-lg font-light text-white/70">
        {statusTexts![state]}
      </p>

      {/* Action button */}
      <button
        onClick={toggle}
        disabled={state === "thinking"}
        className={`mt-6 flex h-16 w-16 items-center justify-center rounded-full transition-all duration-300 ${
          state === "listening"
            ? "bg-red-500 hover:bg-red-600 scale-110"
            : state === "thinking"
            ? "bg-blue-500/50 cursor-not-allowed animate-pulse"
            : state === "speaking"
            ? "bg-green-500 hover:bg-green-600"
            : "bg-white/10 hover:bg-white/20 hover:scale-105"
        }`}
      >
        {state === "listening" ? (
          <Square className="h-6 w-6 text-white" />
        ) : state === "speaking" ? (
          <Volume2 className="h-6 w-6 text-white" />
        ) : (
          <Mic className="h-7 w-7 text-white" />
        )}
      </button>

      {/* Hint */}
      <p className="mt-3 text-xs text-white/25">
        {lang === "pt-BR"
          ? "Espaço para falar \u2022 Fale tudo que quiser \u2022 Toque para enviar"
          : "Space to talk \u2022 Say everything \u2022 Tap to send"}
      </p>

      {/* No support warning */}
      {!recognition.supported && (
        <div className="absolute bottom-6 left-1/2 -translate-x-1/2 rounded-lg bg-red-500/20 px-4 py-2 text-sm text-red-300 backdrop-blur-sm">
          {lang === "pt-BR"
            ? "Seu navegador nao suporta reconhecimento de voz. Use Chrome ou Edge."
            : "Your browser doesn't support speech recognition. Use Chrome or Edge."}
        </div>
      )}
    </div>
  );
}
