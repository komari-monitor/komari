import React from "react";
import { toast } from "sonner";
import {
  Table,
  TableHeader,
  TableBody,
  TableRow,
  TableHead,
  TableCell,
} from "@/components/ui/table";
import { useTranslation } from "react-i18next";
import { Dialog, Flex, Button } from "@radix-ui/themes";
import { UserAgentHelper } from "@/utils/UserAgentHelper";
import Loading from "@/components/loading";
type Resp = {
  current: string;
  data: Array<{
    uuid: string;
    session: string;
    user_agent: string;
    ip: string;
    login_method: string;
    latest_online: string;
    latest_ip: string;
    latest_user_agent: string;
    expires: string;
    created_at: string;
  }>;
  status: string;
};
export default function Sessions() {
  const [t] = useTranslation();
  const [sessions, setSessions] = React.useState<Resp | null>(null);
  React.useEffect(() => {
    fetch("/api/admin/session/get")
      .then((response) => {
        if (!response.ok) {
          throw new Error(`Error: ${response.status} ${response.statusText}`);
        }
        return response.json();
      })
      .then((data: Resp) => {
        setSessions(data);
      })
      .catch((error) => {
        console.error("Error fetching sessions:", error);
        toast.error(error.message);
      });
  }, []);

  function deleteSession(sessionId: string) {
    const isCurrent = sessionId === sessions?.current;
    fetch("/api/admin/session/remove", {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({ session: sessionId }),
    })
      .then((response) => response.json())
      .then((data) => {
        if (data.status === "success") {
          toast.success(t("sessions.deleted_successfully"));
          if (isCurrent) {
            window.location.href = "/"; // 登出
            return;
          }
          setSessions((prev) => ({
            ...prev!,
            data: prev?.data.filter((s) => s.session !== sessionId) || [],
          }));
        } else {
          console.error("Failed to delete session:", data);
          toast.error(t("sessions.delete_failed"));
        }
      })
      .catch((error) => {
        console.error("Error deleting session:", error);
        toast.error(error.message);
      });
  }
  function deleteAllSessions() {
    fetch("/api/admin/session/remove/all", {
      method: "POST",
      headers: { "Content-Type": "application/json" },
    })
      .then((response) => {
        if (!response.ok) {
          toast.error("Error:" + response.status);
          return;
        }
        response
          .json()
          .then(() => {
            window.location.href = "/"; // 登出
          })
          .catch((error) => {
            toast.error("Error parsing JSON:" + error);
          });
      })
      .catch((error) => {
        toast.error(error.message);
      });
  }

  if (!sessions) {
    return <Loading />;
  }

  return (
    <div className="km-page-admin-sessions p-4">
      <h1 className="text-2xl font-semibold mb-4">{t("sessions.title")}</h1>
      <div className="mb-4">
        <Dialog.Root>
          <Dialog.Trigger>
            <Button color="red">{t("sessions.delete_all")}</Button>
          </Dialog.Trigger>
          <Dialog.Content>
            <Dialog.Title>{t("sessions.delete_all")}</Dialog.Title>
            <Dialog.Description>
              {t("sessions.delete_all_desc")}
            </Dialog.Description>
            <Flex gap="2" justify={"end"}>
              <Dialog.Trigger>
                <Button variant="soft">{t("common.cancel")}</Button>
              </Dialog.Trigger>
              <Dialog.Trigger>
                <Button color="red" onClick={deleteAllSessions}>
                  {t("common.delete")}
                </Button>
              </Dialog.Trigger>
            </Flex>
          </Dialog.Content>
        </Dialog.Root>
      </div>
      <div className="km-sessions-table overflow-hidden rounded-lg">
        <Table>
          <TableHeader>
            <TableRow>
              <TableHead>{t("sessions.session_id")}</TableHead>
              <TableHead>UA</TableHead>
              <TableHead>IP</TableHead>
              <TableHead>Latest IP</TableHead>
              <TableHead>{t("sessions.expires_at")}</TableHead>
              <TableHead>{t("sessions.last_login")}</TableHead>
              <TableHead>{t("sessions.actions")}</TableHead>
            </TableRow>
          </TableHeader>
          <TableBody>
            {sessions.data.map((s) => {
              const isCurrent = s.session === sessions.current;
              return (
                <TableRow key={s.uuid} className="km-session-item">
                  <TableCell>
                    <Dialog.Root>
                      <Dialog.Trigger>
                        <label className="hover:underline cursor-pointer">
                          {s.session.slice(0, 8)}...
                          {isCurrent && (
                            <span className="ml-2 text-sm text-blue-600">
                              {t("sessions.current")}
                            </span>
                          )}
                        </label>
                      </Dialog.Trigger>
                      <Dialog.Content>
                        <Dialog.Title>
                          {t("sessions.active_sessions")}
                        </Dialog.Title>
                        <Flex direction="column" gap="1">
                          <label className="text-base font-bold">
                            {t("sessions.session_id")}
                          </label>
                          <label className="text-sm">{s.session}</label>
                          <label className="text-base font-bold">
                            IP / {t("sessions.latest_ip")}
                          </label>
                          <label className="text-sm">
                            {s.ip} / {s.latest_ip}
                          </label>
                          <label className="text-base font-bold">
                            User Agent
                          </label>
                          <label className="text-sm">{s.user_agent}</label>
                          <label className="text-sm text-muted-foreground font-bold">
                            {UserAgentHelper.format(s.user_agent, t)}
                          </label>
                          <label className="text-base font-bold">
                            {t("sessions.last_user_agent")}
                          </label>
                          <label className="text-sm">
                            {s.latest_user_agent}
                          </label>
                          <label className="text-sm text-muted-foreground font-bold">
                            {UserAgentHelper.format(s.latest_user_agent, t)}
                          </label>

                          <label className="text-base font-bold">
                            {t("sessions.login_method")}
                          </label>
                          <label className="text-sm">{s.login_method}</label>
                          <label className="text-base font-bold">
                            {t("sessions.latest_online")}
                          </label>
                          <label className="text-sm">
                            {new Date(s.latest_online).toLocaleString()}
                            {" "}({formatDuration((Date.now() - new Date(s.latest_online).getTime()),t)})
                          </label>
                          <label className="text-base font-bold">
                            {t("sessions.created_at")}
                          </label>
                          <label className="text-sm">
                            {new Date(s.created_at).toLocaleString()}
                          </label>
                          <label className="text-base font-bold">
                            {t("sessions.expires_at")}
                          </label>
                          <label className="text-sm">
                            {new Date(s.expires).toLocaleString()}
                          </label>
                          <Flex justify={"end"}>
                            <Dialog.Trigger>
                              <Button variant="soft">{t("common.close")}</Button>
                            </Dialog.Trigger>
                          </Flex>
                        </Flex>
                      </Dialog.Content>
                    </Dialog.Root>
                  </TableCell>
                  <TableCell>{UserAgentHelper.format(s.user_agent, t)}</TableCell>
                  <TableCell>{s.ip}</TableCell>
                  <TableCell>{s.latest_ip}</TableCell>
                  <TableCell>{new Date(s.expires).toLocaleString()}</TableCell>
                  <TableCell>
                    {new Date(s.latest_online).toLocaleString()}{" "}({formatDuration((Date.now() - new Date(s.latest_online).getTime()),t)})
                  </TableCell>
                  <TableCell>
                    <Dialog.Root>
                      {!isCurrent && (
                        <Dialog.Trigger>
                          <Button color="red" variant="ghost">
                            {t("common.delete")}
                          </Button>
                        </Dialog.Trigger>
                      )}
                      <Dialog.Content>
                        <Dialog.Title>
                          {t("common.confirm_delete")}
                        </Dialog.Title>
                        <Dialog.Description>
                          {t("sessions.delete_one_desc")}
                        </Dialog.Description>
                        <Flex gap="2" justify={"end"}>
                          <Dialog.Trigger>
                            <Button variant="soft">
                              {t("common.cancel")}
                            </Button>
                          </Dialog.Trigger>
                          <Dialog.Trigger>
                            <Button
                              color="red"
                              onClick={() => deleteSession(s.session)}
                            >
                              {t("common.delete")}
                            </Button>
                          </Dialog.Trigger>
                        </Flex>
                      </Dialog.Content>
                    </Dialog.Root>
                  </TableCell>
                </TableRow>
              );
            })}
          </TableBody>
        </Table>
      </div>
    </div>
  );
}

function formatDuration(number: number,t:any ): string {
  const ms = Math.abs(number);
  const seconds = Math.floor(ms / 1000);
  const minutes = Math.floor(seconds / 60);
  const hours = Math.floor(minutes / 60);
  const days = Math.floor(hours / 24);

  if (seconds < 60) {
    return t("just_now");
  }

  if (days > 0) {
    const remainingHours = hours % 24;
    if (remainingHours > 0) {
      return `${days}${t("nodeCard.time_day")}${remainingHours}${t("nodeCard.time_hour")}${t("time.ago")}`;
    }
    return `${days}${t("nodeCard.time_day")} ${t("time.ago")}`;
  }

  if (hours > 0) {
    const remainingMinutes = minutes % 60;
    if (remainingMinutes > 0) {
      return `${hours}${t("nodeCard.time_hour")}${remainingMinutes}${t("nodeCard.time_minute")}${t("time.ago")}`;
    }
    return `${hours}${t("nodeCard.time_hour")}${t("time.ago")}`;
  }

  const remainingSeconds = seconds % 60;
  return `${minutes}${t("nodeCard.time_minute")}${remainingSeconds}${t("nodeCard.time_second")}${t("time.ago")}`;
}