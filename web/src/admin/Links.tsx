import { useEffect, useState } from "react";
import { Badge, Checkbox, Group, Paper, Stack, Table, Text, Title } from "@mantine/core";
import { api } from "../api/client";
import type { LinkTile, TransportList } from "./api";
import { ListTable, Problem, Screen, Status } from "./ui";

// Links: read-only health, plus the editable candidate Transport list
// (ADR 0007).
export function LinksScreen() {
  const [links, setLinks] = useState<LinkTile[]>([]);
  const [tl, setTl] = useState<TransportList | null>(null);
  const [error, setError] = useState("");
  useEffect(() => {
    api.GET("/v1/admin/links").then(({ data }) => setLinks(data ?? []));
    api.GET("/v1/admin/transports").then(({ data }) => setTl(data ?? null));
  }, []);

  const toggle = async (t: string, on: boolean) => {
    if (!tl) return;
    const before = tl;
    const enabled = tl.known.filter((k) => (k === t ? on : tl.enabled.includes(k)));
    setTl({ ...tl, enabled }); // show the change at once; undo it if refused
    const { data, error } = await api.POST("/v1/admin/transports", { body: { enabled, known: tl.known } });
    setError(data ? "" : ((error as { error?: string })?.error ?? "failed"));
    setTl(data ?? before);
  };

  return (
    <Screen title="Links" about="Every pair of Nodes, and how it is carried.">
      <ListTable data-testid="links">
        <Table.Thead>
          <Table.Tr>
            <Table.Th>Link</Table.Th>
            <Table.Th>Crosses the filter</Table.Th>
            <Table.Th>Health</Table.Th>
            <Table.Th>Instances</Table.Th>
          </Table.Tr>
        </Table.Thead>
        <Table.Tbody>
          {links.map((l) => (
            <Table.Tr key={l.id}>
              <Table.Td fw={600}>
                {l.a} – {l.b}
              </Table.Td>
              <Table.Td>{l.crosses ? "yes" : "no"}</Table.Td>
              <Table.Td>
                <Status>{l.up_a || l.up_b ? `up, ${l.transport}, ${l.rtt_ms.toFixed(0)} ms` : "down"}</Status>
              </Table.Td>
              <Table.Td>
                <Group gap={4}>
                  {l.instances.map((i) => (
                    <Badge key={`${i.transport}:${i.listen_port}`} size="sm" variant={i.up ? "light" : "outline"} color={i.up ? "green" : "gray"} tt="none">
                      {`${i.transport}${i.transport === "gre" ? "" : `:${i.listen_port || "?"}`}${i.up ? " ✓" : ""}`}
                    </Badge>
                  ))}
                </Group>
              </Table.Td>
            </Table.Tr>
          ))}
        </Table.Tbody>
      </ListTable>
      {tl && (
        <Paper p="lg">
          <Stack gap="sm">
            <Title order={2} size="h5">
              Candidate Transports
            </Title>
            <Text c="dimmed" fz="sm">
              Off means no Link uses it. AmneziaWG is the only Transport across the filter and stays on.
            </Text>
            <Group>
              {tl.known.map((t) => (
                <Checkbox key={t} name={`transport-${t}`} label={t} checked={tl.enabled.includes(t)} onChange={(e) => toggle(t, e.target.checked)} />
              ))}
            </Group>
            <Problem>{error}</Problem>
          </Stack>
        </Paper>
      )}
    </Screen>
  );
}
