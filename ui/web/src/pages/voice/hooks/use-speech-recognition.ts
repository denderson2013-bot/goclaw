import { useState, useRef, useCallback } from "react";

/* eslint-disable @typescript-eslint/no-explicit-any */
type SpeechRecognitionInstance = {
  continuous: boolean;
  interimResults: boolean;
  lang: string;
  onresult: ((e: any) => void) | null;
  onerror: ((e: any) => void) | null;
  onend: (() => void) | null;
  start: () => void;
  stop: () => void;
  abort: () => void;
};

// SpeechRecognition accessed via window["SpeechRecognition"] to avoid type conflicts

export function useSpeechRecognition(lang = "pt-BR") {
  const [isListening, setIsListening] = useState(false);
  const recognitionRef = useRef<SpeechRecognitionInstance | null>(null);
  const onResultRef = useRef<((text: string) => void) | null>(null);

  const supported =
    typeof window !== "undefined" &&
    !!((window as any).SpeechRecognition || (window as any).webkitSpeechRecognition);

  const start = useCallback(
    (onResult: (text: string) => void) => {
      if (!supported) return;

      const SR = (window as any).SpeechRecognition || (window as any).webkitSpeechRecognition;
      if (!SR) return;

      const recognition = new SR();
      recognition.continuous = false;
      recognition.interimResults = false;
      recognition.lang = lang;

      onResultRef.current = onResult;

      recognition.onresult = (e: any) => {
        const idx = e.results.length - 1;
        const result = e.results[idx];
        if (result?.[0]) {
          const text = result[0].transcript?.trim();
          if (text && onResultRef.current) {
            onResultRef.current(text);
          }
        }
      };

      recognition.onerror = () => {
        setIsListening(false);
      };

      recognition.onend = () => {
        setIsListening(false);
      };

      recognitionRef.current = recognition;
      recognition.start();
      setIsListening(true);
    },
    [supported, lang]
  );

  const stop = useCallback(() => {
    recognitionRef.current?.stop();
    setIsListening(false);
  }, []);

  return { isListening, start, stop, supported };
}
