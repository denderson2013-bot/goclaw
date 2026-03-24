/* eslint-disable @typescript-eslint/no-explicit-any */
import { useState, useEffect, useCallback, useRef } from "react";
import { useTranslation } from "react-i18next";
import {
  Phone,
  FileText,
  RefreshCw,
  Loader2,
  Plus,
  Trash2,
  Unlink,
  CheckCircle2,
  AlertCircle,
} from "lucide-react";
import { useAuthStore } from "@/stores/use-auth-store";
import { useSearchParams } from "react-router";

interface Template {
  id: string;
  name: string;
  language: string;
  status: string;
  category: string;
}

interface ChannelInstance {
  id: string;
  name: string;
  display_name: string;
  channel_type: string;
  agent_id: string;
  enabled: boolean;
  has_credentials: boolean;
  created_at: string;
}

export function WhatsAppCloudPage() {
  const { t } = useTranslation("whatsapp-cloud");
  const [activeTab, setActiveTab] = useState<"instances" | "templates">(
    "instances"
  );
  const [searchParams, setSearchParams] = useSearchParams();

  // Handle success/error from callback redirect
  const success = searchParams.get("success");
  const error = searchParams.get("error");

  useEffect(() => {
    if (success || error) {
      // Clear search params after showing message
      const timer = setTimeout(() => {
        setSearchParams({}, { replace: true });
      }, 8000);
      return () => clearTimeout(timer);
    }
  }, [success, error, setSearchParams]);

  return (
    <div className="flex h-full flex-col">
      <div className="border-b px-6 py-4">
        <h1 className="text-2xl font-semibold">{t("title")}</h1>
        <p className="mt-1 text-sm text-muted-foreground">{t("description")}</p>
      </div>

      {success && (
        <div className="mx-6 mt-4 flex items-center gap-2 rounded-md border border-green-200 bg-green-50 p-4 text-sm text-green-700 dark:border-green-800 dark:bg-green-950 dark:text-green-300">
          <CheckCircle2 className="h-5 w-5 shrink-0" />
          {t("signup.success")}
        </div>
      )}

      {error && (
        <div className="mx-6 mt-4 flex items-center gap-2 rounded-md border border-destructive/50 bg-destructive/10 p-4 text-sm text-destructive">
          <AlertCircle className="h-5 w-5 shrink-0" />
          {t("signup.error")}: {error}
        </div>
      )}

      <div className="border-b px-6">
        <div className="flex gap-4">
          <button
            onClick={() => setActiveTab("instances")}
            className={`flex items-center gap-2 border-b-2 px-1 py-3 text-sm font-medium transition-colors ${
              activeTab === "instances"
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
        {activeTab === "instances" ? <InstancesTab /> : <TemplatesTab />}
      </div>
    </div>
  );
}

function InstancesTab() {
  const { t } = useTranslation("whatsapp-cloud");
  const token = useAuthStore((s) => s.token);
  const [instances, setInstances] = useState<ChannelInstance[]>([]);
  const [loading, setLoading] = useState(false);
  const [signupLoading, setSignupLoading] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const pollRef = useRef<ReturnType<typeof setInterval> | null>(null);

  const fetchInstances = useCallback(async () => {
    setLoading(true);
    setError(null);
    try {
      const resp = await fetch("/v1/channels/instances?limit=100", {
        headers: { Authorization: `Bearer ${token}` },
      });
      if (!resp.ok) {
        const data = await resp.json().catch(() => ({}));
        throw new Error(data.error || `HTTP ${resp.status}`);
      }
      const data = await resp.json();
      const waInstances = (data.instances || []).filter(
        (i: ChannelInstance) => i.channel_type === "whatsapp_cloud"
      );
      setInstances(waInstances);
    } catch (err: any) {
      setError(err.message);
    } finally {
      setLoading(false);
    }
  }, [token]);

  useEffect(() => {
    fetchInstances();
  }, [fetchInstances]);

  // Cleanup poll on unmount
  useEffect(() => {
    return () => {
      if (pollRef.current) clearInterval(pollRef.current);
    };
  }, []);

  const handleConnectNumber = async () => {
    setSignupLoading(true);
    setError(null);
    try {
      const resp = await fetch("/v1/whatsapp-cloud/signup-url", {
        headers: { Authorization: `Bearer ${token}` },
      });
      if (!resp.ok) {
        const data = await resp.json().catch(() => ({}));
        throw new Error(data.error || `HTTP ${resp.status}`);
      }
      const data = await resp.json();
      if (!data.url) throw new Error("No signup URL returned");

      // Open popup
      const w = 600;
      const h = 700;
      const left = window.screenX + (window.outerWidth - w) / 2;
      const top = window.screenY + (window.outerHeight - h) / 2;
      const popup = window.open(
        data.url,
        "meta_embedded_signup",
        `width=${w},height=${h},left=${left},top=${top},scrollbars=yes`
      );

      // Record current instance count for polling
      const initialCount = instances.length;

      // Poll for new instance
      pollRef.current = setInterval(async () => {
        try {
          const pollResp = await fetch("/v1/channels/instances?limit=100", {
            headers: { Authorization: `Bearer ${token}` },
          });
          if (pollResp.ok) {
            const pollData = await pollResp.json();
            const waInstances = (pollData.instances || []).filter(
              (i: ChannelInstance) => i.channel_type === "whatsapp_cloud"
            );
            if (waInstances.length > initialCount) {
              // New instance appeared
              setInstances(waInstances);
              if (pollRef.current) clearInterval(pollRef.current);
              pollRef.current = null;
              setSignupLoading(false);
              if (popup && !popup.closed) popup.close();
            }
          }
        } catch {
          // Ignore poll errors
        }

        // Stop polling if popup was closed manually
        if (popup && popup.closed) {
          if (pollRef.current) clearInterval(pollRef.current);
          pollRef.current = null;
          setSignupLoading(false);
          fetchInstances(); // Final refresh
        }
      }, 3000);
    } catch (err: any) {
      setError(err.message);
      setSignupLoading(false);
    }
  };

  const handleDelete = async (id: string) => {
    if (!confirm(t("instances.confirmDelete"))) return;
    try {
      const resp = await fetch(`/v1/channels/instances/${id}`, {
        method: "DELETE",
        headers: { Authorization: `Bearer ${token}` },
      });
      if (!resp.ok) {
        const data = await resp.json().catch(() => ({}));
        throw new Error(data.error || `HTTP ${resp.status}`);
      }
      setInstances((prev) => prev.filter((i) => i.id !== id));
    } catch (err: any) {
      setError(err.message);
    }
  };

  const handleToggle = async (id: string, enabled: boolean) => {
    try {
      const resp = await fetch(`/v1/channels/instances/${id}`, {
        method: "PUT",
        headers: {
          Authorization: `Bearer ${token}`,
          "Content-Type": "application/json",
        },
        body: JSON.stringify({ enabled: !enabled }),
      });
      if (!resp.ok) {
        const data = await resp.json().catch(() => ({}));
        throw new Error(data.error || `HTTP ${resp.status}`);
      }
      setInstances((prev) =>
        prev.map((i) => (i.id === id ? { ...i, enabled: !enabled } : i))
      );
    } catch (err: any) {
      setError(err.message);
    }
  };

  return (
    <div>
      <div className="mb-4 flex items-center justify-between">
        <h2 className="text-lg font-medium">{t("instances.title")}</h2>
        <div className="flex gap-2">
          <button
            onClick={fetchInstances}
            disabled={loading}
            className="inline-flex items-center gap-2 rounded-md bg-secondary px-3 py-1.5 text-sm font-medium hover:bg-secondary/80"
          >
            {loading ? (
              <Loader2 className="h-4 w-4 animate-spin" />
            ) : (
              <RefreshCw className="h-4 w-4" />
            )}
            {t("instances.refresh")}
          </button>
          <button
            onClick={handleConnectNumber}
            disabled={signupLoading}
            className="inline-flex items-center gap-2 rounded-md bg-primary px-4 py-1.5 text-sm font-medium text-primary-foreground hover:bg-primary/90"
          >
            {signupLoading ? (
              <Loader2 className="h-4 w-4 animate-spin" />
            ) : (
              <Plus className="h-4 w-4" />
            )}
            {t("instances.connect")}
          </button>
        </div>
      </div>

      {error && (
        <div className="mb-4 rounded-md border border-destructive/50 bg-destructive/10 p-4 text-sm text-destructive">
          {error}
        </div>
      )}

      {instances.length === 0 && !loading && !error && (
        <div className="py-12 text-center text-muted-foreground">
          <Phone className="mx-auto mb-3 h-12 w-12 opacity-50" />
          <p>{t("instances.empty")}</p>
          <p className="mt-1 text-xs">{t("instances.emptyHint")}</p>
        </div>
      )}

      {instances.length > 0 && (
        <div className="grid grid-cols-1 gap-4 sm:grid-cols-2 lg:grid-cols-3">
          {instances.map((inst) => (
            <div
              key={inst.id}
              className="rounded-lg border bg-card p-4 shadow-sm"
            >
              <div className="mb-3 flex items-start justify-between">
                <div>
                  <h3 className="font-medium">{inst.display_name || inst.name}</h3>
                  <p className="mt-0.5 text-xs font-mono text-muted-foreground">
                    {inst.name}
                  </p>
                </div>
                <span
                  className={`inline-flex rounded-full px-2 py-0.5 text-xs font-medium ${
                    inst.enabled
                      ? "bg-green-50 text-green-600 dark:bg-green-950 dark:text-green-400"
                      : "bg-muted text-muted-foreground"
                  }`}
                >
                  {inst.enabled ? t("instances.active") : t("instances.inactive")}
                </span>
              </div>

              <div className="flex items-center gap-2 border-t pt-3">
                <button
                  onClick={() => handleToggle(inst.id, inst.enabled)}
                  className="inline-flex items-center gap-1.5 rounded-md bg-secondary px-2.5 py-1 text-xs font-medium hover:bg-secondary/80"
                >
                  <Unlink className="h-3.5 w-3.5" />
                  {inst.enabled
                    ? t("instances.disable")
                    : t("instances.enable")}
                </button>
                <button
                  onClick={() => handleDelete(inst.id)}
                  className="inline-flex items-center gap-1.5 rounded-md bg-destructive/10 px-2.5 py-1 text-xs font-medium text-destructive hover:bg-destructive/20"
                >
                  <Trash2 className="h-3.5 w-3.5" />
                  {t("instances.delete")}
                </button>
              </div>
            </div>
          ))}
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
      case "APPROVED":
        return "text-green-600 bg-green-50";
      case "PENDING":
        return "text-yellow-600 bg-yellow-50";
      case "REJECTED":
        return "text-red-600 bg-red-50";
      default:
        return "text-muted-foreground bg-muted";
    }
  };

  return (
    <div>
      <div className="mb-4 flex items-center justify-between">
        <h2 className="text-lg font-medium">{t("templates.title")}</h2>
        <button
          onClick={fetchTemplates}
          disabled={loading}
          className="inline-flex items-center gap-2 rounded-md bg-secondary px-3 py-1.5 text-sm font-medium hover:bg-secondary/80"
        >
          {loading ? (
            <Loader2 className="h-4 w-4 animate-spin" />
          ) : (
            <RefreshCw className="h-4 w-4" />
          )}
          {t("templates.refresh")}
        </button>
      </div>

      {error && (
        <div className="mb-4 rounded-md border border-destructive/50 bg-destructive/10 p-4 text-sm text-destructive">
          {error}
        </div>
      )}

      {templates.length === 0 && !loading && !error && (
        <div className="py-12 text-center text-muted-foreground">
          <FileText className="mx-auto mb-3 h-12 w-12 opacity-50" />
          <p>{t("templates.empty")}</p>
          <p className="mt-1 text-xs">{t("templates.emptyHint")}</p>
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
                    <span
                      className={`inline-flex rounded-full px-2 py-0.5 text-xs font-medium ${statusColor(tpl.status)}`}
                    >
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
