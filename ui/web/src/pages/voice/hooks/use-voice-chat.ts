import { useState, useCallback, useRef, useEffect } from "react";
import { useWs, useHttp } from "@/hooks/use-ws";
import { useWsEvent } from "@/hooks/use-ws-event";
import { Methods, Events, ChatEventTypes } from "@/api/protocol";
import { useVoiceRecorder } from "./use-voice-recorder";
import { useSpeechRecognition } from "./use-speech-recognition";

export type VoiceStatus = "idle" | "listening" | "thinking" | "speaking";

export interface ConversationEntry {
  role: "user" | "assistant";
  content: string;
  timestamp: number;
}

interface UseVoiceChatOptions {
  agentId: string;
  lang?: string;
}

interface UseVoiceChatReturn {
  status: VoiceStatus;
  conversation: ConversationEntry[];
  currentTranscript: string;
  streamingResponse: string;
  isRecording: boolean;
  error: string | null;
  startListening: () => Promise<void>;
  stopListening: () => Promise<void>;
  cancel: () => void;
  clearConversation: () => void;
}

/**
 * Orchestrates the full voice flow: listen -> transcribe -> send -> receive -> speak.
 * Uses MediaRecorder + /v1/tts/transcribe for STT, with browser SpeechRecognition fallback.
 * Uses /v1/tts/synthesize for TTS, with browser SpeechSynthesis fallback.
 */
export function useVoiceChat({ agentId, lang = "pt-BR" }: UseVoiceChatOptions): UseVoiceChatReturn {
  const ws = useWs();
  const http = useHttp();

  const [status, setStatus] = useState<VoiceStatus>("idle");
  const [conversation, setConversation] = useState<ConversationEntry[]>([]);
  const [currentTranscript, setCurrentTranscript] = useState("");
  const [streamingResponse, setStreamingResponse] = useState("");
  const [error, setError] = useState<string | null>(null);

  const streamRef = useRef("");
  const runIdRef = useRef<string | null>(null);
  const expectingRunRef = useRef(false);
  const agentIdRef = useRef(agentId);
  agentIdRef.current = agentId;
  const cancelledRef = useRef(false);
  const isSpeakingRef = useRef(false);

  const recorder = useVoiceRecorder();
  const speechRecognition = useSpeechRecognition(lang);

  // Session key for voice chat
  const sessionKey = `agent:${agentId}:voice:direct:browser-voice`;

  // Handle agent events for streaming response
  const handleAgentEvent = useCallback((payload: unknown) => {
    const event = payload as {
      type: string;
      runId: string;
      agentId: string;
      payload?: { content?: string };
    };
    if (!event) return;

    if (event.type === "run.started" && event.agentId === agentIdRef.current) {
      if (expectingRunRef.current) {
        runIdRef.current = event.runId;
        expectingRunRef.current = false;
        setStatus("thinking");
        setStreamingResponse("");
        streamRef.current = "";
      }
      return;
    }

    if (!runIdRef.current || event.runId !== runIdRef.current) return;

    if (event.type === "chunk") {
      const content = event.payload?.content ?? "";
      streamRef.current += content;
      setStreamingResponse(streamRef.current);
      // Switch to "thinking" with text (could also be "speaking" but we wait for completion)
      if (status === "thinking") {
        setStatus("thinking");
      }
    }

    if (event.type === "run.completed" || event.type === "run.failed") {
      const finalText = streamRef.current;
      runIdRef.current = null;

      if (finalText && !cancelledRef.current) {
        // Add assistant message to conversation
        setConversation((prev) => [
          ...prev,
          { role: "assistant", content: finalText, timestamp: Date.now() },
        ]);
        // Speak the response
        speakText(finalText);
      } else {
        setStatus("idle");
      }
    }
  }, [status]);

  useWsEvent(Events.AGENT, handleAgentEvent);

  // Also handle chat events (for chunk streaming)
  const handleChatEvent = useCallback((payload: unknown) => {
    const event = payload as {
      type: string;
      sessionKey?: string;
      content?: string;
    };
    if (!event || event.sessionKey !== sessionKey) return;

    if (event.type === ChatEventTypes.CHUNK) {
      const content = event.content ?? "";
      streamRef.current += content;
      setStreamingResponse(streamRef.current);
    }
  }, [sessionKey]);

  useWsEvent(Events.CHAT, handleChatEvent);

  /**
   * Transcribe audio blob using backend /v1/tts/transcribe.
   * Falls back to browser SpeechRecognition transcript if backend fails.
   */
  const transcribeAudio = useCallback(async (blob: Blob, browserTranscript: string): Promise<string> => {
    try {
      const formData = new FormData();
      formData.append("file", blob, "audio.webm");
      formData.append("language", lang);

      const res = await http.upload<{ text: string }>("/v1/tts/transcribe", formData);
      if (res.text && res.text.trim()) {
        return res.text.trim();
      }
    } catch {
      // Backend transcription failed, use browser fallback
    }

    // Fallback: use browser SpeechRecognition transcript
    if (browserTranscript.trim()) {
      return browserTranscript.trim();
    }

    return "";
  }, [http, lang]);

  /**
   * Speak text using backend /v1/tts/synthesize or browser SpeechSynthesis fallback.
   */
  const speakText = useCallback(async (text: string) => {
    if (cancelledRef.current) {
      setStatus("idle");
      return;
    }

    setStatus("speaking");
    isSpeakingRef.current = true;

    try {
      // Try backend TTS first
      const res = await fetch("/v1/tts/synthesize", {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({ text, language: lang }),
      });

      if (res.ok) {
        const audioBlob = await res.blob();
        if (audioBlob.size > 0) {
          const audioUrl = URL.createObjectURL(audioBlob);
          const audio = new Audio(audioUrl);

          await new Promise<void>((resolve) => {
            audio.onended = () => {
              URL.revokeObjectURL(audioUrl);
              resolve();
            };
            audio.onerror = () => {
              URL.revokeObjectURL(audioUrl);
              resolve();
            };
            audio.play().catch(() => resolve());
          });

          isSpeakingRef.current = false;
          if (!cancelledRef.current) setStatus("idle");
          return;
        }
      }
    } catch {
      // Backend TTS failed, use browser fallback
    }

    // Fallback: browser SpeechSynthesis
    if ("speechSynthesis" in window) {
      const utterance = new SpeechSynthesisUtterance(text);
      utterance.lang = lang;
      utterance.rate = 1.0;
      utterance.pitch = 1.0;

      await new Promise<void>((resolve) => {
        utterance.onend = () => resolve();
        utterance.onerror = () => resolve();
        window.speechSynthesis.speak(utterance);
      });
    }

    isSpeakingRef.current = false;
    if (!cancelledRef.current) setStatus("idle");
  }, [lang]);

  /**
   * Send transcribed text to the agent via WebSocket.
   */
  const sendMessage = useCallback(async (message: string) => {
    if (!ws.isConnected || !message.trim()) {
      setStatus("idle");
      return;
    }

    setStatus("thinking");
    setStreamingResponse("");
    streamRef.current = "";
    expectingRunRef.current = true;

    try {
      await ws.call(
        Methods.CHAT_SEND,
        {
          agentId: agentIdRef.current,
          sessionKey,
          message: message.trim(),
          stream: true,
        },
        600_000,
      );
    } catch (err) {
      setError(err instanceof Error ? err.message : "Failed to send message");
      setStatus("idle");
      expectingRunRef.current = false;
    }
  }, [ws, sessionKey]);

  /**
   * Start the voice listening flow.
   * Records audio + uses browser SpeechRecognition simultaneously.
   */
  const startListening = useCallback(async () => {
    setError(null);
    cancelledRef.current = false;
    setCurrentTranscript("");
    setStreamingResponse("");

    // Start both recorder and speech recognition
    await recorder.startRecording();

    if (speechRecognition.supported) {
      speechRecognition.startListening();
    }

    setStatus("listening");
  }, [recorder, speechRecognition]);

  /**
   * Stop listening and process the recorded audio.
   */
  const stopListening = useCallback(async () => {
    // Stop both recorder and speech recognition
    if (speechRecognition.isListening) {
      speechRecognition.stopListening();
    }

    const blob = await recorder.stopRecording();

    // Get browser transcript (combine final + interim)
    const browserTranscript = speechRecognition.transcript || speechRecognition.interimTranscript || "";

    if (!blob && !browserTranscript) {
      setStatus("idle");
      return;
    }

    setStatus("thinking");

    // Transcribe
    const text = blob
      ? await transcribeAudio(blob, browserTranscript)
      : browserTranscript;

    if (!text) {
      setError("Could not recognize speech");
      setStatus("idle");
      return;
    }

    setCurrentTranscript(text);

    // Add user message to conversation
    setConversation((prev) => [
      ...prev,
      { role: "user", content: text, timestamp: Date.now() },
    ]);

    // Send to agent
    await sendMessage(text);
  }, [recorder, speechRecognition, transcribeAudio, sendMessage]);

  /**
   * Cancel any ongoing operation.
   */
  const cancel = useCallback(() => {
    cancelledRef.current = true;

    if (recorder.isRecording) {
      recorder.stopRecording();
    }
    if (speechRecognition.isListening) {
      speechRecognition.stopListening();
    }
    if (isSpeakingRef.current) {
      window.speechSynthesis?.cancel();
    }

    runIdRef.current = null;
    expectingRunRef.current = false;
    setStatus("idle");
    setStreamingResponse("");
    streamRef.current = "";
  }, [recorder, speechRecognition]);

  const clearConversation = useCallback(() => {
    setConversation([]);
    setCurrentTranscript("");
    setStreamingResponse("");
    setError(null);
  }, []);

  // Propagate recorder/speech errors
  useEffect(() => {
    if (recorder.error) setError(recorder.error);
  }, [recorder.error]);

  useEffect(() => {
    if (speechRecognition.error) setError(speechRecognition.error);
  }, [speechRecognition.error]);

  // Update interim transcript while listening
  useEffect(() => {
    if (status === "listening") {
      const interim = speechRecognition.transcript || speechRecognition.interimTranscript;
      if (interim) setCurrentTranscript(interim);
    }
  }, [status, speechRecognition.transcript, speechRecognition.interimTranscript]);

  return {
    status,
    conversation,
    currentTranscript,
    streamingResponse,
    isRecording: recorder.isRecording,
    error,
    startListening,
    stopListening,
    cancel,
    clearConversation,
  };
}
