// The Quota card on the home page (ADR 0010): what the User has used of
// their Quota this period, its Top-ups and Resets, when it renews, the
// 80% and 100% banners, and the Refills they hold.
import { useState } from "react";
import { Alert, Anchor, Button, Group, Paper, Progress, Stack, Text, Title } from "@mantine/core";
import { IconAlertTriangle, IconArrowLeft, IconCalendar, IconTicket } from "@tabler/icons-react";
import { api, type MyUsage, type PeriodUsage } from "../api/client";
import { fa } from "../fa";
import { bytes } from "./Device";

/** A date in the Solar Hijri calendar, as the User panel shows dates. */
export function hijri(at: string | number): string {
  return new Date(at).toLocaleDateString("fa-IR");
}

/** The Solar Hijri day of a Quota Period boundary. Periods start at 00:00
 * UTC on the Renewal Day (03:30 in Tehran), so the day is read in UTC. */
export function periodDay(at: string | number): string {
  return new Date(at).toLocaleDateString("fa-IR", { timeZone: "UTC" });
}

/** A period's Top-ups and Resets, one sum per kind: "+ ۵ GB ریست". */
export function remediesText(p: PeriodUsage): string {
  const sums = new Map<string, number>();
  for (const r of p.remedies) sums.set(r.kind, (sums.get(r.kind) ?? 0) + r.bytes);
  return [...sums].map(([kind, n]) => `+ ${bytes(n)} ${fa.remedy[kind] ?? kind}`).join("، ");
}

type Standing = { state: "unlimited" | "ok" | "warn" | "out"; used: number; may: number; percent: number };

/** Where the User stands: what they may use this period and how much of it is gone. */
export function standing(u: MyUsage): Standing {
  const used = u.current.bytes;
  if (u.quota == null) return { state: "unlimited", used, may: 0, percent: 0 };
  const may = u.quota + u.current.remedies.reduce((n, r) => n + r.bytes, 0);
  const out = u.left === 0;
  const percent = out ? 100 : Math.min(100, Math.floor((used * 100) / may));
  return { state: out ? "out" : percent >= 80 ? "warn" : "ok", used, may, percent };
}

const barColor = { unlimited: undefined, ok: undefined, warn: "yellow", out: "red" } as const;

export function QuotaCard({ usage, onChange }: { usage: MyUsage; onChange: (u: MyUsage) => void }) {
  const [error, setError] = useState("");
  const s = standing(usage);
  const renews = periodDay(usage.renews_at);
  const remedies = remediesText(usage.current);
  const refill = usage.refills[0]; // the soonest to expire
  const canRedeem = refill !== undefined && usage.refill_adds != null;

  const redeem = async () => {
    setError("");
    const adds = usage.refill_adds ?? 0;
    if (!confirm(adds > 0 ? fa.redeemConfirm(bytes(adds)) : fa.redeemNothing)) return;
    const { data, response } = await api.POST("/v1/me/refills/{id}/redeem", { params: { path: { id: refill.id } } });
    if (data) return onChange(data);
    setError(response.status === 404 ? fa.refillGone : fa.error);
    const fresh = await api.GET("/v1/me/usage");
    if (fresh.data) onChange(fresh.data);
  };
  const redeemButton = canRedeem && (
    <Button size="xs" variant={s.state === "out" ? "filled" : "light"} onClick={redeem}>
      {fa.redeem}
    </Button>
  );

  return (
    <Paper p="lg" shadow="xs" data-testid="quota-card" data-state={s.state} data-percent={s.percent}>
      <Stack gap="sm">
        <Group justify="space-between">
          <Title order={2} size="h4">
            {fa.quotaTitle}
          </Title>
          <Anchor href="#/usage" fz="sm">
            <Group gap={4}>
              {fa.usageDetails}
              <IconArrowLeft size={14} />
            </Group>
          </Anchor>
        </Group>
        {s.state === "out" && (
          <Alert color="red" icon={<IconAlertTriangle />} data-testid="quota-out">
            <Stack gap="xs" align="flex-start">
              <Text fz="sm">{usage.running_out === "slow" ? fa.quotaOutSlow(renews) : fa.quotaOutBlock(renews)}</Text>
              {redeemButton}
            </Stack>
          </Alert>
        )}
        {s.state === "warn" && (
          <Alert color="yellow" icon={<IconAlertTriangle />} data-testid="quota-warning">
            {fa.quotaWarning}
          </Alert>
        )}
        {s.state === "unlimited" ? (
          <Text fw={600} data-testid="quota-used">
            {fa.usedUnlimited(bytes(s.used))}
          </Text>
        ) : (
          <>
            <Text fw={600} data-testid="quota-used">
              {fa.usedOf(bytes(s.used), bytes(usage.quota ?? 0))}
              {remedies && (
                <Text span c="dimmed" fw={400} data-testid="quota-remedies">
                  {" "}
                  {remedies}
                </Text>
              )}
            </Text>
            <Progress value={s.percent} size="lg" radius="xl" color={barColor[s.state]} aria-label={fa.quotaTitle} />
          </>
        )}
        <Group gap={6} c="dimmed" fz="sm" data-testid="quota-renews">
          <IconCalendar size={16} />
          {fa.renewsOn(renews)}
        </Group>
        {refill && (
          <Group justify="space-between" gap="xs" data-testid="refills">
            <Group gap={6} fz="sm">
              <IconTicket size={16} />
              <span>{fa.refillsHeld(usage.refills.length, hijri(refill.expires_at))}</span>
            </Group>
            {s.state !== "out" && redeemButton}
          </Group>
        )}
        {error && (
          <Text c="red" fz="sm">
            {error}
          </Text>
        )}
      </Stack>
    </Paper>
  );
}
