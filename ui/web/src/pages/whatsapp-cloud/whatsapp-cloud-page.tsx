/* eslint-disable @typescript-eslint/no-explicit-any */
import { useState, useEffect } from "react";
import { useTranslation } from "react-i18next";
import { Phone, FileText, RefreshCw, Loader2 } from "lucide-react";
import { useAuthStore } from "@/stores/use-auth-store";

interface PhoneNumber {
  id: string;
  display_phone_number: string;
  verified_name: string;
  quality_rating: string;
  status: string;
}

interface Template {
  id: string;
  name: string;
  language: string;
  status: string;
  category: string;
}

export function WhatsAppCloudPage() {
  const { t } = useTranslation("whatsapp-cloud");
  const [activeTab, setActiveTab] = useState<"numbers" | "templates">("numbers");

  return (
    <div className="flex h-full flex-col">
      <div className="border-b px-6 py-4">
        <h1 className="text-2xl font-semibold">{t("title")}</h1>
        <p className="text-sm text-muted-foreground mt-1">{t("description")}</p>
      </div>

      <div className="border-b px-6">
        <div className="flex gap-4">
          <button
            onClick={() => setActiveTab("numbers")}
            className={`flex items-center gap-2 border-b-2 px-1 py-3 text-sm font-medium transition-colors ${
              activeTab === "numbers"
                ? "border-primary text-primary"
                : "border-transparent text-muted-foreground hover:text-foreground"
            }`}
          >
            <Phone className="h-4 w-4" />
            {t("tabs.numbers")}
          </button>
          <button
            onClick={() => setActiveTab("templates")}
            className={`flex items-center gap-2 border-b-2 px-1 py-3 text-sm font-medium transition-colors ${
              activeTab === "templates"
                ? "border-primary text-primary"
                : "border-transparent text-muted-foreground hover:text-foreground"
            }`}
          >
            <FileText className="h-4 w-4" />
            {t("tabs.templates")}
          </button>
        </div>
      </div>

      <div className="flex-1 overflow-y-auto p-6">
        {activeTab === "numbers" ? <NumbersTab /> : <TemplatesTab />}
      </div>
    </div>
  );
}

function NumbersTab() {
  const { t } = useTranslation("whatsapp-cloud");
  const token = useAuthStore((s) => s.token);
  const [numbers, setNumbers] = useState<PhoneNumber[]>([]);
  const [loading, setLoading] = useState(false);
  const [error, setError] = useState<string | null>(null);

  const fetchNumbers = async () => {
    setLoading(true);
    setError(null);
    try {
      const resp = await fetch("/v1/whatsapp-cloud/numbers", {
        headers: { Authorization: `Bearer ${token}` },
      });
      if (!resp.ok) {
        const data = await resp.json().catch(() => ({}));
        throw new Error(data.error || `HTTP ${resp.status}`);
      }
      const data = await resp.json();
      setNumbers(data.data || []);
    } catch (err: any) {
      setError(err.message);
    } finally {
      setLoading(false);
    }
  };

  useEffect(() => {
    fetchNumbers();
  }, []); // eslint-disable-line react-hooks/exhaustive-deps

  const qualityColor = (rating: string) => {
    switch (rating?.toUpperCase()) {
      case "GREEN": return "text-green-600 bg-green-50";
      case "YELLOW": return "text-yellow-600 bg-yellow-50";
      case "RED": return "text-red-600 bg-red-50";
      default: return "text-muted-foreground bg-muted";
    }
  };

  return (
    <div>
      <div className="flex items-center justify-between mb-4">
        <h2 className="text-lg font-medium">{t("numbers.title")}</h2>
        <button
          onClick={fetchNumbers}
          disabled={loading}
          className="inline-flex items-center gap-2 rounded-md bg-secondary px-3 py-1.5 text-sm font-medium hover:bg-secondary/80"
        >
          {loading ? <Loader2 className="h-4 w-4 animate-spin" /> : <RefreshCw className="h-4 w-4" />}
          {t("numbers.refresh")}
        </button>
      </div>

      {error && (
        <div className="rounded-md border border-destructive/50 bg-destructive/10 p-4 text-sm text-destructive mb-4">
          {error}
        </div>
      )}

      {numbers.length === 0 && !loading && !error && (
        <div className="text-center py-12 text-muted-foreground">
          <Phone className="h-12 w-12 mx-auto mb-3 opacity-50" />
          <p>{t("numbers.empty")}</p>
          <p className="text-xs mt-1">{t("numbers.emptyHint")}</p>
        </div>
      )}

      {numbers.length > 0 && (
        <div className="overflow-x-auto">
          <table className="w-full min-w-[600px]">
            <thead>
              <tr className="border-b text-left text-sm text-muted-foreground">
                <th className="pb-2 font-medium">{t("numbers.phone")}</th>
                <th className="pb-2 font-medium">{t("numbers.displayName")}</th>
                <th className="pb-2 font-medium">{t("numbers.quality")}</th>
                <th className="pb-2 font-medium">{t("numbers.status")}</th>
              </tr>
            </thead>
            <tbody>
              {numbers.map((num) => (
                <tr key={num.id} className="border-b last:border-0">
                  <td className="py-3 font-mono text-sm">{num.display_phone_number}</td>
                  <td className="py-3 text-sm">{num.verified_name}</td>
                  <td className="py-3">
                    <span className={`inline-flex rounded-full px-2 py-0.5 text-xs font-medium ${qualityColor(num.quality_rating)}`}>
                      {num.quality_rating || "N/A"}
                    </span>
                  </td>
                  <td className="py-3">
                    <span className={`inline-flex rounded-full px-2 py-0.5 text-xs font-medium ${
                      num.status === "CONNECTED" ? "text-green-600 bg-green-50" : "text-muted-foreground bg-muted"
                    }`}>
                      {num.status}
                    </span>
                  </td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      )}
    </div>
  );
}

function TemplatesTab() {
  const { t } = useTranslation("whatsapp-cloud");
  const token = useAuthStore((s) => s.token);
  const [templates, setTemplates] = useState<Template[]>([]);
  const [loading, setLoading] = useState(false);
  const [error, setError] = useState<string | null>(null);

  const fetchTemplates = async () => {
    setLoading(true);
    setError(null);
    try {
      const resp = await fetch("/v1/whatsapp-cloud/templates", {
        headers: { Authorization: `Bearer ${token}` },
      });
      if (!resp.ok) {
        const data = await resp.json().catch(() => ({}));
        throw new Error(data.error || `HTTP ${resp.status}`);
      }
      const data = await resp.json();
      setTemplates(data.data || []);
    } catch (err: any) {
      setError(err.message);
    } finally {
      setLoading(false);
    }
  };

  useEffect(() => {
    fetchTemplates();
  }, []); // eslint-disable-line react-hooks/exhaustive-deps

  const statusColor = (status: string) => {
    switch (status?.toUpperCase()) {
      case "APPROVED": return "text-green-600 bg-green-50";
      case "PENDING": return "text-yellow-600 bg-yellow-50";
      case "REJECTED": return "text-red-600 bg-red-50";
      default: return "text-muted-foreground bg-muted";
    }
  };

  return (
    <div>
      <div className="flex items-center justify-between mb-4">
        <h2 className="text-lg font-medium">{t("templates.title")}</h2>
        <button
          onClick={fetchTemplates}
          disabled={loading}
          className="inline-flex items-center gap-2 rounded-md bg-secondary px-3 py-1.5 text-sm font-medium hover:bg-secondary/80"
        >
          {loading ? <Loader2 className="h-4 w-4 animate-spin" /> : <RefreshCw className="h-4 w-4" />}
          {t("templates.refresh")}
        </button>
      </div>

      {error && (
        <div className="rounded-md border border-destructive/50 bg-destructive/10 p-4 text-sm text-destructive mb-4">
          {error}
        </div>
      )}

      {templates.length === 0 && !loading && !error && (
        <div className="text-center py-12 text-muted-foreground">
          <FileText className="h-12 w-12 mx-auto mb-3 opacity-50" />
          <p>{t("templates.empty")}</p>
          <p className="text-xs mt-1">{t("templates.emptyHint")}</p>
        </div>
      )}

      {templates.length > 0 && (
        <div className="overflow-x-auto">
          <table className="w-full min-w-[600px]">
            <thead>
              <tr className="border-b text-left text-sm text-muted-foreground">
                <th className="pb-2 font-medium">{t("templates.name")}</th>
                <th className="pb-2 font-medium">{t("templates.language")}</th>
                <th className="pb-2 font-medium">{t("templates.category")}</th>
                <th className="pb-2 font-medium">{t("templates.status")}</th>
              </tr>
            </thead>
            <tbody>
              {templates.map((tpl) => (
                <tr key={tpl.id} className="border-b last:border-0">
                  <td className="py-3 font-mono text-sm">{tpl.name}</td>
                  <td className="py-3 text-sm">{tpl.language}</td>
                  <td className="py-3 text-sm">{tpl.category}</td>
                  <td className="py-3">
                    <span className={`inline-flex rounded-full px-2 py-0.5 text-xs font-medium ${statusColor(tpl.status)}`}>
                      {tpl.status}
                    </span>
                  </td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      )}
    </div>
  );
}
