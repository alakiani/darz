import { useEffect, useState } from "react";
import { Alert, Button, Group, Paper, Stack, Text, Textarea, Title } from "@mantine/core";
import { IconArrowBackUp, IconRefresh } from "@tabler/icons-react";
import { api } from "../api/client";
import { ago, type SetInfo } from "./api";
import { Problem, Screen } from "./ui";

// Destination Sets: feed status and gates, fetch now, roll back, Admin
// overrides (ADR 0004).
export function SetsScreen() {
  const [sets, setSets] = useState<SetInfo[]>([]);
  useEffect(() => {
    api.GET("/v1/admin/sets").then(({ data }) => setSets(data ?? []));
  }, []);
  return (
    <Screen title="Destination Sets" about="The address ranges Rules route by.">
      {sets.map((s) => (
        <SetCard key={s.id} set={s} onChange={setSets} />
      ))}
    </Screen>
  );
}

function SetCard({ set: s, onChange }: { set: SetInfo; onChange: (s: SetInfo[]) => void }) {
  const [adds, setAdds] = useState(s.additions.join("\n"));
  const [rems, setRems] = useState(s.removals.join("\n"));
  const [error, setError] = useState("");
  const p = { params: { path: { id: String(s.id) } } };
  const lines = (t: string) => t.split("\n").map((l) => l.trim()).filter(Boolean);
  const after = (data: SetInfo[] | undefined, error: unknown) => {
    setError(data ? "" : ((error as { error?: string })?.error ?? "failed"));
    if (data) onChange(data);
  };

  return (
    <Paper p="lg" data-testid={`set-${s.name}`}>
      <Stack gap="sm">
        <Group justify="space-between" align="flex-start">
          <div>
            <Title order={2} size="h4">
              {s.name}
            </Title>
            <Text fz="sm" c="dimmed">
              <Text span fw={700} c="var(--mantine-color-text)" data-testid="prefixes">
                {s.prefixes}
              </Text>{" "}
              prefixes, {s.addresses.toLocaleString()} addresses
            </Text>
          </div>
          {s.feed && (
            <Group gap="xs">
              <Button
                variant="light"
                size="xs"
                leftSection={<IconRefresh size={14} />}
                onClick={async () => {
                  const { data, error } = await api.POST("/v1/admin/sets/{id}/fetch", p);
                  after(data, error);
                }}
              >
                Fetch now
              </Button>
              <Button
                variant="default"
                size="xs"
                leftSection={<IconArrowBackUp size={14} />}
                disabled={!s.can_roll_back}
                onClick={async () => {
                  const { data, error } = await api.POST("/v1/admin/sets/{id}/rollback", p);
                  after(data, error);
                }}
              >
                Roll back
              </Button>
            </Group>
          )}
        </Group>
        {s.feed && (
          <>
            <Text fz="sm">
              Feed: updated {ago(s.updated_at)}, last fetch {ago(s.last_attempt)}
            </Text>
            {s.last_error ? (
              <Alert color="red" variant="light" data-testid="feed-error">
                Last update rejected by the gates; the last good set stays: {s.last_error}
              </Alert>
            ) : (
              <Text c="dimmed" fz="xs">
                Gates: parses cleanly, not empty, within ±2% of the last good set.
              </Text>
            )}
          </>
        )}
        <form
          onSubmit={async (e) => {
            e.preventDefault();
            const { data, error } = await api.POST("/v1/admin/sets/{id}/overrides", { ...p, body: { additions: lines(adds), removals: lines(rems) } });
            after(data, error);
          }}
        >
          <Stack gap="sm">
            <div className="pair">
              <Textarea
                label="Additions"
                description="One prefix per line"
                name="additions"
                rows={3}
                autosize
                minRows={3}
                ff="monospace"
                value={adds}
                onChange={(e) => setAdds(e.target.value)}
              />
              <Textarea
                label="Removals"
                description="One prefix per line"
                name="removals"
                rows={3}
                autosize
                minRows={3}
                ff="monospace"
                value={rems}
                onChange={(e) => setRems(e.target.value)}
              />
            </div>
            <Group>
              <Button type="submit" size="xs">
                Save overrides
              </Button>
            </Group>
          </Stack>
        </form>
        <Problem>{error}</Problem>
      </Stack>
    </Paper>
  );
}
