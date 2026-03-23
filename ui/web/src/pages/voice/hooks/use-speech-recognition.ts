import { useState, useRef, useCallback } from "react";

/* eslint-disable @typescript-eslint/no-explicit-any */
type SpeechRecognitionInstance = {
  continuous: boolean;
  interimResults: boolean;
  lang: string;
  maxAlternatives: number;
  onresult: ((e: any) => void) | null;
  onerror: ((e: any) => void) | null;
  onend: (() => void) | null;
  onspeechend: (() => void) | null;
  start: () => void;
  stop: () => void;
  abort: () => void;
};

export function useSpeechRecognition(lang = "pt-BR") {
  const [isListening, setIsListening] = useState(false);
  const [transcript, setTranscript] = useState("");
  const [interimText, setInterimText] = useState("");
  const recognitionRef = useRef<SpeechRecognitionInstance | null>(null);
  const onDoneRef = useRef<((text: string) => void) | null>(null);
  const fullTextRef = useRef("");

  const supported =
    typeof window !== "undefined" &&
    !!((window as any).SpeechRecognition || (window as any).webkitSpeechRecognition);

  const start = useCallback(
    (onDone: (text: string) => void) => {
      if (!supported) return;

      // Clean up previous
      recognitionRef.current?.abort();

      const SR = (window as any).SpeechRecognition || (window as any).webkitSpeechRecognition;
      if (!SR) return;

      const recognition = new SR() as SpeechRecognitionInstance;
      recognition.continuous = true;        // Keep listening until manually stopped
      recognition.interimResults = true;     // Show what's being said in real-time
      recognition.lang = lang;
      recognition.maxAlternatives = 1;

      onDoneRef.current = onDone;
      fullTextRef.current = "";
      setTranscript("");
      setInterimText("");

      recognition.onresult = (e: any) => {
        let finalText = "";
        let interim = "";

        for (let i = 0; i < e.results.length; i++) {
          const r = e.results[i];
          if (r.isFinal) {
            finalText += r[0].transcript;
          } else {
            interim += r[0].transcript;
          }
        }

        fullTextRef.current = finalText;
        setTranscript(finalText);
        setInterimText(interim);
      };

      recognition.onerror = (e: any) => {
        // "no-speech" is normal — user clicked but didn't say anything
        if (e.error !== "no-speech" && e.error !== "aborted") {
          console.warn("Speech recognition error:", e.error);
        }
        setIsListening(false);
      };

      recognition.onend = () => {
        setIsListening(false);
        setInterimText("");
        // Send accumulated text when recognition ends
        const text = fullTextRef.current.trim();
        if (text && onDoneRef.current) {
          onDoneRef.current(text);
        }
      };

      recognitionRef.current = recognition;
      recognition.start();
      setIsListening(true);
    },
    [supported, lang]
  );

  const stop = useCallback(() => {
    // stop() triggers onend which sends the text
    recognitionRef.current?.stop();
  }, []);

  return { isListening, transcript, interimText, start, stop, supported };
}
