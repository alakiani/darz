// The User's Usage by Quota Period, newest first (ADR 0010). Periods
// within the last ~90 days open into each live Device plus one Revoked
// Devices row; older ones have only their stored total, so period totals
// and calendar months are never mixed.
import { Fragment, useEffect, useState } from "react";
import { Anchor, Badge, Button, Group, Paper, Stack, Table, Text, Title } from "@mantine/core";
import { IconArrowRight, IconChevronDown } from "@tabler/icons-react";
import { api, type MyUsage, type PeriodUsage } from "../api/client";
import { fa } from "../fa";
import { bytes } from "./Device";
import { periodDay, remediesText } from "./Quota";

export function UsagePage() {
  const [usage, setUsage] = useState<MyUsage | null>(null);
  useEffect(() => {
    api.GET("/v1/me/usage").then(({ data }) => setUsage(data ?? null));
  }, []);
  if (!usage) return null;

  const periods = [usage.current, ...usage.history];
  return (
    <Stack>
      <Anchor href="#/" fz="sm">
        <Group gap={4}>
          <IconArrowRight size={14} />
          {fa.back}
        </Group>
      </Anchor>
      <Title order={1} size="h2">
        {fa.usageTitle}
      </Title>
      <Paper>
        <Table.ScrollContainer minWidth={30 * 16} type="native">
          <Table>
            <Table.Thead>
              <Table.Tr>
                <Table.Th>{fa.dates}</Table.Th>
                <Table.Th>{fa.used}</Table.Th>
                <Table.Th>{fa.quota}</Table.Th>
                <Table.Th>{fa.remedies}</Table.Th>
                <Table.Th />
              </Table.Tr>
            </Table.Thead>
            <Table.Tbody>
              {periods.map((p, i) => (
                <Period key={p.start} period={p} quota={i === 0 ? quotaText(usage) : ""} current={i === 0} />
              ))}
            </Table.Tbody>
          </Table>
        </Table.ScrollContainer>
      </Paper>
      {usage.history.some((p) => !p.detailed) && (
        <Text c="dimmed" fz="sm">
          {fa.olderPeriods}
        </Text>
      )}
    </Stack>
  );
}

function quotaText(u: MyUsage): string {
  return u.quota == null ? fa.unlimited : bytes(u.quota);
}

// Past periods carry no Quota: what applied then isn't stored, so their
// Quota cell stays empty until that is settled (#81).
function Period({ period: p, quota, current }: { period: PeriodUsage; quota: string; current: boolean }) {
  const [open, setOpen] = useState(false);
  // A period runs up to, not including, its end: show the last day it covers.
  const last = Date.parse(p.end) - 1;
  return (
    <Fragment>
      <Table.Tr data-testid="period" fw={current ? 600 : undefined}>
        <Table.Td>
          {fa.periodRange(periodDay(p.start), periodDay(last))}
          {current && (
            <Badge size="xs" variant="light" ms={6}>
              {fa.currentPeriod}
            </Badge>
          )}
        </Table.Td>
        <Table.Td>{bytes(p.bytes)}</Table.Td>
        <Table.Td>{quota}</Table.Td>
        <Table.Td>{remediesText(p)}</Table.Td>
        <Table.Td>
          {p.detailed && (
            <Button
              variant="subtle"
              size="compact-xs"
              aria-expanded={open}
              rightSection={<IconChevronDown size={14} className="chevron" />}
              onClick={() => setOpen(!open)}
            >
              {fa.byDevice}
            </Button>
          )}
        </Table.Td>
      </Table.Tr>
      {open &&
        (p.devices ?? []).map((d) => (
          <Table.Tr key={d.id} className="sub" data-testid="period-device">
            <Table.Td>{d.name}</Table.Td>
            <Table.Td>{bytes(d.bytes)}</Table.Td>
            <Table.Td colSpan={3} />
          </Table.Tr>
        ))}
      {open && p.revoked > 0 && (
        <Table.Tr className="sub" data-testid="period-revoked">
          <Table.Td>{fa.revokedDevices}</Table.Td>
          <Table.Td>{bytes(p.revoked)}</Table.Td>
          <Table.Td colSpan={3} />
        </Table.Tr>
      )}
    </Fragment>
  );
}
