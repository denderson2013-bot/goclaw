/* eslint-disable @typescript-eslint/no-explicit-any */
import { useState, useEffect, useCallback, useRef } from "react";
import { Smartphone, Plus, RefreshCw, Trash2, Play, Square } from "lucide-react";
import { useTranslation } from "react-i18next";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Badge } from "@/components/ui/badge";
import { PageHeader } from "@/components/shared/page-header";
import { EmptyState } from "@/components/shared/empty-state";
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from "@/components/ui/dialog";
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from "@/components/ui/select";
import { useHttp } from "@/hooks/use-ws";
import { useAgents } from "@/pages/agents/hooks/use-agents";
import { toast } from "@/stores/use-toast-store";
import { useAuthStore } from "@/stores/use-auth-store";

interface WahaSession {
  name: string;
  status: string;
  config?: any;
  me?: {
    id?: string;
    pushName?: string;
  };
}

export function WahaSessionsPage() {
  const { t } = useTranslation("waha-sessions");
  const http = useHttp();
  const { agents } = useAgents();
  const token = useAuthStore((s) => s.token);

  const [sessions, setSessions] = useState<WahaSession[]>([]);
  const [loading, setLoading] = useState(true);
  const [createOpen, setCreateOpen] = useState(false);
  const [deleteTarget, setDeleteTarget] = useState<WahaSession | null>(null);
  const [deleteLoading, setDeleteLoading] = useState(false);
  const [newName, setNewName] = useState("");
  const [selectedAgent, setSelectedAgent] = useState("");
  const [creating, setCreating] = useState(false);
  const [qrSession, setQrSession] = useState<string | null>(null);
  const [linkedSessions, setLinkedSessions] = useState<Set<string>>(new Set());
  const [linkingSession, setLinkingSession] = useState<string | null>(null);

  // Track which agent was selected for each session (for auto-link)
  const sessionAgentMap = useRef<Record<string, string>>({});

  const fetchSessions = useCallback(async () => {
    try {
      const data = await http.get<WahaSession[]>("/v1/waha/sessions");
      setSessions(Array.isArray(data) ? data : []);
    } catch {
      setSessions([]);
    } finally {
      setLoading(false);
    }
  }, [http]);

  // Poll sessions every 3 seconds
  useEffect(() => {
    fetchSessions();
    const interval = setInterval(fetchSessions, 3000);
    return () => clearInterval(interval);
  }, [fetchSessions]);

  // Auto-link when session transitions to WORKING
  useEffect(() => {
    for (const s of sessions) {
      if (
        s.status === "WORKING" &&
        !linkedSessions.has(s.name) &&
        sessionAgentMap.current[s.name] &&
        linkingSession !== s.name
      ) {
        const agentId = sessionAgentMap.current[s.name];
        setLinkingSession(s.name);
        http
          .post<any>(`/v1/waha/sessions/${s.name}/link`, {
            agent_id: agentId,
          })
          .then(() => {
            setLinkedSessions((prev) => new Set([...prev, s.name]));
            toast.success(t("linkSuccess"));
            delete sessionAgentMap.current[s.name];
          })
          .catch(() => {
            toast.error(t("linkError"));
          })
          .finally(() => {
            setLinkingSession(null);
          });
      }
    }
  }, [sessions, linkedSessions, linkingSession, http, t]);

  const handleCreate = async () => {
    if (!newName.trim()) return;
    setCreating(true);
    try {
      await http.post("/v1/waha/sessions", { name: newName.trim() });
      if (selectedAgent) {
        sessionAgentMap.current[newName.trim()] = selectedAgent;
      }
      setQrSession(newName.trim());
      setCreateOpen(false);
      setNewName("");
      setSelectedAgent("");
      fetchSessions();
    } catch {
      toast.error(t("createError"));
    } finally {
      setCreating(false);
    }
  };

  const handleDelete = async () => {
    if (!deleteTarget) return;
    setDeleteLoading(true);
    try {
      await http.delete(`/v1/waha/sessions/${deleteTarget.name}`);
      setDeleteTarget(null);
      fetchSessions();
    } catch {
      toast.error(t("deleteError"));
    } finally {
      setDeleteLoading(false);
    }
  };

  const handleStart = async (name: string) => {
    try {
      await http.post(`/v1/waha/sessions/${name}/start`);
      fetchSessions();
    } catch {
      /* ignore */
    }
  };

  const handleStop = async (name: string) => {
    try {
      await http.post(`/v1/waha/sessions/${name}/stop`);
      fetchSessions();
    } catch {
      /* ignore */
    }
  };

  const handleRestart = async (name: string) => {
    try {
      toast.info(t("restarting"));
      await http.post(`/v1/waha/sessions/${name}/stop`);
      await new Promise((r) => setTimeout(r, 2000));
      await http.post(`/v1/waha/sessions/${name}/start`);
      toast.success(t("restarted"));
      fetchSessions();
    } catch {
      toast.error(t("errorRestarting"));
    }
  };

  const handleLink = async (name: string, agentId: string) => {
    try {
      await http.post(`/v1/waha/sessions/${name}/link`, { agent_id: agentId });
      setLinkedSessions((prev) => new Set([...prev, name]));
      toast.success(t("linkedSuccess"));
      fetchSessions();
    } catch {
      toast.error(t("errorLinking"));
    }
  };

  const statusBadge = (status: string) => {
    switch (status) {
      case "WORKING":
        return <Badge variant="success">{t("connected")}</Badge>;
      case "SCAN_QR_CODE":
        return <Badge variant="warning">{t("scanQR")}</Badge>;
      case "STARTING":
        return <Badge variant="info">{t("connecting")}</Badge>;
      case "STOPPED":
      case "FAILED":
        return <Badge variant="destructive">{t(status === "STOPPED" ? "stopped" : "failed")}</Badge>;
      default:
        return <Badge variant="secondary">{status || t("unknown")}</Badge>;
    }
  };

  // Build QR URL with auth and cache bust
  const qrUrl = (name: string) => {
    const base = import.meta.env.VITE_API_URL || window.location.origin;
    return `${base}/v1/waha/sessions/${name}/qr?token=${encodeURIComponent(token)}&t=${Date.now()}`;
  };

  return (
    <div className="flex flex-col gap-6 p-4 sm:p-6">
      <PageHeader
        title={t("title")}
        description={t("description")}
        actions={
          <div className="flex items-center gap-2">
            <Button variant="outline" size="sm" onClick={fetchSessions}>
              <RefreshCw className="mr-1.5 h-4 w-4" />
              {t("refresh")}
            </Button>
            <Button size="sm" onClick={() => setCreateOpen(true)}>
              <Plus className="mr-1.5 h-4 w-4" />
              {t("newSession")}
            </Button>
          </div>
        }
      />

      {/* Sessions list */}
      {loading && sessions.length === 0 ? (
        <div className="flex items-center justify-center py-12">
          <div className="h-6 w-6 animate-spin rounded-full border-2 border-muted-foreground border-t-transparent" />
        </div>
      ) : sessions.length === 0 ? (
        <EmptyState
          icon={Smartphone}
          title={t("noSessions")}
          description={t("noSessionsDesc")}
        />
      ) : (
        <div className="grid grid-cols-1 gap-4 sm:grid-cols-2 lg:grid-cols-3">
          {sessions.map((session) => (
            <div
              key={session.name}
              className="flex flex-col gap-3 rounded-lg border bg-card p-4"
            >
              <div className="flex items-start justify-between">
                <div className="min-w-0">
                  <h3 className="truncate text-sm font-medium">
                    {session.name}
                  </h3>
                  {session.me?.pushName && (
                    <p className="mt-0.5 truncate text-xs text-muted-foreground">
                      {session.me.pushName}
                      {session.me.id && ` (${session.me.id.split("@")[0]})`}
                    </p>
                  )}
                </div>
                {statusBadge(session.status)}
              </div>

              {/* QR code display */}
              {(session.status === "SCAN_QR_CODE" ||
                qrSession === session.name) &&
                session.status !== "WORKING" && (
                  <QRCodeDisplay name={session.name} qrUrl={qrUrl} t={t} />
                )}

              {/* Auto-linking indicator */}
              {linkingSession === session.name && (
                <p className="text-xs text-muted-foreground animate-pulse">
                  {t("autoLinking")}
                </p>
              )}
              {linkedSessions.has(session.name) && (
                <Badge variant="success" className="self-start">
                  {t("linked")}
                </Badge>
              )}

              {/* Actions */}
              <div className="flex flex-wrap items-center gap-1.5 pt-2 border-t mt-2">
                {/* Start - quando parado */}
                {(session.status === "STOPPED" || session.status === "FAILED") && (
                  <Button variant="outline" size="sm" onClick={() => handleStart(session.name)}>
                    <Play className="mr-1 h-3.5 w-3.5" />
                    {t("start")}
                  </Button>
                )}

                {/* Stop - quando rodando */}
                {(session.status === "WORKING" || session.status === "SCAN_QR_CODE") && (
                  <Button variant="outline" size="sm" onClick={() => handleStop(session.name)}>
                    <Square className="mr-1 h-3.5 w-3.5" />
                    {t("stop")}
                  </Button>
                )}

                {/* Reiniciar - quando rodando */}
                {session.status === "WORKING" && (
                  <Button variant="outline" size="sm" onClick={() => handleRestart(session.name)}>
                    <RefreshCw className="mr-1 h-3.5 w-3.5" />
                    {t("restart")}
                  </Button>
                )}

                {/* QR Code - quando esperando scan */}
                {session.status === "SCAN_QR_CODE" && (
                  <Button
                    variant="default"
                    size="sm"
                    onClick={() => setQrSession(qrSession === session.name ? null : session.name)}
                  >
                    <Smartphone className="mr-1 h-3.5 w-3.5" />
                    {t("scanQR")}
                  </Button>
                )}

                {/* Vincular - quando conectado e não vinculado */}
                {session.status === "WORKING" && !linkedSessions.has(session.name) && agents.length > 0 && (
                  <Button
                    variant="outline"
                    size="sm"
                    className="text-green-600 border-green-300 hover:bg-green-50"
                    onClick={() => { const a = agents[0]; if (a) handleLink(session.name, a.id); }}
                  >
                    {t("link")}
                  </Button>
                )}

                {/* Deletar - sempre */}
                <Button
                  variant="ghost"
                  size="sm"
                  className="ml-auto text-destructive hover:text-destructive"
                  onClick={() => setDeleteTarget(session)}
                >
                  <Trash2 className="h-3.5 w-3.5" />
                </Button>
              </div>
            </div>
          ))}
        </div>
      )}

      {/* Create Dialog */}
      <Dialog open={createOpen} onOpenChange={setCreateOpen}>
        <DialogContent>
          <DialogHeader>
            <DialogTitle>{t("newSession")}</DialogTitle>
            <DialogDescription>{t("description")}</DialogDescription>
          </DialogHeader>
          <div className="flex flex-col gap-4 py-4">
            <div className="flex flex-col gap-2">
              <label className="text-sm font-medium">{t("sessionName")}</label>
              <Input
                className="text-base md:text-sm"
                placeholder={t("sessionNamePlaceholder")}
                value={newName}
                onChange={(e) => setNewName(e.target.value)}
                onKeyDown={(e) => {
                  if (e.key === "Enter" && newName.trim()) handleCreate();
                }}
              />
            </div>
            <div className="flex flex-col gap-2">
              <label className="text-sm font-medium">{t("selectAgent")}</label>
              <Select value={selectedAgent} onValueChange={setSelectedAgent}>
                <SelectTrigger className="text-base md:text-sm">
                  <SelectValue placeholder={t("selectAgentPlaceholder")} />
                </SelectTrigger>
                <SelectContent>
                  {agents.map((agent) => (
                    <SelectItem key={agent.id} value={agent.id}>
                      {agent.display_name || agent.agent_key || agent.id}
                    </SelectItem>
                  ))}
                </SelectContent>
              </Select>
            </div>
          </div>
          <DialogFooter>
            <Button
              onClick={handleCreate}
              disabled={!newName.trim() || creating}
            >
              {creating ? t("creating") : t("create")}
            </Button>
          </DialogFooter>
        </DialogContent>
      </Dialog>

      {/* Delete Confirm Dialog */}
      <Dialog
        open={!!deleteTarget}
        onOpenChange={(open) => !open && setDeleteTarget(null)}
      >
        <DialogContent>
          <DialogHeader>
            <DialogTitle>{t("delete")}</DialogTitle>
            <DialogDescription>
              {t("deleteConfirm", { name: deleteTarget?.name })}
            </DialogDescription>
          </DialogHeader>
          <p className="text-sm text-muted-foreground">
            {t("deleteConfirmDesc")}
          </p>
          <DialogFooter>
            <Button variant="outline" onClick={() => setDeleteTarget(null)}>
              Cancel
            </Button>
            <Button
              variant="destructive"
              onClick={handleDelete}
              disabled={deleteLoading}
            >
              {deleteLoading ? t("deleting") : t("delete")}
            </Button>
          </DialogFooter>
        </DialogContent>
      </Dialog>
    </div>
  );
}

/** QR code display that auto-refreshes every 5 seconds */
function QRCodeDisplay({
  name,
  qrUrl,
  t,
}: {
  name: string;
  qrUrl: (name: string) => string;
  t: (key: string) => string;
}) {
  const [imgSrc, setImgSrc] = useState(() => qrUrl(name));

  useEffect(() => {
    const interval = setInterval(() => {
      setImgSrc(qrUrl(name));
    }, 5000);
    return () => clearInterval(interval);
  }, [name, qrUrl]);

  return (
    <div className="flex flex-col items-center gap-2 rounded-md border bg-white p-3">
      <p className="text-xs text-muted-foreground">{t("scanQRDesc")}</p>
      <img
        src={imgSrc}
        alt="QR Code"
        className="h-48 w-48 object-contain"
        onError={(e) => {
          (e.target as HTMLImageElement).style.display = "none";
        }}
      />
    </div>
  );
}
